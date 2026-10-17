package chatbridge

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	tether "github.com/hollis-labs/libs/mesh/core/tetherclient"
	"net/http"
	"strings"
	"time"
)

func (o *Owned) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, Prefix)
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case path == "/inbox" && r.Method == "GET":
		if strings.Contains(r.Header.Get("Cache-Control"), "no-cache") {
			if err := o.Sync(r.Context()); err != nil {
				o.httpError(w, err)
				return
			}
		}
		inbox, err := o.Inbox(r.Context())
		if err != nil {
			o.httpError(w, err)
			return
		}
		state := "live"
		if inbox.Problem != "" {
			state = "stale"
			w.Header().Set("X-Tachyon-Cache-Error", inbox.Problem)
		}
		w.Header().Set("X-Tachyon-Source", "chat-turns")
		w.Header().Set("X-Tachyon-Cache", state)
		w.Header().Set("X-Tachyon-Updated-At", inbox.SyncedAt)
		w.Header().Set("X-Tachyon-Cache-Enabled", fmt.Sprint(o.CacheEnabled))
		w.Header().Set("X-Tachyon-Fresh-Seconds", fmt.Sprint(o.FreshSeconds))
		w.Header().Set("X-Tachyon-Revalidate-Seconds", fmt.Sprint(o.RevalidateSeconds))
		write(w, inbox)
	case replyPath.MatchString(path) && r.Method == "POST":
		r.Body = http.MaxBytesReader(w, r.Body, 65536)
		var input struct {
			Revision int64  `json:"expected_revision"`
			Action   string `json:"action"`
			Text     string `json:"response_text"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&input); err != nil || input.Action != "respond" {
			fail(w, 400, "invalid_args", "A response and expected revision are required")
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/items/"), "/reply")
		t, err := o.Reply(r.Context(), id, input.Revision, input.Text)
		if err != nil {
			o.httpError(w, err)
			return
		}
		write(w, t)
	default:
		fail(w, 404, "not_found", "Unknown chat history operation")
	}
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func (o *Owned) httpError(w http.ResponseWriter, err error) {
	status, code, message := 502, problem(err), "Could not reach Tether. Your conversation is saved in Tachyon."
	var api *tether.APIError
	switch {
	case errors.Is(err, sql.ErrNoRows):
		status, code, message = 404, "not_found", "Conversation item was not found"
	case errors.Is(err, ErrConflict), errors.Is(err, ErrNotReplyable):
		status, code, message = 409, "conflict", err.Error()
	case errors.As(err, &api):
		status = api.StatusCode
		message = "Tether refused the request (" + api.Code + "). Check chat permissions and session state."
	case code == "configuration":
		status = 503
		message = "Check Tachyon's Tether address and credential configuration. Saved conversations remain available."
	}
	w.Header().Set("X-Tachyon-Source", "chat-turns")
	w.Header().Set("X-Tachyon-Cache", "error")
	w.Header().Set("X-Tachyon-Cache-Error", code)
	w.Header().Set("X-Tachyon-Checked-At", time.Now().UTC().Format(time.RFC3339Nano))
	fail(w, status, code, message)
}
