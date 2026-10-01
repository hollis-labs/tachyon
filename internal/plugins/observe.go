package plugins

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/observefeed"
)

const observeQueueCapacity = 256

// Lifecycle capacity is isolated so HTTP operation volume cannot starve failures.
const observeLifecycleCapacity = 64

// Polling reads may own the pipe for 750ms each; only the worker waits here.
const observeQueueTimeout = 30 * time.Second

// Avoid flooding logs when an old Observe binary rejects the private command.
const observeWarningInterval = 30 * time.Second

// Telemetry may lose records; it never merits the ordinary 120s operation budget.
const observeDeliveryTimeout = 2 * time.Second

// observeRecorder has its own short-held lock. Recording only appends metadata;
// the worker releases it before taking a manager lock or serial subprocess pipe.
type observeRecorder struct {
	mu           sync.Mutex
	epoch        string
	sequence     uint64
	queue        []observefeed.Record
	lifecycle    []observefeed.Record
	queueTimeout time.Duration // injectable before start, for fake-pipe tests
	ioTimeout    time.Duration
	lastWarning  time.Time
	counters     observefeed.Counters
	wake         chan struct{}
	cancel       context.CancelFunc
	done         chan struct{}
	closed       bool
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
	if strings.HasPrefix(r.Kind, "plugin_") {
		if len(f.lifecycle) == observeLifecycleCapacity {
			f.counters.LifecycleOverflow++
			return
		}
		f.lifecycle = append(f.lifecycle, r)
	} else {
		if len(f.queue) == observeQueueCapacity {
			f.counters.Overflow++
			return
		}
		f.queue = append(f.queue, r)
	}
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
	b := observefeed.Batch{Epoch: f.epoch, Counters: f.counters}
	for len(b.Records) < observefeed.MaxBatch && len(f.queue)+len(f.lifecycle) > 0 {
		source := &f.queue
		if len(f.lifecycle) > 0 && (len(f.queue) == 0 || f.lifecycle[0].Sequence < f.queue[0].Sequence) {
			source = &f.lifecycle
		}
		b.Records = append(b.Records, (*source)[0])
		(*source)[0] = observefeed.Record{}
		*source = (*source)[1:]
	}
	return b
}

func (f *observeRecorder) run(ctx context.Context, m *Manager) {
	defer close(f.done)

	for {
		select {
		case <-ctx.Done():
			return
		case <-f.wake:
		}
		for ctx.Err() == nil {
			batch := f.take()
			if len(batch.Records) == 0 {
				break
			}

			m.mu.RLock()
			proc := m.plugins["observe-ops"]
			m.mu.RUnlock()
			available := proc != nil && !proc.dead.Load() && proc.observeToken != ""
			var err error
			if available {
				args, _ := json.Marshal(observefeed.Ingest{Token: proc.observeToken, Batch: batch})
				queueBudget, ioBudget := observeQueueTimeout, observeDeliveryTimeout
				if f.queueTimeout > 0 {
					queueBudget = f.queueTimeout
				}
				if f.ioTimeout > 0 {
					ioBudget = f.ioTimeout
				}
				var raw json.RawMessage
				// Private transport: no public CallPlugin instrumentation, no manager
				// or recorder locks held, and exact captured process identity.
				for {
					raw, err = callProcessContextsBudgets(ctx, ctx, proc, "command/execute", subprocess.CommandExecParams{Name: observefeed.Command, Args: string(args)}, queueBudget, ioBudget)
					if ctx.Err() != nil {
						return
					}
					if !errors.Is(err, errCallQueueWait) {
						break
					}
					// Nothing was written. Keep the frozen batch in this worker, ahead of
					// both bounded queues, until it can acquire a wire or the host stops.
					f.mu.Lock()
					f.counters.QueueWaitTimeouts++
					f.mu.Unlock()
				}
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
				f.mu.Lock()
				now := time.Now()
				warn := f.lastWarning.IsZero() || now.Sub(f.lastWarning) >= observeWarningInterval
				if warn {
					f.lastWarning = now
				}
				f.mu.Unlock()
				if warn {
					m.logger.Warn("Observe feed delivery failed", "reason_class", "delivery_failed")
				}
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
	if isObserve(proc) || method == "command/execute" && verb == "config_schemas" {
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

func admissionReason(err error, fallback string) string {
	var admission *AdmissionError
	if errors.As(err, &admission) {
		return admission.Reason
	}
	return fallback
}
