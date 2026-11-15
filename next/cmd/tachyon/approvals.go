package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/secrets"

	"github.com/hollis-labs/tachyon/next/internal/approvals"
	"github.com/hollis-labs/tachyon/next/internal/routes"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
)

// approvalsMaintainEvery is the old build's maintenance tick: purge spent secrets, expire due items.
const approvalsMaintainEvery = 30 * time.Second

// newApprovals is the approval inbox over the migrated tables (slice B7). Approval secrets are sealed
// with the key tachyon.files.secret_key references; without it the inbox still serves and every
// secret submission is refused (503 secrets_unavailable), as the old build did without its key file.
func newApprovals(ctx context.Context, app *module.App, cfg settings.Settings, domain *store.Store) *approvals.Inbox {
	var raw []byte
	problem := ""
	if ref := strings.TrimSpace(cfg.Files.SecretKey); ref == "" {
		problem = "tachyon.files.secret_key is not set"
	} else if r := secrets.Resolver(app); r == nil {
		problem = "the secrets module is not started"
	} else if res, err := r.Resolve(ctx, ref); err != nil {
		// The reference's resolver error names the reference, never a value.
		problem = "tachyon.files.secret_key could not be resolved"
	} else {
		raw = res.Secret.Bytes()
		res.Secret.Zero()
	}
	st := &approvals.Store{DB: domain}
	inbox := approvals.NewInbox(st, approvals.NewSecrets(domain, raw, problem))
	clear(raw)
	return inbox
}

// startApprovals runs the inbox maintenance the old server ran: the index and decision-log backfill
// once, then the secret purge and the expiry sweep on a ticker. Both write only Tachyon's own tables.
// It returns a function that stops it.
func startApprovals(inbox *approvals.Inbox) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		inbox.Maintain(ctx, approvalsMaintainEvery, slog.Default().Info)
	}()
	return func() { cancel(); <-done }
}

// approvalsRoutes are the inbox's routes (docs/api-contract.md, the approvals rows): reads under
// tachyon.approvals.read, every write under tachyon.approvals.resolve. All of them reach the one inbox
// handler, which checks the caller and, per action, the operator assurance (medium, high and
// destructive resolves, batches, secrets).
func approvalsRoutes(inbox *approvals.Inbox) []routes.Route {
	const read, resolve = "tachyon.approvals.read", "tachyon.approvals.resolve"
	p := approvals.Prefix
	rs := []routes.Route{}
	for _, r := range []struct{ method, path, scope string }{
		{http.MethodGet, "/inbox", read},
		{http.MethodGet, "/history", read},
		{http.MethodGet, "/events", read},
		{http.MethodGet, "/items/{id}", read},
		{http.MethodGet, "/items/{id}/audit", read},
		{http.MethodPost, "/items/{id}/present", resolve},
		{http.MethodPost, "/items/{id}/resolve", resolve},
		{http.MethodPost, "/items/{id}/defer", resolve},
		{http.MethodDelete, "/items/{id}/defer", resolve},
		{http.MethodPost, "/batch/resolve", resolve},
		{http.MethodPost, "/secrets", resolve},
	} {
		rs = append(rs, routes.Route{Method: r.method, Pattern: p + r.path, Scope: r.scope, Handler: inbox})
	}
	return rs
}
