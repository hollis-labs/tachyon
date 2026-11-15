package main

import (
	"errors"
	"fmt"

	"github.com/hollis-labs/libs/core/util/scheduler/sqlstore"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/scheduler"
	"github.com/hollis-labs/substrate/modules/secrets"

	"github.com/hollis-labs/tachyon/next/internal/objectsync"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/upstreamauth"
)

// syncUpstreams are object sync's providers from the configuration. Credentials are secrets
// references resolved per request.
func syncUpstreams(app *module.App, cfg settings.Settings) objectsync.Upstreams {
	res := secrets.Resolver(app)
	u := cfg.Upstreams
	return objectsync.Upstreams{
		TetherAddr: u.Tether.URL, TetherToken: upstreamauth.FromRef(res, u.Tether.Token),
		CerberusAddr: u.Cerberus.URL, CerberusToken: upstreamauth.FromRef(res, u.Cerberus.Token),
		GitHubURL: u.GitHub.URL, GitHubOrg: u.GitHub.Org, GitHubToken: upstreamauth.FromRef(res, u.GitHub.Token),
	}
}

// objectSync builds object sync over the scheduler module's store (the migrated schedules live
// there) and the domain store. It does not start the engine.
func objectSync(app *module.App, cfg settings.Settings) (*objectsync.Jobs, []objectsync.Source, error) {
	db := store.From(app)
	if db == nil {
		return nil, nil, errors.New("the database module is not started")
	}
	st, ok := scheduler.Store(app).(*sqlstore.Store)
	if !ok {
		return nil, nil, errors.New("the scheduler module is not started on the reference SQLite store")
	}
	sources, err := objectsync.Sources(syncUpstreams(app, cfg))
	if err != nil {
		return nil, nil, fmt.Errorf("object sync sources: %w", err)
	}
	j := objectsync.NewJobs(st, objectsync.New(store.Wrap(db)), sources)
	j.Engine = scheduler.Engine(app)
	return j, sources, nil
}
