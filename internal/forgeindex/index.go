package forgeindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/forgeapi"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Row is one pull request as the pending inbox returns it (forgeapi pending.json), minus the fields
// added at read time (triage, age_s, cached, fetched_ms).
type Row struct {
	PRKey string `json:"pr_key"`
	Ref   struct {
		Repo struct {
			Host  string `json:"host"`
			Owner string `json:"owner"`
			Name  string `json:"name"`
		} `json:"repo"`
		Number int `json:"number"`
	} `json:"ref"`
	Title     string     `json:"title"`
	Author    Person     `json:"author"`
	Assignees []Assignee `json:"assignees"`
	State     string     `json:"state"`
	Draft     bool       `json:"draft"`
	Reasons   []string   `json:"reasons"`
	Checks    Checks     `json:"checks"`
	Mergeable string     `json:"mergeable"`
	HeadSHA   string     `json:"head_sha"`
	UpdatedAt string     `json:"updatedAt"`
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
	URL       string     `json:"url"`
}

type Person struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl,omitempty"`
}

type Assignee struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
	// Agent assignees (kind "agent") also carry the link they came from.
	URN    string `json:"urn,omitempty"`
	Task   string `json:"task,omitempty"`
	Status string `json:"link_status,omitempty"`
}

type Failing struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Checks struct {
	Passed  int       `json:"passed"`
	Failed  int       `json:"failed"`
	Pending int       `json:"pending"`
	Total   int       `json:"total"`
	Failing []Failing `json:"failing"`
}

// facts are what reasons are computed from, kept next to the row so time-based and mention reasons
// can be recomputed without refetching.
type facts struct {
	ChangesRequested bool `json:"changes_requested"`
	ReviewRequested  bool `json:"review_requested"`
}

type stored struct {
	Row   Row   `json:"row"`
	Facts facts `json:"facts"`
}

// FeedEvent is an alert-feed event (forgeapi.FeedKinds). The id is stable per (pr, kind, head or
// update), so a reconnect or a restart never re-announces the same thing.
type FeedEvent struct {
	ID    string
	Kind  string
	PRKey string
	AtMS  int64
	Title string
	URL   string
}

// Index keeps the PR index fresh and answers reads from it.
type Index struct {
	DB       *localstore.Store
	GH       *GitHub
	Settings SettingsSource
	Bus      *Broker
	Now      func() time.Time
	// Feed receives alert-feed events for reasons that appear on a PR that was already indexed.
	Feed func(FeedEvent)

	mu       sync.Mutex
	viewer   string
	degraded bool
	note     string
}

func (x *Index) now() time.Time {
	if x.Now != nil {
		return x.Now().UTC()
	}
	return time.Now().UTC()
}

func (x *Index) settings(ctx context.Context) Settings {
	if x.Settings == nil {
		return Settings{PollS: forgeapi.PollSecondsDefault, StaleDays: forgeapi.StaleDaysDefault, RateFloor: forgeapi.RateFloorDefault, MaxPending: forgeapi.MaxPendingDefault}
	}
	return x.Settings.Load(ctx)
}

func (x *Index) setState(degraded bool, note string) {
	x.mu.Lock()
	changed := x.degraded != degraded || x.note != note
	x.degraded, x.note = degraded, note
	x.mu.Unlock()
	if changed && x.Bus != nil {
		x.publishStatus()
	}
}

func (x *Index) publishStatus() {
	rem, reset, _ := x.GH.Rate()
	x.mu.Lock()
	d := x.degraded
	x.mu.Unlock()
	x.Bus.Publish("status", map[string]any{"rate": map[string]any{"remaining": rem, "reset_ms": reset}, "degraded": d})
}

// Run refreshes until ctx ends. Errors degrade the index; they never stop the loop.
func (x *Index) Run(ctx context.Context) {
	for {
		wait := x.refreshOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (x *Index) refreshOnce(ctx context.Context) time.Duration {
	s := x.settings(ctx)
	wait := time.Duration(s.PollS) * time.Second
	err := x.Refresh(ctx)
	switch {
	case err == nil:
	case errors.Is(err, ErrNoCredential):
		x.setState(true, "No GitHub token is configured; showing the last saved data.")
		wait = 5 * time.Minute
	case errors.Is(err, ErrRateLimited):
		x.setState(true, "GitHub rate limit reached; showing cached data.")
		if _, reset, ok := x.GH.Rate(); ok {
			if d := time.Until(time.UnixMilli(reset)); d > wait {
				wait = min(d+time.Second, 10*time.Minute)
			}
		}
	case ctx.Err() == nil:
		slog.Warn("forge index refresh failed", "err", err)
		x.setState(true, "GitHub could not be reached; showing cached data.")
	}
	return wait
}

// Refresh runs one poll: list, hydrate what changed, recompute reasons, emit changes.
func (x *Index) Refresh(ctx context.Context) error {
	s := x.settings(ctx)
	viewer, err := x.viewerLogin(ctx)
	if err != nil {
		return err
	}
	// 1. What is open, newest first, up to the cap.
	seen := map[string]ghSearchItem{}
	var order []string
	for _, w := range s.Watched {
		items, err := x.GH.Search(ctx, "is:pr is:open archived:false "+searchTerm(w), s.MaxPending)
		if err != nil {
			return err
		}
		for _, it := range items {
			o, n, ok := it.repo()
			if !ok || !forgeapi.ValidRepoPart(o) || !forgeapi.ValidRepoPart(n) {
				continue
			}
			key := forgeapi.PRKey(o, n, it.Number)
			if _, dup := seen[key]; !dup {
				seen[key] = it
				order = append(order, key)
			}
		}
	}
	slices.SortStableFunc(order, func(a, b string) int { return strings.Compare(seen[b].UpdatedAt, seen[a].UpdatedAt) })
	if len(order) > s.MaxPending {
		for _, k := range order[s.MaxPending:] {
			delete(seen, k)
		}
		order = order[:s.MaxPending]
	}
	mentioned := map[string]bool{}
	if items, err := x.GH.Search(ctx, "is:pr is:open mentions:"+viewer, s.MaxPending); err == nil {
		for _, it := range items {
			if o, n, ok := it.repo(); ok {
				mentioned[forgeapi.PRKey(o, n, it.Number)] = true
			}
		}
	} else if errors.Is(err, ErrRateLimited) {
		return err
	}

	// 2. Existing rows.
	old, err := x.load(ctx)
	if err != nil {
		return err
	}
	first := len(old) == 0 && !x.initialized(ctx)
	nowMS := x.now().UnixMilli()
	degraded := false
	var events []FeedEvent
	var changed []map[string]any
	next := map[string]stored{}

	for _, key := range order {
		it := seen[key]
		prev, had := old[key]
		cur := prev.st
		if !had || prev.updatedAt != it.UpdatedAt || prev.pending > 0 {
			if rem, _, ok := x.GH.Rate(); ok && rem < s.RateFloor {
				degraded = true
				if !had {
					continue // nothing to show yet; retry next cycle
				}
			} else {
				o, n, _ := it.repo()
				fresh, herr := x.hydrate(ctx, o, n, it, viewer)
				switch {
				case errors.Is(herr, ErrRateLimited):
					return herr
				case herr != nil:
					if !had {
						continue
					}
					degraded = true
				default:
					cur = fresh
				}
			}
		}
		cur.Row.Reasons = reasons(cur, it.UpdatedAt, mentioned[key], s.StaleDays, x.now())
		next[key] = cur
		if !had {
			changed = append(changed, map[string]any{"pr_key": key, "changed": []string{"created"}})
			continue
		}
		if added := diffReasons(prev.st.Row.Reasons, cur.Row.Reasons); len(added) > 0 {
			changed = append(changed, map[string]any{"pr_key": key, "changed": []string{"reasons"}})
			if !first {
				for _, r := range added {
					if kind, ok := feedKind(r); ok {
						ver := cur.Row.HeadSHA
						if r == "stale" || ver == "" {
							ver = cur.Row.UpdatedAt
						}
						events = append(events, FeedEvent{ID: "pr:" + key + ":" + kind + ":" + ver, Kind: kind, PRKey: key, AtMS: nowMS, Title: cur.Row.Title, URL: cur.Row.URL})
					}
				}
			}
		} else if prev.updatedAt != it.UpdatedAt || prev.st.Row.HeadSHA != cur.Row.HeadSHA {
			changed = append(changed, map[string]any{"pr_key": key, "changed": []string{"pr"}})
		}
	}

	// 3. Gone from the open list: merged or closed. Look up a few to announce merges.
	looked := 0
	for key, prev := range old {
		if _, still := seen[key]; still {
			continue
		}
		if !x.inScope(key, s) {
			continue // fell out of the watched set; just drop it
		}
		if looked < 10 && !first {
			looked++
			o, n, num, _ := forgeapi.ParsePRKey(key)
			if p, err := x.GH.Pull(ctx, o, n, num); err == nil && p.Merged {
				events = append(events, FeedEvent{ID: "pr:" + key + ":pr.merged:" + p.Head.SHA, Kind: "pr.merged", PRKey: key, AtMS: nowMS, Title: prev.st.Row.Title, URL: prev.st.Row.URL})
				x.Bus.Publish("pr.merged", map[string]any{"pr_key": key})
			} else if errors.Is(err, ErrRateLimited) {
				return err
			}
		}
	}

	if err := x.save(ctx, next, nowMS); err != nil {
		return err
	}
	x.setState(degraded, map[bool]string{true: "Rate budget is low; showing cached data."}[degraded])
	for _, c := range changed {
		x.Bus.Publish("pr.updated", c)
	}
	x.publishStatus()
	if x.Feed != nil {
		for _, e := range events {
			x.Feed(e)
		}
	}
	return nil
}

func (x *Index) inScope(key string, s Settings) bool {
	o, n, _, ok := forgeapi.ParsePRKey(key)
	if !ok {
		return false
	}
	for _, w := range s.Watched {
		if w == "org:"+o || w == o+"/"+n {
			return true
		}
	}
	return false
}

func (x *Index) viewerLogin(ctx context.Context) (string, error) {
	x.mu.Lock()
	v := x.viewer
	x.mu.Unlock()
	if v != "" {
		return v, nil
	}
	v, err := x.GH.Viewer(ctx)
	if err != nil {
		return "", err
	}
	x.mu.Lock()
	x.viewer = v
	x.mu.Unlock()
	return v, nil
}

func (x *Index) hydrate(ctx context.Context, owner, name string, it ghSearchItem, viewer string) (stored, error) {
	p, err := x.GH.Pull(ctx, owner, name, it.Number)
	if err != nil {
		return stored{}, err
	}
	revs, err := x.GH.Reviews(ctx, owner, name, it.Number)
	if err != nil {
		return stored{}, err
	}
	var checks []ghCheck
	if p.Head.SHA != "" {
		if checks, err = x.GH.Checks(ctx, owner, name, p.Head.SHA); err != nil {
			return stored{}, err
		}
	}
	return build(owner, name, p, revs, checks, viewer), nil
}

// build maps GitHub's answers onto a Row and its facts.
func build(owner, name string, p ghPull, revs []ghReview, checks []ghCheck, viewer string) stored {
	var r Row
	r.PRKey = forgeapi.PRKey(owner, name, p.Number)
	r.Ref.Repo.Host, r.Ref.Repo.Owner, r.Ref.Repo.Name, r.Ref.Number = "github", owner, name, p.Number
	r.Title, r.State, r.Draft = p.Title, "open", p.Draft
	r.Author = Person{Login: p.User.Login, AvatarURL: p.User.AvatarURL}
	r.Assignees = []Assignee{}
	for _, a := range p.Assignees {
		r.Assignees = append(r.Assignees, Assignee{Kind: "user", ID: a.Login, Label: a.Login})
	}
	r.HeadSHA, r.UpdatedAt, r.URL, r.Additions, r.Deletions = p.Head.SHA, p.UpdatedAt, p.HTMLURL, p.Additions, p.Deletions
	switch p.MergeableState {
	case "clean", "has_hooks", "unstable":
		r.Mergeable = "mergeable"
	case "dirty":
		r.Mergeable = "conflicting"
	case "blocked", "behind":
		r.Mergeable = "blocked"
	default:
		r.Mergeable = "unknown"
		if p.Mergeable != nil {
			r.Mergeable = map[bool]string{true: "mergeable", false: "conflicting"}[*p.Mergeable]
		}
	}
	r.Checks.Failing = []Failing{}
	for _, c := range checks {
		r.Checks.Total++
		switch {
		case c.Status != "completed":
			r.Checks.Pending++
		case c.Conclusion == "failure" || c.Conclusion == "timed_out" || c.Conclusion == "action_required" || c.Conclusion == "cancelled":
			r.Checks.Failed++
			r.Checks.Failing = append(r.Checks.Failing, Failing{Name: c.Name, URL: c.HTMLURL})
		default:
			r.Checks.Passed++
		}
	}
	latest := map[string]string{}
	for _, v := range revs {
		switch st := strings.ToUpper(v.State); st {
		case "APPROVED", "CHANGES_REQUESTED":
			latest[v.User.Login] = st
		case "DISMISSED":
			delete(latest, v.User.Login)
		}
	}
	var f facts
	for _, st := range latest {
		f.ChangesRequested = f.ChangesRequested || st == "CHANGES_REQUESTED"
	}
	for _, u := range p.Requested {
		f.ReviewRequested = f.ReviewRequested || strings.EqualFold(u.Login, viewer)
	}
	return stored{Row: r, Facts: f}
}

// reasons says why a PR is pending, in the contract's order.
func reasons(s stored, updatedAt string, mentioned bool, staleDays int, now time.Time) []string {
	out := []string{}
	r := s.Row
	if r.Checks.Failed > 0 {
		out = append(out, "checks_failed")
	}
	if s.Facts.ChangesRequested {
		out = append(out, "changes_requested")
	}
	if r.Mergeable == "conflicting" {
		out = append(out, "conflicting")
	}
	if s.Facts.ReviewRequested {
		out = append(out, "review_requested")
	}
	if mentioned {
		out = append(out, "mentioned")
	}
	if len(r.Assignees) == 0 {
		out = append(out, "unassigned")
	}
	if t, err := time.Parse(time.RFC3339, updatedAt); err == nil && now.Sub(t) > time.Duration(staleDays)*24*time.Hour {
		out = append(out, "stale")
	}
	return out
}

func diffReasons(before, after []string) (added []string) {
	for _, r := range after {
		if !slices.Contains(before, r) {
			added = append(added, r)
		}
	}
	return
}

// feedKind maps a reason to its alert-feed kind; unassigned and mentioned are not feed events.
func feedKind(reason string) (string, bool) {
	switch reason {
	case "review_requested", "checks_failed", "changes_requested", "conflicting", "stale":
		return "pr." + reason, true
	}
	return "", false
}

type loaded struct {
	st        stored
	updatedAt string
	pending   int
	fetchedMS int64
}

func (x *Index) load(ctx context.Context) (map[string]loaded, error) {
	rows, err := x.DB.Reader.QueryContext(ctx, `SELECT pr_key, updated_at, pending, row_json, fetched_ms FROM forge_pr_index`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]loaded{}
	for rows.Next() {
		var key, upd, raw string
		var l loaded
		if err := rows.Scan(&key, &upd, &l.pending, &raw, &l.fetchedMS); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(raw), &l.st) != nil {
			continue
		}
		l.updatedAt = upd
		out[key] = l
	}
	return out, rows.Err()
}

func (x *Index) initialized(ctx context.Context) bool {
	var v string
	return x.DB.Reader.QueryRowContext(ctx, `SELECT v FROM forge_index_state WHERE k = 'last_refresh_ms'`).Scan(&v) == nil
}

// save replaces the index with next in one transaction and records the refresh time.
func (x *Index) save(ctx context.Context, next map[string]stored, nowMS int64) error {
	return x.DB.Writer.Submit(ctx, "forge_index_save", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM forge_pr_index`); err != nil {
			return err
		}
		for key, s := range next {
			raw, _ := json.Marshal(s)
			if _, err := tx.ExecContext(ctx, `INSERT INTO forge_pr_index(pr_key, repo, number, updated_at, head_sha, pending, row_json, fetched_ms) VALUES (?,?,?,?,?,?,?,?)`,
				key, s.Row.Ref.Repo.Owner+"/"+s.Row.Ref.Repo.Name, s.Row.Ref.Number, s.Row.UpdatedAt, s.Row.HeadSHA, s.Row.Checks.Pending, string(raw), nowMS); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO forge_index_state(k, v) VALUES ('last_refresh_ms', ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, strconv.FormatInt(nowMS, 10))
		return err
	})
}

// Snapshot is what reads see: all rows, newest update first, and when they were fetched.
type Snapshot struct {
	Rows          []Row
	FetchedMS     int64
	LastRefreshMS int64
}

func (x *Index) Snapshot(ctx context.Context) (Snapshot, error) {
	m, err := x.load(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	var snap Snapshot
	for _, l := range m {
		snap.Rows = append(snap.Rows, l.st.Row)
		snap.FetchedMS = max(snap.FetchedMS, l.fetchedMS)
	}
	slices.SortFunc(snap.Rows, func(a, b Row) int {
		if c := strings.Compare(b.UpdatedAt, a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.PRKey, b.PRKey)
	})
	var v string
	if x.DB.Reader.QueryRowContext(ctx, `SELECT v FROM forge_index_state WHERE k = 'last_refresh_ms'`).Scan(&v) == nil {
		snap.LastRefreshMS, _ = strconv.ParseInt(v, 10, 64)
	}
	return snap, nil
}

// Ready reports that at least one refresh has completed, so reads can come from the index.
func (x *Index) Ready(ctx context.Context) bool { return x.initialized(ctx) }

var _ = fmt.Sprintf
