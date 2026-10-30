package agentsserve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// The stream carries signals, not state. session, summary and collector are live snapshots with no
// id: after a reconnect the client refetches GET sessions. bus, control and gap are written to
// agents_stream by the collector in the same transaction as the fact itself, so they have one id
// sequence and replay after Last-Event-ID.
//
// One loop (alive only while a client is connected) polls the read model once a second and
// announces what changed. Polling the model, rather than hooking the writers, also announces the
// transitions that come from time passing: a working session going stale.

const (
	tick           = time.Second
	keepalive      = 15 * time.Second
	collectorEvery = 5 * time.Second
	replayLimit    = 2000
	subBuffer      = 256
)

type event struct {
	id   int64 // 0 = a live snapshot, no SSE id
	body []byte
}

type sub struct {
	ch   chan event
	once sync.Once
}

func (s *sub) close() { s.once.Do(func() { close(s.ch) }) }

type hub struct {
	mu        sync.Mutex
	subs      map[*sub]struct{}
	cancel    context.CancelFunc
	cursor    int64
	lastSes   map[string][32]byte
	lastSum   string
	lastCol   string
	lastColAt time.Time
}

func (h *hub) add(srv *Server) *sub {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = map[*sub]struct{}{}
	}
	u := &sub{ch: make(chan event, subBuffer)}
	h.subs[u] = struct{}{}
	if h.cancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		h.cancel = cancel
		go srv.loop(ctx)
	}
	return u
}

func (h *hub) remove(u *sub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[u]; !ok {
		return
	}
	delete(h.subs, u)
	u.close()
	if len(h.subs) == 0 && h.cancel != nil {
		h.cancel()
		h.cancel, h.lastSes, h.lastSum, h.lastCol = nil, nil, "", "" // the next client starts from a fresh baseline
	}
}

func (h *hub) broadcast(ev event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for u := range h.subs {
		select {
		case u.ch <- ev:
		default: // too slow: drop it, the client reconnects and refetches
			delete(h.subs, u)
			u.close()
		}
	}
}

func envelope(typ string, at time.Time, fields map[string]any) []byte {
	m := map[string]any{"type": typ, "at": at.UnixMilli()}
	for k, v := range fields {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

// loop announces changes while a client is connected.
func (s *Server) loop(ctx context.Context) {
	defer func() { _ = recover() }() // a bad row must not take the process down; clients reconnect
	h := &s.hub
	h.mu.Lock()
	h.cursor = s.maxStreamID(ctx)
	h.mu.Unlock()
	s.diff(ctx, true)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.announceStream(ctx)
			s.diff(ctx, false)
		}
	}
}

func (s *Server) maxStreamID(ctx context.Context) (n int64) {
	_ = s.DB.Reader.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM agents_stream`).Scan(&n)
	return
}

type streamRow struct {
	id   int64
	typ  string
	at   int64
	data string
}

func (s *Server) streamRows(ctx context.Context, after int64, limit int) ([]streamRow, error) {
	rs, err := s.DB.Reader.QueryContext(ctx, `SELECT id,type,at_ns,data_json FROM agents_stream WHERE id>? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []streamRow
	for rs.Next() {
		var r streamRow
		if err := rs.Scan(&r.id, &r.typ, &r.at, &r.data); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func (r streamRow) event() event {
	var f map[string]any
	_ = json.Unmarshal([]byte(r.data), &f)
	return event{id: r.id, body: envelope(r.typ, time.Unix(0, r.at), f)}
}

func (s *Server) announceStream(ctx context.Context) {
	h := &s.hub
	h.mu.Lock()
	after := h.cursor
	h.mu.Unlock()
	rows, err := s.streamRows(ctx, after, 500)
	if err != nil {
		return
	}
	for _, r := range rows {
		h.broadcast(r.event())
		h.mu.Lock()
		h.cursor = r.id
		h.mu.Unlock()
	}
}

// diff announces sessions whose derived shape changed, the summary when its counts changed, and the
// collector status when it changed (or every collectorEvery). baseline records state silently.
func (s *Server) diff(ctx context.Context, baseline bool) {
	h := &s.hub
	all, err := s.all(ctx)
	if err != nil {
		return
	}
	now := s.now()
	seen := make(map[string][32]byte, len(all))
	var changed []Session
	h.mu.Lock()
	prev := h.lastSes
	h.mu.Unlock()
	for _, sess := range all {
		b, _ := json.Marshal(sess)
		sum := sha256sum(b)
		seen[sess.ID] = sum
		if old, ok := prev[sess.ID]; !baseline && (!ok || old != sum) {
			changed = append(changed, sess)
		}
	}
	sumJSON, _ := json.Marshal(Summary(all))
	var colJSON []byte
	if st, err := s.Collector.Status(ctx); err == nil {
		colJSON, _ = json.Marshal(st)
	}
	h.mu.Lock()
	h.lastSes = seen
	sumChanged := string(sumJSON) != h.lastSum
	colChanged := colJSON != nil && (string(colJSON) != h.lastCol || now.Sub(h.lastColAt) >= collectorEvery)
	h.lastSum = string(sumJSON)
	if colChanged {
		h.lastCol, h.lastColAt = string(colJSON), now
	}
	h.mu.Unlock()
	if baseline {
		return
	}
	for _, sess := range changed {
		h.broadcast(event{body: envelope("session", now, map[string]any{"session": sess})})
	}
	if sumChanged {
		h.broadcast(event{body: envelope("summary", now, map[string]any{"summary": json.RawMessage(sumJSON)})})
	}
	if colChanged {
		var f map[string]any
		_ = json.Unmarshal(colJSON, &f)
		h.broadcast(event{body: envelope("collector", now, f)})
	}
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	var last int64
	hasLast := false
	if v := r.Header.Get("Last-Event-ID"); v != "" || r.URL.Query().Get("last_event_id") != "" {
		if v == "" {
			v = r.URL.Query().Get("last_event_id")
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			fail(w, 400, "invalid", "Last-Event-ID must be a non-negative integer.")
			return
		}
		last, hasLast = n, true
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		internal(w)
		return
	}
	once := r.URL.Query().Get("once") == "1"
	u := s.hub.add(s) // subscribe before reading the table so nothing falls between replay and live
	defer s.hub.remove(u)
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	var sent int64 // highest id written: drops replay/live duplicates
	emit := func(ev event) {
		if ev.id > 0 {
			if ev.id <= sent {
				return
			}
			sent = ev.id
			fmt.Fprintf(w, "id: %d\n", ev.id)
		}
		fmt.Fprintf(w, "data: %s\n\n", ev.body)
	}
	ctx := r.Context()
	if hasLast {
		// A hole before the oldest retained row means events were pruned: say so, the client refetches.
		var oldest int64
		_ = s.DB.Reader.QueryRowContext(ctx, `SELECT COALESCE(MIN(id),0) FROM agents_stream`).Scan(&oldest)
		if oldest > last+1 {
			emit(event{body: envelope("gap", s.now(), map[string]any{"start": nil, "end": s.now().UnixMilli(), "reason": "purged", "from_seq": 0, "to_seq": 0, "backfilled": false})})
		}
		rows, err := s.streamRows(ctx, last, replayLimit)
		if err == nil {
			for _, row := range rows {
				emit(row.event())
			}
		}
		sent = max64(sent, last)
	} else {
		// A fresh connection gets the current collector status and counts, then live changes.
		now := s.now()
		if st, err := s.Collector.Status(ctx); err == nil {
			b, _ := json.Marshal(st)
			var f map[string]any
			_ = json.Unmarshal(b, &f)
			emit(event{body: envelope("collector", now, f)})
		}
		if all, err := s.all(ctx); err == nil {
			emit(event{body: envelope("summary", now, map[string]any{"summary": Summary(all)})})
		}
	}
	fl.Flush()
	if once {
		return
	}
	ka := time.NewTicker(keepalive)
	defer ka.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-u.ch:
			if !ok {
				return // dropped as too slow, or closed: the client reconnects
			}
			emit(ev)
			for n := len(u.ch); n > 0; n-- { // drain what is already queued in one flush
				if ev, ok = <-u.ch; !ok {
					fl.Flush()
					return
				}
				emit(ev)
			}
			fl.Flush()
		case <-ka.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}
