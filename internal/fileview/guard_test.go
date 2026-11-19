package fileview

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/hollis-labs/tachyon/internal/fileviewapi"
)

// corpus builds the files and links the cases below name, all under <home>/dev unless noted.
func corpus(t *testing.T) (env, *Guard, string) {
	t.Helper()
	e := setup(t)
	dev := filepath.Join(e.home, "dev")
	write := func(rel, body string) {
		p := filepath.Join(dev, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"readme.md", "sub/notes.txt", "Report.PDF", ".git/HEAD", "id_rsa.pub", "app.log",
		".env", ".env.local", "id_rsa", "server.pem", "tls.key", "a.p12", "b.pfx", "credentials.json", "my-secret.txt", "api_token", ".netrc", ".npmrc", ".pgpass", "vault.kdbx", ".git/config", ".ENV", "Credentials.JSON"} {
		write(f, "x")
	}
	write("../.ssh/id_ed25519", "private")
	write("../.config/gcloud/creds", "private")
	link := func(name, target string) {
		if err := os.Symlink(target, filepath.Join(dev, name)); err != nil {
			t.Fatal(err)
		}
	}
	link("ok-link", filepath.Join(dev, "readme.md"))
	link("to-env", filepath.Join(dev, ".env"))
	link("innocent.txt", filepath.Join(e.home, ".ssh", "id_ed25519"))
	link("to-etc", "/etc/hostname")
	link("etc-dir", "/etc")
	link("dangling", filepath.Join(dev, "nowhere"))
	link("loop", filepath.Join(dev, "loop"))
	if err := syscall.Mkfifo(filepath.Join(dev, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	return e, &Guard{Svc: e.svc}, dev
}

func TestGuardCorpus(t *testing.T) {
	e, g, dev := corpus(t)
	in := func(rel string) string { return filepath.Join(dev, rel) }
	cases := []struct {
		name, path, deny string
	}{
		{"plain file", in("readme.md"), ""},
		{"nested file", in("sub/notes.txt"), ""},
		{"tilde", "~/dev/readme.md", ""},
		{"unclean but inside", in("sub/../readme.md"), ""},
		{"link inside the root", in("ok-link"), ""},
		{"public key", in("id_rsa.pub"), ""},
		{"git internals that are not config", in(".git/HEAD"), ""},
		{"uppercase name", in("Report.PDF"), ""},

		{"env", in(".env"), fileviewapi.DenySecretPattern},
		{"env variant", in(".env.local"), fileviewapi.DenySecretPattern},
		{"env upper case", in(".ENV"), fileviewapi.DenySecretPattern},
		{"private key", in("id_rsa"), fileviewapi.DenySecretPattern},
		{"pem", in("server.pem"), fileviewapi.DenySecretPattern},
		{"key", in("tls.key"), fileviewapi.DenySecretPattern},
		{"p12", in("a.p12"), fileviewapi.DenySecretPattern},
		{"pfx", in("b.pfx"), fileviewapi.DenySecretPattern},
		{"credentials", in("credentials.json"), fileviewapi.DenySecretPattern},
		{"credentials upper case", in("Credentials.JSON"), fileviewapi.DenySecretPattern},
		{"secret in name", in("my-secret.txt"), fileviewapi.DenySecretPattern},
		{"token in name", in("api_token"), fileviewapi.DenySecretPattern},
		{"netrc", in(".netrc"), fileviewapi.DenySecretPattern},
		{"npmrc", in(".npmrc"), fileviewapi.DenySecretPattern},
		{"pgpass", in(".pgpass"), fileviewapi.DenySecretPattern},
		{"kdbx", in("vault.kdbx"), fileviewapi.DenySecretPattern},
		{"git config", in(".git/config"), fileviewapi.DenySecretPattern},
		{"link to a secret", in("to-env"), fileviewapi.DenySecretPattern},
		{"innocent link to a ssh key", in("innocent.txt"), fileviewapi.DenySecretPattern},
		{"ssh dir by traversal", in("../.ssh/id_ed25519"), fileviewapi.DenySecretPattern},
		{"gcloud dir", in("../.config/gcloud/creds"), fileviewapi.DenySecretPattern},

		{"link out of the root", in("to-etc"), fileviewapi.DenySymlinkEscape},
		{"dir link out of the root", in("etc-dir/passwd"), fileviewapi.DenySymlinkEscape},
		{"absolute outside", "/etc/passwd", fileviewapi.DenyNotFoundHidden},
		{"traversal outside", in("../../../../../../etc/passwd"), fileviewapi.DenyNotFoundHidden},
		{"proc", "/proc/self/environ", fileviewapi.DenyNotFoundHidden},
		{"missing", in("nope.txt"), fileviewapi.DenyNotFoundHidden},
		{"dangling link", in("dangling"), fileviewapi.DenyNotFoundHidden},
		{"link loop", in("loop"), fileviewapi.DenyNotFoundHidden},

		{"empty", "", DenyInvalid},
		{"relative", "dev/readme.md", DenyInvalid},
		{"dot dot relative", "../etc/passwd", DenyInvalid},
		{"NUL", in("readme.md") + "\x00.txt", DenyInvalid},
		{"too long", "/" + string(make([]byte, 5000)), DenyInvalid},
	}
	for _, c := range cases {
		res, den := g.Resolve(context.Background(), c.path)
		switch {
		case c.deny == "" && den != nil:
			t.Errorf("%s: denied %s", c.name, den.Reason)
		case c.deny != "" && (den == nil || den.Reason != c.deny):
			t.Errorf("%s: got %v want %s", c.name, den, c.deny)
		case c.deny == "" && res.Root.Label == "":
			t.Errorf("%s: no root", c.name)
		}
	}
	// Denials never echo the path.
	if _, den := g.Resolve(context.Background(), "/etc/passwd"); den.Error() != fileviewapi.DenyNotFoundHidden {
		t.Errorf("error text: %q", den.Error())
	}
	_ = e
}

func TestGuardOpenReadsOnlyRegularFiles(t *testing.T) {
	_, g, dev := corpus(t)
	f, res, den := g.Open(context.Background(), filepath.Join(dev, "ok-link"))
	if den != nil {
		t.Fatal(den)
	}
	defer f.Close()
	if b, _ := io.ReadAll(f); string(b) != "x" || res.Rel != "readme.md" {
		t.Errorf("%q %q", b, res.Rel)
	}
	for name, p := range map[string]string{"directory": filepath.Join(dev, "sub"), "fifo": filepath.Join(dev, "pipe")} {
		if f, _, den := g.Open(context.Background(), p); den == nil || den.Reason != DenyNotFile {
			if f != nil {
				f.Close()
			}
			t.Errorf("%s: %v", name, den)
		}
	}
	if _, den := g.Resolve(context.Background(), filepath.Join(dev, "sub")); den != nil {
		t.Errorf("a directory resolves for stat: %v", den)
	}
}

func TestGuardRefusesASwapBetweenCheckAndOpen(t *testing.T) {
	_, g, dev := corpus(t)
	target := filepath.Join(dev, "readme.md")
	res, den := g.Resolve(context.Background(), target)
	if den != nil {
		t.Fatal(den)
	}
	other := filepath.Join(dev, "other.md")
	if err := os.WriteFile(other, []byte("planted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(other, target); err != nil {
		t.Fatal(err)
	}
	if f, den := g.OpenResolved(res); den == nil {
		f.Close()
		t.Error("opened a different file than the one checked")
	}
	// A link planted at the resolved path is not followed.
	res, _ = g.Resolve(context.Background(), filepath.Join(dev, "sub/notes.txt"))
	if err := os.Remove(res.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hostname", res.Path); err != nil {
		t.Fatal(err)
	}
	if f, den := g.OpenResolved(res); den == nil {
		f.Close()
		t.Error("followed a link planted after the check")
	}
}

func TestGuardSiblingPrefixIsNotInsideTheRoot(t *testing.T) {
	if isAncestorOrSame("/a/dev", "/a/dev-evil/x") || !isAncestorOrSame("/a/dev", "/a/dev/x") || !isAncestorOrSame("/a/dev", "/a/dev") || isAncestorOrSame("/a/dev", "/a") {
		t.Error("prefix test is not separator-aware")
	}
}

func TestGuardOwnerOnlyRootsRefuseOtherUsersFiles(t *testing.T) {
	e := setup(t)
	dir, err := os.MkdirTemp("/tmp", "fileview-guard-")
	if err != nil {
		t.Skip("no /tmp")
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := filepath.Join(dir, "planted.txt")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, den := (&Guard{Svc: e.svc}).Resolve(context.Background(), p); den != nil {
		t.Errorf("own file under /tmp: %v", den)
	}
	other := &Guard{Svc: e.svc, UID: os.Getuid() + 1}
	if _, den := other.Resolve(context.Background(), p); den == nil || den.Reason != fileviewapi.DenyNotFoundHidden {
		t.Errorf("another user's file under /tmp: %v", den)
	}
}

func TestGuardUserDenyPatternsAddButNeverRemove(t *testing.T) {
	_, g, dev := corpus(t)
	ctx := context.Background()
	if _, den := g.Resolve(ctx, filepath.Join(dev, "app.log")); den != nil {
		t.Fatalf("before: %v", den)
	}
	deny := []string{"*.log"}
	if _, err := g.Svc.Apply(ctx, Update{App: &struct {
		Roots            *[]string `json:"roots"`
		DenyExtra        *[]string `json:"deny_extra"`
		Caps             *Caps     `json:"caps"`
		CasterNamespaces *[]string `json:"caster_namespaces"`
		RunScripts       *bool     `json:"run_scripts_allowed"`
		AllowUserRoots   *bool     `json:"allow_user_roots"`
		AuditRetention   *int      `json:"audit_retention_days"`
	}{DenyExtra: &deny}}); err != nil {
		t.Fatal(err)
	}
	if _, den := g.Resolve(ctx, filepath.Join(dev, "app.log")); den == nil || den.Reason != fileviewapi.DenySecretPattern {
		t.Errorf("added pattern: %v", den)
	}
	if _, den := g.Resolve(ctx, filepath.Join(dev, ".env")); den == nil {
		t.Error("built-in pattern removed")
	}
}

func TestGuardDropsRootsThatVanishOrMoveIntoSecrets(t *testing.T) {
	e, g, dev := corpus(t)
	ctx := context.Background()
	extra := t.TempDir()
	if err := os.WriteFile(filepath.Join(extra, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := []string{extra}
	if _, err := e.svc.Apply(ctx, Update{App: &struct {
		Roots            *[]string `json:"roots"`
		DenyExtra        *[]string `json:"deny_extra"`
		Caps             *Caps     `json:"caps"`
		CasterNamespaces *[]string `json:"caster_namespaces"`
		RunScripts       *bool     `json:"run_scripts_allowed"`
		AllowUserRoots   *bool     `json:"allow_user_roots"`
		AuditRetention   *int      `json:"audit_retention_days"`
	}{Roots: &roots}}); err != nil {
		t.Fatal(err)
	}
	if _, den := g.Resolve(ctx, filepath.Join(extra, "a.txt")); den != nil {
		t.Fatalf("added root: %v", den)
	}
	if err := os.RemoveAll(extra); err != nil {
		t.Fatal(err)
	}
	if _, den := g.Resolve(ctx, filepath.Join(extra, "a.txt")); den == nil {
		t.Error("removed root still served")
	}
	_ = dev
}
