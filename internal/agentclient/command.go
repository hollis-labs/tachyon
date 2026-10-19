package agentclient

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"strings"

	mcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func Command(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("data", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	endpoint := f.String("url", os.Getenv("TACHYON_API_URL"), "Tachyon origin")
	tokenFile := f.String("token-file", os.Getenv("TACHYON_API_TOKEN_FILE"), "private credential file")
	input := f.String("input", "{}", "JSON verb arguments")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 1 {
		return errors.New("usage: tachyon data [--url ORIGIN --token-file PATH --input JSON] verbs|mcp|VERB")
	}
	c, err := New(*endpoint, *tokenFile)
	if err != nil {
		return err
	}
	switch f.Arg(0) {
	case "verbs":
		verbs, err := c.Verbs(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(verbs)
	case "mcp":
		s, err := c.MCP(ctx)
		if err != nil {
			return err
		}
		return s.SDKServer().Run(ctx, &mcp.StdioTransport{})
	default:
		raw, err := c.Invoke(ctx, f.Arg(0), json.RawMessage(*input))
		if err != nil {
			return err
		}
		_, err = out.Write(append(raw, '\n'))
		return err
	}
}
func (c *Client) MCP(ctx context.Context) (*mcpserver.Server, error) {
	verbs, err := c.Verbs(ctx)
	if err != nil {
		return nil, err
	}
	s := mcpserver.NewServer("tachyon-planner", "1.0.0")
	for _, v := range verbs {
		if v.Mutates {
			continue
		}
		s.RegisterTool(mcpserver.Tool{Name: "tachyon_" + strings.ReplaceAll(v.Name, ".", "_"), Description: v.Description, InputSchema: v.InputSchema, ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: false, Handler: func(ctx context.Context, args map[string]any) (any, error) {
			input, err := json.Marshal(args)
			if err != nil {
				return nil, err
			}
			raw, err := c.Invoke(ctx, v.Name, input)
			if err != nil {
				return nil, err
			}
			// RawMessage preserves large JSON numbers in the structured result.
			return json.RawMessage(raw), nil
		}})
	}
	return s, nil
}
