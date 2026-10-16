// Command planner is Tachyon's Planner plugin backend. Planner data stays in
// Torque for now; this plugin forwards the planner and task-lookup API.
package main

import (
	"context"
	"os"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"

	"github.com/hollis-labs/tachyon/pkg/pluginkit"
)

const version = "0.1.0"

type planner struct{ *pluginkit.Proxy }

func (planner) Init(context.Context, subprocess.InitParams) (subprocess.InitResult, error) {
	return subprocess.InitResult{
		ID:                 "planner",
		Name:               "Planner",
		Version:            version,
		Description:        "Portfolio planner over Torque",
		Protocol:           subprocess.ProtocolVersion,
		CapabilityContract: 1,
	}, nil
}

func (planner) Load(context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}
func (planner) Unload(context.Context) error { return nil }

func main() {
	torque := pluginkit.Env("TORQUE_URL", "http://127.0.0.1:8990")
	// X-Client: browser keeps Torque's browser authorship semantics.
	route := pluginkit.Route{Prefix: "/v1", Upstream: torque, Target: "/api/v1", Bearer: os.Getenv("TORQUE_API_TOKEN"), Headers: map[string]string{"X-Client": "browser"}}
	if err := subprocess.Serve(planner{pluginkit.NewProxy("planner", route)}); err != nil {
		os.Exit(1)
	}
}
