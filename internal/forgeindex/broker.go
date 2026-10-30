package forgeindex

import (
	"sync"
	"time"
)

const (
	ringSize  = 512
	subBuffer = 64
)

// Event is one stream frame. Ids are startMs*1000+n, so an id from before a restart is recognisably
// from another epoch and gets a resync instead of a silent gap.
type Event struct {
	ID   int64
	Type string
	At   int64
	Data map[string]any
}

type Broker struct {
	mu   sync.Mutex
	now  func() time.Time
	base int64
	n    int64
	ring []Event
	subs map[chan Event]struct{}
}

func NewBroker(now func() time.Time) *Broker {
	if now == nil {
		now = time.Now
	}
	return &Broker{now: now, base: now().UnixMilli() * 1000, subs: map[chan Event]struct{}{}}
}

func (b *Broker) Publish(typ string, data map[string]any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.n++
	e := Event{ID: b.base + b.n, Type: typ, At: b.now().UnixMilli(), Data: data}
	b.ring = append(b.ring, e)
	if len(b.ring) > ringSize {
		b.ring = b.ring[len(b.ring)-ringSize:]
	}
	for ch := range b.subs {
		select {
		case ch <- e:
		default: // a slow client is dropped; it reconnects with Last-Event-ID
			delete(b.subs, ch)
			close(ch)
		}
	}
}

// Since returns the events after id and whether the id could be honoured: false means another epoch,
// an id past the head, or one older than the ring, so the client must refetch (resync).
func (b *Broker) Since(id int64) ([]Event, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	head := b.base + b.n
	if id < b.base || id > head {
		return nil, false
	}
	if len(b.ring) > 0 && id < b.ring[0].ID-1 {
		return nil, false
	}
	var out []Event
	for _, e := range b.ring {
		if e.ID > id {
			out = append(out, e)
		}
	}
	return out, true
}

// Head is the id of the newest event (the epoch base when none yet).
func (b *Broker) Head() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.base + b.n
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
