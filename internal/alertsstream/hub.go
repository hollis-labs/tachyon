// Package alertsstream is the Alerts SSE side (AL4): an in-process hub with a replay ring, the
// GET /alerts/stream handler (ids are alert seq values, Last-Event-ID resume, resync on a gap, heartbeat),
// frame constructors, and the GET /alerts/sources handler. Storage and the decision to toast are the callers':
// the hub carries frames, it does not compute them. Design: design_tachyon_alerts_backend_20261030 sections 6, 9.
package alertsstream

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

// Frame is one SSE message. ID is the alert seq it belongs to (the resume cursor); several frames may share
// an ID (alert.state then counts). Data is the marshalled JSON object; the hub never inspects it.
type Frame struct {
	ID   int64
	Data []byte

	n uint64 // hub-internal order, strictly increasing
}

// Hub fans frames out to subscribers and keeps the last Cap frames for resume.
type Hub struct {
	mu     sync.Mutex
	ring   []Frame
	cap    int
	serial uint64
	floor  int64 // highest ID that fell out of the ring (0 until something is evicted)
	top    int64 // highest ID published
	subs   map[*Sub]struct{}
}

// Sub is one subscriber. C closes when the subscriber is too slow; the client reconnects with Last-Event-ID.
type Sub struct {
	C     chan Frame
	hub   *Hub
	gone  bool
	start uint64 // hub serial when subscribed; frames at or below it are not live for this subscriber
}

func NewHub(capacity int) *Hub {
	if capacity < 1 {
		capacity = 1000
	}
	return &Hub{cap: capacity, subs: map[*Sub]struct{}{}}
}

// Publish records and delivers a frame. A subscriber whose buffer is full is dropped, never blocked on.
func (h *Hub) Publish(id int64, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.serial++
	f := Frame{ID: id, Data: data, n: h.serial}
	if id > h.top {
		h.top = id
	}
	h.ring = append(h.ring, f)
	if len(h.ring) > h.cap {
		if ev := h.ring[0].ID; ev > h.floor {
			h.floor = ev
		}
		h.ring = append(h.ring[:0], h.ring[1:]...)
	}
	for s := range h.subs {
		select {
		case s.C <- f:
		default:
			h.drop(s)
		}
	}
	return nil
}

func (h *Hub) drop(s *Sub) {
	if !s.gone {
		s.gone = true
		delete(h.subs, s)
		close(s.C)
	}
}

// Subscribe registers a subscriber (buffer 64). Call Close when done.
func (h *Hub) Subscribe() *Sub {
	s := &Sub{C: make(chan Frame, 64), hub: h}
	h.mu.Lock()
	s.start = h.serial
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (s *Sub) Close() {
	s.hub.mu.Lock()
	s.hub.drop(s)
	s.hub.mu.Unlock()
}

// Top is the highest ID published (0 before the first frame).
func (h *Hub) Top() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.top
}

// Replay returns the frames with ID greater than after, oldest first. ok is false when the cursor cannot be
// served from the ring: older than the ring (frames were evicted) or newer than anything published (the
// server restarted and the client holds a cursor from before). The caller then sends resync.
func (h *Hub) Replay(after int64) (frames []Frame, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if after < h.floor || after > h.top {
		return nil, false
	}
	for _, f := range h.ring {
		if f.ID > after {
			frames = append(frames, f)
		}
	}
	return frames, true
}

// Frame constructors. Times are unix ms; toast is the server's decision (quiet, notify level, severity).

func AlertFrame(typ string, a alertsapi.Alert, toast bool, reason string, at time.Time) map[string]any {
	m := map[string]any{"type": typ, "at_ms": at.UnixMilli(), "alert": a, "toast": toast}
	if reason != "" {
		m["reason"] = reason
	}
	return m
}

// RemovedFrame announces a purged alert (retention). Only the id and seq are sent.
func RemovedFrame(alertID string, seq int64, at time.Time) map[string]any {
	return map[string]any{"type": "alert.removed", "at_ms": at.UnixMilli(), "alert_id": alertID, "seq": seq}
}

func CountsFrame(c alertsapi.Counts, at time.Time) map[string]any {
	return map[string]any{"type": "counts", "at_ms": at.UnixMilli(), "counts": c}
}

func PrefsFrame(p alertsapi.Prefs, at time.Time) map[string]any {
	return map[string]any{"type": "prefs", "at_ms": at.UnixMilli(), "prefs": p}
}

func ResyncFrame(reason string, at time.Time) map[string]any {
	return map[string]any{"type": "resync", "at_ms": at.UnixMilli(), "reason": reason}
}
