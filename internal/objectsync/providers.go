package objectsync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/catalog"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

type TetherProjects struct {
	remote
	scope objects.SourceScope
}

func NewTetherProjects(addr, tokenFile string) (*TetherProjects, error) {
	r, err := newRemote(addr, tokenFile, true)
	if err != nil {
		return nil, err
	}
	instance := r.base
	if strings.HasPrefix(addr, "unix:") {
		instance = addr
	}
	return &TetherProjects{remote: r, scope: objects.SourceScope{Provider: "tether", Instance: instance, Collection: "registry/projects", Kind: objects.ProjectKind}}, nil
}
func (*TetherProjects) Name() string                 { return "tether-projects" }
func (s *TetherProjects) Scope() objects.SourceScope { return s.scope }

type tetherProject struct {
	URN         string            `json:"urn"`
	Kind        string            `json:"kind"`
	DisplayName string            `json:"display_name"`
	Description string            `json:"description"`
	Status      string            `json:"status"`
	Owner       string            `json:"owner"`
	MergedInto  string            `json:"merged_into"`
	UpdatedAt   string            `json:"updated_at"`
	Tags        []string          `json:"tags"`
	Guidelines  string            `json:"guidelines"`
	EntryPoints []string          `json:"entry_points"`
	Props       map[string]string `json:"props"`
	ExternalIDs []struct {
		Substrate  string `json:"substrate"`
		ExternalID string `json:"external_id"`
	} `json:"external_ids"`
}

func (s *TetherProjects) Scan(ctx context.Context, at time.Time, progress func(Progress) error) (Snapshot, error) {
	// The current registry endpoint is an unpaginated, unfiltered full inventory
	// with status=*. Reject envelope extensions until their completeness contract
	// is understood, rather than silently ignoring a future continuation cursor.
	var envelope map[string]json.RawMessage
	if _, err := s.get(ctx, "/registry/projects?status=*&include=external_ids", "", &envelope); err != nil {
		return Snapshot{}, err
	}
	raw, ok := envelope["projects"]
	if !ok || len(envelope) != 1 || string(raw) == "null" {
		return Snapshot{}, scanError("unrecognized Tether inventory envelope")
	}
	var list []tetherProject
	if err := json.Unmarshal(raw, &list); err != nil {
		return Snapshot{}, scanError("invalid Tether project inventory")
	}
	c := collect(s.scope, at)
	for _, p := range list {
		// Registry identity is opaque (currently msg://agent/...); it is never
		// fetched as a URL or interpreted as a local object ID.
		if p.Kind != "project" || strings.TrimSpace(p.DisplayName) == "" || strings.TrimSpace(p.URN) == "" {
			return Snapshot{}, scanError("invalid Tether project identity")
		}
		deleted := false
		switch p.Status {
		case "active", "archived":
		case "deprecated", "merged":
			deleted = true
		default:
			return Snapshot{}, scanError("unknown Tether project status")
		}
		// Retain public correlation facts without treating provider IDs as local FKs.
		d := objects.Project{Common: objects.Common{Name: p.DisplayName, Description: p.Description, Tags: p.Tags, Properties: map[string]any{"tether": map[string]any{"owner": p.Owner, "status": p.Status, "merged_into": p.MergedInto, "props": p.Props, "external_ids": p.ExternalIDs}}}, Guidelines: p.Guidelines, EntryPoints: p.EntryPoints}
		if err := c.add(p.URN, p.UpdatedAt, deleted, d); err != nil {
			return Snapshot{}, err
		}
	}
	c.snapshot.Authoritative = true
	if err := progress(Progress{Pages: 1, Items: len(list), Checkpoint: "inventory_complete"}); err != nil {
		return Snapshot{}, err
	}
	return c.snapshot, nil
}

type GitHubRepositories struct {
	remote
	scope      objects.SourceScope
	org, token string
}

var ownerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

func NewGitHubRepositories(base, org, token string) (*GitHubRepositories, error) {
	org = strings.ToLower(strings.TrimSpace(org))
	if !ownerPattern.MatchString(org) {
		return nil, scanError("invalid GitHub organization")
	}
	// Reuse the credential transport guard even though GitHub's current host
	// configuration supplies a token value rather than a token file.
	if err := upstreamauth.Guard(base, "configured"); err != nil {
		return nil, err
	}
	r, err := newRemote(base, "", false)
	if err != nil {
		return nil, err
	}
	return &GitHubRepositories{remote: r, org: org, token: strings.TrimSpace(token), scope: objects.SourceScope{Provider: "github", Instance: r.base, Collection: "orgs/" + org + "/repos", Kind: objects.RepositoryKind}}, nil
}
func (*GitHubRepositories) Name() string                 { return "github-repositories" }
func (s *GitHubRepositories) Scope() objects.SourceScope { return s.scope }

type githubRepository struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Owner struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	} `json:"owner"`
	Description   string   `json:"description"`
	HTMLURL       string   `json:"html_url"`
	DefaultBranch string   `json:"default_branch"`
	Visibility    string   `json:"visibility"`
	Private       bool     `json:"private"`
	Archived      bool     `json:"archived"`
	Disabled      bool     `json:"disabled"`
	Fork          bool     `json:"fork"`
	Topics        []string `json:"topics"`
	UpdatedAt     string   `json:"updated_at"`
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func (s *GitHubRepositories) Scan(ctx context.Context, at time.Time, progress func(Progress) error) (Snapshot, error) {
	if s.token == "" {
		return Snapshot{}, scanError("GitHub credential unavailable")
	}
	c := collect(s.scope, at)
	var organizationID int64
	for page := 1; page <= 101; page++ {
		path := "/orgs/" + s.org + "/repos"
		query := url.Values{"type": {"all"}, "sort": {"created"}, "direction": {"asc"}, "per_page": {"100"}, "page": {strconv.Itoa(page)}}
		var list []githubRepository
		headers, err := s.get(ctx, path+"?"+query.Encode(), s.token, &list)
		if err != nil {
			return Snapshot{}, err
		}
		if list == nil || len(list) > 100 {
			return Snapshot{}, scanError("invalid GitHub repository inventory")
		}
		for _, p := range list {
			if p.Owner.ID <= 0 || !strings.EqualFold(p.Owner.Login, s.org) {
				return Snapshot{}, scanError("invalid GitHub organization identity")
			}
			if organizationID == 0 {
				organizationID = p.Owner.ID
			}
			if organizationID != p.Owner.ID {
				return Snapshot{}, scanError("GitHub organization changed during inventory")
			}
		}
		// Never follow an upstream URL with a credential. Verify the advertised
		// continuation and construct the next request ourselves on the fixed origin.
		next := nextLink.FindStringSubmatch(headers.Get("Link"))
		if len(next) > 0 {
			u, e := url.Parse(next[1])
			expected, _ := url.Parse(s.base + path)
			// GitHub canonicalizes org slug links to /organizations/{numericID}.
			// Bind that alias to the owner identity observed in the repository page.
			canonicalPath := strings.TrimSuffix(expected.Path, path) + "/organizations/" + strconv.FormatInt(organizationID, 10) + "/repos"
			if e != nil || u.Scheme != expected.Scheme || u.Host != expected.Host || (u.Path != expected.Path && (organizationID == 0 || u.Path != canonicalPath)) || u.User != nil || u.Fragment != "" {
				return Snapshot{}, scanError("unsafe GitHub pagination link")
			}
			nextQuery := u.Query()
			if nextQuery.Get("page") != strconv.Itoa(page+1) || nextQuery.Get("per_page") != "100" {
				return Snapshot{}, scanError("invalid GitHub page sequence")
			}
			nextQuery.Set("page", strconv.Itoa(page))
			if nextQuery.Encode() != query.Encode() {
				return Snapshot{}, scanError("GitHub pagination changed inventory filters")
			}
		}
		for _, p := range list {
			if p.ID <= 0 || strings.TrimSpace(p.Name) == "" || !strings.EqualFold(p.Owner.Login, s.org) {
				return Snapshot{}, scanError("invalid GitHub repository identity")
			}
			visibility := p.Visibility
			if visibility == "" {
				visibility = "public"
				if p.Private {
					visibility = "private"
				}
			}
			if visibility != "public" && visibility != "private" && visibility != "internal" {
				return Snapshot{}, scanError("unknown repository visibility")
			}
			d := objects.Repository{Common: objects.Common{Name: p.Name, Description: p.Description, Tags: p.Topics, Properties: map[string]any{"github": map[string]any{"disabled": p.Disabled, "fork": p.Fork}}}, Host: "github", Owner: p.Owner.Login, Slug: p.Name, URL: p.HTMLURL, DefaultBranch: p.DefaultBranch, Visibility: visibility, Archived: p.Archived}
			if err := c.add(strconv.FormatInt(p.ID, 10), p.UpdatedAt, false, d); err != nil {
				return Snapshot{}, err
			}
		}
		done := len(next) == 0 && len(list) < 100
		checkpoint := fmt.Sprintf("page:%d", page)
		if done {
			checkpoint = "visible_inventory_complete"
		}
		if err := progress(Progress{Pages: page, Items: len(c.snapshot.Observations), Checkpoint: checkpoint}); err != nil {
			return Snapshot{}, err
		}
		if done {
			// Even a fully paginated list is visibility-limited. Absence, 404, token
			// changes and loss of org membership must never become deletion evidence.
			return c.snapshot, nil
		}
	}
	return Snapshot{}, scanError("GitHub inventory exceeded page limit")
}

func Sources(u catalog.Upstreams) ([]Source, error) {
	var sources []Source
	if u.TetherAddr != "" {
		s, err := NewTetherProjects(u.TetherAddr, u.TetherTokenFile)
		if err != nil {
			return nil, err
		}
		sources = append(sources, s)
		agents, err := NewTetherAgents(u.TetherAddr, u.TetherTokenFile)
		if err != nil {
			return nil, err
		}
		sources = append(sources, agents)
		workstreams, err := NewTetherWorkstreams(u.TetherAddr, u.TetherTokenFile)
		if err != nil {
			return nil, err
		}
		sources = append(sources, workstreams)
		sessions, err := NewTetherSessions(u.TetherAddr, u.TetherTokenFile)
		if err != nil {
			return nil, err
		}
		sources = append(sources, sessions)
	}
	if u.CerberusAddr != "" {
		s, err := NewCerberusMachines(u.CerberusAddr, u.CerberusTokenFile)
		if err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	if u.GitHubOrg != "" {
		s, err := NewGitHubRepositories("https://api.github.com", u.GitHubOrg, u.GitHubToken)
		if err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, nil
}
