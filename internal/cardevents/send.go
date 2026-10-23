package cardevents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/ingest"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

// Sender posts queued events to the hub's POST /events.
type Sender struct {
	Outbox *Store
	Base   string
	Client *http.Client
	Now    func() time.Time
}

// NewSender returns a sender for a hub address (https://, unix:<path> or
// tcp:<host:port>) and the token file of the credential bound to the tachyon
// producer. The credential is read from the file on each request; nothing here
// creates or stores one.
func NewSender(outbox *Store, addr, tokenFile string) (*Sender, error) {
	if strings.TrimSpace(addr) == "" || tokenFile == "" {
		return nil, errors.New("card events need a hub address and a token file")
	}
	base := strings.TrimRight(addr, "/")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 10 * time.Second
	transport.MaxResponseHeaderBytes = 64 << 10
	if socket, ok := strings.CutPrefix(addr, "unix:"); ok {
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		}
		base = "http://localhost"
	} else if rest, ok := strings.CutPrefix(addr, "tcp:"); ok {
		base = "http://" + rest
	}
	rt, err := upstreamauth.Wrap(transport, base, tokenFile)
	if err != nil {
		return nil, err
	}
	return &Sender{Outbox: outbox, Base: base, Client: &http.Client{Transport: rt, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (s *Sender) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Flush sends one batch of pending events. Network errors, 429 and 503 leave
// events pending for the next pass; a rejected event is recorded and not
// retried; 401 and 403 leave everything pending (the credential is the problem,
// not the events) and are returned so the caller can log them.
func (s *Sender) Flush(ctx context.Context) error {
	rows, err := s.Outbox.Pending(ctx, ingest.MaxBatchEvents)
	if err != nil || len(rows) == 0 {
		return err
	}
	events := make([]json.RawMessage, len(rows))
	for i, r := range rows {
		events[i] = r.Event
	}
	body, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Base+"/events", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.Client.Do(req)
	if err != nil {
		return fmt.Errorf("hub unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hub answered HTTP %d", resp.StatusCode)
	}
	var out ingest.Response
	if json.Unmarshal(raw, &out) != nil || len(out.Results) != len(rows) {
		return errors.New("hub answer did not match the batch")
	}
	for i, res := range out.Results {
		if res.ID != rows[i].EventID {
			return errors.New("hub answer is out of order")
		}
		switch res.Status {
		case ingest.StatusAccepted, ingest.StatusDuplicate:
			err = s.Outbox.Mark(ctx, rows[i].Seq, StateSent, "", s.now())
		case ingest.StatusRejected:
			slog.Warn("card event rejected by the hub", "kind", rows[i].Kind, "code", res.Code)
			err = s.Outbox.Mark(ctx, rows[i].Seq, StateRejected, string(res.Code), s.now())
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Run collects and sends until ctx ends, backing off while the hub is
// unavailable.
func Run(ctx context.Context, c *Collector, s *Sender, every time.Duration) {
	if every <= 0 {
		every = 3 * time.Second
	}
	wait := every
	for {
		if err := c.Pass(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("card events collect failed", "err", err)
		}
		if s != nil {
			if err := s.Flush(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("card events not delivered", "err", err)
				wait = min(wait*2, time.Minute)
			} else {
				wait = every
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
