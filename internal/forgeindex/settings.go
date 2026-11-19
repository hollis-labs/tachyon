package forgeindex

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/hollis-labs/tachyon/internal/forgeapi"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Settings are the tunables of the poller. Until the settings registry (ST1/ST2) takes them over they
// are read from app_settings behind this small interface, so the registry swaps the source only.
type Settings struct {
	// Watched is a list of "owner/name" repos or "org:<name>" patterns.
	Watched    []string
	PollS      int
	StaleDays  int
	RateFloor  int
	MaxPending int
}

// SettingsSource returns the current settings.
type SettingsSource interface {
	Load(ctx context.Context) Settings
}

// AppSettings reads forge.watched (JSON array), forge.poll_s, forge.stale_days, forge.rate_floor and
// forge.max_pending from app_settings. Org is the default watched org.
type AppSettings struct {
	DB  *localstore.Store
	Org string
}

func (a AppSettings) Load(ctx context.Context) Settings {
	s := Settings{PollS: forgeapi.PollSecondsDefault, StaleDays: forgeapi.StaleDaysDefault, RateFloor: forgeapi.RateFloorDefault, MaxPending: forgeapi.MaxPendingDefault}
	get := func(key string) string {
		var v string
		_ = a.DB.Reader.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, key).Scan(&v)
		return v
	}
	num := func(key string, dst *int, lo, hi int) {
		if n, err := strconv.Atoi(get(key)); err == nil && n >= lo && n <= hi {
			*dst = n
		}
	}
	num("forge.poll_s", &s.PollS, 15, 3600)
	num("forge.stale_days", &s.StaleDays, 1, 365)
	num("forge.rate_floor", &s.RateFloor, 0, 5000)
	num("forge.max_pending", &s.MaxPending, 1, 500)
	var w []string
	if json.Unmarshal([]byte(get("forge.watched")), &w) == nil {
		for _, e := range w {
			if validWatch(e) {
				s.Watched = append(s.Watched, e)
			}
		}
	}
	if len(s.Watched) == 0 && a.Org != "" {
		s.Watched = []string{"org:" + a.Org}
	}
	return s
}

func validWatch(e string) bool {
	if len(e) > 3 && e[:4] == "org:" {
		return forgeapi.ValidRepoPart(e[4:])
	}
	for i := 0; i < len(e); i++ {
		if e[i] == '/' {
			return forgeapi.ValidRepoPart(e[:i]) && forgeapi.ValidRepoPart(e[i+1:])
		}
	}
	return false
}

// searchTerm turns a watched entry into a search qualifier.
func searchTerm(e string) string {
	if len(e) > 3 && e[:4] == "org:" {
		return e
	}
	return "repo:" + e
}
