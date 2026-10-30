package alertsource

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

// ParseActivityFrame turns one activity-stream JSON frame (the console envelope: id, ts, kind, source.id,
// subjects[{type,id,label,url}], severity, payload, correlationId) into a normalized event. id is the SSE
// `id:` when the frame has none. Subject urls that are not safe hrefs are dropped; payload strings are cleaned.
func ParseActivityFrame(data []byte, sseID string) (Event, error) {
	var f struct {
		ID       string `json:"id"`
		TS       int64  `json:"ts"`
		Kind     string `json:"kind"`
		Source   struct{ ID string }
		Subjects []struct{ Type, ID, Label, URL string }
		Severity string         `json:"severity"`
		Payload  map[string]any `json:"payload"`
		Corr     string         `json:"correlationId"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return Event{}, fmt.Errorf("activity frame: %w", err)
	}
	if f.Kind == "" {
		return Event{}, errors.New("activity frame has no kind")
	}
	id := f.ID
	if id == "" {
		id = sseID
	}
	if id == "" {
		// Last resort so a replayed frame is still idempotent: kind, first subject and second.
		id = fmt.Sprintf("%s:%s:%d", f.Kind, firstRef(f.Subjects), f.TS/1000)
	}
	e := Event{Source: "activity", SourceEventID: Clean(id, 200), Kind: Clean(f.Kind, 80), AtMS: f.TS, CorrelationID: Clean(f.Corr, 120), SeverityHint: validSeverity(f.Severity), Payload: cleanPayload(f.Payload)}
	if e.AtMS == 0 {
		e.AtMS = time.Now().UnixMilli()
	}
	for _, s := range f.Subjects {
		sub := alertsapi.Subject{Kind: Clean(s.Type, 40), Ref: Clean(s.ID, 200), Label: Clean(s.Label, 120)}
		if alertsapi.SafeHref(s.URL) {
			sub.URL = s.URL
		}
		if sub.Kind != "" && sub.Ref != "" {
			e.Subjects = append(e.Subjects, sub)
		}
	}
	return e, nil
}

func firstRef(s []struct{ Type, ID, Label, URL string }) string {
	if len(s) > 0 {
		return s[0].Type + "/" + s[0].ID
	}
	return ""
}

func validSeverity(s string) string {
	for _, v := range alertsapi.Severities {
		if v == s {
			return s
		}
	}
	return ""
}

// cleanPayload keeps scalars and one level of nesting, cleaning strings; lists and deep objects are dropped
// (the evaluator reads scalar paths, and raw model output must not be stored).
func cleanPayload(p map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range p {
		switch t := v.(type) {
		case string:
			out[Clean(k, 60)] = Clean(t, 300)
		case float64, bool:
			out[Clean(k, 60)] = t
		case map[string]any:
			in := map[string]any{}
			for k2, v2 := range t {
				switch s := v2.(type) {
				case string:
					in[Clean(k2, 60)] = Clean(s, 300)
				case float64, bool:
					in[Clean(k2, 60)] = s
				}
			}
			out[Clean(k, 60)] = in
		}
	}
	return out
}

// Frame is one decoded SSE message.
type Frame struct{ ID, Data string }

// ReadSSE decodes an event stream, calling fn per message that has data. Comment lines are skipped.
// A line over 1 MiB ends the stream with an error.
func ReadSSE(r io.Reader, fn func(Frame) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var f Frame
	var data []string
	flush := func() error {
		if len(data) == 0 {
			f = Frame{}
			return nil
		}
		f.Data = strings.Join(data, "\n")
		err := fn(f)
		f, data = Frame{}, nil
		return err
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "id:"):
			f.ID = strings.TrimSpace(line[3:])
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(line[5:], " "))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return flush()
}

// Activity follows the activity monitor's SSE stream, resuming with Last-Event-ID from the stored cursor.
type Activity struct {
	URL    string // e.g. TACHYON_ACTIVITY_URL + "/api/stream"
	Client *http.Client
	Sink   Sink
	Cursor CursorStore
	Health *Health
	Limit  *Limiter
}

// Run is one connection: it returns when the stream ends or fails; the supervisor reconnects.
func (a *Activity) Run(ctx context.Context) error {
	last, err := a.Cursor.Cursor("activity")
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	if last != "" {
		req.Header.Set("Last-Event-ID", last)
	}
	c := a.Client
	if c == nil {
		c = &http.Client{} // no overall timeout: the stream is long-lived
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("activity stream: HTTP %d", resp.StatusCode)
	}
	a.Health.Set("activity", "ok", "")
	return ReadSSE(resp.Body, func(f Frame) error {
		ev, err := ParseActivityFrame([]byte(f.Data), f.ID)
		if err != nil {
			return nil // a bad frame is skipped, not fatal
		}
		a.Health.Seen("activity")
		if a.Limit == nil || a.Limit.Allow("activity") {
			if err := a.Sink.Ingest(ev); err != nil {
				return err
			}
		} else {
			a.Sink.Suppressed("activity", 1)
		}
		if f.ID != "" {
			return a.Cursor.SetCursor("activity", f.ID)
		}
		return nil
	})
}
