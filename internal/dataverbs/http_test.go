package dataverbs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/objects"
)

func verbURL(name string) string { return Prefix + "/providers/tachyon/verbs/" + name }
func httpCall(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestHTTPDispatchMethodAndProviderBoundaries(t *testing.T) {
	local, _, _ := setup(t)
	authorized := 0
	guard := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { authorized++; next.ServeHTTP(w, r) })
	}
	h := NewHTTP([]Provider{local}, guard)
	w := httpCall(t, h, "GET", Prefix+"/providers", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"input_schema"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("discovery", w.Code, w.Body.String())
	}
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", Prefix + "/providers", `{}`, 405},
		{"GET", verbURL("objects.create"), "", 405},
		{"HEAD", verbURL("objects.create"), "", 405},
		{"DELETE", verbURL("objects.delete"), `{}`, 405},
		{"POST", verbURL("objects.list"), `{}`, 405},
		{"GET", Prefix + "/providers/other/verbs/objects.list", "", 404},
		{"GET", verbURL("objects.unknown"), "", 404},
		{"GET", verbURL("objects.list") + "?limit=1", "", 400},
		{"GET", verbURL("objects.list") + "?input={}&input={}", "", 400},
		{"GET", verbURL("objects.list"), `{}`, 400},
		{"GET", verbURL("objects.list") + "?input=" + url.QueryEscape(strings.Repeat(" ", 16385)), "", 413},
	} {
		w := httpCall(t, h, test.method, test.path, test.body)
		if w.Code != test.status {
			t.Fatalf("%s %s: %d %s", test.method, test.path, w.Code, w.Body.String())
		}
	}
	if authorized != 0 {
		t.Fatal("nonwrite request entered mutation path")
	}
	id := uuid.NewString()
	payload := `{"id":"` + id + `","kind":"project","definition":{"name":"Local"}}`
	w = httpCall(t, h, "POST", verbURL("objects.create"), payload)
	if w.Code != 200 || authorized != 1 {
		t.Fatal("create", w.Code, w.Body.String())
	}
	var envelope response
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var record objects.Record
	if err := json.Unmarshal(envelope.Data, &record); err != nil || record.ID != id || envelope.Provider != "tachyon" || envelope.Verb != "objects.create" {
		t.Fatal("response envelope", err)
	}
	w = httpCall(t, h, "GET", verbURL("objects.history")+"?input="+url.QueryEscape(`{"id":"`+id+`"}`), "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"actor":"operator:browser"`) || authorized != 1 {
		t.Fatal("actor/read path", w.Code, w.Body.String())
	}
	w = httpCall(t, h, "POST", verbURL("objects.create"), payload)
	if w.Code != 200 {
		t.Fatal("retry", w.Code, w.Body.String())
	}
	w = httpCall(t, h, "POST", verbURL("objects.create"), strings.Replace(payload, "Local", "Changed", 1))
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"conflict"`) {
		t.Fatal("conflict mapping", w.Code, w.Body.String())
	}
	w = httpCall(t, h, "POST", verbURL("objects.patch"), `{"id":"`+id+`","expected_revision":1,"actor":"forged"}`)
	if w.Code != 400 {
		t.Fatal("forged actor accepted")
	}
}
func TestHTTPFailClosedAndBodyLimits(t *testing.T) {
	local, s, _ := setup(t)
	id := uuid.NewString()
	payload := `{"id":"` + id + `","kind":"project","definition":{"name":"Denied"}}`
	denied := NewHTTP([]Provider{local}, nil)
	if w := httpCall(t, denied, "POST", verbURL("objects.create"), payload); w.Code != 403 {
		t.Fatal("nil authorization accepted", w.Code)
	}
	rows, err := s.List(ctx, objects.Filter{})
	if err != nil || len(rows) != 0 {
		t.Fatal("denied write persisted", err)
	}
	h := NewHTTP([]Provider{local}, func(next http.Handler) http.Handler { return next })
	for _, test := range []struct {
		body, contentType, query string
		status                   int
	}{
		{payload, "text/plain", "", 415},
		{payload, "application/jsonbroken", "", 415},
		{payload, "application/json", "?input={}", 400},
		{"", "application/json", "", 400},
		{"{", "application/json", "", 400},
		{strings.Repeat(" ", MaxInputBytes+1), "application/json", "", 413},
	} {
		req := httptest.NewRequest("POST", verbURL("objects.create")+test.query, strings.NewReader(test.body))
		req.Header.Set("Content-Type", test.contentType)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != test.status {
			t.Fatalf("body/mime: %d %s", w.Code, w.Body.String())
		}
	}
	rows, err = s.List(ctx, objects.Filter{})
	if err != nil || len(rows) != 0 {
		t.Fatal("invalid write persisted", err)
	}
}

type alternateProvider struct{ Provider }

func (alternateProvider) ID() string { return "alternate" }
func (alternateProvider) Invoke(_ context.Context, name string, _ json.RawMessage, _ string) (json.RawMessage, error) {
	if name != "objects.list" {
		return nil, errors.New("internal provider detail must not escape")
	}
	return json.RawMessage(`{"items":[],"origin":"alternate"}`), nil
}
func TestHTTPProviderSelectionIsExplicit(t *testing.T) {
	local, _, _ := setup(t)
	h := NewHTTP([]Provider{local, alternateProvider{local}}, nil)
	w := httpCall(t, h, "GET", Prefix+"/providers/alternate/verbs/objects.list", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"origin":"alternate"`) {
		t.Fatal("provider ignored", w.Body.String())
	}
	w = httpCall(t, h, "GET", verbURL("objects.list"), "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "alternate") {
		t.Fatal("provider data mixed", w.Body.String())
	}
	w = httpCall(t, h, "GET", Prefix+"/providers/alternate/verbs/objects.get?input=%7B%22id%22%3A%22x%22%7D", "")
	if w.Code != 500 || strings.Contains(w.Body.String(), "internal provider detail") {
		t.Fatal("raw provider error leaked", w.Body.String())
	}
}
