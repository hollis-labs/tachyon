// Package catalog is Tachyon's reviewed list of first-party plugins.
//
// v2 deliberately keeps the catalog in Go: each entry is a reviewed,
// in-repository plugin whose backend runs as a plugin-sdk subprocess and whose
// UI is compiled into the host frontend. Runtime discovery of manifest-declared
// plugins with separately delivered UI bundles is tracked as follow-up work.
package catalog

import (
	"net/url"
	"strings"
)

// Upstreams are the provider endpoints the host resolves from its own
// environment. Plugins never inherit the host environment; they receive only
// the variables their entry declares.
type Upstreams struct {
	TangentURL      string
	TetherAddr      string
	TetherTokenFile string
	TorqueURL       string
	TorqueToken     string
	GitHubToken     string
	GitHubOrg       string
}

// Stream is a host-owned server-sent-events relay. app-host's plugin route
// adapter buffers responses, so live streams are proxied by the host directly
// to the declared upstream and never pass through the plugin process.
type Stream struct {
	// Upstream returns the absolute upstream URL for this stream.
	Upstream func(Upstreams) string
	// Bearer returns an optional bearer token for the upstream.
	Bearer func(Upstreams) string
}

// Entry describes one first-party plugin.
type Entry struct {
	ID          string
	Name        string
	Version     string
	Description string
	// Env returns the plugin's exact environment.
	Env     func(Upstreams) []string
	Streams map[string]Stream
}

func torqueStream(path string) Stream {
	return Stream{
		Upstream: func(u Upstreams) string { return join(u.TorqueURL, path) },
		Bearer:   func(u Upstreams) string { return u.TorqueToken },
	}
}

func torqueEnv(u Upstreams) []string {
	env := []string{"TORQUE_URL=" + u.TorqueURL}
	if u.TorqueToken != "" {
		env = append(env, "TORQUE_API_TOKEN="+u.TorqueToken)
	}
	return env
}

func consoleEnv(u Upstreams) []string {
	env := append(torqueEnv(u), "TETHER_ADDR="+u.TetherAddr, "TETHER_TOKEN_FILE="+u.TetherTokenFile)
	if u.GitHubToken != "" {
		env = append(env, "GITHUB_TOKEN="+u.GitHubToken)
	}
	if u.GitHubOrg != "" {
		env = append(env, "GITHUB_ORG="+u.GitHubOrg)
	}
	return env
}

// Entries returns the reviewed first-party plugins in navigation order.
func Entries() []Entry {
	return []Entry{
		{
			ID:          "planner",
			Name:        "Planner",
			Version:     "0.1.0",
			Description: "Portfolio planner: board, priorities, workstreams, roadmap, ideas, decisions, risks and inbox.",
			Env:         torqueEnv,
			Streams: map[string]Stream{
				"events": torqueStream("/api/v1/events"),
			},
		},
		{
			ID:          "console",
			Name:        "Console",
			Version:     "0.1.0",
			Description: "Operator console: agent fleet, agent builder, alerts, messaging, forge, HITL, workflow and metrics.",
			Env:         consoleEnv,
			Streams: map[string]Stream{
				"events":   torqueStream("/api/v1/events"),
				"messages": torqueStream("/api/v1/messages/subscribe"),
				"activity": torqueStream("/api/v1/activity/stream"),
			},
		},
	}
}

func join(base, path string) string {
	u, err := url.Parse(strings.TrimRight(base, "/") + path)
	if err != nil {
		return ""
	}
	return u.String()
}
