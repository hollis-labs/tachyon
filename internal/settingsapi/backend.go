package settingsapi

import (
	"context"
	"strconv"
	"sync"

	"github.com/hollis-labs/libs/ui/ui-go/envelopes/admin"
)

// store is the stub's in-memory layered settings: per group, per key, the override layer is writable and the
// file/env/default layers are fixed by the seed. Nothing is durable.
type store struct {
	mu     sync.Mutex
	groups map[string]*gstate
}

type gstate struct {
	spec     Spec
	over     map[string]admin.Scalar
	pending  map[string]bool
	layers   map[string]map[admin.SourceKind]layer
	gen      int
	operator bool // the caller's session is operator-token (set per request)
}

type layer struct {
	v     admin.Scalar
	label string
}

func newStore() *store {
	s := &store{groups: map[string]*gstate{}}
	for _, sp := range Specs() {
		g := &gstate{spec: sp, over: map[string]admin.Scalar{}, pending: map[string]bool{}, layers: map[string]map[admin.SourceKind]layer{}}
		for _, f := range sp.Fields {
			m := map[admin.SourceKind]layer{}
			if f.Default != nil {
				m[admin.DefaultSource] = layer{*f.Default, "Built-in default"}
			}
			if sd, ok := sp.Seed[f.Key]; ok {
				if sd.File != nil {
					m[admin.FileSource] = layer{scalarOf(sd.File), sd.FileName}
				}
				if sd.Env != nil {
					m[admin.EnvSource] = layer{scalarOf(sd.Env), sd.EnvName}
				}
			}
			g.layers[f.Key] = m
		}
		s.groups[sp.ID] = g
	}
	return s
}

func scalarOf(v any) admin.Scalar {
	switch x := v.(type) {
	case int:
		s, _ := admin.Integer(int64(x))
		return s
	case string:
		s, _ := admin.String(x)
		return s
	case bool:
		return admin.Boolean(x)
	}
	return admin.Scalar{}
}

var labels = map[admin.SourceKind]string{admin.OverrideSource: "Saved in Tachyon"}

func (g *gstate) needsOperator(key string) bool { _, ok := g.spec.Operator[key]; return ok }

// state resolves every field. Version changes with the generation and the caller's permission, as the library
// requires ("all value, source, permission and apply changes invalidate it").
func (g *gstate) state(over map[string]admin.Scalar, pending map[string]bool, gen int) admin.State {
	st := admin.State{Revision: ManifestRevision, Version: "g" + strconv.Itoa(gen) + opBit(g.operator), Values: map[string]admin.ResolvedValue{}}
	for _, f := range g.spec.Fields {
		v := admin.ResolvedValue{Editable: f.Editable, ReadOnlyReason: f.ReadOnlyReason, ApplyState: admin.Active}
		if f.Editable && g.needsOperator(f.Key) && !g.operator {
			v.Editable, v.ReadOnlyReason = false, "Operator access is needed to change this."
		}
		if f.RestartRequired && pending[f.Key] {
			v.ApplyState = admin.PendingRestart
		}
		if o, ok := over[f.Key]; ok {
			v.Present, v.Value, v.HasOverride = true, o, true
			v.Source = &admin.Source{Kind: admin.OverrideSource, Label: labels[admin.OverrideSource]}
		} else {
			for _, k := range g.spec.Precedence {
				if l, ok := g.layers[f.Key][k]; ok {
					v.Present, v.Value = true, l.v
					v.Source = &admin.Source{Kind: k, Label: sourceLabel(k, l.label)}
					break
				}
			}
		}
		st.Values[f.Key] = v
	}
	return st
}

func sourceLabel(k admin.SourceKind, name string) string {
	switch k {
	case admin.EnvSource:
		return "Environment: " + name
	case admin.FileSource:
		return "File: " + name
	}
	return name
}

func opBit(op bool) string {
	if op {
		return "-op"
	}
	return ""
}

// backend is one group's admin.GroupBackend for one request.
type backend struct {
	s *store
	g *gstate
}

func (b backend) Read(context.Context) (admin.State, error) {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	return b.g.state(b.g.over, b.g.pending, b.g.gen), nil
}

func (b backend) Preview(_ context.Context, c admin.Changes) (admin.State, error) {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	cur := b.g.state(b.g.over, b.g.pending, b.g.gen)
	over, pend := b.g.apply(c)
	st := b.g.state(over, pend, b.g.gen)
	st.Version = cur.Version // Preview carries the base version
	return st, nil
}

func (g *gstate) apply(c admin.Changes) (map[string]admin.Scalar, map[string]bool) {
	over, pend := map[string]admin.Scalar{}, map[string]bool{}
	for k, v := range g.over {
		over[k] = v
	}
	for k, v := range g.pending {
		pend[k] = v
	}
	for k, v := range c.Set {
		over[k] = v
	}
	for _, k := range c.Unset {
		delete(over, k)
	}
	return over, pend
}

func (b backend) WithTransaction(ctx context.Context, fn func(admin.GroupTransaction) error) error {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	tx := &txn{g: b.g}
	if err := fn(tx); err != nil {
		return err
	}
	if tx.commit != nil {
		tx.commit()
	}
	return nil
}

type txn struct {
	g      *gstate
	commit func()
}

func (t *txn) Current() (admin.State, error) { return t.g.state(t.g.over, t.g.pending, t.g.gen), nil }

func (t *txn) Resolve(c admin.Changes) (admin.State, error) {
	cur := t.g.state(t.g.over, t.g.pending, t.g.gen)
	over, pend := t.g.apply(c)
	st := t.g.state(over, pend, t.g.gen)
	st.Version = cur.Version
	return st, nil
}

func (t *txn) Stage(c admin.Changes) (admin.Staged, error) {
	before := t.g.state(t.g.over, t.g.pending, t.g.gen)
	over, pend := t.g.apply(c)
	var targets []string
	seen := map[string]bool{}
	for _, f := range t.g.spec.Fields {
		if !f.RestartRequired {
			continue
		}
		// Compare the resolved desired value, not just the override layer.
		oldV, newV := before.Values[f.Key], t.g.state(over, pend, t.g.gen).Values[f.Key]
		if oldV.Present != newV.Present || oldV.Value != newV.Value {
			pend[f.Key] = true
			if !seen[f.ApplyTarget] {
				seen[f.ApplyTarget] = true
				targets = append(targets, f.ApplyTarget)
			}
		}
	}
	gen := t.g.gen
	after := t.g.state(over, pend, gen)
	var changed []string
	for _, f := range t.g.spec.Fields {
		if !sameResolved(before.Values[f.Key], after.Values[f.Key]) {
			changed = append(changed, f.Key)
		}
	}
	if len(changed) > 0 {
		gen++
		after = t.g.state(over, pend, gen)
	}
	t.commit = func() { t.g.over, t.g.pending, t.g.gen = over, pend, gen }
	return admin.Staged{State: after, ChangedKeys: changed, Restart: admin.Restart{Required: len(targets) > 0, Targets: targets}}, nil
}

func sameResolved(a, b admin.ResolvedValue) bool {
	srcEq := a.Source == nil && b.Source == nil || a.Source != nil && b.Source != nil && *a.Source == *b.Source
	return a.Present == b.Present && a.Value == b.Value && srcEq && a.Editable == b.Editable && a.ReadOnlyReason == b.ReadOnlyReason && a.HasOverride == b.HasOverride && a.ApplyState == b.ApplyState
}

// registry builds the caller's registry. The operator flag decides which operator-token fields are editable.
func (s *store) registry(operator bool) (*admin.Registry, error) {
	s.mu.Lock()
	for _, g := range s.groups {
		g.operator = operator
	}
	s.mu.Unlock()
	def := admin.Definition{App: admin.App{ID: AppID, Label: "Tachyon"}, Revision: ManifestRevision, BasePath: Root}
	for _, sp := range Specs() {
		g := s.groups[sp.ID]
		grp := admin.Group{ID: sp.ID, Label: sp.Label, Scope: admin.Scope{Kind: "app", ID: AppID}, Fields: sp.Fields,
			Resolution: admin.Resolution{Precedence: sp.Precedence}, Backend: backend{s, g}}
		grp.Capabilities = admin.Capabilities{CanRead: true, CanValidate: true, CanUpdate: !sp.ReadOnly, CanReset: !sp.ReadOnly}
		if !sp.ReadOnly {
			grp.Resolution.WriteLayer = admin.OverrideSource
		}
		def.Groups = append(def.Groups, grp)
	}
	return admin.New(def)
}
