package cardfx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

var (
	torqueTask   = regexp.MustCompile(`^torque:task/([A-Z]{2,5}-\d{8}-\d{4})$`)
	torqueStatus = regexp.MustCompile(`^[a-z_]{2,24}$`)
)

// Torque changes a Torque task's status: target torque:task/<id>, op status,
// value the new status. It never forces a transition; Torque's own rules judge
// the move and a refusal is recorded in the receipt.
type Torque struct {
	Base   string
	Token  string
	Client *http.Client
}

// NewTorque returns the Torque target for a base URL and bearer token.
func NewTorque(base, token string) *Torque {
	return &Torque{Base: strings.TrimRight(base, "/"), Token: token, Client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (*Torque) Scheme() string { return "torque" }

func (*Torque) Validate(e Effect) error {
	if !torqueTask.MatchString(e.Target) {
		return errors.New("target must be torque:task/<task id>")
	}
	if e.Op != "status" {
		return fmt.Errorf("op %q is not supported; use status", e.Op)
	}
	if !torqueStatus.MatchString(e.Value) {
		return errors.New("value must be a status name")
	}
	return nil
}

func (t *Torque) Apply(ctx context.Context, e Effect) (string, error) {
	if err := t.Validate(e); err != nil {
		return "", err
	}
	if err := upstreamauth.Guard(t.Base, t.Token); err != nil {
		return "", errors.New("Torque address is not safe for a credential")
	}
	id := torqueTask.FindStringSubmatch(e.Target)[1]
	body, _ := json.Marshal(map[string]any{"status": e.Value})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Base+"/api/v1/tasks/"+url.PathEscape(id)+"/transition", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if t.Token != "" {
		req.Header.Set("Authorization", "Bearer "+t.Token)
	}
	resp, err := t.Client.Do(req)
	if err != nil {
		return "", errors.New("Torque is unreachable")
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode/100 != 2 {
		var detail struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(msg, &detail)
		if detail.Error == "" {
			detail.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return "", fmt.Errorf("Torque refused: %s", detail.Error)
	}
	return fmt.Sprintf("%s set to %s", id, e.Value), nil
}
