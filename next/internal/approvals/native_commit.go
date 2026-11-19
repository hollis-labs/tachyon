package approvals

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/hollis-labs/libs/mesh/core/gate"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/next/internal/store"
)

// ErrNativeUnconfirmed never carries a usable outcome. Raw retained bytes may
// contain a choice whose COMMIT completed late or whose acknowledgement was lost.
var ErrNativeUnconfirmed = errors.New("native outcome requires commit reconciliation")

type nativeAuthorizationKey struct{}

// nativeResponseQueue only schedules simultaneous browser submissions locally.
// It retains no decision/approval evidence. Every waiter rereads the owner after
// acquiring its turn; fresh producer/assessment reads still refuse missing proof.
type nativeResponseQueue struct {
	mu    sync.Mutex
	items map[string]*nativeResponseTurn
}
type nativeResponseTurn struct {
	token chan struct{}
	refs  int
}

func (q *nativeResponseQueue) acquire(ctx context.Context, id string) (func(), error) {
	q.mu.Lock()
	if q.items == nil {
		q.items = map[string]*nativeResponseTurn{}
	}
	turn := q.items[id]
	if turn == nil {
		turn = &nativeResponseTurn{token: make(chan struct{}, 1)}
		turn.token <- struct{}{}
		q.items[id] = turn
	}
	turn.refs++
	q.mu.Unlock()
	drop := func() {
		q.mu.Lock()
		turn.refs--
		if turn.refs == 0 {
			delete(q.items, id)
		}
		q.mu.Unlock()
	}
	select {
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-turn.token:
		return func() { turn.token <- struct{}{}; drop() }, nil
	}
}

// Classification reveals no outcome and confers no authority. It lets only the
// native browser path queue before its first authoritative view/read.
func (s *Store) isNativeRecord(ctx context.Context, id string) (bool, error) {
	var raw []byte
	if err := s.DB.Reader.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE id=?", id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, hitl.ErrNotFound
		}
		return false, err
	}
	var kind struct{ Kind string }
	if err := json.Unmarshal(raw, &kind); err != nil {
		return false, err
	}
	return kind.Kind == NativeKind, nil
}

type nativeAuthorization struct {
	Proposal       ProposedResponse
	BudgetDeadline time.Time
}

func checkNativeAuthorization(ctx context.Context, a nativeAuthorization) error {
	if err := checkWriteDeadline(ctx); err != nil {
		return err
	}
	now := time.Now()
	if a.BudgetDeadline.IsZero() || !now.Before(a.BudgetDeadline) || !now.Before(a.Proposal.ExpiresAt) {
		return context.DeadlineExceeded
	}
	return nil
}

// nativeCommitEvidence is private durability metadata on the sole owner row,
// never participant-supplied proof or another decision/grant. Only the process
// that observed phase one's successful return before expiry may write ObservedAt.
type nativeCommitEvidence struct {
	Version     string           `json:"version"`
	Attempt     string           `json:"attempt"`
	BindingHash string           `json:"binding_hash"`
	Proposal    ProposedResponse `json:"proposal"`
	ObservedAt  *time.Time       `json:"observed_at,omitempty"`
}

func nativeBindingHash(raw []byte, p ProposedResponse, attempt string) string {
	proposal, _ := json.Marshal(p)
	h := sha256.New()
	_, _ = h.Write([]byte("tachyon/native-commit/v1\x00"))
	_, _ = h.Write([]byte(attempt))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(raw)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(proposal)
	return hex.EncodeToString(h.Sum(nil))
}

func nativeEvidenceMatches(rec hitl.Record, raw []byte, e nativeCommitEvidence) bool {
	r, ok := rec.Outcome.(hitl.Resolved)
	_, nonceErr := hex.DecodeString(e.Attempt)
	if !ok || rec.Validate() != nil || e.Version != "1" || len(e.Attempt) != 32 || nonceErr != nil || e.BindingHash != nativeBindingHash(raw, e.Proposal, e.Attempt) {
		return false
	}
	var req hitl.EnqueueRequest
	if json.Unmarshal(rec.Request, &req) != nil {
		return false
	}
	n, err := NativeRequest(req)
	if err != nil {
		return false
	}
	p := e.Proposal
	o, err := nativeOption(n, r.Resolution.Response.Decision, p.AssessedAt)
	if err != nil || p.AssessedAt.IsZero() || !p.ExpiresAt.After(p.AssessedAt) || p.ExpiresAt.After(boundedExpiry(p.AssessedAt, n, o)) || r.Resolution.ResolvedAt.Before(p.AssessedAt) || !r.Resolution.ResolvedAt.Before(p.ExpiresAt) {
		return false
	}
	d, err := n.Digest()
	if err != nil || p.OwnerRef == "" {
		return false
	}
	want := ProposedResponse{ContractVersion: gate.ContractVersion, OwnerRef: p.OwnerRef, IdempotencyKey: n.IdempotencyKey, ItemID: rec.ItemID, RequestDigest: d, OperationDigest: n.OperationDigest, Binding: n.Binding, NativeRequestID: n.NativeRequestID, ExpectedRevision: rec.Revision - 1, PresentedRevision: rec.Revision - 1, OptionID: o.ID, Participant: r.Resolution.Participant, ActorScopes: []string{"tachyon.operator", "tachyon.approvals.resolve"}, AssessedAt: p.AssessedAt, ExpiresAt: p.ExpiresAt}
	a, _ := json.Marshal(p)
	b, _ := json.Marshal(want)
	return string(a) == string(b)
}

func validateNativeCommit(rec hitl.Record, raw, evidence []byte) error {
	if rec.Kind != NativeKind || rec.State != hitl.StateResolved {
		return nil
	}
	var e nativeCommitEvidence
	if json.Unmarshal(evidence, &e) != nil || !nativeEvidenceMatches(rec, raw, e) || e.ObservedAt == nil || e.ObservedAt.Before(rec.UpdatedAt) || !e.ObservedAt.Before(e.Proposal.ExpiresAt) {
		return ErrNativeUnconfirmed
	}
	return nil
}

func (s *Store) swapNative(ctx context.Context, id string, expected int64, next hitl.Record) error {
	authorization, ok := ctx.Value(nativeAuthorizationKey{}).(nativeAuthorization)
	if !ok {
		return hitl.ErrUnauthorized
	}
	p := authorization.Proposal
	raw, err := recordBytes(next)
	if err != nil {
		return err
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	attempt := hex.EncodeToString(nonce[:])
	e := nativeCommitEvidence{Version: "1", Attempt: attempt, BindingHash: nativeBindingHash(raw, p, attempt), Proposal: p}
	if !nativeEvidenceMatches(next, raw, e) {
		return hitl.ErrUnauthorized
	}
	initial, err := json.Marshal(e)
	if err != nil {
		return err
	}
	initialRecord, err := recordBytesWithEvidence(next, initial)
	if err != nil {
		return err
	}
	// Phase one commits the immutable choice and its unconfirmed descriptor.
	// Neither derived terminal rows nor notifications are published here.
	attempted := false
	err = s.DB.Writer.Submit(ctx, "native_response_unconfirmed", func(ctx context.Context, tx store.Tx) error {
		if deadlineErr := checkNativeAuthorization(ctx, authorization); deadlineErr != nil {
			return deadlineErr
		}
		prev, writeErr := readRecord(tx.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE id=?", id))
		if writeErr != nil {
			return writeErr
		}
		if writeErr = hitl.CheckSwap(prev, expected, next); writeErr != nil {
			return writeErr
		}
		if _, writeErr = tx.ExecContext(ctx, "UPDATE approval_items SET state=?,revision=?,terminal=1,record=? WHERE id=?", next.State, next.Revision, initialRecord, id); writeErr != nil {
			return writeErr
		}
		attempted = true
		// Remove the old pending projection while the response is quarantined.
		quarantineIndexTx(ctx, tx, next)
		return checkNativeAuthorization(ctx, authorization)
	})
	observed := time.Now() // capture immediately after COMMIT returns, never on a later read
	if err != nil {
		// An attempted write can have an uncertain COMMIT acknowledgement.
		if attempted {
			return ErrNativeUnconfirmed
		}
		return err
	}
	if checkNativeAuthorization(ctx, authorization) != nil || !observed.Before(p.ExpiresAt) || !observed.Before(authorization.BudgetDeadline) {
		return ErrNativeUnconfirmed
	}
	confirmed, err := nativeConfirmationBytes(next, raw, e, observed)
	if err != nil {
		return err
	}
	confirmedRecord, err := recordBytesWithEvidence(next, confirmed)
	if err != nil {
		return err
	}
	// Phase two may persist later: it records the captured historical observation,
	// not a new decision or an assertion that this second COMMIT was on time.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	err = s.DB.Writer.Submit(finishCtx, "native_response_confirm", func(ctx context.Context, tx store.Tx) error {
		if confirmErr := confirmNativeRecord(ctx, tx, id, next.Revision, initialRecord, confirmedRecord); confirmErr != nil {
			return confirmErr
		}
		indexTx(ctx, tx, next)
		logTerminalTx(ctx, tx, next)
		return nil
	})
	if err != nil {
		// A persisted exact witness may be read normally, but never synthesize or
		// replay one here after an uncertain acknowledgement.
		return ErrNativeUnconfirmed
	}
	s.publish(next, false)
	return nil
}

// authoritativeEvents also quarantines old derived rows after an upgrade or a
// damaged witness. Derived tables cannot override the owner record's read gate.
func (s *Store) authoritativeEvents(ctx context.Context, events []TimelineRow) ([]TimelineRow, error) {
	unavailable, err := s.NativeUnavailable(ctx)
	if err != nil {
		return nil, err
	}
	quarantined := make(map[string]bool, len(unavailable))
	for _, id := range unavailable {
		quarantined[id] = true
	}
	out := make([]TimelineRow, 0, len(events))
	for _, e := range events {
		if !quarantined[e.ItemID] {
			out = append(out, e)
		}
	}
	return out, nil
}

// NativeUnavailable reports only operational identifiers, never a choice,
// participant, terminal revision or admission evidence. It does not repair rows.
func (s *Store) NativeUnavailable(ctx context.Context) ([]string, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, "SELECT record FROM approval_items WHERE state='resolved' ORDER BY created_ns,id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		rec, raw, evidence, err := readStoredRecord(rows)
		if err != nil {
			return nil, err
		}
		if errors.Is(validateNativeCommit(rec, raw, evidence), ErrNativeUnconfirmed) {
			out = append(out, rec.ItemID)
		}
	}
	return out, rows.Err()
}

func confirmNativeRecord(ctx context.Context, tx store.Tx, id string, revision int64, initialRecord, confirmedRecord []byte) error {
	res, writeErr := tx.ExecContext(ctx, "UPDATE approval_items SET record=? WHERE id=? AND revision=? AND record=?", confirmedRecord, id, revision, initialRecord)
	if writeErr != nil {
		return writeErr
	}
	n, writeErr := res.RowsAffected()
	if writeErr != nil {
		return writeErr
	}
	if n != 1 {
		var persisted []byte
		if readErr := tx.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE id=?", id).Scan(&persisted); readErr != nil {
			return readErr
		}
		if !bytes.Equal(persisted, confirmedRecord) {
			return ErrNativeUnconfirmed
		}
	}
	return nil
}

// Validate exactly the same evidence before B/publication as every reader.
// Clock movement cannot turn an unreadable witness into a successful response.
func nativeConfirmationBytes(rec hitl.Record, raw []byte, e nativeCommitEvidence, observed time.Time) ([]byte, error) {
	stamp := observed.UTC()
	e.ObservedAt = &stamp
	confirmed, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	if err = validateNativeCommit(rec, raw, confirmed); err != nil {
		return nil, err
	}
	return confirmed, nil
}
