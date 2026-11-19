package chatplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ErrNativeAdmissionUnverified never authorizes a local association. It covers
// missing, failed and uncertain Tether evidence, including old session rows.
var ErrNativeAdmissionUnverified = errors.New("native continuation admission is unverified")

// TetherContinuationVerifier reads Tether-owned session rows with the existing
// sessions control client. It does not accept caller readiness claims.
type TetherContinuationVerifier struct {
	Base   string
	Client *http.Client
}

type tetherContinuationSession struct {
	ID              string `json:"id"`
	LogicalAgentID  string `json:"logical_agent_id"`
	ParentSessionID string `json:"parent_session_id"`
	Intent          string `json:"intent"`
	State           string `json:"state"`
	WorkerID        string `json:"worker_id"`
	WorkerLineage   *struct {
		Kind                 string `json:"kind"`
		SourceSessionID      string `json:"source_session_id"`
		ReplacementSessionID string `json:"replacement_session_id"`
		LogicalAgentID       string `json:"logical_agent_id"`
		SourceWorkerID       string `json:"source_worker_id"`
		WorkerID             string `json:"worker_id"`
		CommandID            string `json:"command_id"`
		AcknowledgedAt       string `json:"acknowledged_at"`
	} `json:"worker_lineage"`
	ReplacementLineage *struct {
		SourceActorURI       string `json:"source_actor_uri"`
		ReplacementActorURI  string `json:"replacement_actor_uri"`
		CommittedAt          string `json:"committed_at"`
		Kind                 string `json:"kind"`
		SourceSessionID      string `json:"source_session_id"`
		ReplacementSessionID string `json:"replacement_session_id"`
		LogicalAgentID       string `json:"logical_agent_id"`
		ActorURI             string `json:"actor_uri"`
		IntentKey            string `json:"intent_key"`
		BindingID            string `json:"binding_id"`
		BindingGeneration    int64  `json:"binding_generation"`
	} `json:"replacement_lineage"`
	NativeAdmission *struct {
		Kind                  string `json:"kind"`
		SourceSessionID       string `json:"source_session_id"`
		SourceGeneration      string `json:"source_generation"`
		ReplacementSessionID  string `json:"replacement_session_id"`
		ReplacementGeneration string `json:"replacement_generation"`
		WorkerID              string `json:"worker_id"`
		LogicalAgentID        string `json:"logical_agent_id"`
		NativeThreadID        string `json:"native_thread_id"`
		AdmittedAt            string `json:"admitted_at"`
	} `json:"native_admission"`
}

func (v *TetherContinuationVerifier) session(ctx context.Context, id string) (tetherContinuationSession, error) {
	var out tetherContinuationSession
	if v == nil || v.Client == nil || v.Base == "" {
		return out, ErrNativeAdmissionUnverified
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(v.Base, "/")+"/sessions/"+url.PathEscape(id), nil)
	if err != nil {
		return out, err
	}
	resp, err := v.Client.Do(r)
	if err != nil {
		return out, fmt.Errorf("read Tether session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, ErrNativeAdmissionUnverified
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out) != nil {
		return out, ErrNativeAdmissionUnverified
	}
	return out, nil
}

// Verify requires an exact parent/agent pair and the server-persisted native
// readiness receipt. A generic resume intent plus running state is insufficient.
func (v *TetherContinuationVerifier) Verify(ctx context.Context, source, replacement, agent string) error {
	old, err := v.session(ctx, source)
	if err != nil {
		return err
	}
	newSession, err := v.session(ctx, replacement)
	if err != nil {
		return err
	}
	a := newSession.NativeAdmission
	if old.ID != source || old.LogicalAgentID != agent || newSession.ID != replacement || newSession.LogicalAgentID != agent ||
		(old.State != "completed" && old.State != "failed" && old.State != "killed") ||
		newSession.ParentSessionID != source || newSession.Intent != "resume" || newSession.State != "running" ||
		a == nil || a.Kind != "codex_same_worker_v1" || a.SourceSessionID != source || a.ReplacementSessionID != replacement ||
		a.LogicalAgentID != agent || a.SourceGeneration == "" || a.ReplacementGeneration == "" || a.NativeThreadID == "" || a.AdmittedAt == "" ||
		a.WorkerID == "" || old.WorkerID != a.WorkerID || newSession.WorkerID != a.WorkerID {
		return ErrNativeAdmissionUnverified
	}
	return nil
}

// VerifyHubLineage verifies UI lineage after a hub Restart. It makes no claim
// about native model context and cannot authorize a worker association. The
// public native-continuation endpoint deliberately continues to use Verify.
func (v *TetherContinuationVerifier) VerifyHubLineage(ctx context.Context, source, replacement, agent string) error {
	if source == "" || replacement == "" || source == replacement || agent == "" {
		return ErrNativeAdmissionUnverified
	}
	old, err := v.session(ctx, source)
	if err != nil {
		return err
	}
	next, err := v.session(ctx, replacement)
	if err != nil {
		return err
	}
	if old.ID != source || next.ID != replacement || old.LogicalAgentID != agent || next.LogicalAgentID != agent ||
		old.WorkerID != "" || next.WorkerID != "" ||
		(old.State != "completed" && old.State != "failed" && old.State != "killed") ||
		next.ParentSessionID != source || next.Intent != "resume" || next.State != "running" {
		return errors.New("hub restart lineage is unverified")
	}
	return nil
}
