package agentaccess

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Command is operator-local credential administration, never an HTTP endpoint.
func Command(ctx context.Context, dataDir string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: tachyon access issue|bind|unbind|pin-proxy|unpin-proxy|list|revoke|disable [flags]")
	}
	cmd := args[0]
	f := flag.NewFlagSet("access "+cmd, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	var name, identity, path, id, kinds, verbs, preset, agents string
	var ttl time.Duration
	switch cmd {
	case "issue":
		f.StringVar(&name, "name", "", "service identity name")
		f.StringVar(&identity, "identity", "", "existing identity UUID; omitted creates a new identity")
		f.StringVar(&path, "token-file", "", "new private token file (never overwritten)")
		f.DurationVar(&ttl, "ttl", 30*24*time.Hour, "token lifetime, maximum 2160h")
		f.StringVar(&preset, "preset", "planner-read", "planner-read, planner-torque-read, torque-read, tesseract-read, app-agent-read, chat-workspace-read, chat-topic-add, chat-plan, chat-activity-append, chat-panel-open, terminal-propose, portfolio-read, portfolio-write, actions-request or card-ask")
		f.StringVar(&agents, "agent", "", "comma-separated agent ids or addresses this identity may act for (chat plan verbs)")
		f.StringVar(&kinds, "kinds", "", "explicit Planner kinds; overrides the preset")
		f.StringVar(&verbs, "verbs", "", "explicit supported verbs; overrides the preset")
	case "pin-proxy":
		f.StringVar(&identity, "identity", "", "service identity UUID of the Tether MCP proxy (replaces any previous pin)")
	case "unpin-proxy":
	case "bind", "unbind":
		f.StringVar(&identity, "identity", "", "service identity UUID")
		f.StringVar(&agents, "agent", "", "agent id or address (bind accepts a comma-separated list)")
	case "revoke", "disable":
		f.StringVar(&id, "id", "", "token UUID for revoke, identity UUID for disable")
	case "list":
	default:
		return errors.New("unknown access command")
	}
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if cmd == "issue" {
		var grants Grants
		switch preset {
		case "planner-read":
			grants = PlannerRead()
		case "torque-read":
			grants = TorqueRead()
		case "tesseract-read":
			grants = TesseractRead()
		case "chat-workspace-read":
			grants = ChatWorkspaceRead()
		case "chat-topic-add":
			grants = ChatTopicAdd()
		case "terminal-propose":
			grants = TerminalPropose()
		case "chat-plan":
			grants = ChatPlan()
		case "chat-activity-append":
			grants = ChatActivityAppend()
		case "chat-panel-open":
			grants = ChatPanelOpen()
		case "app-agent-read":
			grants = AppAgentRead()
		case "planner-torque-read":
			grants = PlannerTorqueRead()
		case "portfolio-read":
			grants = PortfolioRead()
		case "portfolio-write":
			grants = PortfolioWrite()
		case "actions-request":
			grants = ActionRequest()
		case "card-ask":
			grants = CardAsk()
		default:
			return errors.New("unknown access preset")
		}
		if kinds == "" {
			kinds = strings.Join(grants.Kinds, ",")
		}
		if verbs == "" {
			verbs = strings.Join(grants.Verbs, ",")
		}
		if path == "" {
			return errors.New("--token-file is required; credentials are never printed")
		}
		if _, err := os.Lstat(path); err == nil {
			return errors.New("token file already exists; use a new path for rotation")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if (cmd == "bind" || cmd == "unbind") && (identity == "" || agents == "") {
		return errors.New("--identity and --agent are required")
	}
	if cmd == "pin-proxy" && identity == "" {
		return errors.New("--identity is required")
	}
	if (cmd == "revoke" || cmd == "disable") && id == "" {
		return errors.New("--id is required")
	}
	db, err := localstore.Open(ctx, filepath.Join(dataDir, "tachyon.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	s := &Store{DB: db}
	switch cmd {
	case "issue":
		t, secret, err := s.Issue(ctx, identity, name, Grants{Verbs: splitList(verbs), Kinds: splitList(kinds)}, ttl)
		if err != nil {
			return err
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if agents != "" {
			if t.Agents, err = s.Bind(ctx, t.IdentityID, splitList(agents)); err != nil {
				return errors.Join(err, s.Revoke(cleanup, t.ID, false))
			}
		}
		if err := tokenfile.Create(path, secret); err != nil {
			return errors.Join(err, s.Revoke(cleanup, t.ID, false))
		}
		return json.NewEncoder(out).Encode(t)
	case "bind":
		bound, err := s.Bind(ctx, identity, splitList(agents))
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"identity_id": identity, "agents": bound})
	case "unbind":
		if err := s.Unbind(ctx, identity, agents); err != nil {
			return err
		}
		bound, err := s.Agents(ctx, identity)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"identity_id": identity, "agents": bound})
	case "pin-proxy":
		if err := s.PinProxy(ctx, identity); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"identity_id": identity, "proxy": true})
	case "unpin-proxy":
		if err := s.UnpinProxy(ctx); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"proxy": false})
	case "list":
		ts, err := s.List(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(ts)
	default:
		if err := s.Revoke(ctx, id, cmd == "disable"); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]string{"id": id, "status": cmd + "d"})
	}
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
