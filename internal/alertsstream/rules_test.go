package alertsstream

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/alertsrules"
)

func TestRulesSummary(t *testing.T) {
	pack, _ := alertsrules.ParsePack(alertsapi.DefaultRulePack())
	w := httptest.NewRecorder()
	Rules(func() alertsrules.Pack { return pack }).ServeHTTP(w, httptest.NewRequest("GET", "/alerts/rules", nil))
	var got struct {
		PackID string `json:"pack_id"`
		Rules  []struct {
			ID       string   `json:"id"`
			Kinds    []string `json:"kinds"`
			Action   string   `json:"action"`
			Severity *string  `json:"severity"`
			NeedsYou bool     `json:"needs_you"`
		} `json:"rules"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.PackID != "console.default" || len(got.Rules) != len(pack.Rules) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	by := map[string]int{}
	for i, r := range got.Rules {
		by[r.ID] = i
		if r.Kinds == nil {
			t.Errorf("%s: kinds must be an array", r.ID)
		}
	}
	if r := got.Rules[by["drop-metrics"]]; r.Action != "drop" || r.Severity != nil || r.NeedsYou {
		t.Errorf("drop: %+v", r)
	}
	if r := got.Rules[by["approval-requested"]]; r.Action != "alert" || r.Severity == nil || *r.Severity != "warning" || !r.NeedsYou {
		t.Errorf("alert: %+v", r)
	}
	if r := got.Rules[by["catch-all"]]; r.Severity == nil || *r.Severity != "inherit" || r.NeedsYou {
		t.Errorf("catch-all: %+v", r)
	}
}
