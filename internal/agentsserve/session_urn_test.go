package agentsserve

import "testing"

func TestRoutedURN(t *testing.T) {
	if got := routedURN(row{urn: "msg://agent/x/y", logical: "z"}); got != "msg://agent/x/y" {
		t.Fatal(got)
	}
	if got := routedURN(row{logical: "tachyon-backend-builder-2"}); got != "msg://agent/agent-mux/tachyon-backend-builder-2" {
		t.Fatal(got)
	}
	if got := routedURN(row{logical: "a b"}); got != "" {
		t.Fatal(got)
	}
	if got := routedURN(row{}); got != "" {
		t.Fatal(got)
	}
}
