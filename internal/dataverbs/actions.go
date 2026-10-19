package dataverbs

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/hollis-labs/tachyon/internal/provideractions"
)

type Actions struct{ Service *provideractions.Service }

func (*Actions) ID() string { return "tether" }
func (*Actions) Verbs() []Verb {
	return []Verb{
		{Name: "executions.project", Mutates: true, Description: "Repair local run projection from a retained team receipt; never sends a provider request.", InputSchema: inputSchema(reflect.TypeFor[IDRequest](), []string{"id"})},
		{Name: "runtime.runs", Description: "List retained provider runs by saved team or attempted assignment; status is observed evidence, not live health.", InputSchema: inputSchema(reflect.TypeFor[provideractions.RunFilter](), nil)},
		{Name: "runtime.activity", Description: "Page a local run's prepared, pending and confirmed action summaries; payloads are fetched separately.", InputSchema: inputSchema(reflect.TypeFor[provideractions.ActivityFilter](), []string{"run_id"})},
		{Name: "agents.publish_preview", Mutates: true, Description: "Review a saved agent publication into a separate Tether catalog profile.", InputSchema: inputSchema(reflect.TypeFor[provideractions.PublishRequest](), []string{"id", "object_id", "expected_revision", "provider", "extends"})},
		{Name: "catalog.apply_preview", Mutates: true, Description: "Retain installation of an exact provider catalog plan; approval stays with Tether.", InputSchema: inputSchema(reflect.TypeFor[provideractions.CatalogApplyRequest](), []string{"id", "plan_action_id"})},
		{Name: "actions.reconcile", Mutates: true, Description: "Record catalog installation confirmation using provider readback only.", InputSchema: inputSchema(reflect.TypeFor[IDRequest](), []string{"id"})},
		{Name: "runtime.preview", Mutates: true, Description: "Review assignment dispatch or a team runtime control against a confirmed formation.", InputSchema: inputSchema(reflect.TypeFor[provideractions.RuntimeRequest](), []string{"id", "object_id", "expected_revision", "formation_id", "operation"})},
		{Name: "actions.apply", Mutates: true, Description: "Apply a retained provider action; only team operations support same-key recovery.", InputSchema: inputSchema(reflect.TypeFor[provideractions.ApplyRequest](), []string{"id", "digest"})},
		{Name: "teams.roster", Description: "Read saved roster immediately, or explicitly refresh it from Tether.", InputSchema: inputSchema(reflect.TypeFor[provideractions.RosterRequest](), []string{"formation_id"})},
		{Name: "teams.preview", Mutates: true, Description: "Retain a reviewed formation request from a saved team; starts no processes.", InputSchema: inputSchema(reflect.TypeFor[provideractions.PrepareRequest](), []string{"id", "object_id", "expected_revision", "mission"})},
		{Name: "teams.apply", Mutates: true, Description: "Submit the retained request; explicit recovery reuses its provider idempotency key.", InputSchema: inputSchema(reflect.TypeFor[provideractions.ApplyRequest](), []string{"id", "digest"})},
		{Name: "actions.get", Description: "Read a retained provider action and receipt.", InputSchema: inputSchema(reflect.TypeFor[IDRequest](), []string{"id"})},
		{Name: "actions.list", Description: "Read 25 retained actions for a saved object, newest first.", InputSchema: inputSchema(reflect.TypeFor[provideractions.ListRequest](), []string{"object_id"})},
	}
}
func (p *Actions) Invoke(ctx context.Context, verb string, raw json.RawMessage, actor string) (json.RawMessage, error) {
	var result any
	var err error
	switch verb {
	case "executions.project":
		var in IDRequest
		if err = decode(raw, &in); err == nil {
			result, err = p.Service.Project(ctx, in.ID, actor)
		}
	case "runtime.runs":
		var in provideractions.RunFilter
		if err = decode(raw, &in); err == nil {
			result, err = p.Service.Runs(ctx, in)
		}
	case "runtime.activity":
		var in provideractions.ActivityFilter
		if err = decode(raw, &in); err == nil {
			result, err = p.Service.Activity(ctx, in)
		}
	case "agents.publish_preview":
		var in provideractions.PublishRequest
		if err = decode(raw, &in); err == nil {
			result, err = p.Service.PreparePublication(ctx, in, actor)
		}
	case "catalog.apply_preview":
		var in provideractions.CatalogApplyRequest
		if err = decode(raw, &in); err == nil {
			result, err = p.Service.PrepareCatalogApply(ctx, in, actor)
		}
	case "actions.reconcile":
		var in IDRequest
		if err = decode(raw, &in); err == nil {
			result, err = p.Service.Reconcile(ctx, in.ID, actor)
		}
	case "runtime.preview":
		var in provideractions.RuntimeRequest
		if err = decode(raw, &in); err == nil {
			result, err = p.Service.PrepareRuntime(ctx, in, actor)
		}
	case "teams.roster":
		var in provideractions.RosterRequest
		if err = decode(raw, &in); err == nil {
			result, err = p.Service.Roster(ctx, in)
		}
	case "teams.preview":
		var in provideractions.PrepareRequest
		if err = decode(raw, &in); err == nil {
			result, err = p.Service.Prepare(ctx, in, actor)
		}
	case "teams.apply", "actions.apply":
		var in provideractions.ApplyRequest
		if err = decode(raw, &in); err == nil {
			result, err = p.Service.Apply(ctx, in, actor)
		}
	case "actions.get":
		var in IDRequest
		if err = decode(raw, &in); err == nil {
			result, err = p.Service.Get(ctx, in.ID)
		}
	case "actions.list":
		var in provideractions.ListRequest
		if err = decode(raw, &in); err == nil {
			result, err = p.Service.List(ctx, in)
		}
	default:
		return nil, invalid("Unknown provider action verb.")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
