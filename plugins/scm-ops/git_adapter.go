package main

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxGitOutput = 2 << 20

type gitRunner func(context.Context, string, ...string) ([]byte, error)

// GitAdapter discovers worktrees under a configured root, including .git files
// used by linked worktrees. Commands never contact remotes or modify the index.
type GitAdapter struct {
	root string
	run  gitRunner
}

func NewGitAdapter(root string) (*GitAdapter, error) {
	if root == "~" || strings.HasPrefix(root, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(root, "~"), "/"))
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &GitAdapter{root: filepath.Clean(abs), run: runGit}, nil
}

// cappedBuffer fails rather than silently returning a partial patch or log.
type cappedBuffer struct{ bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxGitOutput {
		return 0, fmt.Errorf("git output exceeds %d bytes", maxGitOutput)
	}
	return b.Buffer.Write(p)
}

func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	fixed := []string{"--no-pager", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "color.ui=false", "-c", "core.hooksPath=/dev/null", "-C", dir}
	cmd := exec.CommandContext(ctx, "git", append(fixed, args...)...)
	// Inherited Git environment must not redirect operations to another repo.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_NO_LAZY_FETCH=1")
	var out, stderr cappedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Avoid leaking credential-bearing remote URLs or arbitrary git config.
		return nil, fmt.Errorf("git %s failed: %w", args[0], err)
	}
	return out.Bytes(), nil
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (a *GitAdapter) repo(id string) (string, error) {
	if id == "" || filepath.IsAbs(id) || strings.ContainsRune(id, 0) {
		return "", fmt.Errorf("%w: id must be a relative repository path", ErrValidation)
	}
	candidate := filepath.Join(a.root, id)
	if !inside(a.root, candidate) {
		return "", fmt.Errorf("%w: repository outside repos_root", ErrValidation)
	}
	root, err := filepath.EvalSymlinks(a.root)
	if err != nil {
		return "", fmt.Errorf("%w: repos_root unavailable", ErrNotFound)
	}
	path, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("%w: repository does not exist", ErrNotFound)
	}
	if !inside(root, path) {
		return "", fmt.Errorf("%w: repository outside repos_root", ErrValidation)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return "", fmt.Errorf("%w: path is not a git worktree", ErrNotFound)
	}
	return path, nil
}

func (a *GitAdapter) List(ctx context.Context) ([]Repository, error) {
	repos := []Repository{}
	if _, err := os.Stat(a.root); os.IsNotExist(err) {
		return repos, nil
	}
	// WalkDir does not follow symlinks; resolve the configured root only.
	root, err := filepath.EvalSymlinks(a.root)
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && (d.Name() == "node_modules" || d.Name() == "vendor" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			repos = append(repos, Repository{ID: filepath.ToSlash(rel), Name: filepath.Base(path), Path: path})
			return filepath.SkipDir
		}
		return nil
	})
	return repos, err
}

func (a *GitAdapter) Status(ctx context.Context, id string) (RepoStatus, error) {
	path, err := a.repo(id)
	if err != nil {
		return RepoStatus{}, err
	}
	out, err := a.run(ctx, path, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=normal", "--ignore-submodules=all")
	if err != nil {
		return RepoStatus{}, err
	}
	return parseStatus(string(out)), nil
}

func parseStatus(raw string) RepoStatus {
	s := RepoStatus{Changes: []string{}, CIState: "unknown"}
	parts := strings.Split(raw, "\x00")
	for i := 0; i < len(parts); i++ {
		line := parts[i]
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			s.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.oid "):
			s.Head = strings.TrimPrefix(line, "# branch.oid ")
			if s.Head == "(initial)" {
				s.Head = ""
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			s.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			_, _ = fmt.Sscanf(line, "# branch.ab +%d -%d", &s.Ahead, &s.Behind)
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "), strings.HasPrefix(line, "u "), strings.HasPrefix(line, "? "):
			s.Changes = append(s.Changes, line)
			if strings.HasPrefix(line, "2 ") && i+1 < len(parts) {
				i++
				s.Changes[len(s.Changes)-1] += "\x00" + parts[i]
			}
		}
	}
	s.Dirty = len(s.Changes) > 0
	return s
}

func (a *GitAdapter) branches(ctx context.Context, path string) ([]string, error) {
	out, err := a.run(ctx, path, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	branches := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			branches = append(branches, line)
		}
	}
	return branches, nil
}

func (a *GitAdapter) Read(ctx context.Context, id string) (RepoDetails, error) {
	path, err := a.repo(id)
	if err != nil {
		return RepoDetails{}, err
	}
	status, err := a.Status(ctx, id)
	if err != nil {
		return RepoDetails{}, err
	}
	branches, err := a.branches(ctx, path)
	if err != nil {
		return RepoDetails{}, err
	}
	root, err := filepath.EvalSymlinks(a.root)
	if err != nil {
		return RepoDetails{}, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return RepoDetails{}, err
	}
	return RepoDetails{Repository: Repository{ID: filepath.ToSlash(rel), Name: filepath.Base(path), Path: path}, Status: status, Branches: branches}, nil
}

func (a *GitAdapter) Activity(ctx context.Context, id string, limit int) (Activity, error) {
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 200 {
		return Activity{}, fmt.Errorf("%w: limit must be between 1 and 200", ErrValidation)
	}
	path, err := a.repo(id)
	if err != nil {
		return Activity{}, err
	}
	status, err := a.Status(ctx, id)
	if err != nil {
		return Activity{}, err
	}
	branches, err := a.branches(ctx, path)
	if err != nil {
		return Activity{}, err
	}
	activity := Activity{Commits: []Commit{}, Branches: branches}
	if status.Head == "" {
		return activity, nil
	} // unborn branch has no log yet
	out, err := a.run(ctx, path, "log", "--no-show-signature", "--max-count="+strconv.Itoa(limit), "--format=%H%x00%an%x00%aI%x00%s%x00", "HEAD", "--")
	if err != nil {
		return Activity{}, err
	}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+3 < len(fields); i += 4 {
		activity.Commits = append(activity.Commits, Commit{Hash: strings.TrimSpace(fields[i]), Author: fields[i+1], Timestamp: fields[i+2], Subject: fields[i+3]})
	}
	return activity, nil
}

var hashPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}([a-fA-F0-9]{24})?$`)

func (a *GitAdapter) revision(ctx context.Context, path, ref string) (string, error) {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.ContainsRune(ref, 0) {
		return "", fmt.Errorf("%w: invalid revision", ErrValidation)
	}
	out, err := a.run(ctx, path, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%w: revision does not name a commit", ErrValidation)
	}
	hash := strings.TrimSpace(string(out))
	if !hashPattern.MatchString(hash) {
		return "", fmt.Errorf("invalid git revision output")
	}
	return hash, nil
}

func (a *GitAdapter) Diff(ctx context.Context, req DiffRequest) (Diff, error) {
	if req.Head != "" && req.Base == "" {
		return Diff{}, fmt.Errorf("%w: head requires base", ErrValidation)
	}
	if req.Staged && req.Base != "" {
		return Diff{}, fmt.Errorf("%w: staged cannot be combined with base/head", ErrValidation)
	}
	path, err := a.repo(req.ID)
	if err != nil {
		return Diff{}, err
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--ignore-submodules=all"}
	if req.Staged {
		args = append(args, "--cached")
	}
	if req.Base != "" {
		base, err := a.revision(ctx, path, req.Base)
		if err != nil {
			return Diff{}, err
		}
		headRef := req.Head
		if headRef == "" {
			headRef = "HEAD"
		}
		head, err := a.revision(ctx, path, headRef)
		if err != nil {
			return Diff{}, err
		}
		args = append(args, base, head)
	}
	args = append(args, "--")
	out, err := a.run(ctx, path, args...)
	if err != nil {
		return Diff{}, err
	}
	return Diff{Patch: string(out)}, nil
}
