package approvals

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

// The operator elevation lifetime is an operator setting, but Tangent enforces
// it: Tachyon stores the value and hands it to Tangent as TANGENT_ELEVATION_TTL
// when Tangent is launched (`tachyon elevation-ttl env`). The setting takes
// effect when Tangent restarts. Tachyon holds no timer of its own; it mirrors
// the elevation_ttl and elevation_expires_at fields of Tangent's /api/session.
const (
	ElevationTTLKey     = "approvals.elevation_ttl"
	ElevationTTLEnv     = "TANGENT_ELEVATION_TTL"
	DefaultElevationTTL = "always"
	maxElevationDays    = 365
	// ElevationSettingsPath must stay inside CookiePath: the operator cookie
	// is not sent to any other path.
	ElevationSettingsPath = CookiePath + "settings/elevation-ttl"
)

// ParseElevationTTL validates and normalizes always | never | <N>d.
func ParseElevationTTL(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "always" || v == "never" {
		return v, nil
	}
	if n, ok := strings.CutSuffix(v, "d"); ok {
		days, err := strconv.Atoi(n)
		if err == nil && strconv.Itoa(days) == n && days >= 1 && days <= maxElevationDays {
			return v, nil
		}
	}
	return "", fmt.Errorf("elevation lifetime must be always, never, or <N>d with N from 1 to %d", maxElevationDays)
}

// ElevationSettings persists the lifetime in the local app_settings table.
type ElevationSettings struct{ DB *localstore.Store }

func (s ElevationSettings) Get(ctx context.Context) (string, error) {
	var saved string
	err := s.DB.Reader.QueryRowContext(ctx, "SELECT value FROM app_settings WHERE key=?", ElevationTTLKey).Scan(&saved)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultElevationTTL, nil
	}
	if err != nil {
		return "", err
	}
	return ParseElevationTTL(saved)
}

func (s ElevationSettings) Set(ctx context.Context, raw string) (string, error) {
	v, err := ParseElevationTTL(raw)
	if err != nil {
		return "", err
	}
	return v, s.DB.Writer.Submit(ctx, "elevation_ttl", func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "INSERT INTO app_settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", ElevationTTLKey, v)
		return e
	})
}

// Handler serves GET (read) and PUT {"elevation_ttl": "..."} (write). The
// server mounts PUT behind RequireOperator.
func (s ElevationSettings) Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		var body struct {
			ElevationTTL string `json:"elevation_ttl"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
			fail(w, 400, "invalid_request", "Send {\"elevation_ttl\": \"always|never|<N>d\"}.")
			return
		}
		if _, err := s.Set(r.Context(), body.ElevationTTL); err != nil {
			fail(w, 400, "invalid_request", err.Error())
			return
		}
	default:
		fail(w, 405, "method_not_allowed", "Use GET or PUT.")
		return
	}
	v, err := s.Get(r.Context())
	if err != nil {
		fail(w, 500, "settings_unavailable", "Could not read the elevation lifetime.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"elevation_ttl": v, "enforced_by": "tangent", "applies": "when Tangent restarts with " + ElevationTTLEnv})
}

// ElevationCopy describes the lifetime for a refusal or confirmation message.
// expiresAt is Tangent's stamped end for a live elevation (empty otherwise).
func ElevationCopy(ttl, expiresAt string) string {
	if expiresAt != "" {
		if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			return "Elevation lasts until " + t.UTC().Format("2006-01-02 15:04 UTC") + "."
		}
	}
	switch v, _ := ParseElevationTTL(ttl); {
	case v == "always":
		return "Elevation lasts until you revoke it or the browser session ends."
	case v == "never":
		return "Elevation is not remembered; it lasts about a minute."
	case v != "":
		days := strings.TrimSuffix(v, "d")
		if days == "1" {
			return "Elevation lasts 1 day."
		}
		return "Elevation lasts " + days + " days."
	}
	return "Elevation lifetime is set by the authorization service."
}

// ElevationCommand is `tachyon elevation-ttl [get | set <value> | env]`.
// `env` prints the TANGENT_ELEVATION_TTL assignment for Tangent's launcher.
func ElevationCommand(ctx context.Context, dataDir string, args []string, out io.Writer) error {
	if len(args) == 0 {
		args = []string{"get"}
	}
	db, err := localstore.Open(ctx, filepath.Join(dataDir, "tachyon.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	s := ElevationSettings{DB: db}
	switch {
	case args[0] == "get" && len(args) == 1:
		v, err := s.Get(ctx)
		if err == nil {
			_, err = fmt.Fprintln(out, v)
		}
		return err
	case args[0] == "env" && len(args) == 1:
		v, err := s.Get(ctx)
		if err == nil {
			_, err = fmt.Fprintf(out, "%s=%s\n", ElevationTTLEnv, v)
		}
		return err
	case args[0] == "set" && len(args) == 2:
		v, err := s.Set(ctx, args[1])
		if err == nil {
			_, err = fmt.Fprintf(out, "%s\nRestart Tangent with %s=%s for this to take effect.\n", v, ElevationTTLEnv, v)
		}
		return err
	}
	return errors.New("usage: tachyon elevation-ttl [get | env | set always|never|<N>d]")
}
