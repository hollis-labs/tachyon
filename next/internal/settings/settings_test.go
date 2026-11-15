package settings

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// ref is a secrets reference to a token file: a name, never a credential.
func ref(name string) string { return "file:///run/" + name + ".token" }

func TestMissingBlockGivesDefaults(t *testing.T) {
	s, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, Settings{}) {
		t.Fatalf("defaults = %+v, want the zero value", s)
	}
}

// Every outbound switch is off unless the operator turns it on: with no block, with an empty
// features block, and with every other key set.
func TestEveryFeatureDefaultsOff(t *testing.T) {
	blocks := map[string]any{
		"no block":       nil,
		"empty block":    map[string]any{},
		"empty features": map[string]any{"features": map[string]any{}},
		"everything but features": map[string]any{
			"allowed_hosts": []any{"tachyon.example.test"},
			"upstreams": map[string]any{
				"tether": map[string]any{"url": "https://tether.example.test", "token": ref("tether")},
				"torque": map[string]any{"url": "https://torque.example.test", "token": ref("torque")},
			},
		},
	}
	for name, raw := range blocks {
		s, err := Parse(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, k := range FeatureKeys() {
			on, ok := s.Features.Enabled(k)
			if !ok || on {
				t.Errorf("%s: features.%s = %v (known %v), want false", name, k, on, ok)
			}
		}
	}
}

// The key list and the struct cannot drift: every Features field has a key, and setting each key
// turns on exactly its own field.
func TestFeatureKeysCoverEveryField(t *testing.T) {
	keys := FeatureKeys()
	if n := reflect.TypeOf(Features{}).NumField(); n != len(keys) {
		t.Fatalf("Features has %d fields, FeatureKeys lists %d", n, len(keys))
	}
	for _, k := range keys {
		s, err := Parse(map[string]any{"features": map[string]any{k: true}})
		if err != nil {
			t.Fatal(err)
		}
		v := reflect.ValueOf(s.Features)
		on := 0
		for i := range v.NumField() {
			if v.Field(i).Bool() {
				on++
			}
		}
		if got, _ := s.Features.Enabled(k); !got || on != 1 {
			t.Errorf("features.%s: enabled=%v, %d fields on; want exactly its own", k, got, on)
		}
	}
}

func TestUnknownKeysAreNamed(t *testing.T) {
	cases := map[string]struct {
		raw  any
		want string
	}{
		"root":     {map[string]any{"allowed_host": []any{}}, "tachyon.allowed_host"},
		"feature":  {map[string]any{"features": map[string]any{"object_synk": true}}, "tachyon.features.object_synk"},
		"upstream": {map[string]any{"upstreams": map[string]any{"torq": map[string]any{}}}, "tachyon.upstreams.torq"},
		"nested":   {map[string]any{"upstreams": map[string]any{"torque": map[string]any{"addr": "x"}}}, "tachyon.upstreams.torque.addr"},
		"no url":   {map[string]any{"upstreams": map[string]any{"tether_messages": map[string]any{"url": "https://x.test"}}}, "tachyon.upstreams.tether_messages.url"},
		"files":    {map[string]any{"files": map[string]any{"secret_key_file": "/k"}}, "tachyon.files.secret_key_file"},
	}
	for name, c := range cases {
		_, err := Parse(c.raw)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one naming %s", name, err, c.want)
		}
	}
}

func TestBadValuesAreRefusedByName(t *testing.T) {
	ups := func(k string, v map[string]any) map[string]any {
		return map[string]any{"upstreams": map[string]any{k: v}}
	}
	cases := map[string]struct {
		raw  any
		want string
	}{
		"block not a map":   {"x", "tachyon must be a map"},
		"feature not bool":  {map[string]any{"features": map[string]any{"chat_sync": "yes"}}, "tachyon.features.chat_sync"},
		"inline token":      {ups("torque", map[string]any{"token": "abc123"}), "tachyon.upstreams.torque.token must be a secrets reference"},
		"url scheme":        {ups("torque", map[string]any{"url": "ftp://torque"}), "tachyon.upstreams.torque.url"},
		"url credentials":   {ups("torque", map[string]any{"url": "https://u:p@torque"}), "tachyon.upstreams.torque.url"}, //nolint:gosec // a refused URL, not a credential
		"unix not allowed":  {ups("torque", map[string]any{"url": "unix:/run/t.sock"}), "tachyon.upstreams.torque.url"},
		"unix relative":     {ups("cerberus", map[string]any{"url": "unix:run/c.sock"}), "tachyon.upstreams.cerberus.url"},
		"hub addr":          {ups("action_hub", map[string]any{"addr": "hub"}), "tachyon.upstreams.action_hub.addr"},
		"hub key value":     {ups("action_hub", map[string]any{"key": "-----BEGIN"}), "tachyon.upstreams.action_hub.key"},
		"relative file":     {map[string]any{"files": map[string]any{"cache_policy": "cache.json"}}, "tachyon.files.cache_policy"},
		"secret key value":  {map[string]any{"files": map[string]any{"secret_key": "00ff"}}, "tachyon.files.secret_key"},
		"host with path":    {map[string]any{"allowed_hosts": []any{"a/b"}}, "tachyon.allowed_hosts"},
		"hosts not list":    {map[string]any{"allowed_hosts": "a"}, "tachyon.allowed_hosts"},
		"public url":        {map[string]any{"alerts": map[string]any{"public_url": "tachyon"}}, "tachyon.alerts.public_url"},
		"cache dir not str": {map[string]any{"cache_dir": 3.0}, "tachyon.cache_dir"},
		"sync interval":     {map[string]any{"chat_sync_interval": "soon"}, "tachyon.chat_sync_interval"},
		"sync too fast":     {map[string]any{"chat_sync_interval": "100ms"}, "tachyon.chat_sync_interval"},
		"session not map":   {ups("app_agent", map[string]any{"session": "x"}), "tachyon.upstreams.app_agent.session"},
	}
	for name, c := range cases {
		_, err := Parse(c.raw)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one naming %s", name, err, c.want)
		}
	}
}

// Every secrets backend's reference form is accepted where a credential is expected.
func TestSecretReferenceSchemes(t *testing.T) {
	for _, r := range []string{"file:///run/x", "keychain://tachyon/github", "op://vault/item/field", "helper://name/key", "env://VAR"} {
		raw := map[string]any{"files": map[string]any{"secret_key": r}}
		if _, err := Parse(raw); err != nil {
			t.Errorf("%s refused: %v", r, err)
		}
	}
}

func TestAFullBlockParses(t *testing.T) {
	raw := map[string]any{
		"allowed_hosts":      []any{"Tachyon.Example.Test"},
		"cache_dir":          "cache",
		"chat_sync_interval": "30s",
		"upstreams": map[string]any{
			"tether":          map[string]any{"url": "https://tether.example.test", "token": ref("tether")},
			"tether_control":  map[string]any{"url": "unix:/run/tetherd.sock", "token": ref("control")},
			"tether_messages": map[string]any{"token": ref("messages")},
			"cerberus":        map[string]any{"url": "unix:/run/cerberus.sock"},
			"activity":        map[string]any{"url": "http://127.0.0.1:8099"},
			"app_agent":       map[string]any{"url": "http://127.0.0.1:8090", "token": ref("nanite"), "label": "Nanite", "session": map[string]any{"host_settings": map[string]any{"id": "h"}}},
			"github":          map[string]any{"url": "https://api.github.com", "org": "example", "token": ref("github")},
			"action_hub":      map[string]any{"addr": "127.0.0.1:2222", "user": "actions", "key": "file:///run/hub.key", "known_hosts": "/run/known_hosts"},
		},
		"features": map[string]any{"object_sync": true},
		"files":    map[string]any{"secret_key": "file:///run/secret.key", "terminal_policy": "/etc/tachyon/terminal.yaml"}, //nolint:gosec // a secrets reference, not a credential
		"alerts":   map[string]any{"notify_to": "msg://agent/x", "public_url": "https://tachyon.example.test"},
	}
	s, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.AllowedHosts[0] != "tachyon.example.test" || s.CacheDir != "cache" {
		t.Errorf("hosts/cache = %v %q", s.AllowedHosts, s.CacheDir)
	}
	u := s.Upstreams
	if u.TetherControl.URL != "unix:/run/tetherd.sock" || u.TetherMessages.Token != ref("messages") ||
		u.AppAgent.Label != "Nanite" || u.GitHub.Org != "example" || u.ActionHub.User != "actions" {
		t.Errorf("upstreams = %+v", u)
	}
	if s.ChatSyncInterval != 30*time.Second || string(u.AppAgent.Session) != `{"host_settings":{"id":"h"}}` {
		t.Errorf("chat sync interval / app agent session = %s %s", s.ChatSyncInterval, u.AppAgent.Session)
	}
	if def, _ := Parse(map[string]any{}); def.ChatSyncInterval != 5*time.Second || def.Upstreams.AppAgent.Session != nil {
		t.Errorf("defaults = %s %s", def.ChatSyncInterval, def.Upstreams.AppAgent.Session)
	}
	if !s.Features.ObjectSync || s.Files.SecretKey != "file:///run/secret.key" || s.Alerts.NotifyTo != "msg://agent/x" {
		t.Errorf("features/files/alerts = %+v %+v %+v", s.Features, s.Files, s.Alerts)
	}
}
