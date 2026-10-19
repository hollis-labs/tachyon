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
		{Name: "teams.preview", Mutates: true, Description: "Retain a reviewed formation request from a saved team; starts no processes.", InputSchema: inputSchema(reflect.TypeFor[provideractions.PrepareRequest](), []string{"id", "object_id", "expected_revision", "mission"})},
		{Name: "teams.apply", Mutates: true, Description: "Submit the retained request; explicit recovery reuses its provider idempotency key.", InputSchema: inputSchema(reflect.TypeFor[provideractions.ApplyRequest](), []string{"id", "digest"})},
		{Name: "actions.get", Description: "Read a retained provider action and receipt.", InputSchema: inputSchema(reflect.TypeFor[IDRequest](), []string{"id"})},
		{Name: "actions.list", Description: "Read 25 retained actions for a saved team.", InputSchema: inputSchema(reflect.TypeFor[provideractions.ListRequest](), []string{"object_id"})},
	}
}
func (p *Actions) Invoke(ctx context.Context, verb string, raw json.RawMessage, actor string) (json.RawMessage, error) {
	var result any
	var err error
	switch verb {
	case "teams.preview":
		var in provideractions.PrepareRequest
		if err = decode(raw, &in); err == nil {
			result, err = p.Service.Prepare(ctx, in, actor)
		}
	case "teams.apply":
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
