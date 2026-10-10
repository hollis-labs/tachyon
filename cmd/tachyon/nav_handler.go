package main

import (
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/plugins"
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

type registryProvider interface {
	BuildRegistry() plugins.RegistryResponse
}

func newRegistryHandler(provider registryProvider) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		payload, err := json.Marshal(provider.BuildRegistry())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	})
}
