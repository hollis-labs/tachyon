package planner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Ingest turns planner revisions, Torque task changes and merged PRs into timeline events and
// stream entries. Every step is idempotent by (source, source_event_id), so a restart or a second
// pass never repeats an event.
type Ingest struct {
	Owned  *Owned
	Tasks  *Tasks
	Events Events
	DB     *localstore.Store
}

var kindDB = map[string]string{"strategic_priority": "priorities", "workstream": "workstreams", "roadmap_item": "roadmap", "idea": "ideas", "decision": "decisions", "risk": "risks", "inbox_item": "inbox"}

type revisionDoc struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Definition struct {
		Status     string `json:"status"`
		Properties struct {
			Portfolio struct {
				Comments  []map[string]any `json:"comments"`
				Relations []map[string]any `json:"relations"`
			} `json:"portfolio"`
		} `json:"properties"`
	} `json:"definition"`
}

func parseRevision(raw string) (revisionDoc, bool) {
	var d revisionDoc
	return d, json.Unmarshal([]byte(raw), &d) == nil
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// themesOf lists the themes an item belongs to: the theme itself, or themes that name it in one of
// their id arrays, or that it names in priority_ids.
func (g *Ingest) themesOf(ctx context.Context, themes []Item, db, id string, item Item) []string {
	if db == "priorities" {
		return []string{id}
	}
	out := []string{}
	for _, t := range themes {
		hit := false
		for _, f := range []string{"workstream_ids", "decision_ids", "risk_ids", "roadmap_ids", "idea_ids", "inbox_ids", "related_ids"} {
			for _, v := range array(t[f]) {
				hit = hit || fmt.Sprint(v) == id
			}
		}
		for _, v := range array(item["priority_ids"]) {
			hit = hit || fmt.Sprint(v) == textField(t, "id") || fmt.Sprint(v) == textField(t, "legacy_id")
		}
		for _, r := range relationsOf(item) {
			hit = hit || r.To.ID == textField(t, "id")
		}
		if hit {
			out = append(out, textField(t, "id"))
		}
	}
	return out
}

// Tail reads new planner object revisions since the stored cursor. The first call only sets the
// cursor to now. It writes planner.changed and planner.item stream entries and derives events.
func (g *Ingest) Tail(ctx context.Context) error {
	kinds := `'strategic_priority','workstream','roadmap_item','idea','decision','risk','inbox_item'`
	var max int64
	if err := g.DB.Reader.QueryRowContext(ctx, `SELECT coalesce(max(r.rowid),0) FROM object_revisions r JOIN objects o ON o.id=r.object_id WHERE o.ownership='tachyon' AND o.kind IN (`+kinds+`)`).Scan(&max); err != nil {
		return err
	}
	cur, ok := stateGet(ctx, g.DB.Reader, stateTail)
	if !ok {
		return g.DB.Writer.Submit(ctx, "planner_tail_init", func(ctx context.Context, tx *sql.Tx) error {
			if _, have := stateGet(ctx, tx, stateTail); have {
				return nil
			}
			return statePut(ctx, tx, stateTail, strconv.FormatInt(max, 10))
		})
	}
	last, _ := strconv.ParseInt(cur, 10, 64)
	if max <= last {
		return nil
	}
	rows, err := g.DB.Reader.QueryContext(ctx, `SELECT r.rowid, r.object_id, r.revision, r.actor, r.recorded_at, r.record_json FROM object_revisions r JOIN objects o ON o.id=r.object_id WHERE r.rowid>? AND r.rowid<=? AND o.ownership='tachyon' AND o.kind IN (`+kinds+`) ORDER BY r.rowid LIMIT 500`, last, max)
	if err != nil {
		return err
	}
	type rev struct {
		rowid, revision int64
		id, actor, at   string
		raw             string
	}
	var batch []rev
	for rows.Next() {
		var r rev
		if err := rows.Scan(&r.rowid, &r.id, &r.revision, &r.actor, &r.at, &r.raw); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, r)
	}
	rows.Close()
	if len(batch) == 0 {
		return g.setCursor(ctx, max)
	}
	themes, _ := g.Owned.List(ctx, "priorities", nil, "")
	var evs []Event
	for _, r := range batch {
		cur, ok := parseRevision(r.raw)
		if !ok {
			continue
		}
		var prev revisionDoc
		hasPrev := false
		if r.revision > 1 {
			var raw string
			if g.DB.Reader.QueryRowContext(ctx, `SELECT record_json FROM object_revisions WHERE object_id=? AND revision=?`, r.id, r.revision-1).Scan(&raw) == nil {
				prev, hasPrev = parseRevision(raw)
			}
		}
		at, _ := time.Parse(time.RFC3339Nano, r.at)
		evs = append(evs, g.revisionEvents(ctx, themes, r.id, r.revision, r.actor, at, cur, prev, hasPrev)...)
	}
	last = batch[len(batch)-1].rowid
	if err := g.DB.Writer.Submit(ctx, "planner_tail", func(ctx context.Context, tx *sql.Tx) error {
		if err := streamAppend(ctx, tx, "planner.changed", Item{"source": "tachyon", "refresh": true}); err != nil {
			return err
		}
		for _, r := range batch {
			d, ok := parseRevision(r.raw)
			if !ok {
				continue
			}
			if err := streamAppend(ctx, tx, "planner.item", Item{"db": PublicDB(kindDB[d.Kind]), "id": r.id, "rev": r.revision}); err != nil {
				return err
			}
		}
		return statePut(ctx, tx, stateTail, strconv.FormatInt(last, 10))
	}); err != nil {
		return err
	}
	_, err = g.Events.Append(ctx, evs...)
	return err
}

func (g *Ingest) setCursor(ctx context.Context, v int64) error {
	return g.DB.Writer.Submit(ctx, "planner_tail_cursor", func(ctx context.Context, tx *sql.Tx) error {
		return statePut(ctx, tx, stateTail, strconv.FormatInt(v, 10))
	})
}

func (g *Ingest) revisionEvents(ctx context.Context, themes []Item, id string, revision int64, actor string, at time.Time, cur, prev revisionDoc, hasPrev bool) []Event {
	db := kindDB[cur.Kind]
	if db == "" {
		return nil
	}
	item, _ := g.Owned.Get(ctx, db, id)
	ts := g.themesOf(ctx, themes, db, id, item)
	ms := at.UnixMilli()
	subject := PublicDB(db) + "/" + id
	ref := []Item{{"kind": "planner", "ref": subject}}
	src := func(n string) string { return fmt.Sprintf("%s:%d:%s", id, revision, n) }
	var out []Event
	emit := func(theme, kind, text, arrow, n string) {
		out = append(out, Event{ThemeID: theme, SubjectRef: subject, Kind: kind, Text: text, Arrow: arrow, Source: "revision", SourceEventID: src(n + ":" + theme), AtMS: ms, Refs: ref})
	}
	forThemes := func(kind, text, arrow, n string) {
		if len(ts) == 0 {
			emit("", kind, text, arrow, n)
		}
		for _, t := range ts {
			emit(t, kind, text, arrow, n)
		}
	}
	switch db {
	case "priorities":
		if hasPrev && prev.Definition.Status != cur.Definition.Status {
			arrow := "flat"
			switch cur.Definition.Status {
			case "approved", "done":
				arrow = "up"
			case "paused", "archived":
				arrow = "down"
			}
			emit(id, "lifecycle", fmt.Sprintf("%s: %s → %s", cur.Name, prev.Definition.Status, cur.Definition.Status), arrow, "status")
		}
	case "decisions":
		if !hasPrev {
			forThemes("decision", "Decision opened: "+cur.Name, "flat", "open")
		} else if prev.Definition.Status != cur.Definition.Status && cur.Definition.Status == "accepted" {
			forThemes("decision", "Decision made: "+cur.Name, "up", "made")
		}
	case "workstreams":
		if hasPrev && prev.Definition.Status != cur.Definition.Status && (cur.Definition.Status == "closed" || cur.Definition.Status == "archived") {
			forThemes("workstream", "Workstream "+cur.Definition.Status+": "+cur.Name, "up", "closed")
		}
	}
	before := 0
	if hasPrev {
		before = len(prev.Definition.Properties.Portfolio.Comments)
	}
	for i, c := range cur.Definition.Properties.Portfolio.Comments {
		if i < before {
			continue
		}
		if db == "priorities" && lifecycleAudit.MatchString(fmt.Sprint(c["text"])) {
			continue // the lifecycle action's own audit line; the lifecycle event already covers it
		}
		forThemes("comment", fmt.Sprintf("%v: %s", c["author"], clip(fmt.Sprint(c["text"]), 140)), "flat", fmt.Sprintf("comment%d", i))
	}
	beforeRel := 0
	if hasPrev {
		beforeRel = len(prev.Definition.Properties.Portfolio.Relations)
	}
	for i, r := range cur.Definition.Properties.Portfolio.Relations {
		if i < beforeRel {
			continue
		}
		forThemes("link", fmt.Sprintf("%s %s %v", cur.Name, r["kind"], object(r["to"])["id"]), "flat", fmt.Sprintf("link%d", i))
	}
	return out
}

var lifecycleAudit = regexp.MustCompile(`^(ready|approved|parked|unparked|archived|finished|verified|resequenced)\b`)

var taskRef = regexp.MustCompile(`[A-Z]{2,5}-\d{8}-\d{4}`)

func laneDelta(from, to string) map[string]int {
	d := zeroDelta()
	if _, ok := d[from]; ok {
		d[from]--
	}
	if _, ok := d[to]; ok {
		d[to]++
	}
	return d
}

// PollTorque compares each active theme's tasks with the stored snapshot and records the moves.
// A task seen for the first time only records a baseline. Moves between two polls are not seen
// individually: Torque has no stable event ids yet.
func (g *Ingest) PollTorque(ctx context.Context) error {
	themes, err := g.Owned.List(ctx, "priorities", nil, "")
	if err != nil {
		return err
	}
	var evs []Event
	type snap struct{ k, v string }
	var snaps []snap
	for _, th := range themes {
		switch textField(th, "status") {
		case "archived", "done", "achieved":
			continue
		}
		ws, err := g.Owned.themeWorkstreams(ctx, th)
		if err != nil || len(ws) == 0 {
			continue
		}
		tasks, _, err := g.Tasks.scopeTasks(ctx, ws)
		if err != nil {
			return err
		}
		for _, t := range tasks {
			key := "task:" + textField(th, "id") + ":" + t.ID
			old, seen := stateGet(ctx, g.DB.Reader, key)
			snaps = append(snaps, snap{key, t.Status}, snap{"tasktheme:" + t.ID, textField(th, "id")})
			if !seen || old == t.Status {
				continue
			}
			from, _ := Lane(old)
			to, _ := Lane(t.Status)
			kind, arrow := "task", "flat"
			text := fmt.Sprintf("%s moved to %s", t.ID, strings.ToUpper(t.Status[:1])+t.Status[1:])
			switch {
			case t.Status == "blocked":
				kind, arrow, text = "blocker", "down", "Blocked: "+t.Title
			case t.Status == "done" || t.Status == "review":
				arrow = "up"
			case old == "blocked":
				arrow = "up"
			}
			evs = append(evs, Event{ThemeID: textField(th, "id"), SubjectRef: t.ID, Kind: kind, Text: text, Arrow: arrow, Source: "torque", SourceEventID: t.ID + ":" + old + ">" + t.Status + ":" + t.UpdatedAt, AtMS: parseTime(t.UpdatedAt), Refs: []Item{{"kind": "torque", "ref": t.ID}}, Delta: laneDelta(from, to), Status: t.Status})
		}
	}
	if _, err := g.Events.Append(ctx, evs...); err != nil {
		return err
	}
	return g.DB.Writer.Submit(ctx, "planner_task_snapshot", func(ctx context.Context, tx *sql.Tx) error {
		for _, s := range snaps {
			if err := statePut(ctx, tx, s.k, s.v); err != nil {
				return err
			}
		}
		return nil
	})
}

func parseTime(s string) int64 {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UnixMilli()
	}
	return time.Now().UnixMilli()
}

// PollPRs records merged PRs the forge feed put in the alerts store, matched to a theme through the
// task id in the PR title. PRs without a known task are not placed on a theme.
func (g *Ingest) PollPRs(ctx context.Context) error {
	cur, _ := stateGet(ctx, g.DB.Reader, "pr_cursor")
	last, _ := strconv.ParseInt(cur, 10, 64)
	rows, err := g.DB.Reader.QueryContext(ctx, `SELECT seq,title,subjects_json,last_at_ms FROM alerts WHERE source='forge' AND kind='pr.merged' AND seq>? ORDER BY seq LIMIT 200`, last)
	if err != nil {
		return nil // no alerts store: nothing to ingest
	}
	type alert struct {
		seq, at        int64
		title, subject string
	}
	var found []alert
	for rows.Next() {
		var a alert
		var subjects string
		if err := rows.Scan(&a.seq, &a.title, &subjects, &a.at); err != nil {
			rows.Close()
			return err
		}
		var subs []map[string]any
		_ = json.Unmarshal([]byte(subjects), &subs)
		if len(subs) > 0 {
			a.subject = fmt.Sprint(subs[0]["ref"])
		}
		found = append(found, a)
	}
	rows.Close()
	var evs []Event
	for _, a := range found {
		for _, id := range taskRef.FindAllString(a.title, -1) {
			themeIDs, _ := stateGet(ctx, g.DB.Reader, "tasktheme:"+id)
			if themeIDs == "" {
				continue
			}
			ref := strings.Replace(a.subject, "#", "#", 1)
			evs = append(evs, Event{ThemeID: themeIDs, SubjectRef: id, Kind: "pr", Text: "PR merged: " + clip(a.title, 120), Arrow: "up", Source: "forge", SourceEventID: fmt.Sprintf("%d:%s", a.seq, id), AtMS: a.at, Refs: []Item{{"kind": "pr", "ref": ref}, {"kind": "torque", "ref": id}}})
		}
	}
	if _, err := g.Events.Append(ctx, evs...); err != nil {
		return err
	}
	if len(found) == 0 {
		return nil
	}
	return g.DB.Writer.Submit(ctx, "planner_pr_cursor", func(ctx context.Context, tx *sql.Tx) error {
		return statePut(ctx, tx, "pr_cursor", strconv.FormatInt(found[len(found)-1].seq, 10))
	})
}

// Run is the supervised worker: each pass tails revisions, polls Torque and merged PRs and prunes.
// A failure is logged and recorded in ingest state; the next pass retries.
func (g *Ingest) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		g.pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (g *Ingest) pass(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("planner ingest panicked", "panic", r)
		}
	}()
	var failed []string
	for name, step := range map[string]func(context.Context) error{"revisions": g.Tail, "torque": g.PollTorque, "prs": g.PollPRs} {
		if err := step(ctx); err != nil {
			slog.Warn("planner ingest step failed", "step", name, "err", err)
			failed = append(failed, name)
		}
	}
	_ = g.Events.Prune(ctx, LoadSettings(ctx, g.DB).RetentionDays)
	g.announceNeedYou(ctx)
	_ = g.DB.Writer.Submit(ctx, "planner_ingest_status", func(ctx context.Context, tx *sql.Tx) error {
		if err := statePut(ctx, tx, "last_pass_ms", strconv.FormatInt(time.Now().UnixMilli(), 10)); err != nil {
			return err
		}
		return statePut(ctx, tx, "failed_steps", strings.Join(failed, ","))
	})
}

// announceNeedYou puts planner.need_you on the stream when the number of items needing the operator changes.
func (g *Ingest) announceNeedYou(ctx context.Context) {
	h := &HTTP{Owned: g.Owned, Tasks: g.Tasks, Store: g.DB}
	n, err := h.NeedYou(ctx)
	if err != nil {
		return
	}
	total := int(number(n["total"]))
	if old, ok := stateGet(ctx, g.DB.Reader, "need_you_total"); ok && old == strconv.Itoa(total) {
		return
	}
	_ = g.DB.Writer.Submit(ctx, "planner_need_you", func(ctx context.Context, tx *sql.Tx) error {
		if err := streamAppend(ctx, tx, "planner.need_you", Item{"total": total}); err != nil {
			return err
		}
		return statePut(ctx, tx, "need_you_total", strconv.Itoa(total))
	})
}
