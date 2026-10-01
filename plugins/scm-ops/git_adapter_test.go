package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every real git invocation is confined to a disposable local repo and HOME.
// No test adds a remote, invokes GitHub, or launches a model CLI.
func fixture(t *testing.T) (*GitAdapter, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	repo := filepath.Join(root, "repo with spaces")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	localGit(t, repo, "init", "-b", "main")
	localGit(t, repo, "config", "user.name", "Fixture Author")
	localGit(t, repo, "config", "user.email", "fixture@example.invalid")
	a, err := NewGitAdapter(root)
	if err != nil {
		t.Fatal(err)
	}
	return a, repo
}

func localGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func commitFixture(t *testing.T, repo string) string {
	t.Helper()
	writeFile(t, filepath.Join(repo, "file.txt"), "original\n")
	localGit(t, repo, "add", "file.txt")
	localGit(t, repo, "commit", "-m", "First commit")
	return localGit(t, repo, "rev-parse", "HEAD")
}

func TestGitDiscoveryReadAndActivity(t *testing.T) {
	a, repo := fixture(t)
	hash := commitFixture(t, repo)
	ctx := context.Background()
	repos, err := a.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].ID != "repo with spaces" {
		t.Fatalf("repos: %+v", repos)
	}
	details, err := a.Read(ctx, repos[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if details.Status.Dirty || details.Status.Head != hash || details.Status.Branch != "main" || details.Status.CIState != "unknown" {
		t.Fatalf("details: %+v", details)
	}
	act, err := a.Activity(ctx, repos[0].ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(act.Commits) != 1 || act.Commits[0].Hash != hash || act.Commits[0].Subject != "First commit" || act.Commits[0].Author != "Fixture Author" {
		t.Fatalf("activity: %+v", act)
	}
	// Linked worktree discovery uses a .git file rather than directory.
	localGit(t, repo, "worktree", "add", "-b", "feature", filepath.Join(a.root, "linked"))
	repos, err = a.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range repos {
		if r.ID == "linked" {
			found = true
		}
	}
	if !found {
		t.Fatalf("linked worktree missing: %+v", repos)
	}
}

func TestGitStatusAndDiff(t *testing.T) {
	a, repo := fixture(t)
	commitFixture(t, repo)
	ctx := context.Background()
	id := "repo with spaces"
	writeFile(t, filepath.Join(repo, "file.txt"), "staged\n")
	localGit(t, repo, "add", "file.txt")
	writeFile(t, filepath.Join(repo, "file.txt"), "unstaged\n")
	status, err := a.Status(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Dirty {
		t.Fatal("dirty worktree reported clean")
	}
	diff, err := a.Diff(ctx, DiffRequest{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Patch, "+unstaged") {
		t.Fatalf("unstaged patch: %s", diff.Patch)
	}
	diff, err = a.Diff(ctx, DiffRequest{ID: id, Staged: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Patch, "+staged") {
		t.Fatalf("staged patch: %s", diff.Patch)
	}
	localGit(t, repo, "commit", "-m", "Second commit")
	diff, err = a.Diff(ctx, DiffRequest{ID: id, Base: "HEAD~1", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Patch, "+staged") || strings.Contains(diff.Patch, "+unstaged") {
		t.Fatalf("branch patch: %s", diff.Patch)
	}
	localGit(t, repo, "checkout", "--detach")
	status, err = a.Status(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if status.Branch != "(detached)" {
		t.Fatalf("detached status: %+v", status)
	}
}

func TestUnbornRepository(t *testing.T) {
	a, _ := fixture(t)
	ctx := context.Background()
	status, err := a.Status(ctx, "repo with spaces")
	if err != nil {
		t.Fatal(err)
	}
	if status.Head != "" || status.Branch != "main" {
		t.Fatalf("unborn: %+v", status)
	}
	activity, err := a.Activity(ctx, "repo with spaces", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(activity.Commits) != 0 {
		t.Fatalf("unborn activity: %+v", activity)
	}
}

func TestPathAndRevisionValidation(t *testing.T) {
	a, repo := fixture(t)
	commitFixture(t, repo)
	ctx := context.Background()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(a.root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../escape", outside, "escape", ""} {
		if _, err := a.Status(ctx, id); !errors.Is(err, ErrValidation) {
			t.Errorf("id %q: %v", id, err)
		}
	}
	for _, req := range []DiffRequest{
		{ID: "repo with spaces", Base: "--output=/tmp/unsafe"},
		{ID: "repo with spaces", Base: "does-not-exist"},
		{ID: "repo with spaces", Head: "HEAD"},
		{ID: "repo with spaces", Base: "HEAD", Staged: true},
	} {
		if _, err := a.Diff(ctx, req); !errors.Is(err, ErrValidation) {
			t.Errorf("request %+v: %v", req, err)
		}
	}
	if _, err := a.Activity(ctx, "repo with spaces", 201); !errors.Is(err, ErrValidation) {
		t.Fatalf("limit: %v", err)
	}
	if _, err := a.Read(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestStatusParserRenameAndTracking(t *testing.T) {
	raw := "# branch.oid abc\x00# branch.head main\x00# branch.upstream origin/main\x00# branch.ab +2 -3\x002 R. N... 100644 100644 100644 abc def R100 new name\x00old\nname\x00? untracked\x00"
	s := parseStatus(raw)
	if s.Ahead != 2 || s.Behind != 3 || !s.Dirty || s.Upstream != "origin/main" || len(s.Changes) != 2 || !strings.HasSuffix(s.Changes[0], "\x00old\nname") {
		t.Fatalf("status: %+v", s)
	}
}

func TestGitRunnerIsolationAndExternalHelpers(t *testing.T) {
	a, repo := fixture(t)
	commitFixture(t, repo)
	// An inherited GIT_DIR must not redirect a command away from the requested repo.
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "missing"))
	if _, err := a.Status(context.Background(), "repo with spaces"); err != nil {
		t.Fatal(err)
	}
	// Configure helpers that would fail if the read-only adapter invoked them.
	localGit(t, repo, "config", "core.fsmonitor", "/does-not-exist")
	localGit(t, repo, "config", "diff.external", "/does-not-exist")
	writeFile(t, filepath.Join(repo, "file.txt"), "changed\n")
	if _, err := a.Status(context.Background(), "repo with spaces"); err != nil {
		t.Fatal(err)
	}
	diff, err := a.Diff(context.Background(), DiffRequest{ID: "repo with spaces"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Patch, "+changed") {
		t.Fatal("patch missing")
	}
}

func TestGitLimitsAndCancellation(t *testing.T) {
	var buffer cappedBuffer
	if _, err := buffer.Write(make([]byte, maxGitOutput+1)); err == nil {
		t.Fatal("oversized output accepted")
	}
	a, _ := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("list cancellation: %v", err)
	}
	if _, err := a.Status(ctx, "repo with spaces"); !errors.Is(err, context.Canceled) {
		t.Fatalf("status cancellation: %v", err)
	}
}
