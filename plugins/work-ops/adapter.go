package main

import "context"

// WorkAdapter separates work tracking from its provider's HTTP contract.
type WorkAdapter interface {
	Capabilities(context.Context) (WorkCapabilities, error)
	CreateWorkItem(context.Context, CreateWorkRequest) (*WorkItem, error)
	GetWorkItem(context.Context, string) (*WorkItem, error)
	UpdateWorkItem(context.Context, string, UpdateWorkRequest) (*WorkItem, error)
	ListWorkItems(context.Context, WorkFilters) (*WorkList, error)
	SearchWorkItems(context.Context, string) (*WorkList, error)
	AssignWorkItem(context.Context, string, string) (*WorkItem, error)
	TransitionWorkItem(context.Context, string, string) (*WorkItem, error)
	AddComment(context.Context, string, CommentRequest) (*WorkComment, error)
}

// WorkCapabilities describes provider support without performing network IO.
type WorkCapabilities struct {
	Provider      string `json:"provider"`
	CanCreate     bool   `json:"can_create"`
	CanRead       bool   `json:"can_read"`
	CanUpdate     bool   `json:"can_update"`
	CanList       bool   `json:"can_list"`
	CanSearch     bool   `json:"can_search"`
	CanAssign     bool   `json:"can_assign"`
	CanTransition bool   `json:"can_transition"`
	CanComment    bool   `json:"can_comment"`
}

type WorkItem struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Status      string         `json:"status"`
	Priority    int            `json:"priority"`
	ProjectID   *string        `json:"project_id"`
	Manual      bool           `json:"manual"`
	Metadata    map[string]any `json:"metadata"`
}

type CreateWorkRequest struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Priority    int            `json:"priority,omitempty"`
	ProjectID   string         `json:"project_id,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// UpdateWorkRequest preserves omitted fields and explicit empty values.
// Status changes are separate, and scheduler settings are not exposed here.
type UpdateWorkRequest struct {
	Title       *string         `json:"title,omitempty"`
	Description *string         `json:"description,omitempty"`
	Priority    *int            `json:"priority,omitempty"`
	ProjectID   *string         `json:"project_id,omitempty"`
	Tags        *[]string       `json:"tags,omitempty"`
	Metadata    *map[string]any `json:"metadata,omitempty"`
}

type WorkFilters struct {
	Status    string `json:"status,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Tags      string `json:"tags,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

// WorkList retains Torque's pagination so callers do not mistake one page
// for the complete matching cohort. Search currently returns an unpaged list.
type WorkList struct {
	Tasks      []WorkItem `json:"tasks"`
	Total      int        `json:"total"`
	HasMore    bool       `json:"has_more"`
	NextOffset *int       `json:"next_offset,omitempty"`
}

type CommentRequest struct {
	Author  string `json:"author,omitempty"`
	Content string `json:"content"`
}

type WorkComment struct {
	ID       int64  `json:"id"`
	EntityID string `json:"entity_id"`
	Author   string `json:"author"`
	Content  string `json:"content"`
}
