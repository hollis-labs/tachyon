// Package forgeassign is the server-side "assign this pull request to an agent" operation (PR7). It replaces
// the browser's orchestration (set the GitHub assignee, mail the agent, settle a state row) with ONE
// idempotent operation whose steps are recorded on a link row: a retry does only the missing step, the same
// dedupe key returns the existing link, and an explicit force sends a reminder. Agents never merge: that is
// guidance in the message, not a technical block on an agent's own GitHub credentials. Design:
// design_tachyon_pull_requests_backend_20261030, section 5.
package forgeassign

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/tachyon/internal/forgeapi"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Route is the pattern Handler serves, mounted explicitly by the server.
const Route = "POST " + forgeapi.Prefix + "/repos/{host}/{owner}/{name}/pulls/{num}/assign-agent"

const (
	maxBody   = 16 << 10
	maxNote   = 500
	maxChecks = 20
	maxKey    = 128
	assignee  = "operator" // one operator; recorded as assigned_by
	schema    = "console.pr_assignment/v1"
	fence     = "console-pr-assignment"
)

var (
	ErrAgentNotFound       = errors.New("agent not found")
	ErrAgentNotAddressable = errors.New("agent not addressable")
)

// Agent is what the operation needs to know about the addressee.
type Agent struct {
	ID  string
	URN string
}

// Agents resolves an agent id to its Tether address. ErrAgentNotFound and ErrAgentNotAddressable (no
// deliverable address) are the refusals; any other error means the registry could not be read.
type Agents interface {
	Lookup(ctx context.Context, id string) (Agent, error)
}

// Mailer sends the assignment message from Tachyon's service identity and returns the message id.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string, meta map[string]string) (string, error)
}

// Logins maps an agent id to its GitHub login ("" when the mapping has none).
type Logins interface {
	Login(ctx context.Context, agentID string) string
}

// Facts are index facts about a pull request that enrich the message; all optional.
type Facts struct {
	Reasons   []string
	Passed    int
	Failed    int
	Pending   int
	Failing   []Check
	Mergeable string
	Found     bool
}

// Check is a failing check.
type Check struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// Handler assigns pull requests to agents.
type Handler struct {
	Token   string
	Base    string // default https://api.github.com
	HTTP    *http.Client
	DB      *localstore.Store
	Agents  Agents
	Mail    Mailer
	Logins  Logins
	Facts   func(ctx context.Context, prKey string) Facts
	Changed func(prKey string)
	Now     func() time.Time
	// WorkspaceURL is the Tachyon route of a pull request's workspace, as given to the agent.
	Workspace func(owner, name string, num int) string
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// Mux serves the route.
func (h *Handler) Mux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc(Route, h.assign)
	return m
}

// ---- wire ----

func send(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type apiErr struct {
	status    int
	code, msg string
}

func (e *apiErr) Error() string { return e.code }

func fail(w http.ResponseWriter, e *apiErr) {
	send(w, e.status, map[string]any{"error": map[string]any{"code": e.code, "message": e.msg}})
}

func bad(msg string) *apiErr { return &apiErr{status: 400, code: "invalid_args", msg: msg} }

// ---- GitHub ----

func (h *Handler) gh(ctx context.Context, method, path string, in, out any) (int, error) {
	if h.Token == "" {
		return 0, errors.New("no GitHub token is configured")
	}
	root := strings.TrimRight(h.Base, "/")
	if root == "" {
		root = "https://api.github.com"
	}
	var rd io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, root+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+h.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c := h.HTTP
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, errors.New("GitHub could not be reached")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("GitHub answered HTTP %d", resp.StatusCode)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, errors.New("GitHub sent something unreadable")
		}
	}
	return resp.StatusCode, nil
}

type pull struct {
	Title   string `json:"title"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
	MergeableState string `json:"mergeable_state"`
}

func repoPath(owner, name string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// ---- store ----

// Link is one assignment (forgeapi AgentLink).
type Link struct {
	LinkID        string   `json:"link_id"`
	PRKey         string   `json:"pr_key"`
	AgentID       string   `json:"agent_id"`
	AgentURN      string   `json:"agent_urn"`
	Task          string   `json:"task"`
	ForgeLogin    string   `json:"forge_login"`
	DedupeKey     string   `json:"dedupe_key"`
	ForgeAssigned bool     `json:"forge_assigned"`
	MessageID     string   `json:"message_id"`
	Status        string   `json:"status"`
	Reminders     int      `json:"reminders"`
	Note          string   `json:"note"`
	AssignedBy    string   `json:"assigned_by"`
	CreatedMS     int64    `json:"created_ms"`
	UpdatedMS     int64    `json:"updated_ms"`
	Error         string   `json:"error,omitempty"`
	Checks        []string `json:"checks,omitempty"`
}

const cols = `link_id, pr_key, agent_id, agent_urn, task, forge_login, dedupe_key, forge_assigned, message_id, status, reminders, note, checks_json, assigned_by, created_ms, updated_ms, error`

func scan(row interface{ Scan(...any) error }) (Link, error) {
	var l Link
	var assigned int
	var checks string
	err := row.Scan(&l.LinkID, &l.PRKey, &l.AgentID, &l.AgentURN, &l.Task, &l.ForgeLogin, &l.DedupeKey, &assigned, &l.MessageID, &l.Status, &l.Reminders, &l.Note, &checks, &l.AssignedBy, &l.CreatedMS, &l.UpdatedMS, &l.Error)
	l.ForgeAssigned = assigned == 1
	if checks != "" {
		_ = json.Unmarshal([]byte(checks), &l.Checks)
	}
	return l, err
}

func (h *Handler) load(ctx context.Context, dedupe string) (Link, bool, error) {
	l, err := scan(h.DB.Reader.QueryRowContext(ctx, `SELECT `+cols+` FROM pr_agent_links WHERE dedupe_key = ?`, dedupe))
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, false, nil
	}
	return l, err == nil, err
}

func (h *Handler) save(ctx context.Context, l Link) error {
	l.UpdatedMS = h.now().UnixMilli()
	checks := ""
	if len(l.Checks) > 0 {
		b, _ := json.Marshal(l.Checks)
		checks = string(b)
	}
	assigned := 0
	if l.ForgeAssigned {
		assigned = 1
	}
	return h.DB.Writer.Submit(ctx, "pr_agent_link_save", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO pr_agent_links(`+cols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(link_id) DO UPDATE SET agent_urn=excluded.agent_urn, forge_login=excluded.forge_login, forge_assigned=excluded.forge_assigned,
  message_id=excluded.message_id, status=excluded.status, reminders=excluded.reminders, note=excluded.note, checks_json=excluded.checks_json,
  updated_ms=excluded.updated_ms, error=excluded.error`,
			l.LinkID, l.PRKey, l.AgentID, l.AgentURN, l.Task, l.ForgeLogin, l.DedupeKey, assigned, l.MessageID, l.Status, l.Reminders, l.Note, checks, l.AssignedBy, l.CreatedMS, l.UpdatedMS, l.Error)
		return err
	})
}

// Assignees lists the open agent links of every pull request, for the pending list. Failed and cancelled
// links are shown too, so the row says an assignment did not go through.
func (h *Handler) Assignees(ctx context.Context) (map[string][]Link, error) {
	rows, err := h.DB.Reader.QueryContext(ctx, `SELECT `+cols+` FROM pr_agent_links WHERE status <> 'cancelled' AND status <> 'done' ORDER BY created_ms`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Link{}
	for rows.Next() {
		l, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out[l.PRKey] = append(out[l.PRKey], l)
	}
	return out, rows.Err()
}

// MarkDone closes the open links of a merged pull request and returns how many it closed.
func (h *Handler) MarkDone(ctx context.Context, prKey string) int {
	n := 0
	_ = h.DB.Writer.Submit(ctx, "pr_agent_link_done", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE pr_agent_links SET status = 'done', error = '', updated_ms = ? WHERE pr_key = ? AND status IN ('pending','assigned','sent','failed')`, h.now().UnixMilli(), prKey)
		if err == nil {
			c, _ := res.RowsAffected()
			n = int(c)
		}
		return err
	})
	return n
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "lnk_" + hex.EncodeToString(b)
}

// ---- the operation ----

type request struct {
	AgentID string   `json:"agent_id"`
	Task    string   `json:"task"`
	Note    string   `json:"note"`
	Checks  []string `json:"checks"`
	Force   bool     `json:"force"`
}

func (h *Handler) assign(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("host") != "github" {
		fail(w, &apiErr{status: 501, code: "unavailable", msg: "Only GitHub is supported."})
		return
	}
	owner, name := r.PathValue("owner"), r.PathValue("name")
	num, nerr := strconv.Atoi(r.PathValue("num"))
	if !forgeapi.ValidRepoPart(owner) || !forgeapi.ValidRepoPart(name) || nerr != nil || num < 1 {
		fail(w, bad("Invalid pull request."))
		return
	}
	if h.DB == nil || h.Agents == nil || h.Mail == nil {
		fail(w, &apiErr{status: 503, code: "unavailable", msg: "Assigning agents is not configured on this host."})
		return
	}
	var in request
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err != nil || dec.Decode(&in) != nil {
		fail(w, bad("Send a JSON object with the documented fields only."))
		return
	}
	if !slices.Contains(forgeapi.Tasks, in.Task) || strings.TrimSpace(in.AgentID) == "" || len(in.AgentID) > maxKey || utf8.RuneCountInString(in.Note) > maxNote || len(in.Checks) > maxChecks {
		fail(w, bad("agent_id and a task (review, fix_checks, address_comments, resolve_conflicts) are required."))
		return
	}
	for _, c := range in.Checks {
		if c == "" || len(c) > 200 {
			fail(w, bad("Check names are 1 to 200 characters."))
			return
		}
	}
	ctx := r.Context()
	agent, err := h.Agents.Lookup(ctx, in.AgentID)
	switch {
	case errors.Is(err, ErrAgentNotFound):
		fail(w, &apiErr{status: 404, code: "agent_not_found", msg: "No such agent."})
		return
	case errors.Is(err, ErrAgentNotAddressable):
		fail(w, &apiErr{status: 422, code: "agent_not_addressable", msg: "That agent has no deliverable Tether address."})
		return
	case err != nil:
		fail(w, &apiErr{status: 502, code: "unavailable", msg: "The agent registry could not be read."})
		return
	}
	var pr pull
	if status, err := h.gh(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", repoPath(owner, name), num), nil, &pr); err != nil {
		switch status {
		case 404:
			fail(w, &apiErr{status: 404, code: "not_found", msg: "GitHub does not have that pull request."})
		case 401:
			fail(w, &apiErr{status: 401, code: "credential_missing", msg: "GitHub refused the configured token."})
		default:
			fail(w, &apiErr{status: 502, code: "unavailable", msg: "GitHub could not be read."})
		}
		return
	}
	if pr.State != "open" {
		fail(w, &apiErr{status: 409, code: "pr_not_open", msg: "Only open pull requests can be assigned."})
		return
	}
	prKey := forgeapi.PRKey(owner, name, num)
	dedupe := forgeapi.DedupeKey(prKey, agent.ID, in.Task)
	link, found, err := h.load(ctx, dedupe)
	if err != nil {
		fail(w, &apiErr{status: 503, code: "unavailable", msg: "The assignment could not be read."})
		return
	}
	now := h.now().UnixMilli()
	if !found {
		link = Link{LinkID: newID(), PRKey: prKey, AgentID: agent.ID, AgentURN: agent.URN, Task: in.Task, DedupeKey: dedupe, Status: "pending",
			Note: strings.TrimSpace(in.Note), Checks: in.Checks, AssignedBy: assignee, CreatedMS: now}
		if err := h.save(ctx, link); err != nil {
			fail(w, &apiErr{status: 503, code: "unavailable", msg: "The assignment could not be saved."})
			return
		}
	}
	out := func(status int, replayed, reminded bool) {
		body := map[string]any{"link": link, "replayed": replayed}
		if reminded {
			body["reminded"] = true
		}
		send(w, status, map[string]any{"data": body})
	}
	// Already sent: the same assignment is a replay; force sends a reminder.
	if found && link.Status == "sent" && !in.Force {
		out(200, true, false)
		return
	}
	reminding := found && link.Status == "sent" && in.Force
	if reminding {
		link.Reminders++
	}
	if in.Note != "" {
		link.Note = strings.TrimSpace(in.Note)
	}
	if len(in.Checks) > 0 {
		link.Checks = in.Checks
	}

	// Step 1: the GitHub assignee, best effort. An agent without a GitHub login is still assignable.
	if !link.ForgeAssigned && !reminding {
		login := h.login(ctx, agent.ID)
		link.ForgeLogin = login
		if login != "" {
			if _, err := h.gh(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/assignees", repoPath(owner, name), num), map[string][]string{"assignees": {login}}, nil); err == nil {
				link.ForgeAssigned = true
			}
		}
		link.Status = "assigned"
		link.Error = ""
		_ = h.save(ctx, link)
	}

	// Step 2: the Tether message, from Tachyon's service identity.
	facts := Facts{}
	if h.Facts != nil {
		facts = h.Facts(ctx, prKey)
	}
	subject, body := render(link, pr, owner, name, num, facts, h.workspace(owner, name, num), reminding)
	meta := map[string]string{"console.intent": "message", "console.template": "pr_assignment", "console.dedupe_key": dedupe}
	id, err := h.Mail.Send(ctx, agent.URN, subject, body, meta)
	if err != nil {
		link.Status, link.Error = "failed", sentence(err)
		if reminding {
			link.Status = "sent" // the earlier message stands; only the reminder failed
		}
		_ = h.save(ctx, link)
		out(200, found, false)
		return
	}
	link.MessageID, link.Status, link.Error = id, "sent", ""
	if err := h.save(ctx, link); err != nil {
		fail(w, &apiErr{status: 503, code: "unavailable", msg: "The message was sent but the assignment could not be saved."})
		return
	}
	if h.Changed != nil {
		h.Changed(prKey)
	}
	out(200, found, reminding)
}

func (h *Handler) login(ctx context.Context, agentID string) string {
	if h.Logins != nil {
		if l := h.Logins.Login(ctx, agentID); l != "" {
			return l
		}
	}
	// No mapping: the agent's slug, only if that login exists on GitHub.
	if !forgeapi.ValidRepoPart(agentID) {
		return ""
	}
	if _, err := h.gh(ctx, http.MethodGet, "/users/"+url.PathEscape(agentID), nil, nil); err != nil {
		return ""
	}
	return agentID
}

func (h *Handler) workspace(owner, name string, num int) string {
	if h.Workspace != nil {
		return h.Workspace(owner, name, num)
	}
	return fmt.Sprintf("/prs/github/%s/%s/%d", owner, name, num)
}

func sentence(err error) string {
	s := err.Error()
	if len(s) > 300 {
		s = s[:300]
	}
	return s + ". The assignee is set; retry sends the message only."
}

// ---- the message ----

var taskText = map[string]struct {
	Label        string
	Instructions []string
}{
	"review":            {"Review", []string{"Review the diff and submit a review (approve, comment or request changes).", "Do not merge."}},
	"fix_checks":        {"Fix failing checks", []string{"Find why the failing checks fail and fix it on the PR branch.", "Push to the existing branch; do not open a new PR.", "Report back when checks are green."}},
	"address_comments":  {"Address review comments", []string{"Work through the requested changes and open review threads.", "Push fixes to the PR branch and reply on each thread."}},
	"resolve_conflicts": {"Resolve conflicts", []string{"Rebase or merge the base branch into the PR branch and resolve conflicts.", "Re-run checks after the push."}},
}

var reasonText = map[string]string{"review_requested": "review requested", "checks_failed": "checks failed", "changes_requested": "changes requested",
	"conflicting": "merge conflicts", "unassigned": "unassigned", "stale": "stale", "mentioned": "mentioned"}

// render builds the subject and the readable body, ending with the fenced machine payload (the same
// console.pr_assignment/v1 the browser used to send, plus checks_failing when the task is fix_checks).
func render(l Link, p pull, owner, name string, num int, f Facts, workspace string, reminder bool) (string, string) {
	t := taskText[l.Task]
	subject := fmt.Sprintf("[PR] %s: %s/%s#%d", t.Label, owner, name, num)
	if reminder {
		subject = "[Reminder] " + subject
	}
	reasons := f.Reasons
	if reasons == nil {
		reasons = []string{}
	}
	mergeable := f.Mergeable
	if mergeable == "" {
		mergeable = "unknown"
	}
	payload := map[string]any{
		"schema": schema, "task": l.Task, "dedupeKey": l.DedupeKey,
		"pr":           map[string]any{"host": "github", "owner": owner, "repo": name, "number": num, "title": p.Title, "url": p.HTMLURL, "workspace": workspace, "author": p.User.Login},
		"reasons":      reasons,
		"mergeable":    mergeable,
		"instructions": t.Instructions,
		"assignedBy":   l.AssignedBy,
	}
	if f.Found {
		payload["checks"] = map[string]int{"passed": f.Passed, "failed": f.Failed, "pending": f.Pending}
	}
	if l.Note != "" {
		payload["note"] = l.Note
	}
	if l.Task == "fix_checks" {
		failing := []Check{}
		for _, c := range f.Failing {
			if len(l.Checks) == 0 || slices.Contains(l.Checks, c.Name) {
				failing = append(failing, c)
			}
		}
		for _, n := range l.Checks {
			if !slices.ContainsFunc(failing, func(c Check) bool { return c.Name == n }) {
				failing = append(failing, Check{Name: n})
			}
		}
		payload["checks_failing"] = failing
	}
	js, _ := json.MarshalIndent(payload, "", "  ")
	var b strings.Builder
	if reminder {
		b.WriteString("Reminder: this assignment is still open.\n\n")
	}
	fmt.Fprintf(&b, "You have been assigned a pull request: %s.\n\n", t.Label)
	fmt.Fprintf(&b, "PR: %s/%s#%d %q by %s\n", owner, name, num, p.Title, p.User.Login)
	if p.HTMLURL != "" {
		fmt.Fprintf(&b, "URL: %s\n", p.HTMLURL)
	}
	fmt.Fprintf(&b, "Console workspace: %s\n", workspace)
	var why []string
	for _, rs := range reasons {
		if s, ok := reasonText[rs]; ok {
			why = append(why, s)
		} else {
			why = append(why, rs)
		}
	}
	if len(why) == 0 {
		why = []string{"n/a"}
	}
	fmt.Fprintf(&b, "Why it is pending: %s\n", strings.Join(why, ", "))
	if f.Found {
		fmt.Fprintf(&b, "Checks: %d passed, %d failed, %d pending\n", f.Passed, f.Failed, f.Pending)
	}
	fmt.Fprintf(&b, "Mergeable: %s\n\nWhat to do:\n", mergeable)
	for _, i := range t.Instructions {
		fmt.Fprintf(&b, "- %s\n", i)
	}
	if l.Note != "" {
		fmt.Fprintf(&b, "\nNote from the operator: %s\n", l.Note)
	}
	b.WriteString("\nReply on this thread with a one-line result when done.\n\n```" + fence + "\n" + string(js) + "\n```")
	return subject, b.String()
}
