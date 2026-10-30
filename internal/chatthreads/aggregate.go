package chatthreads

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/appagent"
	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/chatbridge"
)

var ErrNotFound = errors.New("no such thread")

type Agent struct {
	Address string `json:"address"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	AgentID string `json:"agent_id"`
}
type Capability struct {
	Mode    string `json:"mode"`
	CanSend bool   `json:"can_send"`
	Detail  string `json:"detail"`
}
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}
type Context struct {
	Used   *int    `json:"used"`
	Window *int    `json:"window"`
	Model  *string `json:"model"`
	Source string  `json:"source"`
}

// Summary is one row of the unified thread list (openapi ThreadSummary + Thread).
type Summary struct {
	Ref           string     `json:"thread_ref"`
	Source        string     `json:"source"`
	Title         string     `json:"title"`
	Agent         Agent      `json:"agent"`
	Capability    Capability `json:"capability"`
	Pinned        bool       `json:"pinned"`
	Archived      bool       `json:"archived"`
	Unread        int        `json:"unread"`
	LastAt        int64      `json:"last_at"`
	LastPreview   string     `json:"last_preview"`
	BlockedOnUser int        `json:"blocked_on_user"`
	PlanProgress  *Progress  `json:"plan_progress"`
	ProjectID     *string    `json:"project_id"`
	Context       Context    `json:"context"`
	ForkedFrom    *string    `json:"forked_from"`
	CreatedAt     int64      `json:"created_at"`

	pinnedNS int64
	search   string // lowercased title, agent, preview and (tether) turn text
}

// Query is the list filter. Lens is all|agents|work|archived.
type Query struct {
	Lens     string
	NeedYou  bool
	Text     string
	BeforeAt int64
	Limit    int
}

// Aggregator merges app-agent threads and Tether-turn threads and overlays
// chat_thread_state. App or Chat may be nil when that source is not configured.
type Aggregator struct {
	App   *appagent.Service
	Chat  *chatbridge.Owned
	State *StateStore
	// Hooks for later slices; nil means "none yet".
	PlanProgress func(ctx context.Context, refs []string) map[string]Progress // T2: chat_plans
	CardBlocks   func(ctx context.Context, refs []string) map[string]int      // T5: open chat_card_refs
}

func preview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 160 {
		s = strings.ToValidUTF8(s[:160], "")
	}
	return s
}

func (a *Aggregator) appSummaries(ctx context.Context) ([]Summary, error) {
	if a.App == nil {
		return nil, nil
	}
	ts, err := a.App.Threads(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(ts))
	for _, t := range ts {
		s := Summary{
			Ref: chatapi.ThreadRef{Source: chatapi.SourceApp, ID: t.ID}.String(), Source: chatapi.SourceApp, Title: t.Title,
			Agent:      Agent{Address: "tachyon://agent/" + t.Agent, Label: t.Title, Kind: "agent", AgentID: t.Agent},
			Capability: Capability{Mode: "live", CanSend: true, Detail: "Tachyon's in-app agent. Replies stream in real time."},
			Unread:     t.Unread, LastAt: t.LastAt.UnixMilli(), LastPreview: t.LastPreview, CreatedAt: t.CreatedAt.UnixMilli(),
		}
		if t.Blocked {
			s.BlockedOnUser = 1
		}
		s.search = strings.ToLower(t.Title + "\n" + t.Agent + "\n" + t.LastPreview)
		out = append(out, s)
	}
	return out, nil
}

// tetherTurns groups the Tether inbox by thread. The key is the turn's session_id,
// else its local session id; turns with neither belong to no thread.
func tetherKey(t chatbridge.Turn) string {
	if t.SessionID != "" {
		return t.SessionID
	}
	return t.LocalSessionID
}

type tgroup struct {
	ref   string
	turns []chatbridge.Turn
}

func (a *Aggregator) tetherGroups(ctx context.Context) ([]tgroup, error) {
	if a.Chat == nil {
		return nil, nil
	}
	in, err := a.Chat.Inbox(ctx)
	if err != nil {
		return nil, err
	}
	by := map[string]*tgroup{}
	var order []string
	for _, t := range append(append([]chatbridge.Turn{}, in.History...), in.Pending...) {
		k := tetherKey(t)
		if k == "" {
			continue
		}
		g := by[k]
		if g == nil {
			g = &tgroup{ref: chatapi.ThreadRef{Source: chatapi.SourceTether, ID: k}.String()}
			by[k] = g
			order = append(order, k)
		}
		g.turns = append(g.turns, t)
	}
	out := make([]tgroup, 0, len(order))
	for _, k := range order {
		g := by[k]
		sort.SliceStable(g.turns, func(i, j int) bool { return g.turns[i].Created < g.turns[j].Created })
		out = append(out, *g)
	}
	return out, nil
}

func open(t chatbridge.Turn) bool { return t.Resolution == nil && t.State == "presented" }

func parseTime(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }

func (a *Aggregator) tetherSummary(g tgroup, readNS int64) Summary {
	first, last := g.turns[0], g.turns[len(g.turns)-1]
	title := strings.TrimSpace(strings.SplitN(first.Content, "\n", 2)[0])
	if len(title) > 60 {
		title = strings.ToValidUTF8(title[:60], "") + "…"
	}
	if title == "" {
		title = "Session " + strings.TrimPrefix(g.ref, "tether:")
	}
	s := Summary{Ref: g.ref, Source: chatapi.SourceTether, Title: title, CreatedAt: parseTime(first.Created).UnixMilli(),
		Context: Context{Source: "unknown"}}
	s.LastAt, s.LastPreview = parseTime(last.Created).UnixMilli(), preview(last.Content)
	agent := last.AgentID
	s.Agent = Agent{Address: agent, Label: agent, Kind: "agent", AgentID: agent}
	if !strings.Contains(agent, "://") {
		s.Agent.Address = "tether://agent/" + agent
	}
	var text strings.Builder
	text.WriteString(title + "\n" + agent + "\n")
	for _, t := range g.turns {
		text.WriteString(t.Content + "\n")
		if parseTime(t.Created).UnixNano() > readNS {
			s.Unread++
		}
		if open(t) && (t.Kind == "question" || t.Kind == "approval") {
			s.BlockedOnUser++
		}
		if open(t) && t.Replyable {
			s.Capability.CanSend = true
		}
	}
	s.Capability.Mode, s.Capability.Detail = "inbox", "Replies go back to the session as Tether turns. This list refreshes about every 5 seconds."
	s.search = strings.ToLower(text.String())
	return s
}

func (a *Aggregator) overlay(s *Summary, st State) {
	if st.Title != "" {
		s.Title = st.Title
	}
	s.Pinned, s.pinnedNS = st.PinnedNS != 0, st.PinnedNS
	s.Archived = st.ArchivedNS != 0
	if st.ProjectID != "" {
		p := st.ProjectID
		s.ProjectID = &p
	}
	if st.ForkedFrom != "" {
		f := st.ForkedFrom
		s.ForkedFrom = &f
	}
}

// all builds every thread's summary with overlay and derived fields applied.
func (a *Aggregator) all(ctx context.Context) ([]Summary, error) {
	states, err := a.State.All(ctx)
	if err != nil {
		return nil, err
	}
	out, err := a.appSummaries(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := a.tetherGroups(ctx)
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		out = append(out, a.tetherSummary(g, states[g.ref].ReadNS))
	}
	refs := make([]string, len(out))
	for i := range out {
		refs[i] = out[i].Ref
		a.overlay(&out[i], states[out[i].Ref])
	}
	if a.PlanProgress != nil {
		m := a.PlanProgress(ctx, refs)
		for i := range out {
			if p, ok := m[out[i].Ref]; ok {
				p := p
				out[i].PlanProgress = &p
			}
		}
	}
	if a.CardBlocks != nil {
		m := a.CardBlocks(ctx, refs)
		for i := range out {
			out[i].BlockedOnUser += m[out[i].Ref]
		}
	}
	return out, nil
}

// work reports whether a thread belongs to the Work lens: it has a plan or a project.
func work(s Summary) bool { return s.PlanProgress != nil || s.ProjectID != nil }

// List returns the filtered page, pinned first then newest, and whether more remain.
func (a *Aggregator) List(ctx context.Context, q Query) ([]Summary, bool, error) {
	all, err := a.all(ctx)
	if err != nil {
		return nil, false, err
	}
	if q.Limit < 1 || q.Limit > 200 {
		q.Limit = 50
	}
	needle := strings.ToLower(strings.TrimSpace(q.Text))
	var bodies map[string]bool
	if needle != "" && a.App != nil {
		if bodies, err = a.App.Store.ThreadsWithText(ctx, needle, 500); err != nil {
			return nil, false, err
		}
	}
	var out []Summary
	for _, s := range all {
		if (q.Lens == "archived") != s.Archived {
			continue
		}
		if q.Lens == "work" && !work(s) {
			continue
		}
		if q.NeedYou && s.BlockedOnUser == 0 {
			continue
		}
		if needle != "" && !strings.Contains(s.search, needle) && !bodies[strings.TrimPrefix(s.Ref, "app:")] {
			continue
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		if out[i].Pinned && out[i].pinnedNS != out[j].pinnedNS {
			return out[i].pinnedNS > out[j].pinnedNS
		}
		if out[i].LastAt != out[j].LastAt {
			return out[i].LastAt > out[j].LastAt
		}
		return out[i].Ref < out[j].Ref
	})
	if q.BeforeAt > 0 {
		kept := out[:0]
		for _, s := range out {
			if s.LastAt < q.BeforeAt {
				kept = append(kept, s)
			}
		}
		out = kept
	}
	more := len(out) > q.Limit
	if more {
		out = out[:q.Limit]
	}
	if out == nil {
		out = []Summary{}
	}
	return out, more, nil
}

// Get returns one thread. App threads are looked up directly; Tether threads
// come from the grouped inbox.
func (a *Aggregator) Get(ctx context.Context, ref string) (Summary, error) {
	all, err := a.all(ctx)
	if err != nil {
		return Summary{}, err
	}
	for _, s := range all {
		if s.Ref == ref {
			return s, nil
		}
	}
	return Summary{}, ErrNotFound
}
