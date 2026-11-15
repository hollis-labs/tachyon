package server

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/actionrun"
	"github.com/hollis-labs/tachyon/internal/forgeassign"
)

type resultRoute struct{ to, body, key string }

func (r *resultRoute) Lookup(_ context.Context, id string) (forgeassign.Agent, error) {
	return forgeassign.Agent{ID: id, URN: "msg://agent/test/" + id}, nil
}
func (r *resultRoute) Send(_ context.Context, to, subject, body string, meta map[string]string) (string, error) {
	r.to = to
	r.body = body
	r.key = meta["tether.message_id"]
	return "receipt", nil
}

func TestActionResultRoutesToRequesterWithStableReceiverDedupKey(t *testing.T) {
	route := &resultRoute{}
	run := actionrun.Run{ID: "run_1", ItemID: "item_1", Action: "tether.catalog.approve", Machine: "hub", Params: map[string]string{"plan_id": "cp_1"}, State: actionrun.StateFailed, Requester: actionrun.Requester{Agent: "chief", Task: "CW-20261115-0125"}, Output: "sensitive output", Error: "sensitive error", Reason: "sensitive reason"}
	for range 2 {
		if err := sendActionResult(context.Background(), route, route, run); err != nil {
			t.Fatal(err)
		}
		if route.to != "msg://agent/test/chief" || route.key != "tachyon-action-result:run_1" {
			t.Fatalf("wrong routing: %+v", route)
		}
		for _, secret := range []string{"sensitive output", "sensitive error", "sensitive reason"} {
			if strings.Contains(route.body, secret) {
				t.Fatal("sensitive content routed")
			}
		}
		if !strings.Contains(route.body, "No command exit code") || !strings.Contains(route.body, "grants no authorization") {
			t.Fatalf("dishonest result: %s", route.body)
		}
	}
}
