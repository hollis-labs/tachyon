package builderread

import (
	"context"

	"github.com/hollis-labs/tachyon/internal/builderapi"
)

// ToolHealth is what a gateway reports about one tool server. Every field is optional: absent means unknown.
type ToolHealth struct {
	Transport, Status, Health string
	Tags                      []string
	ToolCount, Restarts       *int
}

// ToolSource reports tool server health by id. The Tether route behind it is UNVERIFIED (the design names
// "gateway status"; the gateway status seen from a Tachyon agent describes MCP origins, not catalog mcp-servers),
// so no HTTP implementation is wired. With no source, or on error, tool servers read status "unknown", never a
// guessed "ok".
type ToolSource interface {
	Tools(ctx context.Context) (map[string]ToolHealth, error)
}

func (r *Reader) toolHealth(ctx context.Context, kind string) map[string]ToolHealth {
	if kind != builderapi.LibToolServers || r.Tools == nil {
		return nil
	}
	m, err := r.Tools.Tools(ctx)
	if err != nil {
		return nil
	}
	return m
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
