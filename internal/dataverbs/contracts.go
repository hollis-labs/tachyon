// Package dataverbs exposes local domain operations without coupling them to
// HTTP, plugin subprocesses or a particular frontend. Transports authorize calls;
// providers own validation and domain behavior, and actors never come from JSON.
package dataverbs

import (
	"context"
	"encoding/json"

	"github.com/hollis-labs/tachyon/internal/objects"
)

const ContractVersion = 1
const MaxInputBytes = 3 << 20

type Verb struct {
	Name        string         `json:"name"`
	Mutates     bool           `json:"mutates"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Provider is the host's explicit registration seam. No provider fallback or
// name-based identity matching occurs when another provider is added.
type Provider interface {
	ID() string
	Verbs() []Verb
	Invoke(context.Context, string, json.RawMessage, string) (json.RawMessage, error)
}

type IDRequest struct {
	ID string `json:"id"`
}
type RevisionRequest struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expected_revision"`
}
type CreateRequest struct {
	ID         string          `json:"id"`
	Kind       objects.Kind    `json:"kind"`
	Definition json.RawMessage `json:"definition"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}
type PatchRequest struct {
	RevisionRequest
	Definition json.RawMessage `json:"definition,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}
type HistoryRequest struct {
	ID            string `json:"id"`
	AfterRevision int64  `json:"after_revision,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}
type LinksRequest struct {
	ID       string `json:"id"`
	Incoming bool   `json:"incoming,omitempty"`
}
type MembersRequest struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expected_revision,omitempty"`
	Offset           int    `json:"offset,omitempty"`
	Limit            int    `json:"limit,omitempty"`
}
type SetMembersRequest struct {
	RevisionRequest
	Members []objects.Reference `json:"members"`
}
type RecordsPage struct {
	Items []objects.Record `json:"items"`
	// A full page returns a continuation. The final continuation may be empty.
	NextAfterID string `json:"next_after_id,omitempty"`
}
type HistoryPage struct {
	Items             []objects.Revision `json:"items"`
	NextAfterRevision int64              `json:"next_after_revision,omitempty"`
}
type LinksResult struct {
	Items []objects.Link `json:"items"`
}
