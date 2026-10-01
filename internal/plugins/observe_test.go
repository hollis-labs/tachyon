package plugins

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/observefeed"
)

func feedProcess(t *testing.T, id string, caps contract.PluginCapabilities, command func(subprocess.CommandExecParams) (any, *subprocess.RPCError)) *pluginProcess {
	t.Helper()
	reader, input := io.Pipe()
	output, writer := io.Pipe()
	t.Cleanup(func() { reader.Close(); input.Close(); output.Close(); writer.Close() })
	go func() {
		dec, enc := json.NewDecoder(reader), json.NewEncoder(writer)
		for {
			var req struct {
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if dec.Decode(&req) != nil {
				return
			}
			var result any = struct{}{}
			var rpcErr *subprocess.RPCError
			if req.Method == "command/execute" {
				var args subprocess.CommandExecParams
				_ = json.Unmarshal(req.Params, &args)
				if args.Name == "plugin_capabilities" {
					raw, _ := json.Marshal(caps)
					result = subprocess.CommandExecResult{Action: "message", Content: string(raw)}
				} else {
					result, rpcErr = command(args)
				}
			}
			raw, _ := json.Marshal(result)
			if enc.Encode(subprocess.RPCResponse{JSONRPC: "2.0", Result: raw, Error: rpcErr}) != nil {
				return
			}
		}
	}()
	return &pluginProcess{id: id, stdin: input, stdout: output, stdoutDec: json.NewDecoder(output)}
}

func feedCaps(module string) contract.PluginCapabilities {
	return contract.PluginCapabilities{Modules: []string{module}, Verbs: map[string]contract.VerbDeclaration{
		module + "_read": {Effect: contract.EffectReads}, module + "_write": {Effect: contract.EffectWrites}, module + "_ask": {Effect: contract.EffectOpenWorld}, module + "_error": {Effect: contract.EffectDestroys},
	}}
}

func envelopeCommand(p subprocess.CommandExecParams) (any, *subprocess.RPCError) {
	status := "ok"
	if strings.HasSuffix(p.Name, "_ask") {
		status = "ask"
	}
	if strings.HasSuffix(p.Name, "_error") {
		status = "error"
	}
	return subprocess.CommandExecResult{Action: "message", Content: `{"status":"` + status + `","data":{"secret":"OUTPUT-SECRET"},"ask":{"prompt":"PROMPT-SECRET"},"error":{"message":"UPSTREAM-SECRET","detail":"CREDENTIAL-SECRET"}}`}, nil
}

func queuedRecords(m *Manager) []observefeed.Record {
	m.observe.mu.Lock()
	defer m.observe.mu.Unlock()
	return append([]observefeed.Record(nil), m.observe.queue...)
}

func TestObserveFeedVerbOutcomesAndPrivacy(t *testing.T) {
	m := watchdogManager()
	p := feedProcess(t, "work-ops", feedCaps("work"), envelopeCommand)
	if err := m.initializePlugin(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"read", "write", "ask", "error"} {
		raw, err := m.InvokeVerb(context.Background(), "work_"+suffix, json.RawMessage(`{"config":"CONFIG-SECRET","payload":"PAYLOAD-SECRET","credential":"CREDENTIAL-SECRET"}`))
		if err != nil || !strings.Contains(string(raw), "OUTPUT-SECRET") {
			t.Fatal("operation changed", err, string(raw))
		}
	}
	records := queuedRecords(m)
	if len(records) != 8 {
		t.Fatal("start/result count", len(records))
	}
	for i, status := range []string{"ok", "ok", "ask", "error"} {
		start, result := records[2*i], records[2*i+1]
		if start.Kind != "operation_start" || result.Kind != "operation_result" || result.Status != status || start.Operation != result.Operation || result.DurationMS < 0 || start.Effect == "unknown" || start.Generation == "" || start.Module != "work" {
			t.Fatalf("bad operation metadata: %+v %+v", start, result)
		}
	}
	raw, _ := json.Marshal(records)
	for _, secret := range []string{"PAYLOAD-SECRET", "CONFIG-SECRET", "CREDENTIAL-SECRET", "OUTPUT-SECRET", "PROMPT-SECRET", "UPSTREAM-SECRET"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("secret entered recorder", secret)
		}
	}
	// The legacy command entry point must report the same declared effect/outcome.
	if _, err := m.CallPlugin(context.Background(), p.id, "command/execute", subprocess.CommandExecParams{Name: "work_ask", Args: "PROMPT-SECRET"}); err != nil {
		t.Fatal(err)
	}
	last := queuedRecords(m)
	if last[len(last)-1].Status != "ask" || last[len(last)-1].Effect != "open_world" {
		t.Fatal(last)
	}
	if _, err := m.CallPlugin(context.Background(), p.id, "command/execute", subprocess.CommandExecParams{Name: observefeed.Command}); err == nil {
		t.Fatal("private command public")
	}
}

func TestObserveFeedLifecycleOrderAndRetirement(t *testing.T) {
	m := watchdogManager()
	m.spawn = func(context.Context, string) (*pluginProcess, error) {
		return feedProcess(t, "work-ops", feedCaps("work"), envelopeCommand), nil
	}
	if err := m.LoadPlugin(context.Background(), "fake-work"); err != nil {
		t.Fatal(err)
	}
	if err := m.RestartPlugin("work-ops"); err != nil {
		t.Fatal(err)
	}
	m.lifecycleMu.Lock()
	m.unloadPlugin(context.Background(), "work-ops")
	m.lifecycleMu.Unlock()
	want := []string{"plugin_load:ok", "plugin_restart:started", "plugin_unload:ok", "plugin_load:ok", "plugin_restart:ok", "plugin_unload:ok"}
	for i, r := range queuedRecords(m) {
		if i >= len(want) || r.Kind+":"+r.Status != want[i] {
			t.Fatalf("lifecycle order: %+v", queuedRecords(m))
		}
	}
	if len(queuedRecords(m)) != len(want) {
		t.Fatal("missing lifecycle")
	}
	oldGeneration := queuedRecords(m)[0].Generation
	if oldGeneration == queuedRecords(m)[3].Generation {
		t.Fatal("restart reused generation")
	}
	// Admission failure is ordered on the same lifecycle path.
	m.spawn = func(context.Context, string) (*pluginProcess, error) {
		caps := feedCaps("work")
		caps.Verbs["work_bad"] = contract.VerbDeclaration{Effect: "invalid"}
		return feedProcess(t, "work-ops", caps, envelopeCommand), nil
	}
	if m.LoadPlugin(context.Background(), "fake-work") == nil {
		t.Fatal("invalid admission")
	}
	last := queuedRecords(m)
	if last[len(last)-1].Kind != "plugin_failure" || last[len(last)-1].Reason != "admission" {
		t.Fatal(last)
	}
	proc, _, _ := stalledProcess(t, "decode")
	registerStalled(m, proc)
	if _, err := m.CallPlugin(context.Background(), proc.id, "command/execute", subprocess.CommandExecParams{Name: "hung_list"}); err == nil {
		t.Fatal("hung call succeeded")
	}
	awaitCondition(t, func() bool { m.mu.RLock(); defer m.mu.RUnlock(); return m.plugins[proc.id] == nil })
	awaitCondition(t, func() bool {
		records := queuedRecords(m)
		return len(records) > 0 && records[len(records)-1].Kind == "plugin_retire"
	})
	last = queuedRecords(m)
	if last[len(last)-2].Kind != "plugin_failure" || last[len(last)-1].Kind != "plugin_retire" || last[len(last)-1].Reason != "timeout" {
		t.Fatalf("retirement order: %+v", last)
	}
	batch := observefeed.Batch{Epoch: m.observe.epoch, Records: last[len(last)-2:]}
	if err := batch.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestObserveAbsentAndOverflowAreBounded(t *testing.T) {
	m := watchdogManager()
	p := feedProcess(t, "work-ops", feedCaps("work"), envelopeCommand)
	if err := m.initializePlugin(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < observeQueueCapacity; n++ {
		if _, err := m.InvokeVerb(context.Background(), "work_write", nil); err != nil {
			t.Fatal(err)
		}
	}
	m.observe.mu.Lock()
	length, lost := len(m.observe.queue), m.observe.counters.Overflow
	m.observe.mu.Unlock()
	if length != observeQueueCapacity || lost != observeQueueCapacity {
		t.Fatal(length, lost)
	}
	// Once a worker exists, loss due to Observe disappearing is counted separately.
	m.observe.start(m)
	t.Cleanup(m.observe.stop)
	awaitCondition(t, func() bool {
		m.observe.mu.Lock()
		defer m.observe.mu.Unlock()
		return m.observe.counters.Unavailable == observeQueueCapacity
	})
}

func TestObserveHungDeliveryDoesNotHoldOtherPluginOrManagerLocks(t *testing.T) {
	m := watchdogManager()
	entered, release := make(chan struct{}), make(chan struct{})
	observer := feedProcess(t, "observe-ops", feedCaps("observe"), func(p subprocess.CommandExecParams) (any, *subprocess.RPCError) {
		if p.Name == observefeed.Command {
			close(entered)
			<-release
		}
		return subprocess.CommandExecResult{Action: "message", Content: `{"accepted":true}`}, nil
	})
	t.Cleanup(func() { close(release) })
	observer.observeToken = "private-marker"
	if err := m.initializePlugin(context.Background(), observer); err != nil {
		t.Fatal(err)
	}
	m.observe.start(m)
	t.Cleanup(m.observe.stop)
	worker := feedProcess(t, "work-ops", feedCaps("work"), envelopeCommand)
	if err := m.initializePlugin(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InvokeVerb(context.Background(), "work_write", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("no delivery")
	}
	done := make(chan error, 1)
	go func() { _, err := m.InvokeVerb(context.Background(), "work_read", nil); done <- err }()
	if err := awaitError(t, done); err != nil {
		t.Fatal(err)
	}
	if observer.dead.Load() {
		t.Fatal("other operation waited for Observe watchdog")
	}
	// Delivery is blocked on Observe's pipe; these locks still remain available.
	m.mu.Lock()
	m.mu.Unlock()
	m.lifecycleMu.Lock()
	m.lifecycleMu.Unlock()
	awaitCondition(t, func() bool { return observer.dead.Load() })
	awaitCondition(t, func() bool {
		m.observe.mu.Lock()
		defer m.observe.mu.Unlock()
		return m.observe.counters.DeliveryFailed > 0
	})
	if _, err := m.InvokeVerb(context.Background(), "work_write", nil); err != nil {
		t.Fatal("other plugin lost", err)
	}
}

func TestObserveDeliveryNoRecursionAndConcurrentOrdering(t *testing.T) {
	m := watchdogManager()
	var mu sync.Mutex
	var records []observefeed.Record
	var counters observefeed.Counters
	observer := feedProcess(t, "observe-ops", feedCaps("observe"), func(p subprocess.CommandExecParams) (any, *subprocess.RPCError) {
		if p.Name == observefeed.Command {
			var in observefeed.Ingest
			if err := json.Unmarshal([]byte(p.Args), &in); err != nil || in.Token != "private-marker" || in.Batch.Validate() != nil {
				t.Error("bad private carrier", err)
			}
			mu.Lock()
			records = append(records, in.Batch.Records...)
			counters = in.Batch.Counters
			mu.Unlock()
			return subprocess.CommandExecResult{Action: "message", Content: `{"accepted":true}`}, nil
		}
		return envelopeCommand(p)
	})
	observer.observeToken = "private-marker"
	if err := m.initializePlugin(context.Background(), observer); err != nil {
		t.Fatal(err)
	}
	m.observe.start(m)
	t.Cleanup(m.observe.stop)
	worker := feedProcess(t, "work-ops", feedCaps("work"), envelopeCommand)
	if err := m.initializePlugin(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := 0; n < 40; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.InvokeVerb(context.Background(), "work_write", nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	awaitCondition(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(records) == 80 && counters.Delivered == 80 })
	for n := 0; n < 5; n++ {
		if _, err := m.InvokeVerb(context.Background(), "observe_read", nil); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(records) != 80 {
		t.Fatal("Observe recursed", len(records))
	}
	for n, r := range records {
		if r.Sequence != uint64(n+1) || r.Plugin != "work-ops" {
			t.Fatal("ordering/recursion", r)
		}
	}
}

func TestObserveBusyWireLosesTelemetryWithoutKillingActiveRead(t *testing.T) {
	m := watchdogManager()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	observer := feedProcess(t, "observe-ops", feedCaps("observe"), func(p subprocess.CommandExecParams) (any, *subprocess.RPCError) {
		if p.Name == observefeed.Command {
			return subprocess.CommandExecResult{Action: "message", Content: `{"accepted":true}`}, nil
		}
		close(entered)
		<-release
		return envelopeCommand(p)
	})
	observer.observeToken = "private-marker"
	if err := m.initializePlugin(context.Background(), observer); err != nil {
		t.Fatal(err)
	}
	m.observe.start(m)
	t.Cleanup(m.observe.stop)
	done := make(chan error, 1)
	go func() { _, err := m.InvokeVerb(context.Background(), "observe_read", nil); done <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("read never started")
	}
	worker := feedProcess(t, "work-ops", feedCaps("work"), envelopeCommand)
	if err := m.initializePlugin(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InvokeVerb(context.Background(), "work_write", nil); err != nil {
		t.Fatal(err)
	}
	awaitCondition(t, func() bool {
		m.observe.mu.Lock()
		defer m.observe.mu.Unlock()
		return m.observe.counters.DeliveryFailed > 0
	})
	if observer.dead.Load() {
		t.Fatal("queue deadline killed an active Observe read")
	}
	releaseOnce.Do(func() { close(release) })
	if err := awaitError(t, done); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InvokeVerb(context.Background(), "work_read", nil); err != nil {
		t.Fatal(err)
	}
	awaitCondition(t, func() bool { m.observe.mu.Lock(); defer m.observe.mu.Unlock(); return m.observe.counters.Delivered > 0 })
	if observer.dead.Load() {
		t.Fatal("healthy Observe was retired")
	}
}

func TestObserveFeedTransportErrorDoesNotExposeBodyOrRetry(t *testing.T) {
	m := watchdogManager()
	var calls atomic.Int32
	p := feedProcess(t, "work-ops", feedCaps("work"), func(params subprocess.CommandExecParams) (any, *subprocess.RPCError) {
		calls.Add(1)
		return nil, &subprocess.RPCError{Code: -32000, Message: "UPSTREAM-CREDENTIAL-SECRET"}
	})
	if err := m.initializePlugin(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InvokeVerb(context.Background(), "work_write", nil); err == nil {
		t.Fatal("missing RPC error")
	}
	if p.dead.Load() || calls.Load() != 1 {
		t.Fatal("changed RPC semantics or retried write", calls.Load())
	}
	records := queuedRecords(m)
	if len(records) != 2 || records[1].Status != "error" || records[1].Effect != "writes" {
		t.Fatal(records)
	}
	raw, _ := json.Marshal(records)
	if strings.Contains(string(raw), "UPSTREAM-CREDENTIAL-SECRET") {
		t.Fatal("upstream error leaked")
	}
	for _, params := range []any{subprocess.CommandExecParams{Name: observefeed.Command}, map[string]string{"name": observefeed.Command}, struct {
		Name string `json:"name"`
	}{observefeed.Command}, json.RawMessage(`{"name":"tachyon_host_observe"}`)} {
		if _, err := m.CallPlugin(context.Background(), p.id, "command/execute", params); err == nil {
			t.Fatal("private command public for", params)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("private carrier reached public pipe")
	}
}

func TestObservePrivateCommandCannotBeAdmittedAsPublicVerb(t *testing.T) {
	m := watchdogManager()
	caps := contract.PluginCapabilities{Modules: []string{"tachyon"}, Verbs: map[string]contract.VerbDeclaration{observefeed.Command: {Effect: contract.EffectReads}}}
	p := feedProcess(t, "forged", caps, envelopeCommand)
	if err := m.initializePlugin(context.Background(), p); err == nil {
		t.Fatal("private ingestion admitted as public verb")
	}
	if len(m.AllCapabilities()) != 0 || len(m.BuildRegistry().Plugins) != 0 {
		t.Fatal("rejected private verb advertised")
	}
}

func TestShutdownStopsHungObserveWorker(t *testing.T) {
	m := watchdogManager()
	entered, release := make(chan struct{}), make(chan struct{})
	observer := feedProcess(t, "observe-ops", feedCaps("observe"), func(p subprocess.CommandExecParams) (any, *subprocess.RPCError) {
		close(entered)
		<-release
		return nil, nil
	})
	t.Cleanup(func() { close(release) })
	observer.observeToken = "private-marker"
	if err := m.initializePlugin(context.Background(), observer); err != nil {
		t.Fatal(err)
	}
	m.observe.start(m)
	t.Cleanup(m.observe.stop)
	worker := feedProcess(t, "work-ops", feedCaps("work"), envelopeCommand)
	if err := m.initializePlugin(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InvokeVerb(context.Background(), "work_write", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("no delivery")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Shutdown(ctx) }()
	if err := awaitError(t, done); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.observe.done:
	default:
		t.Fatal("delivery worker leaked")
	}
	if len(m.BuildRegistry().Plugins) != 0 || !observer.dead.Load() || !worker.dead.Load() {
		t.Fatal("shutdown left processes registered")
	}
}
