// Hello is the minimum-viable subprocess plugin for testing Tachyon's
// plugin-sdk integration. This is a copy of plugin-sdk's examples/hello
// for the proof-of-concept demonstration.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

type hello struct{}

func (hello) Init(ctx context.Context, params subprocess.InitParams) (subprocess.InitResult, error) {
	return subprocess.InitResult{
		ID:          "hello",
		Name:        "Hello",
		Version:     "0.1.0",
		Description: "Minimum-viable plugin-sdk example",
		Protocol:    subprocess.ProtocolVersion,
	}, nil
}

func (hello) Load(ctx context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}

func (hello) Unload(ctx context.Context) error { return nil }

func (hello) Command(ctx context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
	// Answer only our own command. The host probes every plugin with the
	// reserved plugin_capabilities command; a catch-all reply would be read as
	// a malformed capability declaration, which is a hard load error.
	if req.Name != "hello" {
		return subprocess.CommandResult{}, fmt.Errorf("unknown command: %s", req.Name)
	}
	return subprocess.CommandResult{Action: "message", Content: "hello, " + req.Args}, nil
}

func main() {
	if err := subprocess.Serve(hello{}); err != nil {
		os.Exit(1)
	}
}
