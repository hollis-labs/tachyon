// Package approvals owns the durable human approval queue. The legacy Bridge
// supplies browser session and elevation verification during the custody cutover.
package approvals

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

const Prefix = "/api/plugins/console/v1/approval-inbox"
const CookieName = "tachyon_approvals"
const CookiePath = "/api/plugins/console/v1/"

var itemPath = regexp.MustCompile(`^/items/[a-zA-Z0-9_-]+$`)
var mutationPath = regexp.MustCompile(`^/items/[a-zA-Z0-9_-]+/(present|resolve)$`)
var evidencePath = regexp.MustCompile(`^/items/[a-zA-Z0-9_-]+/evidence/[0-9]+/(reference|preview)$`)

type Bridge struct {
	URL    string
	Client *http.Client
}

func New(addr string) *Bridge {
	return &Bridge{URL: strings.TrimRight(addr, "/"), Client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u, err := url.Parse(b.URL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || upstreamauth.Guard(b.URL, "participant") != nil {
		fail(w, 503, "configuration", "Configure Tachyon's approval queue address (HTTPS or loopback).")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, Prefix)
	bootstrap := path == "/session" && r.Method == http.MethodPost
	target := ""
	switch {
	case bootstrap:
		target = "/hitl"
	case path == "/status" && r.Method == http.MethodGet:
		target = "/api/session"
	case path == "/elevate" && r.Method == http.MethodPost:
		target = "/api/session/elevate"
	case path == "/de-elevate" && r.Method == http.MethodPost:
		target = "/api/session/de-elevate"
	case path == "/inbox" && r.Method == http.MethodGet:
		target = "/api/hitl"
	case (itemPath.MatchString(path) || evidencePath.MatchString(path)) && r.Method == http.MethodGet:
		target = "/api/hitl" + path
	case mutationPath.MatchString(path) && r.Method == http.MethodPost:
		target = "/api/hitl" + path
	default:
		fail(w, 404, "not_found", "Unknown approval operation")
		return
	}
	// Validate the browser origin before adding the upstream Origin. Otherwise a
	// proxy would turn a cross-site POST into an apparently trusted decision.
	if r.Method == http.MethodPost {
		origin, e := url.Parse(r.Header.Get("Origin"))
		if e != nil || origin.Host != r.Host || (origin.Scheme != "http" && origin.Scheme != "https") || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			fail(w, 403, "origin_denied", "Approval actions require the Tachyon browser origin.")
			return
		}
	}
	method := r.Method
	var body io.Reader
	if bootstrap {
		method = http.MethodGet
	} else if r.Method == http.MethodPost {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			fail(w, 415, "invalid_request", "Use a JSON approval command.")
			return
		}
		data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<10))
		if e != nil || !json.Valid(data) {
			fail(w, 400, "invalid_request", "Invalid or oversized approval command.")
			return
		}
		body = strings.NewReader(string(data))
	}
	req, e := http.NewRequestWithContext(r.Context(), method, b.URL+target, body)
	if e != nil {
		fail(w, 503, "configuration", "Invalid approval address.")
		return
	}
	if c, e := r.Cookie(CookieName); e == nil && c.Value != "" {
		req.AddCookie(&http.Cookie{Name: "tangent_participant", Value: c.Value})
	} else if !bootstrap {
		fail(w, 401, "session_required", "Connect the approval queue first.")
		return
	}
	req.Header.Set("Accept", "application/json")
	if bootstrap {
		req.Header.Set("Accept", "text/html")
	} else if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", u.Scheme+"://"+u.Host)
	}
	res, e := b.Client.Do(req)
	if e != nil {
		fail(w, 502, "unreachable", "Could not reach the approval queue. No decision is confirmed.")
		return
	}
	defer res.Body.Close()
	// Elevation rotates the upstream session. Forward the replacement too,
	// not just the cookie originally minted during bootstrap.
	connected := false
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		for _, c := range res.Cookies() {
			if c.Name != "tangent_participant" {
				continue
			}
			c.Name = CookieName
			c.Path = CookiePath
			c.Domain = ""
			c.HttpOnly = true
			c.SameSite = http.SameSiteStrictMode
			c.Secure = r.TLS != nil || strings.HasPrefix(r.Header.Get("Origin"), "https://")
			http.SetCookie(w, c)
			// Remove the earlier approvals-only cookie, which otherwise shadows it.
			http.SetCookie(w, &http.Cookie{Name: CookieName, Path: Prefix, Value: "", MaxAge: -1, HttpOnly: true, Secure: c.Secure, SameSite: http.SameSiteStrictMode})
			connected = true
		}
	}
	if bootstrap {
		if res.StatusCode != 200 {
			fail(w, 502, "session_unavailable", "The approval queue could not establish a participant session.")
			return
		}
		if !connected {
			c, e := r.Cookie(CookieName)
			if e != nil {
				fail(w, 502, "session_unavailable", "The approval queue did not issue a participant session.")
				return
			}
			c.Path = CookiePath
			c.HttpOnly = true
			c.SameSite = http.SameSiteStrictMode
			c.Secure = r.TLS != nil || strings.HasPrefix(r.Header.Get("Origin"), "https://")
			http.SetCookie(w, c)
			http.SetCookie(w, &http.Cookie{Name: CookieName, Path: Prefix, MaxAge: -1, HttpOnly: true, Secure: c.Secure, SameSite: http.SameSiteStrictMode})
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"connected":true}`))
		return
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if e != nil || len(data) > 8<<20 || !json.Valid(data) {
		fail(w, 502, "invalid_response", "The approval queue returned an unreadable response. Refresh to check the outcome.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(data)
}
