package provideractions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/teams"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

type Failure struct {
	Message  string
	NoEffect bool
}

func (f *Failure) Error() string { return f.Message }

type tether struct {
	target, identity, base, token string
	client                        *http.Client
}

func (t *tether) Target() string   { return t.target }
func (t *tether) Identity() string { return t.identity }

// Tether binds one validated credential snapshot to an exact configured origin.
// Neither the token nor its fingerprint is returned to the browser.
func Tether(addr, tokenPath string) func() (Bound, error) {
	return func() (Bound, error) {
		addr := strings.TrimRight(strings.TrimSpace(addr), "/")
		if addr == "" {
			return nil, fmt.Errorf("missing Tether address")
		}
		if err := upstreamauth.Guard(addr, tokenPath); err != nil {
			return nil, err
		}
		token := ""
		if tokenPath != "" {
			var err error
			token, err = tokenfile.NewWatcher(tokenPath, tokenfile.Options{Validate: func(s string) bool {
				if s == "" || len(s) > 256 {
					return false
				}
				for _, c := range s {
					if c <= 32 || c >= 127 {
						return false
					}
				}
				return true
			}}, nil).Token()
			if err != nil {
				return nil, err
			}
		}
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.MaxResponseHeaderBytes = 64 << 10
		base := addr
		if socket, ok := strings.CutPrefix(addr, "unix:"); ok {
			if socket == "" {
				return nil, fmt.Errorf("missing socket")
			}
			tr.Proxy = nil
			tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
			}
			base = "http://localhost"
		} else if rest, ok := strings.CutPrefix(addr, "tcp:"); ok {
			base = "http://" + rest
		}
		u, err := url.Parse(base)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("invalid Tether address")
		}
		return &tether{target: addr, identity: digest([]byte(addr + "\n" + token)), base: base, token: token, client: &http.Client{Transport: tr, Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
	}
}
func (t *tether) Form(ctx context.Context, key string, payload json.RawMessage) (json.RawMessage, error) {
	defer t.client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/teams/form", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Tether-Protocol", "1")
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("Content-Type", "application/json")
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	res, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		message := fmt.Sprintf("Tether returned HTTP %d without confirming formation. Reconcile or retry this same request.", res.StatusCode)
		if res.StatusCode == 401 || res.StatusCode == 403 {
			message = "Tether refused the caller or its permissions. Check the host credential and Tether team policy before retrying this same request."
		}
		return nil, &Failure{Message: message}
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (256<<10)+1))
	if err != nil || len(raw) > 256<<10 {
		return nil, &Failure{Message: "Tether returned an unreadable receipt. Retry this same request to recover its outcome."}
	}
	var result struct {
		Run         *teams.TeamRun `json:"run"`
		Self        string         `json:"self,omitempty"`
		Coordinator string         `json:"coordinator,omitempty"`
		Channel     string         `json:"channel,omitempty"`
		Members     []struct {
			ID     string `json:"id"`
			Slot   string `json:"slot"`
			Actor  string `json:"actor"`
			Status string `json:"status"`
			Kind   string `json:"kind"`
		} `json:"members,omitempty"`
	}
	var request struct {
		Team teams.Team `json:"team"`
	}
	_ = json.Unmarshal(payload, &request)
	if json.Unmarshal(raw, &result) != nil || result.Run == nil || result.Run.ID == "" || result.Run.TeamID != request.Team.ID || result.Run.TeamVersion != request.Team.Version {
		return nil, &Failure{Message: "Tether returned an unexpected receipt. Retry this same request to recover its outcome."}
	}
	return json.Marshal(result)
}

// Execute uses a closed operation list. No browser input can select an origin,
// path, credential, HTTP method or caller identity.
func (t *tether) Execute(ctx context.Context, verb, key string, payload json.RawMessage) (json.RawMessage, error) {
	if verb == "catalog.plan" || verb == "catalog.apply" {
		return t.catalogExecute(ctx, verb, payload)
	}
	if verb == "teams.form" {
		return t.Form(ctx, key, payload)
	}
	var path string
	switch verb {
	case "teams.assign", "teams.address", "teams.member_add", "teams.member_remove", "teams.cancel", "teams.dissolve":
		path = "/teams/" + strings.TrimPrefix(verb, "teams.")
	default:
		return nil, invalid("Unknown provider operation.")
	}
	raw, err := t.request(ctx, http.MethodPost, path, key, payload)
	if err != nil {
		return nil, err
	}
	var result struct {
		Run          *teams.TeamRun `json:"run,omitempty"`
		Member       *Member        `json:"member,omitempty"`
		Recipients   []Member       `json:"recipients,omitempty"`
		DeliveryKeys []string       `json:"delivery_keys,omitempty"`
	}
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) || json.Unmarshal(raw, &result) != nil {
		return nil, &Failure{Message: "Tether returned an unreadable outcome; recover this same request."}
	}
	switch verb {
	case "teams.assign", "teams.address":
		if len(result.Recipients) == 0 || len(result.DeliveryKeys) == 0 {
			return nil, &Failure{Message: "Tether did not confirm delivery; recover this same request."}
		}
	case "teams.member_add":
		if result.Member == nil || result.Member.ID == "" {
			return nil, &Failure{Message: "Tether did not confirm a member; recover this same request."}
		}
	}
	return json.Marshal(result)
}
func (t *tether) request(ctx context.Context, method, path, key string, body json.RawMessage) ([]byte, error) {
	defer t.client.CloseIdleConnections()
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.base+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Tether-Protocol", "1")
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	res, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, &Failure{Message: fmt.Sprintf("Tether returned HTTP %d. The operation is not confirmed; retain this request for reconciliation.", res.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (512<<10)+1))
	if err != nil || len(raw) > 512<<10 {
		return nil, &Failure{Message: "Tether returned an unreadable response."}
	}
	return raw, nil
}
func (t *tether) Roster(ctx context.Context, runID string) ([]RosterView, error) {
	raw, err := t.request(ctx, http.MethodGet, "/teams/roster?"+url.Values{"run_id": {runID}, "limit": {"25"}}.Encode(), "", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Runs []RosterView `json:"runs"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Runs) != 1 || result.Runs[0].Run.ID != runID {
		return nil, &Failure{Message: "Tether returned an unexpected roster."}
	}
	return result.Runs, nil
}
