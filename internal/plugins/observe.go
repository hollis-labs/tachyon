package plugins

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/observefeed"
)

const observeQueueCapacity = 256

// Telemetry may lose records; it never merits the ordinary 120s operation budget.
const observeDeliveryTimeout = time.Second

// observeRecorder has its own short-held lock. Recording only appends metadata;
// the worker releases it before taking a manager lock or serial subprocess pipe.
type observeRecorder struct {
	mu       sync.Mutex
	epoch    string
	sequence uint64
	queue    []observefeed.Record
	counters observefeed.Counters
	wake     chan struct{}
	cancel   context.CancelFunc
	done     chan struct{}
	closed   bool
}

func opaqueID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("host identity entropy unavailable")
	}
	return hex.EncodeToString(b[:])
}

func safeIdentity(value string) string {
	if value == "" || observefeed.Identifier(value) {
		return value
	}
	digest := sha256.Sum256([]byte(value))
	return "opaque-" + hex.EncodeToString(digest[:16])
}

func observeGeneration(proc *pluginProcess) string {
	if proc.observeGeneration != "" {
		return proc.observeGeneration
	}
	digest := sha256.Sum256([]byte(InvocationIdentity{proc: proc}.Generation()))
	return hex.EncodeToString(digest[:16])
}

func newObserveRecorder() *observeRecorder {
	return &observeRecorder{epoch: opaqueID(), wake: make(chan struct{}, 1)}
}

func (f *observeRecorder) record(r observefeed.Record) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.sequence++
	r.Sequence = f.sequence
	r.ID = fmt.Sprintf("%s-%d", f.epoch, f.sequence)
	r.Timestamp = time.Now().UTC()
	if len(f.queue) == observeQueueCapacity {
		f.counters.Overflow++
		return
	}
	f.queue = append(f.queue, r)
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *observeRecorder) start(m *Manager) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancel != nil || f.closed {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.done = make(chan struct{})
	go f.run(ctx, m)
}

func (f *observeRecorder) stop() {
	f.mu.Lock()
	f.closed = true
	cancel, done := f.cancel, f.done
	f.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (f *observeRecorder) take() observefeed.Batch {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := min(len(f.queue), observefeed.MaxBatch)
	b := observefeed.Batch{Epoch: f.epoch, Records: append([]observefeed.Record(nil), f.queue[:n]...), Counters: f.counters}
	f.queue = append(f.queue[:0], f.queue[n:]...)
	return b
}

func (f *observeRecorder) run(ctx context.Context, m *Manager) {
	defer close(f.done)
	var lastSent observefeed.Counters
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.wake:
		}
		for ctx.Err() == nil {
			batch := f.take()
			if len(batch.Records) == 0 && batch.Counters == lastSent {
				break
			}
			lastSent = batch.Counters
			m.mu.RLock()
			proc := m.plugins["observe-ops"]
			m.mu.RUnlock()
			available := proc != nil && !proc.dead.Load() && proc.observeToken != ""
			var err error
			if available {
				args, _ := json.Marshal(observefeed.Ingest{Token: proc.observeToken, Batch: batch})
				bounded, cancel := context.WithTimeout(ctx, observeDeliveryTimeout)
				var raw json.RawMessage
				// Private transport: no public CallPlugin instrumentation, no manager
				// or recorder locks held, and exact captured process identity.
				raw, err = callProcess(bounded, proc, "command/execute", subprocess.CommandExecParams{Name: observefeed.Command, Args: string(args)})
				cancel()
				if err == nil {
					var ack subprocess.CommandExecResult
					if json.Unmarshal(raw, &ack) != nil || ack.Action != "message" || ack.Content != `{"accepted":true}` {
						err = fmt.Errorf("ingestion rejected")
					}
				}
				if proc.dead.Load() {
					m.retireProcess(proc)
				}
			}
			f.mu.Lock()
			n := uint64(len(batch.Records))
			if !available {
				f.counters.Unavailable += n
			} else if err != nil {
				f.counters.DeliveryFailed += n
			} else {
				f.counters.Delivered += n
			}
			f.mu.Unlock()
			if err != nil {
				m.logger.Warn("Observe feed delivery failed", "reason_class", "delivery_failed")
			}
		}
	}
}

func isObserve(proc *pluginProcess) bool {
	if proc.id == "observe-ops" {
		return true
	}
	if proc.capabilities != nil {
		for _, module := range proc.capabilities.Modules {
			if module == "observe" {
				return true
			}
		}
	}
	return false
}

func (m *Manager) lifecycleRecord(proc *pluginProcess, id, kind, status, reason string) {
	if proc != nil && isObserve(proc) || id == "observe-ops" {
		return
	}
	r := observefeed.Record{Plugin: safeIdentity(id), Kind: kind, Status: status, Reason: reason}
	if r.Plugin == "" {
		r.Plugin = "unknown"
	}
	if proc != nil {
		r.Generation = observeGeneration(proc)
	}
	m.observe.record(r)
}

func (m *Manager) beginOperation(proc *pluginProcess, method, verb string) func(json.RawMessage, error) {
	if isObserve(proc) {
		return func(json.RawMessage, error) {}
	}
	r := observefeed.Record{Plugin: safeIdentity(proc.id), Generation: observeGeneration(proc), Kind: "operation_start", Status: "started", Operation: opaqueID(), Effect: "unknown"}
	switch method {
	case "command/execute", "crud/list", "crud/read", "crud/create", "crud/update", "crud/delete":
		r.Method = method
	default:
		r.Method = "other"
	}
	declared := false
	if proc.capabilities != nil {
		if decl, ok := proc.capabilities.Verbs[verb]; ok {
			declared = true
			r.Verb = safeIdentity(verb)
			r.Module = safeIdentity(proc.capabilities.ModuleForVerb(verb))
			r.Effect = string(decl.Effect)
		}
	}
	m.observe.record(r)
	started := time.Now()
	return func(raw json.RawMessage, err error) {
		r.Kind = "operation_result"
		r.Status = "ok"
		r.DurationMS = time.Since(started).Milliseconds()
		if err != nil {
			r.Status = "error"
			r.Reason = "transport"
		} else if declared {
			var env struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(raw, &env) != nil || (env.Status != "ok" && env.Status != "ask" && env.Status != "error") {
				r.Status = "unknown"
				r.Reason = "invalid_result"
			} else {
				r.Status = env.Status
			}
		}
		m.observe.record(r)
	}
}

func commandName(params any) string {
	switch p := params.(type) {
	case subprocess.CommandExecParams:
		return p.Name
	case *subprocess.CommandExecParams:
		if p != nil {
			return p.Name
		}
	case subprocess.CommandRequest:
		return p.Name
	case map[string]any:
		if name, ok := p["name"].(string); ok {
			return name
		}
	case json.RawMessage:
		var p2 struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(p, &p2)
		return p2.Name
	}
	return ""
}

func commandEnvelope(raw json.RawMessage) json.RawMessage {
	var result subprocess.CommandExecResult
	if json.Unmarshal(raw, &result) == nil && result.Action == "message" {
		return json.RawMessage(result.Content)
	}
	return nil
}

// Reserved commands are never routable through the public host APIs.
func PrivateCommand(name string) bool { return strings.EqualFold(name, observefeed.Command) }
