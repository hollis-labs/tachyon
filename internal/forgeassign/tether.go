package forgeassign

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// TetherAgents resolves agents through Tether's catalog (id -> URN) with the read-scope client.
type TetherAgents struct {
	Base   string
	Client *http.Client
}

func (t *TetherAgents) Lookup(ctx context.Context, id string) (Agent, error) {
	var env struct {
		Agents []struct {
			ID  string `json:"id"`
			URN string `json:"urn"`
		} `json:"agents"`
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(t.Base, "/")+"/catalog/agents", nil)
	req.Header.Set("Accept", "application/json")
	resp, err := t.Client.Do(req)
	if err != nil {
		return Agent{}, errors.New("Tether unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Agent{}, errors.New("Tether refused the agent catalog")
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&env) != nil {
		return Agent{}, errors.New("Tether sent an unreadable agent catalog")
	}
	for _, a := range env.Agents {
		if a.ID == id || (a.URN != "" && a.URN == id) {
			if strings.TrimSpace(a.URN) == "" {
				return Agent{}, ErrAgentNotAddressable
			}
			return Agent{ID: a.ID, URN: a.URN}, nil
		}
	}
	return Agent{}, ErrAgentNotFound
}

// TetherMail posts the assignment as a notice envelope. The sender is the principal of the token; the
// envelope has no idempotency field, so repeat protection is the link row.
type TetherMail struct {
	Base   string
	Client *http.Client
}

func (t *TetherMail) Send(ctx context.Context, to, subject, body string, meta map[string]string) (string, error) {
	md := map[string]any{"subject": subject}
	for k, v := range meta {
		md[k] = v
	}
	env := map[string]any{"kind": "notice", "to": to, "payload": map[string]any{"subject": subject, "body": body}, "metadata": md}
	b, _ := json.Marshal(env)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.Base, "/")+"/messages", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.Client.Do(req)
	if err != nil {
		return "", errors.New("Tether could not be reached")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", errors.New("Tether refused the message (HTTP " + http.StatusText(resp.StatusCode) + ")")
	}
	var out struct {
		ID        string `json:"id"`
		MessageID string `json:"message_id"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.ID == "" {
		out.ID = out.MessageID
	}
	return out.ID, nil
}

// SettingsLogins reads {agent_id: github_login} from app_settings key forge.agent_logins.
type SettingsLogins struct {
	DB interface {
		QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	}
}

func (s SettingsLogins) Login(ctx context.Context, agentID string) string {
	var raw string
	if s.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, "forge.agent_logins").Scan(&raw) != nil {
		return ""
	}
	m := map[string]string{}
	if json.Unmarshal([]byte(raw), &m) != nil {
		return ""
	}
	return strings.TrimPrefix(m[agentID], "@")
}
