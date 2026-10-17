// Package chatbridge contains the temporary Tangent turns adapter. Tether
// remains the session runtime; Tangent retains turn custody during migration.
package chatbridge

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const Prefix = "/api/plugins/console/v1/chat-turns"
const CookieName = "tachyon_chat_turns"

var replyPath = regexp.MustCompile(`^/items/[a-zA-Z0-9_-]+/reply$`)

type Turns struct {
	URL    string
	Client *http.Client
}

func NewTurns(addr string) *Turns {
	return &Turns{URL: strings.TrimRight(addr, "/"), Client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}
func (t *Turns) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u, err := url.Parse(t.URL)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		fail(w, 503, "configuration", "Configure the Tangent turns service address")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, Prefix)
	bootstrap := path == "/session" && r.Method == "POST"
	target := ""
	switch {
	case bootstrap:
		target = "/chat"
	case path == "/inbox" && r.Method == "GET":
		target = "/api/turns"
	case replyPath.MatchString(path) && r.Method == "POST":
		target = "/api/turns" + path
	default:
		fail(w, 404, "not_found", "Unknown turns operation")
		return
	}
	method := r.Method
	if bootstrap {
		method = "GET"
	}
	req, err := http.NewRequestWithContext(r.Context(), method, t.URL+target, r.Body)
	if err != nil {
		fail(w, 503, "configuration", "Invalid turns service address")
		return
	}
	// Each browser keeps its own upstream participant. No shared operator cookie,
	// no incoming Authorization, and no unrelated browser cookies cross the bridge.
	if c, e := r.Cookie(CookieName); e == nil {
		req.AddCookie(&http.Cookie{Name: "tangent_participant", Value: c.Value})
	} else if !bootstrap {
		fail(w, 401, "session_required", "Connect the conversation history first")
		return
	}
	req.Header.Set("Accept", "application/json")
	if bootstrap {
		req.Header.Set("Accept", "text/html")
	} else if r.Method == "POST" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", u.Scheme+"://"+u.Host)
	}
	res, err := t.Client.Do(req)
	if err != nil {
		fail(w, 502, "unreachable", "Could not reach Tangent conversation history")
		return
	}
	defer res.Body.Close()
	w.Header().Set("Cache-Control", "no-store")
	if bootstrap {
		if res.StatusCode != 200 {
			fail(w, res.StatusCode, "session_unavailable", "Tangent did not establish a conversation session")
			return
		}
		for _, c := range res.Cookies() {
			if c.Name == "tangent_participant" {
				c.Name = CookieName
				c.Path = Prefix
				c.Domain = ""
				c.HttpOnly = true
				c.SameSite = http.SameSiteLaxMode
				http.SetCookie(w, c)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"connected":true}`))
		return
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20+1))
	if err != nil || len(body) > 8<<20 {
		fail(w, 502, "invalid_response", "Conversation history response is unreadable or oversized")
		return
	}
	if !json.Valid(body) {
		fail(w, 502, "invalid_response", "Conversation history did not return JSON")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(body)
}
