package catalogindex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	ringSize  = 256
	subBuffer = 64
)

// Event is one stream frame. Replayable types (catalog) carry an id; summary snapshots do not.
type Event struct {
	ID   int64
	Type string
	At   int64
	Data map[string]any
}

// Broker is an in-process ring of recent catalog events with live fan-out. Ids are
// startMs*1000+n, so a restart can never reuse an id a client holds: an older epoch simply gets
// no replay and the client refetches the summary.
type Broker struct {
	mu   sync.Mutex
	now  func() time.Time
	base int64
	n    int64
	ring []Event
	subs map[chan Event]struct{}
}

func NewBroker(now func() time.Time) *Broker {
	return &Broker{now: now, base: now().UnixMilli() * 1000, subs: map[chan Event]struct{}{}}
}

func (b *Broker) Publish(typ string, data map[string]any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := Event{Type: typ, At: b.now().UnixMilli(), Data: data}
	if typ == "catalog" || typ == "change" { // builderapi.IDed: replayable
		b.n++
		e.ID = b.base + b.n
		b.ring = append(b.ring, e)
		if len(b.ring) > ringSize {
			b.ring = b.ring[len(b.ring)-ringSize:]
		}
	}
	for ch := range b.subs {
		select {
		case ch <- e:
		default: // a slow client is dropped; it reconnects and refetches
			delete(b.subs, ch)
			close(ch)
		}
	}
}

// Since returns the replayable events after id. A zero id returns none (a fresh connection starts
// from the summary snapshot); an id from another epoch returns none too.
func (b *Broker) Since(id int64) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id < b.base {
		return nil
	}
	var out []Event
	for _, e := range b.ring {
		if e.ID > id {
			out = append(out, e)
		}
	}
	return out
}

func (b *Broker) subscribe() chan Event {
	ch := make(chan Event, subBuffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *Broker) unsubscribe(ch chan Event) {
	b.mu.Lock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
	b.mu.Unlock()
}

func writeEvent(w http.ResponseWriter, e Event) {
	env := map[string]any{"type": e.Type, "at": e.At}
	for k, v := range e.Data {
		env[k] = v
	}
	body, _ := json.Marshal(env)
	if e.ID != 0 {
		fmt.Fprintf(w, "id: %d\n", e.ID)
	}
	fmt.Fprintf(w, "data: %s\n\n", body)
}

// StreamHandler serves GET builder/stream: the current summary first, then replayed and live
// catalog events. Last-Event-ID resumes; ?once=1 ends after the replay (for tests and scripts).
func (x *Index) StreamHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, `{"code":"internal","message":"streaming unsupported"}`, 500)
			return
		}
		var last int64
		if v := r.Header.Get("Last-Event-ID"); v != "" {
			if _, err := fmt.Sscanf(v, "%d", &last); err != nil || last < 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(400)
				fmt.Fprint(w, `{"code":"invalid","message":"Last-Event-ID must be a non-negative integer."}`)
				return
			}
		}
		ch := x.Bus.subscribe()
		defer x.Bus.unsubscribe(ch)
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-store")
		w.WriteHeader(200)
		if s, err := x.Summary(r.Context()); err == nil {
			writeEvent(w, Event{Type: "summary", At: x.Store.now().UnixMilli(), Data: s.asMap()})
		}
		seen := last
		for _, e := range x.Bus.Since(last) {
			writeEvent(w, e)
			seen = e.ID
		}
		fl.Flush()
		if r.URL.Query().Get("once") == "1" {
			return
		}
		keep := time.NewTicker(15 * time.Second)
		defer keep.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				if e.ID != 0 && e.ID <= seen {
					continue
				}
				writeEvent(w, e)
				fl.Flush()
			case <-keep.C:
				fmt.Fprint(w, ": keepalive\n\n")
				fl.Flush()
			}
		}
	})
}

// SummaryHandler serves GET builder/summary.
func (x *Index) SummaryHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		s, err := x.Summary(context.WithoutCancel(r.Context()))
		if err != nil {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"code":"internal","message":"The catalog index is unavailable."}`)
			return
		}
		_ = json.NewEncoder(w).Encode(s.asMap())
	})
}
