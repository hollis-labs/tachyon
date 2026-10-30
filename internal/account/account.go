// Package account is the operator's whoami and local profile (ST3). The profile is one local record for the
// whole Tachyon (there is no user model) and is never an authorization input: "verified" in the identity only
// means the operator token is in use on this session, not that the name or email were checked.
package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/settingsapi"
)

// ProfileKey is the app_settings row holding the profile.
const ProfileKey = "account.profile"

type stored struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

// Service reads and writes the profile and answers whoami. Provider supplies the session's assurance.
type Service struct {
	DB       *localstore.Store
	Provider assurance.Provider
	Now      func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) load(ctx context.Context) (*stored, error) {
	var raw string
	err := s.DB.Reader.QueryRowContext(ctx, "SELECT value FROM app_settings WHERE key=?", ProfileKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p stored
	if json.Unmarshal([]byte(raw), &p) != nil || p.DisplayName == "" {
		return nil, nil // a damaged row reads as no profile; the next save replaces it
	}
	return &p, nil
}

// Mount registers one explicit pattern per route. Both need a browser session through the Provider; the PUT also
// needs a same-origin browser request. Neither needs the operator token (assurance.ActionAccountWrite is
// browser-session).
func (s *Service) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET "+settingsapi.AccountPath, s.get)
	mux.HandleFunc("PUT "+settingsapi.ProfilePath, s.put)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"code": code, "message": msg})
}

func refuse(w http.ResponseWriter, e *assurance.Error) {
	if e.Body != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(e.Status)
		_, _ = w.Write([]byte(e.Body))
		return
	}
	fail(w, e.Status, e.Code, e.Message)
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	st, err := s.Provider.Require(r, assurance.ActionRead)
	if err != nil {
		refuse(w, err)
		return
	}
	s.respond(w, r, st)
}

func (s *Service) respond(w http.ResponseWriter, r *http.Request, st assurance.Status) {
	p, e := s.load(r.Context())
	if e != nil {
		fail(w, 503, "account_unavailable", "The profile could not be read.")
		return
	}
	now := s.now()
	out := settingsapi.Account{Access: settingsapi.AccessFor(st, now), CanSetProfile: true,
		Identity: settingsapi.Identity{State: "unknown", Source: "session", Assurance: "local"}}
	if p != nil {
		out.Identity = settingsapi.Identity{State: "identified", DisplayName: p.DisplayName, Email: p.Email, Source: "local-profile", Assurance: "local"}
		out.ProfileUpdatedAt = p.UpdatedAt
	}
	if st.Elevated() {
		out.Identity.Assurance, out.Identity.VerifiedBy = "verified", settingsapi.MethodOperatorToken
	}
	writeJSON(w, 200, out)
}

func sameOrigin(r *http.Request) bool {
	o, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && o.Host == r.Host && (o.Scheme == "https" || o.Scheme == "http") && o.User == nil && o.Path == "" &&
		o.RawQuery == "" && o.Fragment == "" && r.Header.Get("Sec-Fetch-Site") != "cross-site"
}

func (s *Service) put(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		fail(w, 403, settingsapi.CodeOriginDenied, "Use the Tachyon browser origin.")
		return
	}
	st, err := s.Provider.Require(r, assurance.ActionAccountWrite)
	if err != nil {
		refuse(w, err)
		return
	}
	var in settingsapi.Profile
	dec := json.NewDecoder(io.LimitReader(r.Body, settingsapi.MaxProfileBytes+1))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || dec.More() {
		fail(w, 400, "malformed_input", "Send {display_name, email?} as JSON.")
		return
	}
	p, errs := settingsapi.ValidateProfile(in)
	if len(errs) > 0 {
		writeJSON(w, 422, map[string]any{"error": map[string]any{"code": settingsapi.CodeValidationFailed, "message": "The profile is invalid.", "errors": errs}})
		return
	}
	raw, _ := json.Marshal(stored{DisplayName: p.DisplayName, Email: p.Email, UpdatedAt: s.now().UTC().Format(time.RFC3339)})
	if e := s.DB.Writer.Submit(r.Context(), "account_profile", func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "INSERT INTO app_settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", ProfileKey, string(raw))
		return e
	}); e != nil {
		fail(w, 503, "account_unavailable", "The profile could not be saved.")
		return
	}
	s.respond(w, r, st)
}
