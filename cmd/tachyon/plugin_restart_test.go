package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/tachyon/internal/plugins"
)

type fakeRestarter struct {
	called string
	err    error
}

func (f *fakeRestarter) RestartPlugin(id string) error { f.called = id; return f.err }

func TestPluginRestartEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		httpStatus int
		loadStatus string
	}{
		{"loaded", nil, http.StatusOK, "loaded"},
		{"unknown", plugins.ErrPluginNotFound, http.StatusNotFound, "unloaded"},
		{"failed", errors.New("respawn failed"), http.StatusInternalServerError, "unloaded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &fakeRestarter{err: tc.err}
			mux := http.NewServeMux()
			mux.Handle("POST /api/plugins/{id}/restart", newPluginRestartHandler(manager))
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/plugins/example/restart", nil))
			var body struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  string `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != tc.httpStatus || body.Status != tc.loadStatus || body.ID != "example" || manager.called != "example" {
				t.Fatalf("response: %d %+v", recorder.Code, body)
			}
			if (body.Error != "") != (tc.err != nil) {
				t.Fatalf("error status: %+v", body)
			}
		})
	}
}
