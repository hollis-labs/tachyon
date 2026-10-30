package server

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/alertsource"
	"github.com/hollis-labs/tachyon/internal/alertstore"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/forgeapi"
	"github.com/hollis-labs/tachyon/internal/forgeassign"
	"github.com/hollis-labs/tachyon/internal/forgeindex"
	"github.com/hollis-labs/tachyon/internal/itemstate"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const forgeIndexKey = "forgeindex"

func init() {
	registerStart(startForgeIndex)
	registerServerMount(mountForgeIndex)
}

// startForgeIndex runs the PR index poller (PR3) with the server's GitHub token. TACHYON_FORGE_INDEX=0
// turns it off; the forge stub (TACHYON_FORGE_STUBS=1) replaces it. New reasons on already-indexed
// PRs go to the Alerts store as pr.* events.
func startForgeIndex(ctx context.Context, s *Server, db *localstore.Store) {
	if os.Getenv("TACHYON_FORGE_INDEX") == "0" || forgeStubsOn() {
		return
	}
	x := &forgeindex.Index{
		DB:       db,
		GH:       &forgeindex.GitHub{Token: s.cfg.Upstreams.GitHubToken},
		Settings: forgeindex.AppSettings{DB: db, Org: s.cfg.Upstreams.GitHubOrg},
		Bus:      forgeindex.NewBroker(nil),
	}
	x.Feed = func(e forgeindex.FeedEvent) { feedPRAlert(s, e) }
	s.ext.Store(forgeIndexKey, x)
	go x.Run(ctx)
}

// feedPRAlert hands a pr.* event to the Alerts store when it is running.
func feedPRAlert(s *Server, e forgeindex.FeedEvent) {
	v, ok := s.ext.Load(alertsKey)
	if !ok {
		return
	}
	api, ok := v.(*alertstore.API)
	if !ok {
		return
	}
	owner, name, num, ok := forgeapi.ParsePRKey(e.PRKey)
	if !ok {
		return
	}
	repo := owner + "/" + name
	err := api.Store.Ingest(alertsource.Event{
		Source: "forge", SourceEventID: e.ID, Kind: e.Kind, AtMS: e.AtMS,
		Subjects:    []alertsapi.Subject{{Kind: "pr", Ref: e.PRKey, Label: repo + "#" + strconv.Itoa(num), URL: e.URL}},
		CoalesceKey: "pr:" + repo + "#" + strconv.Itoa(num),
		Payload:     map[string]any{"repo": repo, "number": num, "title": e.Title},
	})
	if err != nil {
		slog.Warn("forge index: alert ingest failed", "err", err)
	}
}

// mountForgeIndex mounts the three reads the index serves. Reads are open like the rest of the forge
// reads; there are no writes here. Before the first refresh, or without a credential, the pending
// route falls through to the legacy handler.
func mountForgeIndex(s *Server, mux *http.ServeMux, _ *approvals.Bridge) {
	v, ok := s.ext.Load(forgeIndexKey)
	if !ok {
		return
	}
	x := v.(*forgeindex.Index)
	var legacy http.Handler = s.inventory.APIRoutes()
	if s.cache != nil {
		legacy = s.cache.Wrap(legacy)
	}
	api := &forgeindex.API{Index: x, Fallback: legacy, Triage: func(ctx context.Context) (map[string]forgeindex.Triage, error) {
		out := map[string]forgeindex.Triage{}
		iv, ok := s.ext.Load(itemStateKey)
		if !ok {
			return out, nil
		}
		rows, err := iv.(*itemstate.Store).List(ctx, "pr-inbox:github:")
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			t := forgeindex.Triage{Status: "active", By: r.By}
			switch r.Status {
			case "snoozed":
				t.Status = "snoozed"
				if r.SnoozeUtil != nil {
					t.SnoozeUntilMS = r.SnoozeUtil.UnixMilli()
				}
			case "dismissed":
				t.Status = "dismissed"
			case "settled":
				t.Status = "resolved"
			}
			out[strings.TrimPrefix(r.Key, "pr-inbox:")] = t
		}
		return out, nil
	}}
	api.Agents = func(ctx context.Context) (map[string][]forgeindex.Assignee, error) {
		av, ok := s.ext.Load(forgeAssignKey)
		if !ok {
			return nil, nil
		}
		links, err := av.(*forgeassign.Handler).Assignees(ctx)
		if err != nil {
			return nil, err
		}
		out := map[string][]forgeindex.Assignee{}
		for k, ls := range links {
			for _, l := range ls {
				out[k] = append(out[k], forgeindex.Assignee{Kind: "agent", ID: l.AgentID, Label: l.AgentID, URN: l.AgentURN, Task: l.Task, Status: l.Status})
			}
		}
		return out, nil
	}
	h := api.Handler()
	for _, pat := range forgeindex.Routes() {
		mux.Handle(pat, h)
	}
}
