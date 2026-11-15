package needsyou

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// HTTPTether reads Tether over HTTP with the read token (builderread.NewTetherClient): GET
// /messages/pending-replies?as= and GET /messages/list?as=&to= (a mailbox read claims its owner with as).
type HTTPTether struct {
	Base   string
	Client *http.Client
}

func (t *HTTPTether) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.Base+path, nil) //nolint:gosec // the configured Tether base, a fixed path
	if err != nil {
		return err
	}
	resp, err := t.Client.Do(req) //nolint:gosec // the configured Tether base, a fixed path
	if err != nil {
		return fmt.Errorf("tether unreachable")
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Error.Code != "" {
			return fmt.Errorf("tether answered %d (%s)", resp.StatusCode, e.Error.Code)
		}
		return fmt.Errorf("tether answered %d", resp.StatusCode)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("tether sent an unreadable answer")
	}
	return nil
}

func (t *HTTPTether) PendingReplies(ctx context.Context, as string) ([]Pending, error) {
	var out struct {
		Pending []Pending `json:"pending"`
	}
	if err := t.get(ctx, "/messages/pending-replies?as="+url.QueryEscape(as), &out); err != nil {
		return nil, err
	}
	return out.Pending, nil
}

// Mailbox returns the owner's newest messages (archived excluded) and the mailbox total.
func (t *HTTPTether) Mailbox(ctx context.Context, owner string, limit int) ([]Message, int, error) {
	var out struct {
		Messages []Message `json:"messages"`
		Total    int       `json:"total"`
	}
	q := url.QueryEscape(owner)
	if err := t.get(ctx, "/messages/list?as="+q+"&to="+q+"&limit="+strconv.Itoa(limit), &out); err != nil {
		return nil, 0, err
	}
	return out.Messages, out.Total, nil
}
