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
	prefs := alertstore.SettingsPrefs{DB: db}
	st := &alertstore.Store{DB: db, Pack: func() alertsrules.Pack { return pack }, Window: func(ctx context.Context) time.Duration {
		if p, err := prefs.Get(ctx); err == nil && p != nil {
			return time.Duration(p.CoalesceWindowS) * time.Second
		}
		return alertstore.DefaultCoalesceWindow
	}}
	tz := time.Local.String()
	if tz == "" || tz == "Local" {
		tz = alertsDefaultZone
	}
	api := &alertstore.API{Store: st, Prefs: prefs, TZ: tz}
	s.ext.Store(alertsKey, api)
	if !forgeStubsOn() {
		s.ext.Store(stateWrapKey, func(next http.Handler) http.Handler { return api.StateCompat(alertsapi.StatePrefix, next) })
	}
	// AL5: approval alerts settle when the queue item is decided or expires; the detail shows its state.
	st.ApprovalState = func(ctx context.Context, id string) string {
		if rec, err := (&approvals.Store{DB: db}).Get(ctx, id); err == nil {
			return string(rec.State)
		}
		return ""
	}
	go st.FollowApprovals(ctx, approvalFeed{approvals.Changes(db)}, func(err error) { slog.Warn("alerts: approval follow failed", "err", err) })
	// AL3: snooze and defer wake-ups and escalation, on the server clock.
	go st.Maintain(ctx, func(err error) { slog.Warn("alerts: maintenance failed", "err", err) })
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

// approvalFeed adapts the Approvals change broker to alertstore.ApprovalEvents.
type approvalFeed struct{ b *approvals.Broker }

func (f approvalFeed) Subscribe() (<-chan alertstore.ApprovalChange, func()) {
	_, in, _ := f.b.Since(0, true)
	out := make(chan alertstore.ApprovalChange, 64)
	done := make(chan struct{})
	go func() {
		defer close(out)
		for {
			select {
			case c, ok := <-in:
				if !ok {
					return
				}
				select {
				case out <- alertstore.ApprovalChange{Type: c.Type, ItemID: c.ItemID, State: c.State}:
				case <-done:
					return
				}
			case <-done:
				return
			}
		}
	}()
	return out, func() {
		select {
		case <-done:
		default:
			close(done)
			f.b.Unsubscribe(in)
		}
	}
}
