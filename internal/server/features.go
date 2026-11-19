package server

import (
	"context"
	"net/http"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// featureMount registers one feature's routes on the mux. Features add themselves from an init()
// in their own register_<feature>.go, read their own switches from the environment, and never
// touch Config, cmd/tachyon/main.go or Routes(), so parallel feature PRs stop colliding on the
// same shared lines. Mount routes explicitly (the /api/plugins/ catch-all is unguarded) and wrap
// every route that writes in operator.RequireOperator.
type featureMount func(mux *http.ServeMux, operator *approvals.Bridge)

var featureMounts []featureMount

func registerFeature(m featureMount) { featureMounts = append(featureMounts, m) }

// Features that need the running Server (its store, its started components) register a startHook,
// which runs from Start with the open store and may keep state in s.ext, and a serverMount, which
// runs from Routes and reads it back. Same rule as above: nothing in Config, main.go or Routes().
type (
	startHook   func(ctx context.Context, s *Server, store *localstore.Store)
	serverMount func(s *Server, mux *http.ServeMux, operator *approvals.Bridge)
)

var (
	startHooks   []startHook
	serverMounts []serverMount
)

func registerStart(h startHook)         { startHooks = append(startHooks, h) }
func registerServerMount(m serverMount) { serverMounts = append(serverMounts, m) }
