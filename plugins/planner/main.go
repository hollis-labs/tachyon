// Command planner registers the Planner UI. Its data and change stream are
// served by the host's owned Planner service, without a Torque Planner proxy.
package main

import (
	"context"
	"os"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
)

const version = "0.1.0"

type planner struct{}

func (planner) Init(context.Context, subprocess.InitParams) (subprocess.InitResult, error) {
	return subprocess.InitResult{
		ID:                 "planner",
		Name:               "Planner",
		Version:            version,
		Description:        "Tachyon-owned portfolio planner",
		Protocol:           subprocess.ProtocolVersion,
		CapabilityContract: 1,
	}, nil
}

func (planner) Load(context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}
func (planner) Unload(context.Context) error { return nil }

func main() {
	if err := subprocess.Serve(planner{}); err != nil {
		os.Exit(1)
	}
}
