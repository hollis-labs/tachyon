package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

func TestEmptyNaniteConfigStillBuildsAnAbsoluteProviderRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TETHER_ADDR", "")
	t.Setenv("TACHYON_LAUNCH_DEFAULT_PROVIDER", "")
	for _, configured := range []string{"", " \t\n"} {
		p := &plugin{}
		if _, err := p.Init(context.Background(), subprocess.InitParams{DataDir: t.TempDir(), Config: map[string]string{"nanite_url": configured}}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := p.Unload(context.Background()); err != nil {
				t.Error(err)
			}
		})
		adapter := p.adapter.(*launchRouter).adapters["nanite"].(*NaniteLaunchAdapter)
		called := false
		// Intercept the request without asserting a particular fallback URL or
		// contacting it. Blank config must produce a usable absolute request.
		adapter.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			called = true
			if !r.URL.IsAbs() || r.URL.Host == "" || r.URL.Path != "/api/agents/fixture" {
				t.Errorf("blank configuration produced an unusable provider request")
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"agent":{"id":"fixture","name":"Fixture","enabled":true}}`))}, nil
		})}
		if _, err := p.adapter.Prepare(context.Background(), PrepareRequest{AgentID: "fixture", Backend: "nanite"}); err != nil {
			t.Fatal(err)
		}
		if !called {
			t.Fatal("provider request not made")
		}
	}
}
