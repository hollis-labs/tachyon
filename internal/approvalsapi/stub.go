package approvalsapi

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

//go:embed fixtures/*.json openapi.yaml
var assets embed.FS

// OpenAPI is the contract document, served at GET <Prefix>/openapi.yaml by the stub.
func OpenAPI() []byte { b, _ := assets.ReadFile("openapi.yaml"); return b }

// Routes lists every route the stub serves, relative to Prefix. The real
// session, status, elevate and de-elevate routes are not in this list: they stay
// with the real bridge even with the stub on.
var Routes = []string{
	"GET /inbox", "GET /items/{id}", "GET /items/{id}/audit", "POST /items/{id}/present", "POST /items/{id}/resolve",
	"POST /items/{id}/defer", "DELETE /items/{id}/defer", "POST /batch/resolve", "GET /history", "GET /events",
	"GET /access", "POST /secrets", "GET /openapi.yaml",
}

// Stub serves the fixtures. Each route is mounted by explicit pattern (see
// Routes), so nothing falls through to the unguarded /api/plugins/ catch-all.
// Writes validate like the real routes will but change nothing durable; the
// only memory is the batch idempotency table.
type Stub struct{}

type batchMemo struct{ digest, response string }

var (
	batchMu   sync.Mutex
	batchSeen = map[string]batchMemo{}
)

func (Stub) Mux() *http.ServeMux {
	m := http.NewServeMux()
	for _, route := range Routes {
		method, path, _ := strings.Cut(route, " ")
		f := handlers[route]
		m.HandleFunc(method+" "+Prefix+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Tachyon-Approvals-Stub", "1")
			// Error-shape tests: X-Stub-Status: 400|401|403|404|409|422|429|503.
			if s, _ := strconv.Atoi(r.Header.Get("X-Stub-Status")); s >= 400 && s < 600 && path != "/events" {
				fail(w, s, errCode(s), "Forced by X-Stub-Status.")
				return
			}
			f(w, r)
		})
	}
	return m
}

var handlers map[string]http.HandlerFunc

func init() {
	handlers = map[string]http.HandlerFunc{
		"GET /inbox":               inbox,
		"GET /items/{id}":          func(w http.ResponseWriter, r *http.Request) { withItem(w, r, func(it item) { write(w, 200, it) }) },
		"GET /items/{id}/audit":    audit,
		"POST /items/{id}/present": func(w http.ResponseWriter, r *http.Request) { withItem(w, r, func(it item) { write(w, 200, it) }) },
		"POST /items/{id}/resolve": resolve,
		"POST /items/{id}/defer":   deferItem,
		"DELETE /items/{id}/defer": undefer,
		"POST /batch/resolve":      batchResolve,
		"GET /history":             history,
		"GET /events":              events,
		"GET /access":              access,
		"POST /secrets":            secrets,
		"GET /openapi.yaml": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(OpenAPI())
		},
	}
}

type item = map[string]any

func load(name string, v any) bool {
	b, err := assets.ReadFile("fixtures/" + name + ".json")
	return err == nil && json.Unmarshal(b, v) == nil
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	write(w, status, map[string]string{"code": code, "message": msg})
}

func errCode(s int) string {
	switch s {
	case 400:
		return "invalid"
	case 401:
		return "session_required"
	case 403:
		return "operator_access_required"
	case 404:
		return "not_found"
	case 409:
		return "stale_revision"
	case 413:
		return "payload_too_large"
	case 422:
		return "confirmation_required"
	case 429:
		return "rate_limited"
	case 503:
		return "secrets_unavailable"
	}
	return "internal"
}

func readBody(w http.ResponseWriter, r *http.Request, v any, max int64) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	if err != nil {
		fail(w, 400, "invalid", "Body must be a JSON object within the size limit.")
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, 400, "invalid", "Send a JSON object with the documented fields only.")
		return nil, false
	}
	return b, true
}

func items() map[string]item {
	var m map[string]item
	load("items", &m)
	return m
}

func withItem(w http.ResponseWriter, r *http.Request, f func(item)) {
	if it, ok := items()[r.PathValue("id")]; ok {
		f(it)
		return
	}
	fail(w, 404, "not_found", "No such item.")
}

func elevated(r *http.Request) bool { return r.Header.Get("X-Stub-Scenario") != "not_elevated" }

func pending(it item) bool {
	s, _ := it["state"].(string)
	return slices.Contains(pendingStates, s)
}

func str(it item, k string) string { s, _ := it[k].(string); return s }

func rank(risk string) int {
	switch risk {
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	}
	return 1
}

// inbox filters: group, risk, q, batchable=1, status=pending|history, order=urgency|created.
// Urgency: deferred last; then expires_at ascending (none last); then risk descending; then oldest first.
func inbox(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var d struct {
		Pending     []item         `json:"pending"`
		History     []item         `json:"history"`
		NextPending *string        `json:"next_pending"`
		NextHistory *string        `json:"next_history"`
		SyncedAt    string         `json:"synced_at"`
		ServerNowMs int64          `json:"server_now_ms"`
		Revision    string         `json:"revision"`
		Counts      map[string]any `json:"counts"`
	}
	load("inbox", &d)
	switch q.Get("order") {
	case "", "urgency", "created":
	default:
		fail(w, 400, "invalid", "order must be urgency or created.")
		return
	}
	switch q.Get("status") {
	case "", "pending", "history":
	default:
		fail(w, 400, "invalid", "status must be pending or history.")
		return
	}
	text := strings.ToLower(q.Get("q"))
	keep := func(it item) bool {
		if g := q.Get("group"); g != "" && str(it, "group") != g {
			return false
		}
		if v := q.Get("risk"); v != "" && str(it, "risk") != v {
			return false
		}
		if q.Get("batchable") == "1" && it["batchable"] != true {
			return false
		}
		if text != "" && !strings.Contains(strings.ToLower(fmt.Sprint(it["title"], " ", it["summary"], " ", it["item_id"], " ", it["requester"])), text) {
			return false
		}
		return true
	}
	filter := func(in []item) []item {
		out := in[:0:0]
		for _, it := range in {
			if keep(it) {
				out = append(out, it)
			}
		}
		return out
	}
	d.Pending, d.History = filter(d.Pending), filter(d.History)
	if q.Get("order") != "created" {
		sort.SliceStable(d.Pending, func(i, j int) bool {
			a, b := d.Pending[i], d.Pending[j]
			if (a["deferred_until"] != nil) != (b["deferred_until"] != nil) {
				return b["deferred_until"] != nil
			}
			ea, eb := str(a, "expires_at"), str(b, "expires_at")
			if ea != eb {
				return eb == "" || ea != "" && ea < eb
			}
			if ra, rb := rank(str(a, "risk")), rank(str(b, "risk")); ra != rb {
				return ra > rb
			}
			return str(a, "asked_at") < str(b, "asked_at")
		})
	}
	switch q.Get("status") {
	case "pending":
		d.History = []item{}
	case "history":
		d.Pending = []item{}
	}
	write(w, 200, d)
}

func audit(w http.ResponseWriter, r *http.Request) {
	withItem(w, r, func(it item) {
		var m map[string][]item
		load("audit", &m)
		ev := m[str(it, "item_id")]
		if ev == nil {
			ev = []item{}
		}
		write(w, 200, map[string]any{"item_id": it["item_id"], "events": ev})
	})
}

type resolveBody struct {
	ExpectedRevision            int64  `json:"expected_revision"`
	PresentedProjectionRevision int64  `json:"presented_projection_revision"`
	ConnectionID                string `json:"connection_id"`
	Response                    struct {
		Kind          string          `json:"kind"`
		Decision      string          `json:"decision"`
		Note          string          `json:"note"`
		Reply         string          `json:"reply"`
		Confirmation  string          `json:"confirmation"`
		AlternativeID string          `json:"alternative_id"`
		Answers       json.RawMessage `json:"answers"`
		Decisions     json.RawMessage `json:"decisions"`
		Payload       json.RawMessage `json:"payload"`
	} `json:"response"`
}

// resolve applies the documented checks in the documented order and changes nothing.
func resolve(w http.ResponseWriter, r *http.Request) {
	withItem(w, r, func(it item) {
		var b resolveBody
		if _, ok := readBody(w, r, &b, 256<<10); !ok {
			return
		}
		dec := b.Response.Decision
		if b.ExpectedRevision < 1 || dec == "" {
			fail(w, 400, "invalid", "expected_revision and response.decision are required.")
			return
		}
		if !pending(it) {
			write(w, 409, item{"code": "already_resolved", "message": "This item was already decided.", "terminal_outcome": it["terminal_outcome"], "revision": it["revision"]})
			return
		}
		if rev, _ := it["revision"].(float64); int64(rev) != b.ExpectedRevision {
			write(w, 409, item{"code": "stale_revision", "message": "The item changed. Review it again.", "revision": it["revision"]})
			return
		}
		destructive, _ := it["destructive"].(map[string]any)
		confirm, _ := it["confirm"].(map[string]any)
		if (str(it, "risk") != RiskLow || destructive != nil) && !elevated(r) {
			fail(w, 403, "operator_access_required", "Operator access is needed for this item.")
			return
		}
		approve := dec == "approved"
		if confirm != nil && approve && b.Response.AlternativeID == "" {
			if strings.TrimSpace(b.Response.Confirmation) != confirm["phrase"] {
				fail(w, 422, "confirmation_required", "Type the confirmation phrase exactly.")
				return
			}
		}
		if b.Response.AlternativeID != "" {
			alts, _ := it["alternatives"].([]any)
			found := false
			for _, a := range alts {
				found = found || a.(map[string]any)["id"] == b.Response.AlternativeID
			}
			if !approve || !found {
				fail(w, 400, "invalid", "alternative_id must name one of the item's alternatives and goes with an approval.")
				return
			}
		}
		if it["reason_required"] == true && dec == "denied" && strings.TrimSpace(b.Response.Note) == "" {
			fail(w, 422, "reason_required", "Denying this item needs a reason.")
			return
		}
		if str(it, "group") == "secret" || r.Header.Get("X-Stub-Scenario") == "secret_ref_invalid" {
			var a map[string]json.RawMessage
			_ = json.Unmarshal(b.Response.Answers, &a)
			snap, _ := it["request_snapshot"].(map[string]any)
			fields, _ := snap["fields"].([]any)
			bad := r.Header.Get("X-Stub-Scenario") == "secret_ref_invalid"
			for _, f := range fields {
				fm, _ := f.(map[string]any)
				if fm["kind"] != "secret" {
					continue
				}
				id, _ := fm["id"].(string)
				var ans struct {
					Ref string `json:"secret_ref"`
				}
				if json.Unmarshal(a[id], &ans) != nil || !secretRefOK(ans.Ref, str(it, "item_id"), id) {
					bad = true
				}
			}
			if bad {
				fail(w, 422, "secret_ref_invalid", "Secret answers must be {\"secret_ref\": ...} from /secrets for this item and field.")
				return
			}
		}
		var ok item
		load("resolve_ok", &ok)
		ok["item_id"] = it["item_id"]
		ok["revision"] = it["revision"].(float64) + 1
		oc := ok["terminal_outcome"].(map[string]any)
		oc["decision"] = dec
		if b.Response.AlternativeID != "" {
			oc["alternative_id"] = b.Response.AlternativeID
		}
		write(w, 200, ok)
	})
}

type deferBody struct {
	ExpectedRevision int64  `json:"expected_revision"`
	DurationS        *int   `json:"duration_s"`
	UntilMs          *int64 `json:"until_ms"`
	Reason           string `json:"reason"`
}

func deferItem(w http.ResponseWriter, r *http.Request) {
	withItem(w, r, func(it item) {
		var b deferBody
		if _, ok := readBody(w, r, &b, 16<<10); !ok {
			return
		}
		if b.DurationS != nil && b.UntilMs != nil || b.ExpectedRevision < 1 || utf8.RuneCountInString(b.Reason) > 280 {
			fail(w, 400, "invalid", "Send expected_revision and one of duration_s or until_ms; reason is at most 280 characters.")
			return
		}
		now, _ := it["server_now_ms"].(float64)
		secs := int64(DefaultDeferS)
		if b.DurationS != nil {
			secs = int64(*b.DurationS)
		}
		if b.UntilMs != nil {
			secs = (*b.UntilMs - int64(now)) / 1000
		}
		if secs < MinDeferS || secs > MaxDeferS {
			fail(w, 400, "invalid", "The snooze must be between 60 seconds and 7 days.")
			return
		}
		if !pending(it) {
			write(w, 409, item{"code": "already_resolved", "message": "This item was already decided.", "terminal_outcome": it["terminal_outcome"], "revision": it["revision"]})
			return
		}
		if rev, _ := it["revision"].(float64); int64(rev) != b.ExpectedRevision {
			write(w, 409, item{"code": "stale_revision", "message": "The item changed. Review it again.", "revision": it["revision"]})
			return
		}
		until := int64(now) + secs*1000
		clamped := false
		if exp := str(it, "expires_at"); exp != "" {
			if t, err := time.Parse(time.RFC3339, exp); err == nil && t.UnixMilli() < until {
				until, clamped = t.UnixMilli(), true
			}
		}
		write(w, 200, item{"item_id": it["item_id"], "deferred_until": time.UnixMilli(until).UTC().Format(time.RFC3339), "expires_before_snooze_ends": clamped, "revision": it["revision"]})
	})
}

func undefer(w http.ResponseWriter, r *http.Request) {
	withItem(w, r, func(it item) {
		write(w, 200, item{"item_id": it["item_id"], "deferred_until": nil, "revision": it["revision"]})
	})
}

type batchBody struct {
	BatchID  string `json:"batch_id"`
	Decision string `json:"decision"`
	Note     string `json:"note"`
	Items    []struct {
		ItemID                      string `json:"item_id"`
		ExpectedRevision            int64  `json:"expected_revision"`
		PresentedProjectionRevision int64  `json:"presented_projection_revision"`
	} `json:"items"`
}

// batchResolve is N independent resolves in request order. Each item re-checks
// eligibility, revision and state, and one failure never stops the next.
// X-Stub-Fail-Item names an item whose resolve should fail with an error result.
func batchResolve(w http.ResponseWriter, r *http.Request) {
	var b batchBody
	raw, ok := readBody(w, r, &b, 64<<10)
	if !ok {
		return
	}
	if !elevated(r) {
		fail(w, 403, "operator_access_required", "A batch needs elevated operator access.")
		return
	}
	if b.BatchID == "" || len(b.BatchID) > 64 || b.Decision != "approved" && b.Decision != "denied" || len(b.Items) < 1 || len(b.Items) > MaxBatchItems {
		fail(w, 400, "invalid", "batch_id, decision (approved|denied) and 1..25 items are required.")
		return
	}
	seen := map[string]bool{}
	for _, it := range b.Items {
		if it.ItemID == "" || it.ExpectedRevision < 1 || seen[it.ItemID] {
			fail(w, 400, "invalid", "Each item needs item_id and expected_revision, and appears once.")
			return
		}
		seen[it.ItemID] = true
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	batchMu.Lock()
	memo, replay := batchSeen[b.BatchID]
	batchMu.Unlock()
	if replay {
		if memo.digest != digest {
			fail(w, 409, "batch_id_reuse", "This batch_id was already used with a different request.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Tachyon-Batch-Replay", "1")
		_, _ = w.Write([]byte(memo.response))
		return
	}
	all := items()
	counts := map[string]int{}
	results := make([]item, 0, len(b.Items))
	for _, in := range b.Items {
		res := item{"item_id": in.ItemID}
		it, found := all[in.ItemID]
		switch {
		case !found:
			res["status"], res["error"] = "error", item{"code": "not_found", "message": "No such item."}
		case !pending(it):
			res["status"], res["revision"] = "conflict", it["revision"]
			if oc, _ := it["terminal_outcome"].(map[string]any); oc != nil {
				res["decision"] = oc["decision"]
			}
			if str(it, "state") == "expired" {
				res["status"] = "expired"
			}
		case it["batchable"] != true:
			res["status"], res["blockers"] = "ineligible", it["batch_blockers"]
			if bl, _ := it["batch_blockers"].([]any); slices.Contains(bl, any(BlockExpired)) {
				res["status"] = "expired"
			}
		case it["revision"].(float64) != float64(in.ExpectedRevision):
			res["status"], res["revision"] = "stale", it["revision"]
			res["error"] = item{"code": "stale_revision", "message": "The item changed. Review it again."}
		case r.Header.Get("X-Stub-Fail-Item") == in.ItemID:
			res["status"], res["error"] = "error", item{"code": "internal", "message": "The resolve failed. It stays in the queue."}
		default:
			res["status"], res["decision"], res["revision"] = "resolved", b.Decision, it["revision"].(float64)+1
			res["resolution_id"], res["run_state"] = "res_"+in.ItemID, "none"
		}
		counts[res["status"].(string)]++
		results = append(results, res)
	}
	body, _ := json.Marshal(item{"batch_id": b.BatchID, "results": results, "counts": counts, "completed_at": time.Now().UTC().Format(time.RFC3339)})
	batchMu.Lock()
	if len(batchSeen) < 256 {
		batchSeen[b.BatchID] = batchMemo{digest, string(body)}
	}
	batchMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// history filters: status (approved|denied|answered|expired|withdrawn), requester, q, from, to, limit, cursor.
func history(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := "history"
	switch q.Get("cursor") {
	case "":
	case "h2":
		page = "history_page2"
	default:
		fail(w, 400, "invalid", "Unknown cursor.")
		return
	}
	switch q.Get("status") {
	case "", "approved", "denied", "answered", "expired", "withdrawn":
	default:
		fail(w, 400, "invalid", "status must be approved, denied, answered, expired or withdrawn.")
		return
	}
	var d struct {
		Events     []item  `json:"events"`
		NextCursor *string `json:"next_cursor"`
		Total      int     `json:"total"`
	}
	load(page, &d)
	text := strings.ToLower(q.Get("q"))
	out := d.Events[:0:0]
	for _, e := range d.Events {
		mini, _ := e["mini"].(map[string]any)
		at := str(e, "at")
		if v := q.Get("status"); v != "" && e["status"] != v || q.Get("requester") != "" && !strings.Contains(strings.ToLower(fmt.Sprint(e["requester"])), strings.ToLower(q.Get("requester"))) ||
			text != "" && !strings.Contains(strings.ToLower(fmt.Sprint(mini["title"], " ", e["item_id"], " ", e["note"])), text) ||
			q.Get("from") != "" && at < q.Get("from") || q.Get("to") != "" && at > q.Get("to") {
			continue
		}
		out = append(out, e)
	}
	d.Events = out
	write(w, 200, d)
}

func access(w http.ResponseWriter, r *http.Request) {
	name := "access_elevated"
	if !elevated(r) {
		name = "access_none"
	}
	var v item
	load(name, &v)
	write(w, 200, v)
}

// secretMemo is the stub's only secret memory: minted refs (never values) and Idempotency-Key replays.
type secretMemo struct {
	digest string
	status int
	body   []byte
}

var (
	secretMu   sync.Mutex
	secretSeq  int
	secretRefs = map[string][2]string{"secret://tachyon/sec_01hqexample": {"ap_secret", "deploy_token"}}
	secretIdem = map[string]secretMemo{}
)

func secretRefOK(ref, itemID, field string) bool {
	secretMu.Lock()
	defer secretMu.Unlock()
	b, ok := secretRefs[ref]
	return ok && b == [2]string{itemID, field}
}

type secretBody struct {
	ItemID string `json:"item_id"`
	Field  string `json:"field"`
	Value  string `json:"value"`
}

// readSecret takes JSON {item_id, field, value<=16 KiB} or multipart (item_id, field, file<=64 KiB).
// It returns the size and a digest of the request for Idempotency-Key checks; the value itself is dropped.
func readSecret(w http.ResponseWriter, r *http.Request) (b secretBody, size int, digest string, ok bool) {
	h := sha256.New()
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, MaxSecretFileSize+8<<10)
		if err := r.ParseMultipartForm(MaxSecretFileSize + 8<<10); err != nil {
			if strings.Contains(err.Error(), "too large") {
				fail(w, 413, "payload_too_large", "A secret file is at most 64 KiB.")
			} else {
				fail(w, 400, "invalid", "Send multipart fields item_id, field and a file.")
			}
			return
		}
		b.ItemID, b.Field = r.FormValue("item_id"), r.FormValue("field")
		f, _, err := r.FormFile("file")
		if err != nil {
			fail(w, 400, "invalid", "Send the secret as the file part.")
			return
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, MaxSecretFileSize+1))
		if err != nil || len(data) == 0 {
			fail(w, 400, "invalid", "The file is empty.")
			return
		}
		if len(data) > MaxSecretFileSize {
			fail(w, 413, "payload_too_large", "A secret file is at most 64 KiB.")
			return
		}
		h.Write(data)
		size = len(data)
	} else {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxSecretBytes+8<<10))
		if err != nil {
			fail(w, 413, "payload_too_large", "A secret value is at most 16 KiB.")
			return
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if dec.Decode(&b) != nil || len(b.Value) == 0 {
			fail(w, 400, "invalid", "Send item_id, field and a non-empty value.")
			return
		}
		if len(b.Value) > MaxSecretBytes {
			fail(w, 413, "payload_too_large", "A secret value is at most 16 KiB.")
			return
		}
		h.Write([]byte(b.Value))
		size = len(b.Value)
	}
	h.Write([]byte("|" + b.ItemID + "|" + b.Field))
	return b, size, hex.EncodeToString(h.Sum(nil)), true
}

// secrets mints a fake reference and stores nothing; the value is never echoed or kept. Scenarios
// (X-Stub-Scenario): secrets_unavailable 503, not_elevated 403 assurance_insufficient with step_up,
// item_not_pending 409, secret_field_unknown 409, rate_limited 429.
func secrets(w http.ResponseWriter, r *http.Request) {
	switch r.Header.Get("X-Stub-Scenario") {
	case "secrets_unavailable":
		var v item
		load("secrets_unavailable", &v)
		write(w, 503, v)
		return
	case "not_elevated":
		write(w, 403, item{"code": "assurance_insufficient", "message": "Enter your operator key to submit a secret.",
			"step_up": item{"required": "operator-token", "can_elevate": true, "elevate_path": Prefix + "/elevate"}})
		return
	case "rate_limited":
		fail(w, 429, "rate_limited", "At most 10 secrets a minute.")
		return
	case "item_not_pending":
		fail(w, 409, "item_not_pending", "That request was already decided or has expired.")
		return
	case "secret_field_unknown":
		fail(w, 409, "secret_field_unknown", "That request has no secret field with that id.")
		return
	}
	b, size, digest, ok := readSecret(w, r)
	if !ok {
		return
	}
	it, found := items()[b.ItemID]
	if !found {
		fail(w, 404, "not_found", "No such item.")
		return
	}
	if !pending(it) {
		fail(w, 409, "item_not_pending", "That request was already decided or has expired.")
		return
	}
	snap, _ := it["request_snapshot"].(map[string]any)
	fields, _ := snap["fields"].([]any)
	has := false
	for _, f := range fields {
		fm, _ := f.(map[string]any)
		has = has || fm["id"] == b.Field && fm["kind"] == "secret"
	}
	if !has {
		fail(w, 409, "secret_field_unknown", "That request has no secret field with that id.")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > 128 {
		fail(w, 400, "invalid", "Idempotency-Key is at most 128 characters.")
		return
	}
	secretMu.Lock()
	defer secretMu.Unlock()
	if key != "" {
		if memo, seen := secretIdem[key]; seen {
			if memo.digest != digest {
				fail(w, 409, "idempotency_key_reuse", "That Idempotency-Key was used for a different secret.")
				return
			}
			w.Header().Set("Idempotent-Replay", "true")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(memo.status)
			_, _ = w.Write(memo.body)
			return
		}
	}
	secretSeq++
	var v item
	load("secret_ref", &v)
	ref := fmt.Sprintf("secret://tachyon/sec_stub%04d", secretSeq)
	v["secret_ref"], v["size"] = ref, size
	secretRefs[ref] = [2]string{b.ItemID, b.Field}
	body, _ := json.Marshal(v)
	if key != "" {
		secretIdem[key] = secretMemo{digest: digest, status: 201, body: append(body, '\n')}
	}
	write(w, 201, v)
}

type scripted struct {
	ID   int64          `json:"id"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// events replays the scripted stream after Last-Event-ID (or ?after=). An id the
// ring no longer holds (past the end), or X-Stub-Scenario: gap, gets one resync
// event telling the client to refetch /inbox.
func events(w http.ResponseWriter, r *http.Request) {
	var evs []scripted
	load("events", &evs)
	var last int64
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("after")
	}
	if v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			fail(w, 400, "invalid", "Last-Event-ID must be a non-negative integer.")
			return
		}
		last = n
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "internal", "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Tachyon-Approvals-Stub", "1")
	w.WriteHeader(200)
	base := time.Now().UnixMilli()
	if r.Header.Get("X-Stub-Scenario") == "gap" || last > evs[len(evs)-1].ID {
		body, _ := json.Marshal(item{"type": "resync", "at": base, "reason": "gap"})
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", evs[len(evs)-1].ID, body)
	} else {
		for i, e := range evs {
			if e.ID <= last {
				continue
			}
			env := item{"type": e.Type, "at": base + int64(i)}
			for k, v := range e.Data {
				env[k] = v
			}
			body, _ := json.Marshal(env)
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.ID, body)
		}
	}
	fl.Flush()
	if r.URL.Query().Get("once") == "1" {
		return
	}
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}

// Handler is the stub as one http.Handler.
func (s Stub) Handler() http.Handler { return s.Mux() }
