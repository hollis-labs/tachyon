package catalogindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"
)

const (
	DefaultInterval = 30 * time.Second
	FullScanEvery   = 5 * time.Minute // files whose listing has no fingerprint are re-read this often
	maxFileBytes    = 256 << 10
)

// Index follows the Tether catalog and keeps the read model current.
type Index struct {
	Source   Source
	Store    *Store
	Interval time.Duration
	Bus      *Broker

	done    chan struct{}
	mu      sync.Mutex
	pending map[string]Snapshot // paths of the Builder's own applies awaiting the next pass
	seeded  bool
}

func (x *Index) interval() time.Duration {
	if x.Interval > 0 {
		return x.Interval
	}
	return DefaultInterval
}

// NoteApplied records that the Builder just applied planID over paths, so the next pass labels
// the resulting snapshots source "builder" (with the plan and the actor) instead of "observed",
// and refreshes immediately via Kick. Actor is an operator label, never a credential.
func (x *Index) NoteApplied(planID, actor string, paths []string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.pending == nil {
		x.pending = map[string]Snapshot{}
	}
	for _, p := range paths {
		x.pending[p] = Snapshot{Path: p, Source: "builder", PlanID: planID, Actor: actor}
	}
}

// Start runs the index in the background; Wait returns once it has stopped.
func (x *Index) Start(ctx context.Context) {
	if x.Bus == nil {
		x.Bus = NewBroker(time.Now)
	}
	x.done = make(chan struct{})
	go func() {
		defer close(x.done)
		x.Run(ctx)
	}()
}

// Wait blocks until an index started with Start has stopped, or ctx ends.
func (x *Index) Wait(ctx context.Context) {
	if x.done == nil {
		return
	}
	select {
	case <-x.done:
	case <-ctx.Done():
	}
}

// Run polls until ctx ends. A failed pass records the source status and keeps the old data.
func (x *Index) Run(ctx context.Context) {
	if x.Bus == nil {
		x.Bus = NewBroker(time.Now)
	}
	t := time.NewTimer(0)
	defer t.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-prune.C:
			if err := x.Store.Prune(ctx); err != nil {
				slog.Warn("catalog index prune failed", "err", err)
			}
		case <-t.C:
			if err := x.Pass(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("catalog index pass failed", "err", err)
			}
			t.Reset(x.interval())
		}
	}
}

func shaOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Pass runs one poll. It lists the files, reads those whose fingerprint moved (or, without a
// fingerprint, when the history token moved or a full scan is due), and writes the entities,
// vanished paths and masked snapshots in one transaction. Events are published after the commit.
func (x *Index) Pass(ctx context.Context) error {
	if x.Bus == nil {
		x.Bus = NewBroker(time.Now)
	}
	st, err := x.Store.State(ctx)
	if err != nil {
		return err
	}
	now := x.Store.now()
	fail := func(err error) error {
		st.SourceStatus, st.LastError = "unreachable", clean(err.Error(), 200)
		_ = x.Store.SetState(ctx, st)
		x.publishSummary(ctx)
		return err
	}
	plan, err := x.Source.LastPlan(ctx)
	if err != nil {
		return fail(err)
	}
	refs, err := x.Source.Files(ctx)
	if err != nil {
		return fail(err)
	}
	have, err := x.Store.Entities(ctx)
	if err != nil {
		return err
	}
	full := plan != st.LastPlanID || now.Sub(st.FullScanAt) >= FullScanEvery || !x.seededOnce(have)
	x.mu.Lock()
	pending := x.pending
	x.pending = nil
	x.mu.Unlock()

	var up []Entity
	var snaps []Snapshot
	changed := []string{}
	listed := map[string]bool{}
	for _, ref := range refs {
		kind, readable, ok := kindOf(ref.Path)
		if !ok {
			continue
		}
		listed[ref.Path] = true
		old, known := have[ref.Path]
		if !readable {
			// Counted by presence only; credentials and sandbox contents are never read.
			if !known {
				up = append(up, Entity{Path: ref.Path, Kind: kind, ID: idOf(ref.Path), Layer: ref.Layer, State: "active", Fields: map[string]any{}})
				changed = append(changed, ref.Path)
			}
			continue
		}
		if known && ((ref.Fingerprint != "" && ref.Fingerprint == old.Fingerprint) || (ref.Fingerprint == "" && !full && pending[ref.Path].Path == "")) {
			continue
		}
		content, err := x.Source.Read(ctx, ref.Path)
		if err != nil {
			return fail(err)
		}
		if len(content) > maxFileBytes {
			continue
		}
		sha := shaOf(content)
		if known && sha == old.SHA {
			if ref.Fingerprint != old.Fingerprint {
				old.Fingerprint = ref.Fingerprint
				old.Layer = ref.Layer
				up = append(up, old)
			}
			continue
		}
		e, perr := parseEntity(ref.Path, kind, content)
		if perr != nil {
			slog.Warn("catalog file does not parse; skipped", "path", ref.Path)
			continue
		}
		e.Layer, e.SHA, e.Fingerprint = ref.Layer, sha, ref.Fingerprint
		body := content
		if kind == KindSkill {
			body = MaskText(content)
		} else if body, _, err = MaskYAML(content); err != nil {
			continue
		}
		sn := Snapshot{Path: ref.Path, SHA: sha, Content: body, At: now, Source: "observed"}
		if p, ok := pending[ref.Path]; ok {
			sn.Source, sn.PlanID, sn.Actor = p.Source, p.PlanID, p.Actor
		}
		up = append(up, e)
		snaps = append(snaps, sn)
		changed = append(changed, ref.Path)
	}
	var gone []string
	for p := range have {
		if !listed[p] {
			gone = append(gone, p)
			changed = append(changed, p)
		}
	}
	if err := x.Store.Apply(ctx, up, gone, snaps); err != nil {
		return err
	}
	first := !x.seeded && st.ObservedAt.IsZero()
	x.seeded = true
	st.ObservedAt, st.SourceStatus, st.LastError, st.LastPlanID = now, "ok", "", plan
	if full {
		st.FullScanAt = now
	}
	if err := x.Store.SetState(ctx, st); err != nil {
		return err
	}
	sort.Strings(changed)
	if len(changed) > 0 && !first {
		src := "observed"
		for _, p := range changed {
			if _, ok := pending[p]; ok {
				src = "builder"
			}
		}
		x.Bus.Publish("catalog", map[string]any{"observed_at": now.UnixMilli(), "changed_paths": changed, "source": src})
	}
	if len(changed) > 0 || first {
		x.publishSummary(ctx)
	}
	return nil
}

// seededOnce is true once the index holds any entity (a restart does not rescan from scratch).
func (x *Index) seededOnce(have map[string]Entity) bool { return len(have) > 0 }

func idOf(p string) string {
	b := p
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			b = p[i+1:]
			break
		}
	}
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == '.' {
			return b[:i]
		}
	}
	return b
}

func (x *Index) publishSummary(ctx context.Context) {
	if s, err := x.Summary(ctx); err == nil {
		x.Bus.Publish("summary", s.asMap())
	}
}

var errNoStore = errors.New("catalog index has no store")
