package planner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/forgeindex"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/plannerapi"
)

// Lane maps a Torque status onto a theme-board lane; ok is false for closed-out statuses that have
// no lane (archived, abandoned, cancelled).
func Lane(status string) (string, bool) {
	switch status {
	case "backlog", "todo", "queued", "paused":
		return "todo", true
	case "doing", "review", "blocked", "done":
		return status, true
	}
	return "", false
}

// TaskMeta is what Tachyon keeps beside a Torque task.
type TaskMeta struct {
	Task, ThemeID, DoneSummary, By, At string
	PRCites                            []Item
}

type taskMetaStore struct{ db *localstore.Store }

func (m taskMetaStore) get(ctx context.Context, ids []string) (map[string]TaskMeta, error) {
	out := map[string]TaskMeta{}
	if m.db == nil || len(ids) == 0 {
		return out, nil
	}
	rows, err := m.db.Reader.QueryContext(ctx, `SELECT task_ref,theme_id,done_summary,pr_cites_json,summary_by,summary_at FROM planner_task_meta`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for rows.Next() {
		var t TaskMeta
		var cites string
		if err := rows.Scan(&t.Task, &t.ThemeID, &t.DoneSummary, &cites, &t.By, &t.At); err != nil {
			return nil, err
		}
		if !want[t.Task] {
			continue
		}
		var arr []any
		_ = json.Unmarshal([]byte(cites), &arr)
		for _, v := range arr {
			t.PRCites = append(t.PRCites, object(v))
		}
		out[t.Task] = t
	}
	return out, rows.Err()
}

func (m taskMetaStore) put(ctx context.Context, t TaskMeta) error {
	cites := t.PRCites
	if cites == nil {
		cites = []Item{}
	}
	return m.db.Writer.Submit(ctx, "planner_task_meta", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO planner_task_meta(task_ref,theme_id,done_summary,pr_cites_json,summary_by,summary_at,updated_ms) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(task_ref) DO UPDATE SET theme_id=excluded.theme_id,done_summary=excluded.done_summary,pr_cites_json=excluded.pr_cites_json,summary_by=excluded.summary_by,summary_at=excluded.summary_at,updated_ms=excluded.updated_ms`,
			t.Task, t.ThemeID, t.DoneSummary, string(encode(cites)), t.By, t.At, time.Now().UnixMilli())
		return err
	})
}

func priorityWord(p int) string {
	switch {
	case p >= 1 && p <= 2:
		return "high"
	case p >= 4:
		return "low"
	}
	return "medium"
}

// detectedCites finds open or recent PRs in the forge index whose title carries the task id.
// Branch and body are not in the index, so only titles match.
func (h *HTTP) detectedCites(ctx context.Context, ids []string) map[string][]Item {
	out := map[string][]Item{}
	if h.Store == nil {
		return out
	}
	snap, err := (&forgeindex.Index{DB: h.Store}).Snapshot(ctx)
	if err != nil {
		return out
	}
	for _, row := range snap.Rows {
		for _, id := range ids {
			if strings.Contains(row.Title, id) {
				out[id] = append(out[id], Item{"host": row.Ref.Repo.Host, "owner": row.Ref.Repo.Owner, "repo": row.Ref.Repo.Name, "number": row.Ref.Number, "source": "detected"})
			}
		}
	}
	return out
}

func citeKey(c Item) string {
	return fmt.Sprint(c["host"], "/", c["owner"], "/", c["repo"], "#", number(c["number"]))
}

// laneTask is the shape in a lane card.
func (h *HTTP) lanes(ctx context.Context, tasks []TaskBrief) (Item, map[string]int, error) {
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	metas, err := taskMetaStore{h.Store}.get(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	detected := h.detectedCites(ctx, ids)
	lanes := Item{}
	for _, l := range plannerapi.Lanes {
		lanes[l] = []Item{}
	}
	counts := map[string]int{}
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].UpdatedAt > tasks[j].UpdatedAt })
	for _, t := range tasks {
		lane, ok := Lane(t.Status)
		if !ok {
			continue
		}
		card := Item{"id": t.ID, "title": t.Title, "status": t.Status, "priority": priorityWord(t.Priority), "updated_at": t.UpdatedAt, "assignees": []Item{}}
		cites, seen := []Item{}, map[string]bool{}
		meta := metas[t.ID]
		for _, c := range meta.PRCites {
			cites, seen[citeKey(c)] = append(cites, c), true
		}
		for _, c := range detected[t.ID] {
			if !seen[citeKey(c)] {
				cites, seen[citeKey(c)] = append(cites, c), true
			}
		}
		card["pr_cites"] = cites
		if meta.DoneSummary != "" {
			card["done_summary"] = meta.DoneSummary
		}
		lanes[lane] = append(lanes[lane].([]Item), card)
		counts[lane]++
		counts["total"]++
	}
	for _, l := range plannerapi.Lanes {
		if _, ok := counts[l]; !ok {
			counts[l] = 0
		}
	}
	return lanes, counts, nil
}

// scopeTasks lists the Torque tasks of workstream rows, deduplicated.
func (t *Tasks) scopeTasks(ctx context.Context, rows []Item) ([]TaskBrief, []string, error) {
	seen, notes, out := map[string]bool{}, []string{}, []TaskBrief{}
	for _, row := range rows {
		tag := textField(row, "torque_tag")
		projects := []string{}
		for _, v := range array(row["torque_project_ids"]) {
			p, ok := v.(string)
			if !ok || !taskID.MatchString(p) {
				return nil, nil, objects.ErrInvalid
			}
			projects = append(projects, p)
		}
		if len(projects) == 0 && tag == "" {
			notes = append(notes, textField(row, "id")+" names no Torque project or tag")
			continue
		}
		if len(projects) == 0 {
			projects = []string{""}
		}
		for _, p := range projects {
			q := url.Values{}
			if p != "" {
				q.Set("project_id", p)
			}
			if tag != "" {
				q.Set("tags", tag)
			}
			list, err := t.List(ctx, q)
			if err != nil {
				return nil, nil, err
			}
			for _, r := range list {
				if !seen[r.ID] {
					seen[r.ID] = true
					out = append(out, r)
				}
			}
		}
	}
	return out, notes, nil
}

// themeWorkstreams are the workstreams a theme names (workstream_ids) or that name it (priority_ids).
func (s *Owned) themeWorkstreams(ctx context.Context, theme Item) ([]Item, error) {
	all, err := s.List(ctx, "workstreams", nil, "")
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, v := range array(theme["workstream_ids"]) {
		ids[fmt.Sprint(v)] = true
	}
	themeIDs := map[string]bool{textField(theme, "id"): true, textField(theme, "legacy_id"): true}
	out := []Item{}
	for _, w := range all {
		hit := ids[textField(w, "id")] || (textField(w, "legacy_id") != "" && ids[textField(w, "legacy_id")])
		for _, v := range array(w["priority_ids"]) {
			hit = hit || themeIDs[fmt.Sprint(v)]
		}
		if hit {
			out = append(out, w)
		}
	}
	return out, nil
}

// ThemeTasks groups the tasks of a theme's workstreams by lane.
func (h *HTTP) ThemeTasks(ctx context.Context, id string) (Item, error) {
	theme, err := h.Owned.Get(ctx, "priorities", id)
	if err != nil {
		return nil, err
	}
	ws, err := h.Owned.themeWorkstreams(ctx, theme)
	if err != nil {
		return nil, err
	}
	tasks, notes, err := h.Tasks.scopeTasks(ctx, ws)
	if err != nil {
		return nil, err
	}
	lanes, counts, err := h.lanes(ctx, tasks)
	if err != nil {
		return nil, err
	}
	out := Item{"theme_id": theme["id"], "counts": counts, "lanes": lanes, "source": Item{"torque": "live"}}
	if len(notes) > 0 {
		out["notes"] = notes
	}
	return out, nil
}

// ErrTaskConflict carries Torque's current status when a move's expected_status is stale.
type ErrTaskConflict struct{ ID, Status string }

func (e ErrTaskConflict) Error() string { return "task status changed" }

// ErrNeedsReason is returned when a block carries no reason.
var ErrNeedsReason = errors.New("a reason is required to block a task")

var prArg = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+#[0-9]+$`)

// Transition moves a task between lanes as a real Torque status change made with Tachyon's Torque token.
func (h *HTTP) Transition(ctx context.Context, task string, b Item, actor string) (Item, error) {
	to := textField(b, "to")
	if _, ok := Lane(to); !ok || !slices.Contains(plannerapi.Lanes, to) {
		return nil, fmt.Errorf("%w: to must be one of %s", objects.ErrInvalid, strings.Join(plannerapi.Lanes, ", "))
	}
	reason, summary := strings.TrimSpace(textField(b, "reason")), strings.TrimSpace(textField(b, "summary"))
	if to == "blocked" && reason == "" {
		return nil, ErrNeedsReason
	}
	if len([]rune(summary)) > plannerapi.MaxSummaryChars {
		return nil, fmt.Errorf("%w: summary is at most %d characters", objects.ErrInvalid, plannerapi.MaxSummaryChars)
	}
	if pr := textField(b, "pr"); pr != "" && !prArg.MatchString(pr) {
		return nil, fmt.Errorf("%w: pr is owner/repo#number", objects.ErrInvalid)
	}
	cur, err := h.Tasks.Get(ctx, task)
	if err != nil {
		return nil, err
	}
	if exp := textField(b, "expected_status"); exp != "" && exp != cur.Status {
		return nil, ErrTaskConflict{ID: task, Status: cur.Status}
	}
	if cur.Status == to {
		return Item{"task": Item{"id": task, "status": to, "previous": cur.Status}}, nil
	}
	comment := reason
	if comment == "" && to == "done" && summary != "" {
		comment = "Done: " + summary
	}
	if err := h.Tasks.Transition(ctx, task, to, comment, cur.Status == "done" || cur.Status == "archived"); err != nil {
		return nil, err
	}
	if summary != "" || textField(b, "pr") != "" {
		if err := h.saveMeta(ctx, task, summary, textField(b, "pr"), actor); err != nil {
			return nil, err
		}
	}
	prev, _ := Lane(cur.Status)
	delta := map[string]int{"done": 0, "doing": 0, "review": 0, "blocked": 0}
	if _, ok := delta[prev]; ok {
		delta[prev]--
	}
	if _, ok := delta[to]; ok {
		delta[to]++
	}
	return Item{"task": Item{"id": task, "status": to, "previous": cur.Status}, "event": Item{"kind": "task", "delta": delta}, "undo": Item{"to": cur.Status, "expected_status": to, "window_seconds": plannerapi.UndoWindowSeconds}}, nil
}

func parseCiteArg(pr string) Item {
	return prCite(pr)
}

// saveMeta merges a summary and/or a manual PR cite into the task's meta.
func (h *HTTP) saveMeta(ctx context.Context, task, summary, pr, actor string) error {
	store := taskMetaStore{h.Store}
	existing, err := store.get(ctx, []string{task})
	if err != nil {
		return err
	}
	m := existing[task]
	m.Task = task
	if summary != "" {
		m.DoneSummary, m.By, m.At = summary, strings.TrimPrefix(actor, "operator:"), time.Now().UTC().Format(time.RFC3339)
	}
	if c := parseCiteArg(pr); c != nil {
		c["source"] = "manual"
		dup := false
		for _, e := range m.PRCites {
			dup = dup || citeKey(e) == citeKey(c)
		}
		if !dup {
			m.PRCites = append(m.PRCites, c)
		}
	}
	return store.put(ctx, m)
}

// PutMeta replaces a task's done summary and manual PR cites, and posts a best-effort Torque comment.
func (h *HTTP) PutMeta(ctx context.Context, task string, b Item, actor string) (Item, error) {
	if !taskID.MatchString(task) {
		return nil, objects.ErrInvalid
	}
	if _, err := h.Tasks.Get(ctx, task); err != nil {
		return nil, err
	}
	summary := strings.TrimSpace(textField(b, "done_summary"))
	if len([]rune(summary)) > plannerapi.MaxSummaryChars {
		return nil, fmt.Errorf("%w: done_summary is at most %d characters", objects.ErrInvalid, plannerapi.MaxSummaryChars)
	}
	store := taskMetaStore{h.Store}
	existing, err := store.get(ctx, []string{task})
	if err != nil {
		return nil, err
	}
	m := existing[task]
	m.Task = task
	if _, ok := b["done_summary"]; ok {
		m.DoneSummary, m.By, m.At = summary, strings.TrimPrefix(actor, "operator:"), time.Now().UTC().Format(time.RFC3339)
	}
	if v, ok := b["pr_cites"]; ok {
		m.PRCites = nil
		list, isList := v.([]any)
		if !isList || len(list) > 20 {
			return nil, fmt.Errorf("%w: pr_cites is a list of at most 20", objects.ErrInvalid)
		}
		for _, e := range list {
			var c Item
			switch x := e.(type) {
			case string:
				c = prCite(x)
			case map[string]any:
				c = prCite(fmt.Sprintf("%v/%v#%v", x["owner"], x["repo"], x["number"]))
			}
			if c == nil {
				return nil, fmt.Errorf("%w: each pr cite is owner/repo#number or {owner,repo,number}", objects.ErrInvalid)
			}
			c["source"] = "manual"
			m.PRCites = append(m.PRCites, c)
		}
	}
	if err := store.put(ctx, m); err != nil {
		return nil, err
	}
	posted := "skipped"
	if _, ok := b["done_summary"]; ok && summary != "" {
		text := "Done: " + summary
		if len(m.PRCites) > 0 {
			text += fmt.Sprintf(" (PR %v)", m.PRCites[0]["number"])
		}
		posted = "posted"
		if h.Tasks.Comment(ctx, task, text) != nil {
			posted = "failed"
		}
	}
	cites := m.PRCites
	if cites == nil {
		cites = []Item{}
	}
	return Item{"task": task, "done_summary": m.DoneSummary, "pr_cites": cites, "summary_by": m.By, "summary_at": m.At, "torque_comment": posted}, nil
}

var mention = regexp.MustCompile(`(^|[^A-Za-z0-9_])(@[A-Za-z0-9][A-Za-z0-9_.-]{0,63})`)

// themeStatusAction names the lifecycle action that reaches a theme status.
func themeStatusAction(from, to string) string {
	switch to {
	case "approved":
		return "approve"
	case "ready":
		if from == "paused" {
			return "unpark"
		}
		return "ready"
	case "paused":
		return "park"
	case "done":
		return "finish"
	case "archived":
		return "archive"
	}
	return ""
}

// CommentWithStatus appends a comment and, in the same revision write, applies an optional status
// change (themes through their lifecycle rules, other databases as a plain status) and an optional pin.
func (s *Owned) CommentWithStatus(ctx context.Context, db, id string, rev int64, b Item, actor string) (Item, error) {
	db = CanonicalDB(db)
	text := textField(b, "text")
	if len(text) > plannerapi.MaxCommentBytes {
		return nil, fmt.Errorf("%w: text is at most %d bytes", objects.ErrInvalid, plannerapi.MaxCommentBytes)
	}
	status := textField(b, "status")
	pin, hasPin := b["pin"].(bool)
	if _, present := b["pin"]; present && !hasPin {
		return nil, fmt.Errorf("%w: pin must be a boolean", objects.ErrInvalid)
	}
	var comment Item
	applied := Item{}
	row, err := s.change(ctx, db, id, rev, actor, func(row Item) error {
		was := textField(row, "status")
		if status != "" && status != was {
			if db == "priorities" {
				action := themeStatusAction(was, status)
				if action == "" {
					return fmt.Errorf("%w: a theme cannot be set to %q here", objects.ErrInvalid, status)
				}
				args := Item{"reason": text}
				if err := s.themeAction(ctx, row, action, args, actor); err != nil {
					return err
				}
			} else {
				ok := false
				for _, v := range nativeStatuses[db] {
					ok = ok || v == status
				}
				if !ok {
					return fmt.Errorf("%w: %q is not a %s status", objects.ErrInvalid, status, PublicDB(db))
				}
				row["status"] = status
			}
			applied["status"] = status
		}
		if hasPin {
			if pin && row["pinned"] != true {
				row["pinned"], row["pinned_at"] = true, time.Now().UTC().Format(time.RFC3339Nano)
			} else if !pin {
				delete(row, "pinned")
				delete(row, "pinned_at")
			}
			applied["pin"] = pin
		}
		before := len(array(row["comments"]))
		if err := appendComment(row, text, textField(b, "kind"), actor); err != nil {
			return err
		}
		all := array(row["comments"])
		if len(all) == before {
			return objects.ErrInvalid
		}
		last := object(all[len(all)-1])
		ms := []Item{}
		for _, m := range mention.FindAllStringSubmatch(text, -1) {
			ms = append(ms, Item{"token": m[2], "kind": "mention", "id": strings.TrimPrefix(m[2], "@")})
		}
		last["mentions"] = ms
		comment = Item{"id": last["id"], "kind": last["kind"], "author": last["author"], "text": last["text"], "mentions": ms, "created_at": last["created"]}
		return nil
	})
	if err != nil {
		return nil, err
	}
	item := Item{"id": row["id"], "db": PublicDB(db), "rev": row["rev"], "status": row["status"]}
	if row["pinned"] == true {
		item["pinned"] = true
	}
	return Item{"comment": comment, "item": item, "applied": applied}, nil
}

// CreateThemeTask opens a todo Torque task under a theme. The theme's workstreams name the Torque project and tag the
// task lands in; the call refuses when they name none, or name more than one place, rather than choosing for the operator.
func (h *HTTP) CreateThemeTask(ctx context.Context, themeID string, b Item, actor string) (Item, error) {
	title := strings.TrimSpace(textField(b, "title"))
	if title == "" || len([]rune(title)) > 300 {
		return nil, fmt.Errorf("%w: title is required, at most 300 characters", objects.ErrInvalid)
	}
	theme, err := h.Owned.Get(ctx, "priorities", themeID)
	if err != nil {
		return nil, err
	}
	if _, has := b["expected_revision"]; has {
		rev, e := expected(b)
		if e != nil {
			return nil, e
		}
		if cur, _ := theme["rev"].(int64); cur != 0 && cur != rev {
			return nil, objects.ErrConflict
		}
	}
	ws, err := h.Owned.themeWorkstreams(ctx, theme)
	if err != nil {
		return nil, err
	}
	type scope struct{ project, tag string }
	var scopes []scope
	seen := map[scope]bool{}
	for _, w := range ws {
		tag := textField(w, "torque_tag")
		projects := []string{}
		for _, v := range array(w["torque_project_ids"]) {
			if p, ok := v.(string); ok && taskID.MatchString(p) {
				projects = append(projects, p)
			}
		}
		if len(projects) == 0 && tag == "" {
			continue
		}
		if len(projects) == 0 {
			projects = []string{""}
		}
		for _, p := range projects {
			if sc := (scope{p, tag}); !seen[sc] {
				seen[sc] = true
				scopes = append(scopes, sc)
			}
		}
	}
	switch {
	case len(scopes) == 0:
		return nil, fmt.Errorf("%w: this theme links no Torque project or tag, so there is nowhere to put a task", ErrRule)
	case len(scopes) > 1:
		return nil, fmt.Errorf("%w: this theme links more than one Torque project or tag; create the task in Torque or narrow the theme", ErrRule)
	}
	body := map[string]any{"title": title}
	if scopes[0].project != "" {
		body["project_id"] = scopes[0].project
	}
	if scopes[0].tag != "" {
		body["tags"] = []string{scopes[0].tag}
	}
	t, err := h.Tasks.Create(ctx, body)
	if err != nil {
		return nil, err
	}
	status := t.Status
	if status == "" {
		status = "todo"
	}
	slog.Info("planner: task created under theme", "theme", themeID, "task", t.ID, "actor", actor)
	return Item{"task": Item{"id": t.ID, "title": orString(t.Title, title), "status": status}}, nil
}

func orString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
