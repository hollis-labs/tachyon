package forgeapi

import "testing"

func TestPRKeysRoundTripAndRefuseUnsafeParts(t *testing.T) {
	k := PRKey("acme", "widgets", 12)
	if o, n, num, ok := ParsePRKey(k); !ok || o != "acme" || n != "widgets" || num != 12 {
		t.Fatalf("%s: %s %s %d %v", k, o, n, num, ok)
	}
	for _, bad := range []string{"github:acme/widgets#0", "github:../x#1", "gitlab:a/b#1", "github:a/b#1x", "github:a/b/c#1"} {
		if _, _, _, ok := ParsePRKey(bad); ok {
			t.Errorf("%s parsed", bad)
		}
	}
	for s, want := range map[string]bool{"acme": true, "a.b-c_d": true, "..": false, "": false, "a/b": false} {
		if ValidRepoPart(s) != want {
			t.Errorf("ValidRepoPart(%q) != %v", s, want)
		}
	}
	if !ValidHeadSHA("0da8631e75cfa86e2506e6b076cceaa4a56f4aa3") || ValidHeadSHA("0DA8") {
		t.Error("head sha")
	}
}
