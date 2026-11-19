package dataverbs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tachyon/next/internal/access"
	"github.com/hollis-labs/tachyon/next/internal/approvals"
)

func gateVerbs() []Verb {
	return []Verb{
		{Name: "gate.enqueue", Method: "POST", Mutates: true, Description: "Enqueue an immutable gate_native_v1 request."},
		{Name: "gate.get", Method: "GET", Description: "Read the scoped native HITL RetrievalResult."},
		{Name: "gate.await", Method: "GET", Description: "Await the scoped native HITL RetrievalResult; a timeout does not withdraw."},
		{Name: "gate.withdraw", Method: "POST", Mutates: true, Description: "Withdraw a scoped native item; returns its HITL Outcome."},
		{Name: "gate.assess-response", Method: "GET", Description: "Read current responder authority for an actual terminal outcome; observation only."},
	}
}

func gateInvoke(ctx context.Context, i *approvals.Inbox, a access.Agent, name string, raw json.RawMessage) (json.RawMessage, error) {
	if i == nil || i.Native == nil {
		return nil, &hitl.CodedError{Code: "unavailable", Message: "Native gate integration is unavailable."}
	}
	g := i.Native
	if a.Identity.ID != g.ProducerIdentity || !a.Allows(name, "") {
		return nil, hitl.ErrUnauthorized
	}
	caller := func(c hitl.CallerAssertion) error {
		if c.ApplicationID != g.CallerScope {
			return hitl.ErrUnauthorized
		}
		return nil
	}
	// Do not let this family retrieve or withdraw an old card in the same namespace.
	scopedItem := func(id string) error {
		rec, err := i.Store.Get(ctx, id)
		if err != nil {
			return err
		}
		if rec.CallerScope != g.CallerScope || rec.Kind != approvals.NativeKind {
			return hitl.ErrNotFound
		}
		return nil
	}
	var result any
	var err error
	switch name {
	case "gate.enqueue":
		var q hitl.EnqueueRequest
		if json.Unmarshal(raw, &q) != nil {
			return nil, hitl.ErrInvalidRequest
		}
		if q.Source.ApplicationID != g.CallerScope {
			return nil, hitl.ErrUnauthorized
		}
		if _, err = approvals.NativeRequest(q); err != nil {
			return nil, err
		}
		result, err = i.Service.Enqueue(ctx, q)
	case "gate.get":
		q, e := hitl.DecodeGetCommand(raw)
		if e != nil {
			return nil, e
		}
		if e = caller(q.Caller); e != nil {
			return nil, e
		}
		if e = scopedItem(q.ItemID); e != nil {
			return nil, e
		}
		result, err = i.Service.Get(ctx, q)
	case "gate.await":
		q, e := hitl.DecodeAwaitCommand(raw)
		if e != nil {
			return nil, e
		}
		if e = caller(q.Caller); e != nil {
			return nil, e
		}
		if e = scopedItem(q.ItemID); e != nil {
			return nil, e
		}
		result, err = i.Service.Await(ctx, q)
	case "gate.withdraw":
		q, e := hitl.DecodeWithdrawCommand(raw)
		if e != nil {
			return nil, e
		}
		if e = caller(q.Caller); e != nil {
			return nil, e
		}
		if e = scopedItem(q.ItemID); e != nil {
			return nil, e
		}
		result, err = i.Service.Withdraw(ctx, q)
	case "gate.assess-response":
		var q approvals.AssessResponseCommand
		if e := decode(raw, &q); e != nil {
			return nil, hitl.ErrInvalidRequest
		}
		if e := caller(q.Caller); e != nil {
			return nil, e
		}
		if e := scopedItem(q.ItemID); e != nil {
			return nil, e
		}
		result, err = i.AssessResponse(ctx, q)
	default:
		return nil, hitl.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func nativeGateHTTP(i *approvals.Inbox, acc *access.Service, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	name := strings.TrimPrefix(r.URL.Path, AgentPrefix+"/providers/tachyon/verbs/")
	var verb *Verb
	for _, v := range gateVerbs() {
		if v.Name == name {
			verb = &v
			break
		}
	}
	if verb == nil {
		writeGateError(w, hitl.ErrNotFound)
		return
	}
	if r.Method != verb.Method {
		w.Header().Set("Allow", verb.Method)
		writeGateStatus(w, 405, &hitl.CodedError{Code: "method_not_allowed", Message: "Use the declared gate verb HTTP method."})
		return
	}
	p, _ := libauth.FromContext(r.Context())
	a, err := acc.Resolve(r.Context(), p)
	if err != nil {
		if errors.Is(err, access.ErrInactive) || errors.Is(err, access.ErrNotAgent) || errors.Is(err, access.ErrUnmapped) {
			err = hitl.ErrUnauthorized
		}
		writeGateError(w, err)
		return
	}
	if i == nil || i.Native == nil {
		writeGateError(w, &hitl.CodedError{Code: "unavailable", Message: "Native gate integration is unavailable."})
		return
	}
	if a.Identity.ID != i.Native.ProducerIdentity || !a.Allows(name, "") {
		writeGateError(w, hitl.ErrUnauthorized)
		return
	}
	raw, err := requestInput(w, r, verb.Method == http.MethodPost)
	if err != nil {
		writeGateError(w, fmt.Errorf("%w: invalid gate input", hitl.ErrInvalidRequest))
		return
	}
	data, err := gateInvoke(r.Context(), i, a, name, raw)
	if err != nil {
		writeGateError(w, err)
		return
	}
	writeJSON(w, response{Version: ContractVersion, Provider: "tachyon", Verb: name, Data: data})
}

func writeGateStatus(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(json.RawMessage(hitl.EncodeError(err)))
}
func writeGateError(w http.ResponseWriter, err error) {
	status := 503
	switch {
	case errors.Is(err, hitl.ErrInvalidRequest):
		status = 400
	case errors.Is(err, hitl.ErrNotFound):
		status = 404
	case errors.Is(err, hitl.ErrUnauthorized):
		status = 403
	case errors.Is(err, hitl.ErrStaleRevision), errors.Is(err, hitl.ErrTerminalConflict), errors.Is(err, hitl.ErrIdempotencyConflict):
		status = 409
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = 408
	}
	if status == 503 {
		err = &hitl.CodedError{Code: "unavailable", Message: "Native gate evidence is unavailable; fetch the retained item before retrying."}
	}
	writeGateStatus(w, status, err)
}
