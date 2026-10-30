package chatapi

import "testing"

func TestParseRef(t *testing.T) {
	for _, ok := range []string{"app:12", "tether:3f2a-91"} {
		r, err := ParseRef(ok)
		if err != nil || r.String() != ok {
			t.Fatalf("%s: %v %v", ok, r, err)
		}
	}
	for _, bad := range []string{"", "app", "app:", ":1", "x:1", "app:a/b", "tether:a b"} {
		if _, err := ParseRef(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
