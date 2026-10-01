package main

import (
	"context"
	"errors"
)

var (
	ErrValidation = errors.New("validation")
	ErrNotFound   = errors.New("not found")
)

// Repository IDs are paths relative to repos_root, including "." for the root.
type Repository struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type RepoDetails struct {
	Repository
	Status   RepoStatus `json:"status"`
	Branches []string   `json:"branches"`
}

type RepoStatus struct {
	Branch   string   `json:"branch"`
	Head     string   `json:"head,omitempty"`
	Upstream string   `json:"upstream,omitempty"`
	Ahead    int      `json:"ahead"`
	Behind   int      `json:"behind"`
	Dirty    bool     `json:"dirty"`
	Changes  []string `json:"changes"`
	CIState  string   `json:"ci_state"` // unknown: the local adapter never contacts CI
}

type Commit struct {
	Hash      string `json:"hash"`
	Author    string `json:"author"`
	Timestamp string `json:"timestamp"`
	Subject   string `json:"subject"`
}

type Activity struct {
	Commits  []Commit `json:"commits"`
	Branches []string `json:"branches"`
}

type DiffRequest struct {
	ID     string `json:"id"`
	Base   string `json:"base,omitempty"`
	Head   string `json:"head,omitempty"`
	Staged bool   `json:"staged,omitempty"`
}

type Diff struct {
	Patch string `json:"patch"`
}

// SCMAdapter exposes read-only source control operations. The local Git
// implementation does not fetch, mutate repositories, or query GitHub.
type SCMAdapter interface {
	List(context.Context) ([]Repository, error)
	Read(context.Context, string) (RepoDetails, error)
	Activity(context.Context, string, int) (Activity, error)
	Status(context.Context, string) (RepoStatus, error)
	Diff(context.Context, DiffRequest) (Diff, error)
}
