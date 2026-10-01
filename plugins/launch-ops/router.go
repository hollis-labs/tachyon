package main

import (
	"context"
	"fmt"
)

// launchRouter routes persisted launches by their backend even when defaults
// change. Provider remains the existing runtime/model-provider selector.
type launchRouter struct {
	store           *LaunchStore
	defaultProvider string
	adapters        map[string]LaunchAdapter
}

func (r *launchRouter) Prepare(ctx context.Context, req PrepareRequest) (*Launch, error) {
	backend := req.Backend
	if backend == "" && (req.Provider == "nanite" || req.Provider == "tether") {
		backend = req.Provider
	}
	if backend == "" {
		backend = r.defaultProvider
	}
	adapter, ok := r.adapters[backend]
	if !ok {
		return nil, fmt.Errorf("unsupported launch backend %q", backend)
	}
	if req.Provider == "" {
		req.Provider = backend
	}
	return adapter.Prepare(ctx, req)
}
func (r *launchRouter) adapter(ctx context.Context, id string) (LaunchAdapter, error) {
	l, err := r.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	a, ok := r.adapters[l.Backend]
	if !ok {
		return nil, fmt.Errorf("unsupported stored backend %q", l.Backend)
	}
	return a, nil
}
func (r *launchRouter) Execute(ctx context.Context, req ExecuteRequest) (*Launch, error) {
	a, err := r.adapter(ctx, req.LaunchID)
	if err != nil {
		return nil, err
	}
	return a.Execute(ctx, req)
}
func (r *launchRouter) Cancel(ctx context.Context, req CancelRequest) (*Launch, error) {
	a, err := r.adapter(ctx, req.LaunchID)
	if err != nil {
		return nil, err
	}
	return a.Cancel(ctx, req)
}
func (r *launchRouter) Read(ctx context.Context, req ReadRequest) (*Launch, error) {
	a, err := r.adapter(ctx, req.LaunchID)
	if err != nil {
		return nil, err
	}
	return a.Read(ctx, req)
}
func (r *launchRouter) Status(ctx context.Context, req StatusRequest) (*LaunchStatus, error) {
	a, err := r.adapter(ctx, req.LaunchID)
	if err != nil {
		return nil, err
	}
	return a.Status(ctx, req)
}
func (r *launchRouter) List(ctx context.Context, req ListRequest) ([]Launch, error) {
	return r.store.List(ctx, req)
}
