// Package observefeed defines the private host-to-Observe metadata carrier.
// It deliberately has no free-form fields or user-supplied correlation values.
package observefeed

import (
	"fmt"
	"regexp"
	"time"
)

const Command = "tachyon_host_observe"

// TokenConfig is injected only into the subprocess init handshake, never into
// settings declarations, persisted settings, the registry or recorded metadata.
const TokenConfig = "__tachyon_observe_token"
const MaxBatch = 32
const MaxBytes = 64 << 10

type Record struct {
	ID         string    `json:"id"`
	Sequence   uint64    `json:"sequence"`
	Timestamp  time.Time `json:"timestamp"`
	Kind       string    `json:"kind"`
	Plugin     string    `json:"plugin"`
	Generation string    `json:"generation,omitempty"`
	Operation  string    `json:"operation,omitempty"`
	Module     string    `json:"module,omitempty"`
	Verb       string    `json:"verb,omitempty"`
	Method     string    `json:"method,omitempty"`
	Effect     string    `json:"effect,omitempty"`
	Status     string    `json:"status"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	Reason     string    `json:"reason,omitempty"`
}

type Counters struct {
	LifecycleOverflow uint64 `json:"lifecycle_overflow"`
	QueueWaitTimeouts uint64 `json:"queue_wait_timeouts"`
	Overflow          uint64 `json:"overflow"`
	Unavailable       uint64 `json:"unavailable"`
	DeliveryFailed    uint64 `json:"delivery_failed"`
	Delivered         uint64 `json:"delivered"`
}

type Batch struct {
	Epoch    string   `json:"epoch"`
	Records  []Record `json:"records"`
	Counters Counters `json:"counters"`
}

type Ingest struct {
	Token string `json:"token"`
	Batch Batch  `json:"batch"`
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_.:/-]{1,128}$`)

func Identifier(s string) bool { return identifier.MatchString(s) }

func (b Batch) Validate() error {
	if !Identifier(b.Epoch) || len(b.Records) > MaxBatch {
		return fmt.Errorf("invalid host batch")
	}
	for _, r := range b.Records {
		if !Identifier(r.ID) || !Identifier(r.Plugin) || r.Sequence == 0 || r.Timestamp.IsZero() || r.DurationMS < 0 {
			return fmt.Errorf("invalid host record")
		}
		for _, id := range []string{r.Generation, r.Operation, r.Module, r.Verb} {
			if id != "" && !Identifier(id) {
				return fmt.Errorf("invalid host identity")
			}
		}
		if !oneOf(r.Kind, "operation_start", "operation_result", "plugin_load", "plugin_restart", "plugin_unload", "plugin_failure", "plugin_retire") || !oneOf(r.Status, "started", "ok", "ask", "error", "unknown") || !oneOf(r.Effect, "", "unknown", "reads", "writes", "destroys", "open_world") || !oneOf(r.Reason, "", "transport", "invalid_result", "admission", "spawn", "settings", "handshake", "load", "discovery", "declaration", "validation", "collision", "host_stop", "timeout", "transport_error", "canceled", "invalid_response", "unload_failed", "restart_failed") || !oneOf(r.Method, "", "command/execute", "crud/list", "crud/read", "crud/create", "crud/update", "crud/delete", "other") {
			return fmt.Errorf("invalid host classification")
		}
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, a := range allowed {
		if value == a {
			return true
		}
	}
	return false
}
