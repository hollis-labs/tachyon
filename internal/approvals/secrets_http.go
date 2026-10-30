package approvals

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
	"github.com/hollis-labs/tachyon/internal/cards"
)

// submitSecret is POST /secrets: the value goes in, a reference comes out. The value is never returned,
// logged, put in the record, digest or events; the audit event holds the reference and size only.
func (i *Inbox) submitSecret(w http.ResponseWriter, r *http.Request, participant hitl.Participant, session sessionStatus) {
	if !assurance.Satisfies(assurance.ParseLevel(participant.Assurance), assurance.ActionSecretSubmit) {
		fail(w, 403, "assurance_insufficient", "Enter your operator key to submit a secret. "+ElevationCopy(session.ElevationTTL, session.ElevationExpiresAt))
		return
	}
	if !i.Secrets.Available() {
		fail(w, 503, "secrets_unavailable", "Secret storage is not configured.")
		return
	}
	itemID, field, value, status, err := readSecretBody(w, r)
	if err != nil {
		code := "invalid"
		if status == 413 {
			code = "too_large"
		}
		fail(w, status, code, err.Error())
		return
	}
	rec, err := i.Store.Get(r.Context(), itemID)
	if err != nil {
		writeHITLError(w, err)
		return
	}
	if rec.State.IsTerminal() || (rec.ExpiresAt != nil && !rec.ExpiresAt.After(time.Now())) {
		fail(w, 409, "item_not_pending", "This request is no longer open.")
		return
	}
	var request hitl.EnqueueRequest
	_ = json.Unmarshal(rec.Request, &request)
	known := false
	for _, f := range cards.SecretFields(request) {
		known = known || f == field
	}
	if !known {
		fail(w, 409, "secret_field_unknown", "This request has no secret field with that id.")
		return
	}
	var exp time.Time
	if rec.ExpiresAt != nil {
		exp = *rec.ExpiresAt
	}
	meta, err := i.Secrets.Put(r.Context(), PutRequest{ItemID: itemID, Field: field, Value: value, Session: participant.PrincipalRef, IdempotencyKey: r.Header.Get("Idempotency-Key"), Expires: exp})
	if err != nil {
		writeSecretError(w, err)
		return
	}
	_ = i.Store.LogEvent(r.Context(), Event{EventID: "evt_" + itemID + "_secret_" + meta.Ref[len(SecretRefPrefix):], ItemID: itemID, Kind: "secret_submitted", ActorRef: participant.PrincipalRef, Assurance: participant.Assurance, Revision: rec.Revision, Extra: map[string]any{"field": field, "secret_ref": meta.Ref, "size": meta.Size}})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(meta)
}

// readSecretBody accepts JSON {item_id, field, value<=16 KiB} or multipart (item_id, field, file<=64 KiB).
// The status is the HTTP status to answer when err is set.
func readSecretBody(w http.ResponseWriter, r *http.Request) (item, field string, value []byte, status int, err error) {
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch media {
	case "application/json":
		r.Body = http.MaxBytesReader(w, r.Body, MaxSecretValue+4<<10)
		raw, rerr := io.ReadAll(r.Body)
		if rerr != nil {
			return "", "", nil, 413, errors.New("secret value is too large")
		}
		var b struct {
			ItemID string `json:"item_id"`
			Field  string `json:"field"`
			Value  string `json:"value"`
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&b) != nil || d.More() {
			return "", "", nil, 400, errors.New("body must be {item_id, field, value}")
		}
		if len(b.Value) > MaxSecretValue {
			return "", "", nil, 413, errors.New("secret value is too large")
		}
		item, field, value = b.ItemID, b.Field, []byte(b.Value)
	case "multipart/form-data":
		r.Body = http.MaxBytesReader(w, r.Body, MaxSecretFile+8<<10)
		if perr := r.ParseMultipartForm(MaxSecretFile + 8<<10); perr != nil {
			return "", "", nil, 413, errors.New("secret file is too large")
		}
		defer r.MultipartForm.RemoveAll()
		item, field = r.FormValue("item_id"), r.FormValue("field")
		f, _, ferr := r.FormFile("file")
		if ferr != nil {
			return "", "", nil, 400, errors.New("file part is required")
		}
		defer f.Close()
		v, rerr := io.ReadAll(io.LimitReader(f, MaxSecretFile+1))
		if rerr != nil || len(v) > MaxSecretFile {
			return "", "", nil, 413, errors.New("secret file is too large")
		}
		value = v
	default:
		return "", "", nil, 400, errors.New("use application/json or multipart/form-data")
	}
	if !itemPath.MatchString("/items/"+item) || strings.TrimSpace(field) == "" || len(field) > 128 || len(value) == 0 {
		return "", "", nil, 400, errors.New("item_id, field and a non-empty value are required")
	}
	return item, field, value, 0, nil
}

func writeSecretError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrSecretsUnavailable):
		fail(w, 503, "secrets_unavailable", "Secret storage is not configured.")
	case errors.Is(err, ErrSecretRefInvalid):
		fail(w, 422, "secret_ref_invalid", "Submit this secret again; the reference is not valid for this field.")
	case errors.Is(err, ErrSecretLimit):
		fail(w, 409, "secret_limit", "This request already holds the most secrets allowed.")
	case errors.Is(err, ErrIdempotencyReuse):
		fail(w, 409, "idempotency_key_reuse", "That Idempotency-Key was used for a different secret.")
	case errors.Is(err, ErrSecretRate):
		fail(w, 429, "rate_limited", "Too many secret submissions; wait a minute.")
	default:
		fail(w, 500, "internal", "Secret operation failed.")
	}
}

// checkSecretRefs validates every secret answer of a resolve against the store and returns the references
// to consume once the response is recorded. A free-typed string where a reference is required is 422.
func (i *Inbox) checkSecretRefs(ctx context.Context, itemID string, request hitl.EnqueueRequest, response hitl.Response) ([]string, error) {
	fields := cards.SecretFields(request)
	if len(fields) == 0 || len(response.Extra["answers"]) == 0 {
		return nil, nil
	}
	var answers []struct {
		QuestionID string          `json:"questionId"`
		Value      json.RawMessage `json:"value"`
	}
	if json.Unmarshal(response.Extra["answers"], &answers) != nil {
		return nil, nil
	}
	var refs []string
	for _, a := range answers {
		for _, f := range fields {
			if a.QuestionID != f {
				continue
			}
			var v struct {
				Ref string `json:"secret_ref"`
			}
			if json.Unmarshal(a.Value, &v) != nil {
				return nil, ErrSecretRefInvalid
			}
			if err := i.Secrets.Validate(ctx, itemID, f, v.Ref); err != nil {
				return nil, err
			}
			refs = append(refs, v.Ref)
		}
	}
	return refs, nil
}
