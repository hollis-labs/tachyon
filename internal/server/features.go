package server

import (
	"net/http"

	"github.com/hollis-labs/tachyon/internal/approvals"
)

// featureMount registers one feature's routes on the mux. Features add themselves from an init()
// in their own register_<feature>.go, read their own switches from the environment, and never
// touch Config, cmd/tachyon/main.go or Routes(), so parallel feature PRs stop colliding on the
// same shared lines. Mount routes explicitly (the /api/plugins/ catch-all is unguarded) and wrap
// every route that writes in operator.RequireOperator.
type featureMount func(mux *http.ServeMux, operator *approvals.Bridge)

var featureMounts []featureMount

func registerFeature(m featureMount) { featureMounts = append(featureMounts, m) }
