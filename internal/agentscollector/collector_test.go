package agentscollector

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

const secret = "ghp_" + "Zx9Qw3Er5Ty7Ui1Op2As4Df6Gh8Jk0Lm"

type fakeSource struct {
	mu          sync.Mutex
	snap        Snapshot
	snapErr     error
	sessions    []Frame
	head        int64
	history     []Frame // newest first
	histErr     error
	streamFrame []string // raw wire frames
	streamErr   error
	hold        bool // block after the frames until ctx ends, sending no keepalive
	panicOn     bool
	streamedAt  []int64
}

func (f *fakeSource) Snapshot(context.Context) (Snapshot, error) { return f.snap, f.snapErr }
func (f *fakeSource) Sessions(context.Context) ([]Frame, error)  { return f.sessions, nil }
func (f *fakeSource) Head(context.Context) (int64, error)        { return f.head, nil }
func (f *fakeSource) History(_ context.Context, since, before int64, _ int) ([]Frame, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.histErr != nil {
		return nil, 0, f.histErr
	}
	var page []Frame
	more := false
	for _, fr := range f.history {
		if (before > 0 && fr.Seq >= before) || fr.Seq <= since {
			continue
		}
		if len(page) == 2 {
			more = true
			break
		}
		page = append(page, fr)
	}
	if !more || len(page) == 0 {
		return page, 0, nil
	}
	return page, page[len(page)-1].Seq, nil
}
func (f *fakeSource) Stream(ctx context.Context, after int64, on func(Frame) error, alive func()) error {
	if f.panicOn {
		panic("payload text: " + secret)
	}
	f.mu.Lock()
	f.streamedAt = append(f.streamedAt, after)
	f.mu.Unlock()
	alive()
	for _, raw := range f.streamFrame {
		fr, ok := ParseFrame([]byte(raw))
		if !ok {
			continue
		}
		if err := on(fr); err != nil {
			return err
		}
	}
	if f.hold {
		<-ctx.Done()
		return nil
	}
	if f.streamErr != nil {
		return f.streamErr
	}
	<-ctx.Done()
	return nil
}

func newCollector(t *testing.T, src *fakeSource) *Collector {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &Collector{Source: src, Store: &Store{DB: db}, BackoffMin: 5 * time.Millisecond, BackoffMax: 20 * time.Millisecond, Watchdog: 200 * time.Millisecond}
}

func frameJSON(id, kind, session, at, payload string) string {
	return fmt.Sprintf(`{"id":%q,"kind":%q,"session_id":%q,"time":%q,"payload":%s}`, id, kind, session, at, payload)
}

// runFor runs the collector until cond holds (or fails after a timeout).
func runUntil(t *testing.T, c *Collector, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	deadline := time.Now().Add(8 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

func count(t *testing.T, c *Collector, q string, args ...any) (n int) {
	t.Helper()
	if err := c.Store.DB.Reader.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return
}

func TestParseFrameAcceptsBothShapesAndNeverReadsOutput(t *testing.T) {
	f, ok := ParseFrame([]byte(frameJSON("env1:42", "session.work_state_changed", "s1", "2026-10-30T01:00:00Z", `{"from":"idle","to":"processing"}`)))
	if !ok || f.Seq != 42 || f.Env != "env1" || f.WorkState != "processing" || f.From != "idle" || f.At.IsZero() {
		t.Fatalf("%+v", f)
	}
	for _, pj := range []string{`"{\"to\":\"idle\"}"`, `{"to":"idle"}`} {
		h, ok := ParseFrame([]byte(`{"seq":7,"kind":"session.work_state_changed","session_id":"s2","at":"2026-10-30T01:00:00Z","payload_json":` + pj + `}`))
		if !ok || h.Seq != 7 || h.WorkState != "idle" {
			t.Fatalf("history shape %s: %+v", pj, h)
		}
	}
	o, ok := ParseFrame([]byte(frameJSON("e:9", "session.output", "s1", "2026-10-30T01:00:00Z", `{"data":"`+secret+`"}`)))
	if !ok || !o.Output || o.Tool != "" || o.Summary() != "session.output" {
		t.Fatalf("%+v", o)
	}
	if g, _ := ParseFrame([]byte(`{"kind":"gap","reason":"purged"}`)); !g.Gap || g.Reason != "purged" {
		t.Fatalf("gap: %+v", g)
	}
	if s, _ := ParseFrame([]byte(`{"kind":"synchronized"}`)); !s.Synchronized {
		t.Fatal("synchronized")
	}
	if _, ok := ParseFrame([]byte(`not json`)); ok {
		t.Fatal("garbage parsed")
	}
	long, _ := ParseFrame([]byte(frameJSON("e:1", "tool.call.finished", "s1", "2026-10-30T01:00:00Z", `{"tool":"`+strings.Repeat("x\u0007", 200)+`"}`)))
	if len(long.Tool) > 80 || strings.ContainsRune(long.Tool, 7) {
		t.Fatalf("tool not cleaned: %q", long.Tool)
	}
}

func TestSnapshotBootstrapThenStreamAppliesStateIdempotently(t *testing.T) {
	yes := true
	src := &fakeSource{
		snap: Snapshot{Env: "env1", HighWater: 100, Sessions: []Frame{{Kind: "session.snapshot", SessionID: "s1", SessionState: "running", WorkState: "idle", Provider: "claude", PendingApproval: &yes}}},
		streamFrame: []string{
			frameJSON("env1:101", "session.work_state_changed", "s1", "2026-10-30T01:00:01Z", `{"from":"idle","to":"processing"}`),
			frameJSON("env1:101", "session.work_state_changed", "s1", "2026-10-30T01:00:01Z", `{"from":"idle","to":"processing"}`), // replay
			frameJSON("env1:102", "tool.call.finished", "s1", "2026-10-30T01:00:02Z", `{"tool":"Read"}`),
			frameJSON("env1:103", "session.output", "s1", "2026-10-30T01:00:03Z", `{"data":"`+secret+`"}`),
			frameJSON("env1:104", "session.lifecycle", "s2", "2026-10-30T01:00:04Z", `{"state":"ended","reason":"restart"}`),
		},
	}
	c := newCollector(t, src)
	runUntil(t, c, func() bool {
		st, _ := c.Store.State(context.Background())
		return st.LastSeq >= 104 && count(t, c, `SELECT COUNT(*) FROM agents_sessions WHERE session_id='s1' AND last_activity_ns>=?`, mustTime("2026-10-30T01:00:03Z").UnixNano()) == 1
	})
	if count(t, c, `SELECT COUNT(*) FROM agents_events WHERE tether_event_id='env1:101'`) != 1 {
		t.Fatal("a replayed frame stored twice")
	}
	if count(t, c, `SELECT COUNT(*) FROM agents_events WHERE type='tool' AND session_id='s1'`) != 1 {
		t.Fatal("tool event missing")
	}
	var work string
	var known int
	var pa, pq sql.NullInt64
	var tools int
	if err := c.Store.DB.Reader.QueryRow(`SELECT work_state,work_known,pending_approval,pending_question,tool_calls FROM agents_sessions WHERE session_id='s1'`).Scan(&work, &known, &pa, &pq, &tools); err != nil {
		t.Fatal(err)
	}
	if work != "processing" || known != 1 || !pa.Valid || pa.Int64 != 1 || pq.Valid || tools != 1 {
		t.Fatalf("s1: work=%s known=%d pa=%v pq=%v tools=%d (an absent flag must stay NULL = unknown)", work, known, pa, pq, tools)
	}
	var ended int64
	var reason string
	_ = c.Store.DB.Reader.QueryRow(`SELECT ended_ns,end_reason FROM agents_sessions WHERE session_id='s2'`).Scan(&ended, &reason)
	if ended == 0 || reason != "restart" {
		t.Fatalf("s2 end: %d %q", ended, reason)
	}
	st, _ := c.Store.State(context.Background())
	if st.Strategy != "snapshot" || st.Env != "env1" {
		t.Fatalf("state: %+v", st)
	}
	src.mu.Lock()
	if len(src.streamedAt) == 0 || src.streamedAt[0] != 100 {
		t.Fatalf("stream must start after the snapshot high-water mark: %v", src.streamedAt)
	}
	src.mu.Unlock()
	// output activity keeps the session fresh without storing a row
	var act int64
	_ = c.Store.DB.Reader.QueryRow(`SELECT last_activity_ns FROM agents_sessions WHERE session_id='s1'`).Scan(&act)
	if time.Unix(0, act).UTC().Format(time.RFC3339) != "2026-10-30T01:00:03Z" {
		t.Fatalf("output must count as activity: %v", time.Unix(0, act).UTC())
	}
}

func TestNothingRawIsPersistedAnywhere(t *testing.T) {
	src := &fakeSource{
		snap: Snapshot{Env: "e", HighWater: 1},
		streamFrame: []string{
			frameJSON("e:2", "session.output", "s1", "2026-10-30T01:00:00Z", `{"data":"`+secret+`"}`),
			frameJSON("e:3", "tool.call.finished", "s1", "2026-10-30T01:00:01Z", `{"tool":"Bash","args":"curl -H 'Authorization: `+secret+`'","input":{"command":"`+secret+`"}}`),
			frameJSON("e:4", "session.message", "s1", "2026-10-30T01:00:02Z", `{"body":"`+secret+`","text":"`+secret+`"}`),
		},
	}
	c := newCollector(t, src)
	runUntil(t, c, func() bool {
		st, _ := c.Store.State(context.Background())
		return st.LastSeq >= 4 && count(t, c, `SELECT COUNT(*) FROM agents_events`) == 2
	})
	for _, table := range []string{"agents_events", "agents_sessions", "agents_gaps", "agents_collector_state"} {
		rows, err := c.Store.DB.Reader.Query(`SELECT * FROM ` + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			_ = rows.Scan(ptrs...)
			if strings.Contains(fmt.Sprint(vals...), secret) {
				t.Fatalf("a secret reached %s: %v", table, vals)
			}
		}
		rows.Close()
	}
}

func TestSnapshotFailureFallsBackAndIsRecorded(t *testing.T) {
	src := &fakeSource{snapErr: errors.New("HTTP 500"), head: 55, sessions: []Frame{{Kind: "session.snapshot", SessionID: "s1", SessionState: "running"}}}
	c := newCollector(t, src)
	runUntil(t, c, func() bool {
		st, _ := c.Store.State(context.Background())
		return st.Strategy == "fallback" && st.LastSeq == 55
	})
	if count(t, c, `SELECT COUNT(*) FROM agents_sessions WHERE session_id='s1'`) != 1 {
		t.Fatal("session list not applied")
	}
	s, _ := c.Status(context.Background())
	if s.Strategy != "fallback" {
		t.Fatalf("status: %+v", s)
	}
}

func TestStreamGapIsRecordedAndResynced(t *testing.T) {
	src := &fakeSource{snap: Snapshot{Env: "e", HighWater: 10}, streamFrame: []string{`{"kind":"gap","reason":"purged"}`}}
	c := newCollector(t, src)
	runUntil(t, c, func() bool { return count(t, c, `SELECT COUNT(*) FROM agents_gaps WHERE reason='purged'`) >= 1 })
	if n, _ := c.Store.OpenGaps(context.Background()); n != 1 {
		t.Fatalf("a retried pass must not duplicate the same hole: %d open", n)
	}
}

func TestOfflineGapIsBackfilledOrStaysOpen(t *testing.T) {
	old := time.Now().UTC().Add(-time.Hour)
	mk := func(seq int64, typ string) Frame {
		return Frame{Seq: seq, EventID: fmt.Sprint("e:", seq), Kind: typ, SessionID: "s1", At: old.Add(time.Duration(seq) * time.Second), Tool: "Read"}
	}
	t.Run("filled", func(t *testing.T) {
		src := &fakeSource{history: []Frame{mk(30, "tool.call.finished"), mk(20, "tool.call.finished"), mk(15, "tool.call.finished")}}
		c := newCollector(t, src)
		seed(t, c, 10, old)
		runUntil(t, c, func() bool { return count(t, c, `SELECT COUNT(*) FROM agents_gaps WHERE backfilled=1`) == 1 })
		if count(t, c, `SELECT COUNT(*) FROM agents_events`) != 3 {
			t.Fatal("history not applied")
		}
		if st, _ := c.Store.State(context.Background()); st.LastSeq != 30 {
			t.Fatalf("cursor %d", st.LastSeq)
		}
	})
	t.Run("history unavailable leaves the hole open", func(t *testing.T) {
		src := &fakeSource{histErr: errors.New("HTTP 503")}
		c := newCollector(t, src)
		seed(t, c, 10, old)
		runUntil(t, c, func() bool { return count(t, c, `SELECT COUNT(*) FROM agents_gaps`) >= 1 })
		if n, _ := c.Store.OpenGaps(context.Background()); n != 1 {
			t.Fatalf("open gaps %d", n)
		}
		s, _ := c.Status(context.Background())
		if s.Collecting || s.OpenGaps != 1 || s.Sources["tether"]["state"] != "unreachable" {
			t.Fatalf("an unreachable Tether must not look healthy: %+v", s)
		}
	})
}

func seed(t *testing.T, c *Collector, seq int64, at time.Time) {
	t.Helper()
	err := c.Store.DB.Writer.Submit(context.Background(), "seed", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agents_collector_state SET last_seq=?, last_event_at_ns=?, strategy='snapshot' WHERE id=1`, seq, at.UnixNano())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWatchdogReconnectsASilentStream(t *testing.T) {
	src := &fakeSource{snap: Snapshot{Env: "e", HighWater: 1}, hold: true}
	c := newCollector(t, src)
	c.Watchdog = 80 * time.Millisecond
	runUntil(t, c, func() bool {
		src.mu.Lock()
		defer src.mu.Unlock()
		return len(src.streamedAt) >= 2
	})
}

func TestAPanicInAPassIsRecoveredWithoutLeakingItsValue(t *testing.T) {
	src := &fakeSource{snap: Snapshot{Env: "e", HighWater: 1}, panicOn: true}
	c := newCollector(t, src)
	err := c.pass(context.Background())
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("panic err: %v", err)
	}
}

func TestRetentionPrunesInBatchesAndLeavesReceipts(t *testing.T) {
	c := newCollector(t, &fakeSource{})
	now := time.Now().UTC()
	err := c.Store.DB.Writer.Submit(context.Background(), "seed", func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < 2500; i++ {
			at := now.Add(-8 * 24 * time.Hour).UnixNano()
			if i%5 == 0 {
				at = now.UnixNano()
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO agents_events(session_id,tether_seq,tether_event_id,type,at_ns,summary) VALUES('s',?,?,'tool',?,'x')`, i, fmt.Sprint("e:", i), at); err != nil {
				return err
			}
		}
		tx.ExecContext(ctx, `INSERT INTO agents_gaps(start_ns,end_ns,reason) VALUES(?,?,'old')`, now.Add(-40*24*time.Hour).UnixNano(), now.Add(-40*24*time.Hour).UnixNano())
		tx.ExecContext(ctx, `INSERT INTO agents_sessions(session_id,ended_ns,updated_ns) VALUES('gone',?,1),('live',0,1)`, now.Add(-31*24*time.Hour).UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := c.Store.Prune(context.Background())
	if err != nil || n != 2000+1+1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	if count(t, c, `SELECT COUNT(*) FROM agents_events`) != 500 || count(t, c, `SELECT COUNT(*) FROM agents_sessions`) != 1 {
		t.Fatal("wrong rows kept")
	}
	st, _ := c.Store.State(context.Background())
	if st.Pruned != n || st.RetentionLast.IsZero() {
		t.Fatalf("receipt: %+v", st)
	}
}

func TestStatusRouteShape(t *testing.T) {
	c := newCollector(t, &fakeSource{})
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, Path, nil))
	var m map[string]any
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil || w.Code != 200 {
		t.Fatal(w.Code, err)
	}
	for _, k := range []string{"collecting", "sessions", "last_event_at", "strategy", "sources", "dropped_output_frames", "open_gaps", "retention", "bus_topics"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
	if m["collecting"] != false || m["last_event_at"] != nil {
		t.Errorf("a collector that never ran: %v", m)
	}
}

func TestHTTPSourceStreamsAndGuardsTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Tether-Protocol") != "1" {
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/environment/events":
			if r.URL.Query().Get("after_seq") != "5" {
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, ": hello\n\n")
			fmt.Fprintf(w, "data: %s\n\n", frameJSON("e:6", "session.work_state_changed", "s1", "2026-10-30T01:00:00Z", `{"to":"idle"}`))
			fmt.Fprintf(w, "data: %s\n\n", frameJSON("e:7", "session.output", "s1", "2026-10-30T01:00:00Z", `{"data":"x"}`))
		case "/environment/snapshot":
			fmt.Fprint(w, `{"env_id":"e","high_water_seq":5,"sessions":[{"session_id":"s1","session_state":"running","work_state":{"state":"unknown","known":false},"pending_approval":false}]}`)
		case "/events":
			fmt.Fprint(w, `{"events":[{"seq":9,"kind":"session.work_state_changed","session_id":"s1","at":"2026-10-30T01:00:00Z","payload_json":{"to":"idle"}}],"next_cursor":0}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	s, err := NewHTTPSource(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []Frame
	err = s.Stream(context.Background(), 5, func(f Frame) error { got = append(got, f); return nil }, func() {})
	if err == nil || len(got) != 2 || got[0].Seq != 6 || !got[1].Output {
		t.Fatalf("stream: %v %+v", err, got)
	}
	snap, err := s.Snapshot(context.Background())
	if err != nil || snap.HighWater != 5 || len(snap.Sessions) != 1 || snap.Sessions[0].WorkState != "" || snap.Sessions[0].PendingApproval == nil {
		t.Fatalf("snapshot: %v %+v (work_state known:false must stay unknown)", err, snap)
	}
	if h, err := s.Head(context.Background()); err != nil || h != 9 {
		t.Fatalf("head: %d %v", h, err)
	}
	if _, err := NewHTTPSource("http://tether.example.com:8080", "/tmp/token"); err == nil {
		t.Fatal("a token must not be sent over plain http to a non-loopback host")
	}
	if _, err := NewHTTPSource("", ""); err == nil {
		t.Fatal("no address")
	}
}

func mustTime(v string) time.Time { t, _ := time.Parse(time.RFC3339, v); return t }

// Shapes seen against live Tether: the snapshot names the environment environment_id, and
// /sessions pages with an opaque string cursor.
func TestHTTPSourceReadsTheLiveSnapshotKeyAndOpaqueSessionCursors(t *testing.T) {
	var cursors []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/environment/snapshot":
			fmt.Fprint(w, `{"environment_id":"env-live-1","high_water_seq":7,"sessions":[]}`)
		case "/sessions":
			cursors = append(cursors, r.URL.Query().Get("cursor"))
			page := func(from, n int, next string) {
				var items []string
				for i := from; i < from+n; i++ {
					items = append(items, fmt.Sprintf(`{"id":"s%d","session_state":"running"}`, i))
				}
				fmt.Fprintf(w, `{"sessions":[%s],"next_cursor":%s}`, strings.Join(items, ","), next)
			}
			switch r.URL.Query().Get("cursor") {
			case "":
				page(0, 200, `"v1.eyJvIjoyMDB9"`)
			case "v1.eyJvIjoyMDB9":
				page(200, 50, `""`)
			default:
				w.WriteHeader(400)
			}
		case "/events":
			fmt.Fprint(w, `{"events":[{"seq":9,"kind":"session.work_state_changed","session_id":"s1","at":"2026-10-30T01:00:00Z","payload_json":{"to":"idle"}}],"next_cursor":"v1.opaque"}`)
		}
	}))
	defer srv.Close()
	s, err := NewHTTPSource(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot(context.Background())
	if err != nil || snap.Env != "env-live-1" || snap.HighWater != 7 {
		t.Fatalf("snapshot: %v %+v", err, snap)
	}
	got, err := s.Sessions(context.Background())
	if err != nil || len(got) != 250 || len(cursors) != 2 || cursors[1] != "v1.eyJvIjoyMDB9" {
		t.Fatalf("more than 200 sessions must page on the opaque cursor: %d %v %v", len(got), cursors, err)
	}
	if _, next, err := s.History(context.Background(), 0, 0, 10); err != nil || next != 0 {
		t.Fatalf("an opaque /events cursor ends the walk, it does not fail it: %d %v", next, err)
	}
}
