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
		return errors.New("usage: tachyon access issue|list|revoke|disable [flags]")
	}
	cmd := args[0]
	f := flag.NewFlagSet("access "+cmd, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	var name, identity, path, id, kinds, verbs string
	var ttl time.Duration
	switch cmd {
	case "issue":
		f.StringVar(&name, "name", "", "service identity name")
		f.StringVar(&identity, "identity", "", "existing identity UUID; omitted creates a new identity")
		f.StringVar(&path, "token-file", "", "new private token file (never overwritten)")
		f.DurationVar(&ttl, "ttl", 30*24*time.Hour, "token lifetime, maximum 2160h")
		f.StringVar(&kinds, "kinds", strings.Join(PlannerRead().Kinds, ","), "explicit Planner kinds")
		f.StringVar(&verbs, "verbs", strings.Join(PlannerRead().Verbs, ","), "explicit supported read verbs")
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
		if path == "" {
			return errors.New("--token-file is required; credentials are never printed")
		}
		if _, err := os.Lstat(path); err == nil {
			return errors.New("token file already exists; use a new path for rotation")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
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
		t, secret, err := s.Issue(ctx, identity, name, Grants{Verbs: strings.Split(verbs, ","), Kinds: strings.Split(kinds, ",")}, ttl)
		if err != nil {
			return err
		}
		if err := tokenfile.Create(path, secret); err != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			return errors.Join(err, s.Revoke(cleanup, t.ID, false))
		}
		return json.NewEncoder(out).Encode(t)
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
