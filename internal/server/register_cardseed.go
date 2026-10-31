package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertstore"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/dataverbs"
)

// CardSeedPath creates labelled TEST approval cards so the Approvals UI can be proven on a live queue.
const CardSeedPath = "/api/plugins/console/v1/approval-inbox-seed"

// mountCardSeed mounts POST CardSeedPath {"shape":"a|b|c|d|e|f"} for same-origin browser requests only (no
// operator elevation: it only creates labelled TEST cards that target test:noop, so deciding them changes
// nothing). It is a test aid, not a product route.
func mountCardSeed(mux *http.ServeMux, _ *approvals.Bridge, c *dataverbs.Cards) {
	mux.Handle("POST "+CardSeedPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || !alertstore.SameOrigin(r) {
			http.Error(w, `{"code":"forbidden","message":"Same-origin browser requests only."}`, 403)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
		var in struct {
			Shape string `json:"shape"`
		}
		w.Header().Set("Content-Type", "application/json")
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"code":"invalid","message":"Send {\"shape\":\"a|b|c|d|e|f\"}."}`))
			return
		}
		ids, err := c.Seed(r.Context(), in.Shape, strconv.FormatInt(time.Now().UnixNano(), 36))
		if err != nil {
			w.WriteHeader(400)
			b, _ := json.Marshal(map[string]string{"code": "invalid", "message": err.Error()})
			_, _ = w.Write(b)
			return
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"shape": in.Shape, "item_ids": ids})
	}))
}
