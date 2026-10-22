package approvals

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// RequireOperator protects Tachyon-owned mutations with the same actual
// participant assurance as Tangent. Never trust a browser-supplied flag or
// a cached status; Tangent enforces the configured elevation lifetime on every lookup.
func (b *Bridge) RequireOperator(next http.Handler) http.Handler { return b.require(next, false) }

// RequireOperatorReads is RequireOperator for routes whose reads are
// sensitive: GET and HEAD need the same operator assurance as a mutation.
// A read carries no Origin header, so only a cross-site fetch is refused
// before the assurance lookup.
func (b *Bridge) RequireOperatorReads(next http.Handler) http.Handler { return b.require(next, true) }

func (b *Bridge) require(next http.Handler, reads bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read := r.Method == http.MethodGet || r.Method == http.MethodHead
		if read && !reads {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if read {
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				fail(w, 403, "origin_denied", "Use the Tachyon browser to perform this action.")
				return
			}
		} else {
			origin, e := url.Parse(r.Header.Get("Origin"))
			if e != nil || origin.Host != r.Host || (origin.Scheme != "https" && origin.Scheme != "http") || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				fail(w, 403, "origin_denied", "Use the Tachyon browser to perform this action.")
				return
			}
		}
		// Reuse the fixed-origin, bounded bridge; this read is never cached.
		check := r.Clone(r.Context())
		u := *r.URL
		u.Path = Prefix + "/status"
		u.RawPath = ""
		u.RawQuery = ""
		check.URL = &u
		check.Method = http.MethodGet
		check.Body = nil
		captured := &statusCapture{header: make(http.Header), status: 200}
		b.ServeHTTP(captured, check)
		if captured.status != 200 {
			if captured.status == 401 || captured.status == 403 {
				fail(w, captured.status, "operator_session_required", "Connect your operator session before continuing.")
			} else {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(captured.status)
				_, _ = w.Write([]byte(captured.body.String()))
			}
			return
		}
		var status struct {
			sessionStatus
		}
		if json.Unmarshal([]byte(captured.body.String()), &status) != nil || !status.Elevated || status.Assurance != "operator-token" {
			fail(w, 403, "assurance_insufficient", "Enter your operator key to continue. "+ElevationCopy(status.ElevationTTL, status.ElevationExpiresAt))
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusCapture struct {
	header http.Header
	status int
	body   strings.Builder
}

func (c *statusCapture) Header() http.Header         { return c.header }
func (c *statusCapture) WriteHeader(status int)      { c.status = status }
func (c *statusCapture) Write(b []byte) (int, error) { return c.body.Write(b) }
