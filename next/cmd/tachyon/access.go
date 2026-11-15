package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tachyon/next/internal/access"
	"github.com/hollis-labs/tachyon/next/internal/store"
)

// accessCmd is `tachyon access`: service identities and their identity-key credentials, offline
// on the data directory (the operator can open the data file, as for `identity setup`). No command
// prints a key: keys go to new 0600 files.
func (t *tachyon) accessCmd(ctx context.Context) *cobra.Command {
	svc := func() (*access.Service, error) {
		st, ids := store.From(t.app), identity.From(t.app)
		if st == nil || ids == nil {
			return nil, errors.New("the database and identity modules must be started")
		}
		return access.New(st, ids, nil), nil
	}
	group := &cobra.Command{Use: "access", Short: "Service identities and their credentials (identity API keys)"}

	var name, identityID, preset, out string
	var verbs, kinds, agents []string
	var ttl time.Duration
	issue := &cobra.Command{
		Use:   "issue --name NAME --out FILE [--preset P | --verbs V --kinds K]",
		Short: "Create an identity key for a service identity (new, or --identity) and write it to a new 0600 file",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			s, err := svc()
			if err != nil {
				return err
			}
			if out == "" {
				return errors.New("--out is required: the key is written only to a new file")
			}
			g, err := access.Preset(preset)
			if err != nil {
				return err
			}
			if len(verbs) > 0 {
				g.Verbs = verbs
			}
			if len(kinds) > 0 {
				g.Kinds = kinds
			}
			f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the operator names the file
			if err != nil {
				return fmt.Errorf("--out: %w (an existing file is never overwritten)", err)
			}
			key, cred, err := s.Issue(ctx, access.IssueInput{Name: name, IdentityID: identityID, Grants: g, TTL: ttl})
			if err != nil {
				_ = f.Close()
				_ = os.Remove(out)
				return err
			}
			_, werr := io.WriteString(f, key+"\n")
			if err := errors.Join(werr, f.Close()); err != nil {
				_ = s.Revoke(ctx, cred.ID)
				return fmt.Errorf("write the key (the credential was revoked): %w", err)
			}
			if len(agents) > 0 {
				if err := s.Bind(ctx, cred.IdentityID, agents); err != nil {
					return err
				}
			}
			return printJSON(c.OutOrStdout(), cred)
		},
	}
	issue.Flags().StringVar(&name, "name", "", "the service identity's name (a role)")
	issue.Flags().StringVar(&identityID, "identity", "", "an existing service identity id (omit to create one)")
	issue.Flags().StringVar(&preset, "preset", "planner-read", "a named grant (the old presets)")
	issue.Flags().StringSliceVar(&verbs, "verbs", nil, "the verbs, replacing the preset's")
	issue.Flags().StringSliceVar(&kinds, "kinds", nil, "the kinds, replacing the preset's")
	issue.Flags().DurationVar(&ttl, "ttl", 30*24*time.Hour, "lifetime (1m to 90 days)")
	issue.Flags().StringSliceVar(&agents, "agent", nil, "agents the identity may act for")
	issue.Flags().StringVar(&out, "out", "", "new file (0600) to write the key to")

	bind := &cobra.Command{Use: "bind --identity ID --agent A", Short: "Let an identity act for agents", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			s, err := svc()
			if err != nil {
				return err
			}
			return s.Bind(ctx, identityID, agents)
		}}
	unbind := &cobra.Command{Use: "unbind --identity ID --agent A", Short: "Remove agents from an identity", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			s, err := svc()
			if err != nil {
				return err
			}
			return s.Unbind(ctx, identityID, agents)
		}}
	for _, c := range []*cobra.Command{bind, unbind} {
		c.Flags().StringVar(&identityID, "identity", "", "service identity id")
		c.Flags().StringSliceVar(&agents, "agent", nil, "agent references")
	}
	pin := &cobra.Command{Use: "pin-proxy --identity ID", Short: "Make an identity the one trusted Tether proxy", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			s, err := svc()
			if err != nil {
				return err
			}
			return s.PinProxy(ctx, identityID)
		}}
	pin.Flags().StringVar(&identityID, "identity", "", "service identity id")
	unpin := &cobra.Command{Use: "unpin-proxy", Short: "Remove the proxy pin", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			s, err := svc()
			if err != nil {
				return err
			}
			return s.UnpinProxy(ctx)
		}}
	list := &cobra.Command{Use: "list", Short: "Identities, credentials and old tokens (no keys, no hashes)", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			s, err := svc()
			if err != nil {
				return err
			}
			l, err := s.List(ctx)
			if err != nil {
				return err
			}
			return printJSON(c.OutOrStdout(), l)
		}}
	var id string
	revoke := &cobra.Command{Use: "revoke --id CREDENTIAL", Short: "Revoke a credential at once", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			s, err := svc()
			if err != nil {
				return err
			}
			return s.Revoke(ctx, id)
		}}
	revoke.Flags().StringVar(&id, "id", "", "credential (identity key) id")
	disable := &cobra.Command{Use: "disable --id IDENTITY", Short: "Disable an identity and every credential of it", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			s, err := svc()
			if err != nil {
				return err
			}
			return s.Disable(ctx, id)
		}}
	disable.Flags().StringVar(&id, "id", "", "service identity id")

	var outDir string
	reissue := &cobra.Command{
		Use:   "reissue --out-dir DIR",
		Short: "Turn every active old token into one identity key with its exact grant (once, after the owner exists)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			s, err := svc()
			if err != nil {
				return err
			}
			if e := newPrivateDir(outDir); e != nil {
				return e
			}
			done, err := s.Reissue(ctx)
			w := c.OutOrStdout()
			for _, r := range done {
				_, _ = fmt.Fprintf(w, "identity %s (%s) old token %s -> credential %s: %d (verb, kind) pairs, allow-set equal %v, expiry not extended %v\n",
					r.Identity.ID, r.Identity.Name, r.OldTokenID, r.Credential.ID, r.Pairs, r.AllowSetEqual, r.ExpiryKept)
			}
			if err != nil {
				if len(done) == 0 {
					return err // refused before any key was made (no owner, keys exist, an invalid grant)
				}
				// No key leaves the process: every credential made here is revoked again. The identity
				// keys stay in the module (no exported revoke, CW-20261114-0131), so the reissue cannot
				// simply run again: redo the window from migrate-from on a new data directory.
				var rerr []error
				for _, r := range done {
					rerr = append(rerr, s.Revoke(ctx, r.Credential.ID))
				}
				if revokeErr := errors.Join(rerr...); revokeErr != nil {
					return fmt.Errorf("%w\nno key file was written; revoking the credentials made also failed: %w", err, revokeErr)
				}
				return fmt.Errorf("%w\nno key file was written and the %d credential(s) above were revoked; redo from migrate-from on a new data directory", err, len(done))
			}
			for _, r := range done {
				file := filepath.Join(outDir, r.Identity.ID+"."+r.Credential.ID+".key")
				if err := writeNew(file, r.Key+"\n"); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintf(w, "reissued %d credential(s); one key file each in %s (0600)\n", len(done), outDir)
			return nil
		},
	}
	reissue.Flags().StringVar(&outDir, "out-dir", "", "a new or empty directory for the key files (0700)")

	group.AddCommand(issue, bind, unbind, pin, unpin, list, revoke, disable, reissue)
	return group
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// newPrivateDir creates dir 0700, or accepts an existing empty one.
func newPrivateDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("--out-dir is required")
	}
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return os.MkdirAll(dir, 0o700)
	case err != nil:
		return err
	case len(entries) > 0:
		return fmt.Errorf("--out-dir %s is not empty", dir)
	}
	return os.Chmod(dir, 0o700) //nolint:gosec // owner-only is the point
}

func writeNew(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a file in the operator's out-dir
	if err != nil {
		return err
	}
	_, werr := io.WriteString(f, content)
	return errors.Join(werr, f.Close())
}
