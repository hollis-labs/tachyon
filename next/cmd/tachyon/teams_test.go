package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tachyon/next/internal/dataverbs"
)

// teamPreview is a saved team's builder preview, as the Builder retains it (the provideractions fixture).
const teamPreview = `{"id":"delivery","name":"Delivery","version":1,"slots":[{"name":"coordinator","role":"owner","definition_ref":{"id":"owner","revision":"v1","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"resolution":"fresh","activation":"singleton","required":false,"min":0,"max":1},{"name":"worker","role":"work","definition_ref":{"id":"worker","revision":"v1","digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"resolution":"fresh","activation":"singleton","required":true,"min":1,"max":1}],"phases":[{"id":"work","kind":"flex","active_slots":["coordinator","worker"],"owner_slot":"coordinator","exit_trigger":{"kind":"manual","spec":{"signal":"done"}}}],"authority":{"mode":"strict","grants":[{"from_slot":"coordinator","verb":"may_assign","to_slot":"worker"}]},"routing":{"coordinator_slot":"coordinator","rules":[]},"policy":{"history":"none","spawn":{"max_depth":1,"max_children":4}}}`

// Team operations in the served binary (slice B9b-2): the console object writes and the Tether provider's
// verbs. A key is refused whatever its scopes, anonymous is 401, and the owner saves a team with
// objects.create. With builder_changes off a team preview answers unavailable and nothing reaches Tether;
// on, the preview is retained and applying it reaches Tether.
func TestTeamOperations(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(fmt.Sprintf("builder_changes=%v", on), func(t *testing.T) {
			fake, url := newFakeCatalogTether(t)
			tok := filepath.Join(t.TempDir(), "tether.token")
			if err := os.WriteFile(tok, []byte("test-read-credential"), 0o600); err != nil {
				t.Fatal(err)
			}
			base, app := started(t, loopbackOverride+fmt.Sprintf("tachyon:\n  upstreams:\n    tether: { url: %q, token: %q }\n  features: { builder_changes: %v }\n", url, "file://"+tok, on))
			k, _, err := identity.From(app).CreateKey(context.Background(), identity.KeyInput{Name: "teams-test", Scopes: []string{"*"}})
			if err != nil {
				t.Fatal(err)
			}
			bearer := map[string]string{"Authorization": "Bearer " + k}
			anon, keys, owner := newClient(t, base), newClient(t, base), newClient(t, base)
			owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
			verb := func(provider, name string) string { return dataverbs.Prefix + "/providers/" + provider + "/verbs/" + name }

			var preview any
			if err := json.Unmarshal([]byte(teamPreview), &preview); err != nil {
				t.Fatal(err)
			}
			team := "4f0c9a8e-1d2b-4c3a-9e8f-7a6b5c4d3e2f"
			create := map[string]any{"id": team, "kind": "team", "definition": map[string]any{
				"name": "Delivery", "properties": map[string]any{"builder_team": map[string]any{"preview": preview}}, "coordinator": "coordinator",
				"slots": []map[string]any{{"name": "coordinator", "role": "owner", "min": 0, "max": 1}, {"name": "worker", "role": "work", "min": 1, "max": 1}},
			}}
			prepare := map[string]any{"id": "9b8a7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d", "object_id": team, "expected_revision": 1, "mission": "Review this implementation"}
			for _, w := range []struct {
				provider, name string
				body           any
			}{{"tachyon", "objects.create", create}, {"tether", "teams.preview", prepare}} {
				anon.want(401, "POST", verb(w.provider, w.name), w.body, xrw)
				keys.want(403, "POST", verb(w.provider, w.name), w.body, bearer)
			}
			keys.want(403, "GET", verb("tether", "runtime.runs")+"?input=%7B%7D", nil, bearer)
			owner.want(200, "GET", verb("tether", "runtime.runs")+"?input=%7B%7D", nil, xrw)
			owner.want(200, "POST", verb("tachyon", "objects.create"), create, xrw)
			if n := fake.count(); n != 0 {
				t.Fatalf("%d requests to Tether before any team operation", n)
			}
			code, body := owner.do("POST", verb("tether", "teams.preview"), prepare, xrw)
			if !on {
				if code != 503 || fake.count() != 0 {
					t.Errorf("a team preview with builder_changes off: %d %s, %d requests to Tether", code, body, fake.count())
				}
				return
			}
			// On, the preview is retained without a request (as before); applying it is what reaches Tether.
			var receipt struct{ Data struct{ ID, Digest string } }
			if err := json.Unmarshal(body, &receipt); code != 200 || err != nil || receipt.Data.Digest == "" || fake.count() != 0 {
				t.Fatalf("a team preview with builder_changes on: %d %s, %d requests to Tether", code, body, fake.count())
			}
			owner.do("POST", verb("tether", "teams.apply"), map[string]any{"id": receipt.Data.ID, "digest": receipt.Data.Digest}, xrw)
			if fake.count() == 0 {
				t.Error("applying a team preview with builder_changes on did not reach Tether")
			}
		})
	}
}
