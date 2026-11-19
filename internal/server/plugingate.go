package server

import "net/http"

// The plugin subprocess admits 16 concurrent HTTP calls and answers the rest with a bare 502
// ("plugin error"). Every browser page fires bursts above that (the Agents observation store
// sends two reads per running session), so calls queue here instead, leaving headroom for
// lifecycle traffic. A caller that goes away while queued is dropped without touching the plugin.
const pluginCallSlots = 12

func (s *Server) pluginRoutes() http.Handler {
	return gatePluginCalls(s.inventory.APIRoutes(), s.pluginSlots)
}

func gatePluginCalls(next http.Handler, slots chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-r.Context().Done():
			http.Error(w, "request cancelled", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}
