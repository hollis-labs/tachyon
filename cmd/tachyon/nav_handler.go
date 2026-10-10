package main

import (
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/tachyon/internal/contract"
)

type navProvider interface {
	MergedNav() contract.NavDeclaration
}

func newNavHandler(provider navProvider) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(provider.MergedNav())
	})
}
