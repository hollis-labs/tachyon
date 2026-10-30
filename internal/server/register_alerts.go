package server

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/alertsrules"
	"github.com/hollis-labs/tachyon/internal/alertstore"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const (
	alertsKey         = "alerts"
	stateWrapKey      = "itemstate.wrap"
	alertsPruneEvery  = time.Hour
	alertsDefaultZone = "UTC"
)

func init() {
	registerStart(startAlerts)
	registerServerMount(mountAlerts)
}

func alertsStubsOn() bool { return os.Getenv("TACHYON_ALERTS_STUBS") == "1" }

// startAlerts opens the Alerts store (AL1) on the shared database, with the default rule pack, and
// prunes retained rows hourly. It also registers the /console/state compat wrapper, which mountItemState
// applies, so alert keys ("al_...") reach the alert store through the existing console client.
func startAlerts(ctx context.Context, s *Server, db *localstore.Store) {
	if alertsStubsOn() {
		return
	}
	pack, problems := alertsrules.ParsePack(alertsapi.DefaultRulePack())
	if len(problems) > 0 {
		slog.Warn("alerts: default rule pack problems", "problems", problems)
	}
	st := &alertstore.Store{DB: db, Pack: func() alertsrules.Pack { return pack }}
	tz := time.Local.String()
	if tz == "" || tz == "Local" {
		tz = alertsDefaultZone
	}
	api := &alertstore.API{Store: st, Prefs: alertstore.SettingsPrefs{DB: db}, TZ: tz}
	s.ext.Store(alertsKey, api)
	if !forgeStubsOn() {
		s.ext.Store(stateWrapKey, func(next http.Handler) http.Handler { return api.StateCompat(alertsapi.StatePrefix, next) })
	}
	go func() {
		t := time.NewTicker(alertsPruneEvery)
		defer t.Stop()
		for {
			if err := st.Prune(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("alerts: prune failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// mountAlerts mounts either the fixture stub (TACHYON_ALERTS_STUBS=1, dev only) or the real routes.
// Alert writes need a same-origin browser request and refuse an Authorization header; there is no
// operator elevation, because alert ops are low impact. The /console/state routes belong to
// mountItemState (or the forge stub), never to this mount.
func mountAlerts(s *Server, mux *http.ServeMux, _ *approvals.Bridge) {
	if alertsStubsOn() {
		stub := alertsapi.NewStub().Handler()
		for _, route := range alertsapi.Routes {
			if route.Base == alertsapi.StatePrefix && forgeStubsOn() {
				continue
			}
			method, path, _ := strings.Cut(route.Pattern, " ")
			mux.Handle(method+" "+route.Base+path, stub)
		}
		return
	}
	v, ok := s.ext.Load(alertsKey)
	if !ok {
		return
	}
	api := v.(*alertstore.API)
	h := api.Handler()
	for _, pat := range api.Routes() {
		mux.Handle(pat, h)
	}
}
