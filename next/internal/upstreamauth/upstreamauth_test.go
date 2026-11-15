package upstreamauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func static(v string) Token { return func(context.Context) (string, error) { return v, nil } }

func TestWrapSendsBearerAndFollowsRotation(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = append(got, r.Header.Get("Authorization")) }))
	defer srv.Close()
	current := "first-value"
	rt, err := Wrap(nil, srv.URL, func(context.Context) (string, error) { return current, nil })
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Transport: rt}
	for _, v := range []string{"first-value", "second-value"} {
		current = v
		res, err := c.Get(srv.URL + "/x")
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
	}
	if len(got) != 2 || got[0] != "Bearer first-value" || got[1] != "Bearer second-value" {
		t.Fatalf("headers %q", got)
	}
}

func TestWrapFailsClosedWithoutLeakingToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request sent") }))
	defer srv.Close()
	for name, tok := range map[string]Token{
		"unresolvable":  func(context.Context) (string, error) { return "", errors.New("reference not found") },
		"not printable": static("has space"),
		"empty":         static(""),
	} {
		rt, err := Wrap(nil, srv.URL, tok)
		if err != nil {
			t.Fatal(err)
		}
		_, err = (&http.Client{Transport: rt}).Get(srv.URL)
		if err == nil || !errors.Is(err, ErrCredential) || strings.Contains(err.Error(), "has space") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestWrapPinsOrigin(t *testing.T) {
	rt, err := Wrap(nil, "https://upstream.example.test", static("v"))
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://elsewhere.example.test/", nil)
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("the token left its origin")
	}
}

func TestGuard(t *testing.T) {
	for addr, ok := range map[string]bool{
		"https://upstream.example.test": true, "http://127.0.0.1:8080": true, "http://localhost:1": true,
		"unix:/run/x.sock": true, "http://upstream.example.test": false, "ftp://x": false, "https://u:p@x": false,
	} {
		if err := Guard(addr, true); (err == nil) != ok {
			t.Errorf("Guard(%s) = %v", addr, err)
		}
	}
	if err := Guard("http://upstream.example.test", false); err != nil {
		t.Errorf("no credential: %v", err)
	}
}

func TestWrapWithoutTokenIsPassthrough(t *testing.T) {
	base := http.DefaultTransport
	if rt, err := Wrap(base, "http://upstream.example.test", nil); err != nil || rt != base {
		t.Fatalf("%v %v", rt, err)
	}
	if FromRef(nil, "") != nil {
		t.Fatal("an empty reference must be no credential")
	}
}

func TestWithHeaderSetsMarkerUnderWrap(t *testing.T) {
	var marker, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		marker, auth = r.Header.Get("Tether-Protocol"), r.Header.Get("Authorization")
	}))
	defer srv.Close()
	rt, err := Wrap(WithHeader(nil, "Tether-Protocol", "1"), srv.URL, static("v"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := (&http.Client{Transport: rt}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if marker != "1" || auth != "Bearer v" {
		t.Fatalf("marker %q auth %q", marker, auth)
	}
}
