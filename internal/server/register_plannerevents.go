package server

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/planner"
)

func init() { registerStart(startPlannerEvents) }

// startPlannerEvents runs the Planner ingest worker (PL4): planner object revisions, Torque task moves
// and merged PRs become timeline events and stream entries. TACHYON_PLANNER_EVENTS=0 turns it off.
// Without a Torque URL only revisions are ingested; the Torque and PR steps record their failure in
// the ingest state and the next pass retries.
func startPlannerEvents(ctx context.Context, s *Server, db *localstore.Store) {
	if os.Getenv("TACHYON_PLANNER_EVENTS") == "0" {
		return
	}
	torque := strings.TrimRight(strings.TrimSpace(s.cfg.Upstreams.TorqueURL), "/")
	in := &planner.Ingest{
		Owned:  &planner.Owned{Objects: objects.New(db)},
		Tasks:  planner.NewTasks(torque, s.cfg.Upstreams.TorqueToken),
		Events: planner.Events{DB: db},
		DB:     db,
	}
	go in.Run(ctx, 45*time.Second)
}
