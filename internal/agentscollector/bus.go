package agentscollector

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/tachyon/internal/agentsapi"
)

// BusRef is a cite: a task, a pull request or a session.
type BusRef struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Label string `json:"label"`
}

type BusCard struct {
	Title     string   `json:"title"`
	Cite      []BusRef `json:"cite"`
	Summary   string   `json:"summary"`
	ReviewRef *string  `json:"review_ref"`
}

// BusEvent is the normalised activity-feed event (agentsapi: BusEvent).
type BusEvent struct {
	Seq      int64
	EventID  string
	At       time.Time
	Type     string
	Subject  string
	Actor    string
	App      string
	Severity string
	Refs     []BusRef
	Card     *BusCard
}

var (
	taskRefRE = regexp.MustCompile(`^[A-Z]{2,4}-\d{8}-\d{4}$`)
	prRefRE   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,60}/[A-Za-z0-9._-]{1,80}#\d{1,7}$`)
	nameRE    = regexp.MustCompile(`^[A-Za-z0-9._:/@? -]{1,80}$`)
	stateRE   = regexp.MustCompile(`^[a-z_]{2,24}$`)
)

func okName(s string) string {
	s = clean(s, 80)
	if nameRE.MatchString(s) {
		return s
	}
	return ""
}

// busType maps a Tether kind to a feed type, or "" when the kind is not part of the feed.
func busType(f Frame) string {
	k := f.Kind
	switch {
	case k == "deploy.state_changed":
		return "deploy"
	case k == "resource.state_changed" && f.Reason == "restart":
		return "restart"
	case strings.HasPrefix(k, "fleet.node."):
		return "host"
	case k == "alert.raised" || k == "alert.cleared":
		return "failure"
	case k == "task.state_changed":
		return "task"
	case k == "workstream.created":
		return "workstream"
	case strings.HasPrefix(k, "card."):
		return "card"
	case f.App == "" && (k == "session.started" || k == "session.ended" || k == "session.resumed" || k == "session.restarted"):
		return "lifecycle"
	}
	return ""
}

// NormalizeBus builds the feed event from a frame's kind and allowlisted fields. The subject is a
// sentence assembled here, never copied from payload text beyond a validated name.
func NormalizeBus(f Frame) (BusEvent, bool) {
	typ := busType(f)
	if typ == "" || f.EventID == "" {
		return BusEvent{}, false
	}
	b := BusEvent{Seq: f.Seq, EventID: f.EventID, At: f.At, Type: typ, App: okName(f.App), Actor: okName(f.BusActor), Severity: "info", Refs: []BusRef{}}
	if b.Actor == "" {
		b.Actor = "system"
	}
	if b.App == "" {
		b.App = "tether"
	}
	name := okName(f.Name)
	if name == "" {
		name = "unknown"
	}
	state := f.State
	if !stateRE.MatchString(state) {
		state = ""
	}
	var summary string
	switch typ {
	case "deploy":
		v := ""
		if x := okName(f.Version); x != "" {
			v = " (" + x + ")"
		}
		switch state {
		case "started":
			b.Subject = name + " deploy started"
		case "succeeded":
			b.Subject = name + " deployed" + v
		case "failed":
			b.Subject, b.Severity = name+" deploy failed", "error"
		case "rolled_back":
			b.Subject, b.Severity = name+" rolled back", "warn"
		default:
			b.Subject = name + " deploy " + strings.ReplaceAll(orDash(state), "_", " ")
		}
		summary = b.Subject
	case "restart":
		b.Subject, b.Severity = name+" restarted", "warn"
	case "host":
		b.Subject = name + " " + strings.TrimPrefix(f.Kind, "fleet.node.")
		if strings.HasSuffix(f.Kind, ".down") || strings.HasSuffix(f.Kind, ".offline") {
			b.Severity = "warn"
		}
	case "failure":
		if f.Kind == "alert.cleared" {
			b.Subject = name + " recovered"
		} else {
			b.Subject, b.Severity = name+" alert raised", "error"
		}
		summary = clean(redact.Text(f.Title), 160)
	case "task":
		if f.TaskRef != "" && taskRefRE.MatchString(f.TaskRef) {
			b.Refs = append(b.Refs, BusRef{"task", f.TaskRef, f.TaskRef})
			name = f.TaskRef
		}
		b.Subject = name + " " + orDash(strings.ReplaceAll(state, "_", " "))
		summary = clean(redact.Text(f.Title), 160)
	case "workstream":
		b.Subject = "workstream " + name + " created"
	case "card":
		b.Subject = "card " + orDash(strings.TrimPrefix(f.Kind, "card.")) + ": " + name
	case "lifecycle":
		who := name
		if f.SessionID != "" {
			who = f.SessionID
			b.Refs = append(b.Refs, BusRef{"session", f.SessionID, shortID(f.SessionID)})
		}
		b.Subject = who + " " + strings.TrimPrefix(f.Kind, "session.")
		if f.Reason != "" {
			b.Subject += " (" + f.Reason + ")"
		}
	}
	if f.PR != "" && prRefRE.MatchString(f.PR) {
		b.Refs = append(b.Refs, BusRef{"pr", f.PR, f.PR[strings.LastIndex(f.PR, "#"):]})
	}
	b.Subject = clean(b.Subject, 160)
	if summary != "" || len(b.Refs) > 0 && (typ == "deploy" || typ == "task" || typ == "failure") {
		c := &BusCard{Title: b.Subject, Cite: b.Refs, Summary: clean(summary, 200)}
		if typ == "task" && (state == "done" || state == "closed") {
			for _, r := range b.Refs {
				if r.Kind == "pr" {
					ref := r.Ref
					c.ReviewRef = &ref
				}
			}
		}
		b.Card = c
	}
	return b, true
}

func orDash(s string) string {
	if s == "" {
		return "changed"
	}
	return s
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func insertBus(ctx context.Context, tx *sql.Tx, b BusEvent, now int64) error {
	at := b.At.UnixNano()
	if b.At.IsZero() {
		at = now
	}
	refs, _ := json.Marshal(b.Refs)
	var card any
	if b.Card != nil {
		cj, _ := json.Marshal(b.Card)
		card = string(cj)
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO bus_events(tether_seq,tether_event_id,at_ns,type,subject,actor,app,severity,refs_json,card_json) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		b.Seq, b.EventID, at, b.Type, b.Subject, b.Actor, b.App, b.Severity, string(refs), card)
	return err
}

// BusQuery is the feed filter. Type accepts the UI names (deploys, restarts, tasks, failures) or the types.
type BusQuery struct {
	Before int64
	Limit  int
	Type   string
	App    string
	Q      string
}

var busTypes = map[string]string{"deploys": "deploy", "restarts": "restart", "tasks": "task", "failures": "failure",
	"deploy": "deploy", "restart": "restart", "task": "task", "failure": "failure", "host": "host", "workstream": "workstream", "lifecycle": "lifecycle", "card": "card"}

// BusEvents returns the feed newest first, and the cursor for the next page (0 = none).
func (s *Store) BusEvents(ctx context.Context, q BusQuery) ([]map[string]any, int64, error) {
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 50
	}
	sqlq := `SELECT id,tether_seq,at_ns,type,subject,actor,app,severity,refs_json,card_json FROM bus_events WHERE 1=1`
	var args []any
	if q.Before > 0 {
		sqlq += ` AND tether_seq<?`
		args = append(args, q.Before)
	}
	if q.Type != "" {
		sqlq += ` AND type=?`
		args = append(args, busTypes[q.Type])
	}
	if q.App != "" {
		sqlq += ` AND app=?`
		args = append(args, q.App)
	}
	if q.Q != "" {
		sqlq += ` AND LOWER(subject) LIKE ? ESCAPE '\'`
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(q.Q))+"%")
	}
	sqlq += ` ORDER BY tether_seq DESC, id DESC LIMIT ?`
	args = append(args, q.Limit+1)
	rows, err := s.DB.Reader.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, seq, ns int64
		var typ, subject, actor, app, sev, refs string
		var card sql.NullString
		if err := rows.Scan(&id, &seq, &ns, &typ, &subject, &actor, &app, &sev, &refs, &card); err != nil {
			return nil, 0, err
		}
		e := map[string]any{"id": "bus_" + strconv.FormatInt(id, 10), "seq": seq, "at": ns / int64(time.Millisecond), "type": typ, "subject": subject, "actor": actor, "app": app,
			"severity": sev, "refs": json.RawMessage(refs), "card": nil}
		if card.Valid {
			e["card"] = json.RawMessage(card.String)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var next int64
	if len(out) > q.Limit {
		out = out[:q.Limit]
		next = out[len(out)-1]["seq"].(int64)
	}
	return out, next, nil
}

// BusTopics reports, per topic, whether it can have data today: live (an event of that type arrived
// in the last 7 days), off (none did, but an emitter may exist behind a disabled export), or
// no_emitter (nothing in the fleet produces it; design section 6).
func (s *Store) BusTopics(ctx context.Context) map[string]string {
	out := map[string]string{"deploy": "off", "restart": "off", "task": "off", "failure": "off", "host": "no_emitter", "workstream": "no_emitter", "lifecycle": "off", "card": "off"}
	cut := s.now().Add(-EventRetention).UnixNano()
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT DISTINCT type FROM bus_events WHERE at_ns>=?`, cut)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			out[t] = "live"
		}
	}
	return out
}

// BusHandler serves GET agents/bus/events.
func (c *Collector) BusHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := func(status int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}
		bad := func(msg string) { reply(400, map[string]string{"code": "invalid", "message": msg}) }
		q := r.URL.Query()
		bq := BusQuery{Type: q.Get("type"), App: q.Get("app"), Q: strings.TrimSpace(q.Get("q"))}
		if bq.Type != "" && busTypes[bq.Type] == "" {
			bad("type must be deploys, restarts, tasks or failures.")
			return
		}
		if bq.App != "" && okName(bq.App) != bq.App {
			bad("app is not a valid name.")
			return
		}
		if v := q.Get("before_seq"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				bad("before_seq must be a non-negative integer.")
				return
			}
			bq.Before = n
		}
		if v := q.Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 100 {
				bad("limit must be 1 to 100.")
				return
			}
			bq.Limit = n
		}
		evs, next, err := c.Store.BusEvents(r.Context(), bq)
		if err != nil {
			reply(500, map[string]string{"code": "internal", "message": "The request could not be completed."})
			return
		}
		var nb any
		if next > 0 {
			nb = next
		}
		reply(200, map[string]any{"events": evs, "next_before": nb})
	})
}

// BusPath is the feed route.
const BusPath = agentsapi.Prefix + "/bus/events"
