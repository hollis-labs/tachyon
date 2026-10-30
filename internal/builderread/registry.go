package builderread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/builderapi"
	"github.com/hollis-labs/tachyon/internal/catalogindex"
)

// RegProject is one Tether registry project profile.
type RegProject struct {
	URN, DisplayName, Description, Status, MergedInto string
	Tags, EntryPoints                                 []string
	Guidelines                                        string
	Props                                             map[string]string
	ExternalIDs                                       []ExternalID
}

type ExternalID struct{ Substrate, ID string }

// ProjectSource lists registry projects. An error means "unknown": the routes answer 503, never an empty list.
type ProjectSource interface {
	Projects(ctx context.Context) ([]RegProject, error)
}

type tetherProjects struct {
	base   string
	client *http.Client
}

// NewTetherProjects reads the registry with the read-scope token, on the wire objectsync already scans
// (GET /registry/projects?status=*&include=external_ids -> {projects:[...]}).
func NewTetherProjects(addr, tokenFile string) (ProjectSource, error) {
	base, client, err := tetherClient(addr, tokenFile)
	if err != nil {
		return nil, err
	}
	return &tetherProjects{base: base, client: client}, nil
}

func (t *tetherProjects) Projects(ctx context.Context) ([]RegProject, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", t.base+"/registry/projects?status=*&include=external_ids", nil)
	req.Header.Set("Accept", "application/json")
	res, err := t.client.Do(req)
	if err != nil {
		return nil, errors.New("Tether unreachable")
	}
	defer res.Body.Close()
	raw, rerr := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if res.StatusCode != 200 || rerr != nil {
		return nil, fmt.Errorf("Tether answered HTTP %d for /registry/projects", res.StatusCode)
	}
	var w struct {
		Projects []struct {
			URN         string            `json:"urn"`
			Kind        string            `json:"kind"`
			DisplayName string            `json:"display_name"`
			Description string            `json:"description"`
			Status      string            `json:"status"`
			MergedInto  string            `json:"merged_into"`
			Tags        []string          `json:"tags"`
			Guidelines  string            `json:"guidelines"`
			EntryPoints []string          `json:"entry_points"`
			Props       map[string]string `json:"props"`
			ExternalIDs []struct {
				Substrate  string `json:"substrate"`
				ExternalID string `json:"external_id"`
			} `json:"external_ids"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, errors.New("Tether registry response unreadable")
	}
	var out []RegProject
	for _, p := range w.Projects {
		if p.Kind != "" && p.Kind != "project" {
			continue
		}
		rp := RegProject{URN: clean(p.URN, 200), DisplayName: clean(p.DisplayName, 120), Description: clean(p.Description, 2000), Status: clean(p.Status, 30),
			MergedInto: clean(p.MergedInto, 200), Tags: p.Tags, Guidelines: clean(p.Guidelines, 8000), EntryPoints: p.EntryPoints, Props: p.Props}
		for _, e := range p.ExternalIDs {
			rp.ExternalIDs = append(rp.ExternalIDs, ExternalID{clean(e.Substrate, 60), clean(e.ExternalID, 200)})
		}
		out = append(out, rp)
	}
	return out, nil
}

type cachedProjects struct {
	src ProjectSource
	ttl time.Duration
	now func() time.Time

	mu   sync.Mutex
	at   time.Time
	list []RegProject
	err  error
}

func (c *cachedProjects) Projects(ctx context.Context) ([]RegProject, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && c.now().Sub(c.at) < c.ttl {
		return c.list, c.err
	}
	c.list, c.err = c.src.Projects(ctx)
	c.at = c.now()
	return c.list, c.err
}

var slugRe = regexp.MustCompile(`[^a-z0-9._-]+`)

// slug turns a name into an id the contract accepts, or "" when nothing usable is left.
func slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-._")
	if len(s) > 64 {
		s = strings.Trim(s[:64], "-._")
	}
	if !builderapi.IDPattern.MatchString(s) {
		return ""
	}
	return s
}

// regID is the id the Builder uses for a registry project: the registry's own slug when it carries one in
// props, otherwise the last URN segment, otherwise the slugged display name. UNVERIFIED against live Tether: the
// registry has no documented short id, so ids are derived and a duplicate gets the next candidate or is dropped.
func regID(p RegProject) string {
	for _, c := range []string{p.Props["id"], p.Props["slug"], p.URN[strings.LastIndex(p.URN, "/")+1:], p.DisplayName} {
		if s := slug(c); s != "" {
			return s
		}
	}
	return ""
}

// link resolves the registry project to a catalog project id. A catalog id is accepted when it is named by
// props.catalog_id or a catalog external id, or when it equals the registry id or the slugged display name; more than
// one distinct candidate is ambiguous and reads unlinked. UNVERIFIED: the real mapping is not documented.
func link(p RegProject, id string, catalog map[string]bool) (string, string) {
	var cands []string
	add := func(c string) {
		c = slug(c)
		if c != "" && catalog[c] {
			for _, e := range cands {
				if e == c {
					return
				}
			}
			cands = append(cands, c)
		}
	}
	add(p.Props["catalog_id"])
	for _, e := range p.ExternalIDs {
		if strings.Contains(strings.ToLower(e.Substrate), "catalog") {
			add(e.ID)
		}
	}
	if len(cands) == 0 {
		add(id)
		add(p.DisplayName)
	}
	switch len(cands) {
	case 1:
		return cands[0], ""
	case 0:
		return "", "No catalog project matches this registry id."
	}
	return "", "More than one catalog project matches this registry project."
}

type regView struct {
	id         string
	p          RegProject
	catalogID  string
	linkReason string
}

func (r *Reader) registry(w http.ResponseWriter, req *http.Request) ([]regView, map[string]file, bool) {
	if r.Projects == nil {
		fail(w, 503, "registry_unavailable", "The Tether registry is not configured.")
		return nil, nil, false
	}
	list, err := r.Projects.Projects(req.Context())
	if err != nil {
		fail(w, 503, "registry_unavailable", "The Tether registry could not be read.")
		return nil, nil, false
	}
	ps, ok := r.kind(w, req, catalogindex.KindProject)
	if !ok {
		return nil, nil, false
	}
	byID := map[string]file{}
	cat := map[string]bool{}
	for _, f := range ps {
		byID[f.ID], cat[f.ID] = f, true
	}
	seen := map[string]bool{}
	var out []regView
	for _, p := range list {
		if p.MergedInto != "" || p.Status == "merged" {
			continue // folded into another project
		}
		id := regID(p)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		cid, reason := link(p, id, cat)
		out = append(out, regView{id: id, p: p, catalogID: cid, linkReason: reason})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out, byID, true
}

func linkJSON(v regView) map[string]any {
	if v.catalogID == "" {
		return map[string]any{"state": "unlinked", "catalog_id": nil, "reason": v.linkReason}
	}
	return map[string]any{"state": "linked", "catalog_id": v.catalogID, "reason": nil}
}

func (r *Reader) registryList(w http.ResponseWriter, req *http.Request) {
	views, _, ok := r.registry(w, req)
	if !ok {
		return
	}
	ls, ok := r.kind(w, req, catalogindex.KindLaunch)
	if !ok {
		return
	}
	live, _, liveOK := r.liveByLaunch(req.Context())
	q := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("q")))
	var rows []map[string]any
	for _, v := range views {
		if q != "" && !strings.Contains(strings.ToLower(v.id+" "+v.p.DisplayName+" "+v.p.Description), q) {
			continue
		}
		counts := map[string]any{"agents": nil, "launches": nil, "planner_items": nil, "tether_sessions": nil}
		if v.catalogID != "" {
			agents, launches, sessions := projectConsumers(ls, v.catalogID, live)
			counts["agents"], counts["launches"] = len(agents), len(launches)
			if liveOK {
				counts["tether_sessions"] = sessions
			}
		}
		rows = append(rows, map[string]any{"id": v.id, "display_name": orDefault(v.p.DisplayName, v.id), "description": v.p.Description,
			"tags": nonNilStrings(v.p.Tags), "status": v.p.Status, "link": linkJSON(v), "counts": counts})
	}
	out, next := page(req, rows)
	if out == nil {
		out = []map[string]any{}
	}
	write(w, 200, map[string]any{"projects": out, "next_cursor": orNull(next)})
}

// projectConsumers lists the agent ids and launch ids that run in a catalog project and how many live sessions
// belong to those launches.
func projectConsumers(launches []file, catalogID string, live map[string][]LiveSession) (agents, launchIDs []string, sessions int) {
	seen := map[string]bool{}
	for _, l := range launches {
		if str(l.Fields["project"]) != catalogID {
			continue
		}
		launchIDs = append(launchIDs, l.ID)
		sessions += len(live[l.ID])
		if a := str(l.Fields["agent"]); a != "" && !seen[a] {
			seen[a] = true
			agents = append(agents, a)
		}
	}
	sort.Strings(agents)
	return agents, launchIDs, sessions
}

func (r *Reader) registryItem(w http.ResponseWriter, req *http.Request) {
	views, catalog, ok := r.registry(w, req)
	if !ok {
		return
	}
	var v *regView
	for i := range views {
		if views[i].id == req.PathValue("id") {
			v = &views[i]
		}
	}
	if v == nil {
		fail(w, 404, "not_found", "No such project.")
		return
	}
	out := map[string]any{"id": v.id,
		"identity": map[string]any{"display_name": orDefault(v.p.DisplayName, v.id), "description": v.p.Description, "tags": nonNilStrings(v.p.Tags), "status": v.p.Status},
		"guidance": map[string]any{"entry_points": nonNilStrings(v.p.EntryPoints), "guidelines": v.p.Guidelines},
		"link":     linkJSON(*v), "catalog": nil, "soon": []string{"machines", "services", "repos"},
		"consumed_by": map[string]any{"agents": nil, "launches": nil, "planner_items": nil, "tether_sessions": nil}}
	if v.catalogID != "" {
		f := catalog[v.catalogID]
		d := doc(f)
		ws := map[string]any{}
		if m := str(sub(d, "workspace")["mode"]); m != "" {
			ws["mode"] = m
		}
		var servers any
		if _, set := f.Fields["mcp_servers"]; set {
			servers = strs(f.Fields["mcp_servers"])
		}
		out["catalog"] = map[string]any{"repo_root": orNull(str(d["repo_root"])), "workspace": ws, "mcp_servers": servers, "sha": f.SHA}
		ls, ok := r.kind(w, req, catalogindex.KindLaunch)
		if !ok {
			return
		}
		as, ok := r.kind(w, req, catalogindex.KindAgent)
		if !ok {
			return
		}
		live, _, liveOK := r.liveByLaunch(req.Context())
		agentIDs, launchIDs, sessions := projectConsumers(ls, v.catalogID, live)
		agents, launches := []map[string]any{}, []map[string]any{}
		for _, id := range agentIDs {
			a, _ := find(as, id)
			agents = append(agents, map[string]any{"id": id, "name": orDefault(a.Name, id)})
		}
		for _, id := range launchIDs {
			launches = append(launches, map[string]any{"id": id})
		}
		cb := out["consumed_by"].(map[string]any)
		cb["agents"], cb["launches"] = agents, launches
		if liveOK {
			cb["tether_sessions"] = map[string]any{"live": sessions, "total": nil}
		}
	}
	write(w, 200, out)
}
