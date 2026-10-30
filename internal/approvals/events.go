package approvals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Change is one thing that happened to a queue item. It carries ids and state
// only, never item content: the client refetches what it shows.
type Change struct {
	Seq      int64  `json:"-"`
	Type     string `json:"type"` // item.created|updated|resolved|expired|deferred|undeferred
	ItemID   string `json:"item_id"`
	Revision int64  `json:"revision"`
	State    string `json:"state"`
	At       int64  `json:"at"`
	// Extra holds event-specific fields (deferred_until).
	Extra map[string]any `json:"-"`
}

const ringSize = 512

// Broker is the in-process replay ring and fan-out for queue changes. Ids are
// epoch-based (start time in ms * 1000 + counter), so an id from before a
// restart is older than anything held and gets a resync, never a wrong replay.
// A client that cannot keep up is dropped and reconnects with Last-Event-ID.
type Broker struct {
	mu   sync.Mutex
	seq  int64
	base int64
	ring []Change
	subs map[chan Change]struct{}
}

func newBroker() *Broker {
	base := time.Now().UnixMilli() * 1000
	return &Broker{seq: base, base: base, subs: map[chan Change]struct{}{}}
}

var (
	brokersMu sync.Mutex
	brokers   = map[*localstore.Store]*Broker{}
)

// Changes returns the one broker for a database; every Store on it publishes to the same ring.
func Changes(db *localstore.Store) *Broker {
	brokersMu.Lock()
	defer brokersMu.Unlock()
	b := brokers[db]
	if b == nil {
		b = newBroker()
		brokers[db] = b
	}
	return b
}

// Publish records a change and wakes subscribers. It never blocks.
func (b *Broker) Publish(c Change) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	c.Seq, c.At = b.seq, time.Now().UnixMilli()
	b.ring = append(b.ring, c)
	if len(b.ring) > ringSize {
		b.ring = append(b.ring[:0:0], b.ring[len(b.ring)-ringSize:]...)
	}
	for ch := range b.subs {
		select {
		case ch <- c:
		default:
			delete(b.subs, ch) // too slow: drop; the client resumes by Last-Event-ID
			close(ch)
		}
	}
	return c.Seq
}

// Head is the id of the newest change (the epoch base when none).
func (b *Broker) Head() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

// Since returns the changes after id and a subscription for the next ones. fresh
// means a new connection with no id: nothing is replayed. gap is true when id is
// from another epoch, older than the ring holds, or newer than the head: the
// client must refetch.
func (b *Broker) Since(id int64, fresh bool) (missed []Change, ch chan Change, gap bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch = make(chan Change, 64)
	b.subs[ch] = struct{}{}
	if fresh {
		return nil, ch, false
	}
	if id > b.seq || id < b.base {
		return nil, ch, true
	}
	if len(b.ring) > 0 && id < b.ring[0].Seq-1 {
		return nil, ch, true
	}
	for _, c := range b.ring {
		if c.Seq > id {
			missed = append(missed, c)
		}
	}
	return missed, ch, false
}

func (b *Broker) unsubscribe(ch chan Change) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
}

// changeType classifies a record transition.
func changeType(created bool, state hitl.State) string {
	switch {
	case created:
		return "item.created"
	case state == hitl.StateExpired:
		return "item.expired"
	case state.IsTerminal():
		return "item.resolved"
	}
	return "item.updated"
}

func (s *Store) publish(rec hitl.Record, created bool) {
	if s.DB == nil {
		return
	}
	Changes(s.DB).Publish(Change{Type: changeType(created, rec.State), ItemID: rec.ItemID, Revision: rec.Revision, State: string(rec.State)})
}

// publishID reads the committed record and publishes it (for writers that hold a transaction).
func (s *Store) publishID(ctx context.Context, id string) {
	if rec, err := s.Get(ctx, id); err == nil {
		s.publish(rec, false)
	}
}

func (c Change) data() []byte {
	m := map[string]any{"type": c.Type, "at": c.At, "item_id": c.ItemID, "revision": c.Revision, "state": c.State}
	for k, v := range c.Extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

// events streams changes to a browser session that may read the inbox (the
// caller has already resolved the participant). Last-Event-ID or ?after= resumes;
// a gap yields one resync event telling the client to refetch /inbox. Polling
// /inbox stays the fallback.
func (i *Inbox) events(w http.ResponseWriter, r *http.Request) {
	var last int64
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("after")
	}
	if v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			fail(w, 400, "invalid", "Last-Event-ID must be a non-negative integer.")
			return
		}
		last = n
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "internal", "Streaming is not supported here.")
		return
	}
	broker := Changes(i.Store.DB)
	missed, ch, gap := broker.Since(last, v == "")
	defer broker.unsubscribe(ch)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	head := broker.Head()
	switch {
	case gap:
		fmt.Fprintf(w, "id: %d\ndata: {\"type\":\"resync\",\"reason\":\"gap\",\"at\":%d}\n\n", head, time.Now().UnixMilli())
	case v == "":
		// A fresh connection gets the baseline so the client knows what it has missed since.
		fmt.Fprintf(w, "id: %d\ndata: {\"type\":\"inbox.changed\",\"revision\":\"%d\",\"at\":%d}\n\n", head, head, time.Now().UnixMilli())
	}
	for _, c := range missed {
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", c.Seq, c.data())
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
		case c, open := <-ch:
			if !open {
				return
			}
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", c.Seq, c.data())
			fl.Flush()
		case <-keep.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}
