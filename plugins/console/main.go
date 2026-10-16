// Command console is Tachyon's Console plugin backend: agent fleet, builder
// and the operator console overlay. It forwards to Torque, which also relays
// the agent activity monitor. Backends that do not exist yet answer 404 from upstream;
// see docs/data-gaps.md.
package main

import (
	"context"
	"os"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"

	"github.com/hollis-labs/tachyon/pkg/pluginkit"
)

const version = "0.1.0"

type console struct{ *pluginkit.Proxy }

func (console) Init(context.Context, subprocess.InitParams) (subprocess.InitResult, error) {
	return subprocess.InitResult{
		ID:                 "console",
		Name:               "Console",
		Version:            version,
		Description:        "Operator console over Torque",
		Protocol:           subprocess.ProtocolVersion,
		CapabilityContract: 1,
	}, nil
}

func (console) Load(context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}
func (console) Unload(context.Context) error { return nil }

func main() {
	torque := pluginkit.Env("TORQUE_URL", "http://127.0.0.1:8990")
	proxy := pluginkit.NewProxy("console",
		pluginkit.Route{Prefix: "/v1", Upstream: torque, Target: "/api/v1", Bearer: os.Getenv("TORQUE_API_TOKEN"), Headers: map[string]string{"X-Client": "browser"}},
	)
	if err := subprocess.Serve(console{proxy}); err != nil {
		os.Exit(1)
	}
}
