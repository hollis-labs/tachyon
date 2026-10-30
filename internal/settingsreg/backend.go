package settingsreg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"database/sql"
	"github.com/hollis-labs/libs/ui/ui-go/envelopes/admin"
)

// backend is one group's admin.GroupBackend for one request (the operator bit is the caller's).
type backend struct {
	reg      *Registry
	g        *Group
	revision string
	operator bool
}

var sourceLabels = map[admin.SourceKind]string{admin.OverrideSource: "Saved in Tachyon", admin.DefaultSource: "Built-in default"}

func (b *backend) layers(ctx context.Context) map[string]map[admin.SourceKind]Layer {
	if b.g.Layers == nil {
		return nil
	}
	return b.g.Layers(ctx)
}

// resolve builds the library State for the stored writable layer plus the read-only layers.
func (b *backend) resolve(ctx context.Context, s Stored) admin.State {
	layers := b.layers(ctx)
	st := admin.State{Revision: b.revision, Values: map[string]admin.ResolvedValue{}}
	for _, f := range b.g.Fields {
		v := admin.ResolvedValue{Editable: f.Editable && !b.g.ReadOnly, ReadOnlyReason: f.ReadOnlyReason, ApplyState: admin.Active}
		if b.g.ReadOnly && v.ReadOnlyReason == "" {
			v.ReadOnlyReason = "This group is read-only."
		}
		if _, op := b.g.Operator[f.Key]; op && v.Editable && !b.operator {
			v.Editable, v.ReadOnlyReason = false, "Operator access is needed to change this."
		}
		if f.RestartRequired && s.Pending[f.Key] {
			v.ApplyState = admin.PendingRestart
		}
		for _, k := range b.g.Precedence {
			switch k {
			case admin.OverrideSource:
				if o, ok := s.Over[f.Key]; ok {
					v.Present, v.Value, v.HasOverride = true, o, true
					v.Source = &admin.Source{Kind: k, Label: sourceLabels[k]}
				}
			case admin.DefaultSource:
				if f.Default != nil {
					v.Present, v.Value = true, *f.Default
					v.Source = &admin.Source{Kind: k, Label: sourceLabels[k]}
				}
			default:
				if l, ok := layers[f.Key][k]; ok {
					v.Present, v.Value = true, l.Value
					v.Source = &admin.Source{Kind: k, Label: layerLabel(k, l.Label)}
				}
			}
			if v.Source != nil {
				break
			}
		}
		// A saved override is shown as such even when a higher layer wins the resolution.
		if o, ok := s.Over[f.Key]; ok && !v.HasOverride {
			_ = o
			v.HasOverride = true
		}
		st.Values[f.Key] = v
	}
	st.Version = version(st, b.operator)
	return st
}

func layerLabel(k admin.SourceKind, name string) string {
	switch k {
	case admin.EnvSource:
		return "Environment: " + name
	case admin.FileSource:
		return "File: " + name
	}
	return name
}

// version is an opaque hash of the resolved values, sources, permissions and apply state, so any change of any
// layer (including an env or file edit) invalidates the ETag. Secret fields are not supported, so values are safe.
func version(st admin.State, operator bool) string {
	keys := make([]string, 0, len(st.Values))
	for k := range st.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		v := st.Values[k]
		var val any
		if v.Present {
			val = v.Value.Value()
		}
		src := ""
		if v.Source != nil {
			src = string(v.Source.Kind) + "/" + v.Source.Label
		}
		b, _ := json.Marshal([]any{k, v.Present, val, src, v.Editable, v.ReadOnlyReason, v.HasOverride, v.ApplyState})
		h.Write(b)
	}
	return "v" + hex.EncodeToString(h.Sum(nil))[:16]
}

func (b *backend) apply(s Stored, c admin.Changes) Stored {
	out := Stored{Over: map[string]admin.Scalar{}, Pending: map[string]bool{}}
	for k, v := range s.Over {
		out.Over[k] = v
	}
	for k := range s.Pending {
		out.Pending[k] = true
	}
	for k, v := range c.Set {
		out.Over[k] = v
	}
	for _, k := range c.Unset {
		delete(out.Over, k)
	}
	return out
}

func (b *backend) Read(ctx context.Context) (admin.State, error) {
	s, err := b.g.Storage.Load(ctx, b.reg.DB.Reader)
	if err != nil {
		return admin.State{}, err
	}
	return b.resolve(ctx, s), nil
}

func (b *backend) Preview(ctx context.Context, c admin.Changes) (admin.State, error) {
	s, err := b.g.Storage.Load(ctx, b.reg.DB.Reader)
	if err != nil {
		return admin.State{}, err
	}
	cur := b.resolve(ctx, s)
	st := b.resolve(ctx, b.apply(s, c))
	st.Version = cur.Version // a preview carries the base version
	return st, nil
}

// WithTransaction runs fn inside the serialized writer: the current state is read in the transaction, Stage
// computes the post-write view, and the writable layer is saved in the same transaction only if fn succeeds.
func (b *backend) WithTransaction(ctx context.Context, fn func(admin.GroupTransaction) error) error {
	var changed []string
	err := b.reg.DB.Writer.Submit(ctx, "settings_"+b.g.ID, func(ctx context.Context, tx *sql.Tx) error {
		cur, err := b.g.Storage.Load(ctx, tx)
		if err != nil {
			return err
		}
		t := &txn{b: b, ctx: ctx, cur: cur}
		if err := fn(t); err != nil {
			return err
		}
		if t.next == nil {
			return nil
		}
		if err := b.g.Storage.Save(ctx, tx, *t.next); err != nil {
			return err
		}
		changed = t.changed
		return nil
	})
	if err == nil && len(changed) > 0 && b.g.Applied != nil {
		b.g.Applied(context.WithoutCancel(ctx), changed)
	}
	return err
}

type txn struct {
	b       *backend
	ctx     context.Context
	cur     Stored
	next    *Stored
	changed []string
}

func (t *txn) Current() (admin.State, error) { return t.b.resolve(t.ctx, t.cur), nil }

func (t *txn) Resolve(c admin.Changes) (admin.State, error) {
	cur := t.b.resolve(t.ctx, t.cur)
	st := t.b.resolve(t.ctx, t.b.apply(t.cur, c))
	st.Version = cur.Version
	return st, nil
}

func (t *txn) Stage(c admin.Changes) (admin.Staged, error) {
	before := t.b.resolve(t.ctx, t.cur)
	next := t.b.apply(t.cur, c)
	probe := t.b.resolve(t.ctx, next)
	var targets []string
	seen := map[string]bool{}
	for _, f := range t.b.g.Fields {
		if !f.RestartRequired {
			continue
		}
		o, n := before.Values[f.Key], probe.Values[f.Key]
		if o.Present != n.Present || o.Value != n.Value {
			next.Pending[f.Key] = true
			if !seen[f.ApplyTarget] {
				seen[f.ApplyTarget] = true
				targets = append(targets, f.ApplyTarget)
			}
		}
	}
	after := t.b.resolve(t.ctx, next)
	var changed []string
	for _, f := range t.b.g.Fields {
		if !sameResolved(before.Values[f.Key], after.Values[f.Key]) {
			changed = append(changed, f.Key)
		}
	}
	t.next, t.changed = &next, changed
	return admin.Staged{State: after, ChangedKeys: changed, Restart: admin.Restart{Required: len(targets) > 0, Targets: targets}}, nil
}

func sameResolved(a, b admin.ResolvedValue) bool {
	srcEq := a.Source == nil && b.Source == nil || a.Source != nil && b.Source != nil && *a.Source == *b.Source
	return a.Present == b.Present && a.Value == b.Value && srcEq && a.Editable == b.Editable && a.ReadOnlyReason == b.ReadOnlyReason && a.HasOverride == b.HasOverride && a.ApplyState == b.ApplyState
}
