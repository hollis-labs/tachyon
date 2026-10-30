// Package settingsreg is the settings registry (ST1): every feature registers a group of flat settings and the
// registry serves them through the libs adminhttp handler (admin contract v1) with layered values and real
// provenance. Values resolve override > env > file > default (a group may narrow the precedence); only the
// override layer is written, in app_settings, inside the serialized writer. The handler takes a caller-filtered
// registry per request, so fields that need an operator session read as not editable until the session has one.
//
// Features keep their setting behind a small interface in their own package and register a Group here; a Group
// with a custom Storage maps a legacy app_settings row onto the same fields (ST2).
package settingsreg

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/hollis-labs/libs/ui/ui-go/envelopes/admin"

	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/settingsapi"
)

// Layer is the value one non-override layer holds for a key (env or file; defaults come from the field).
type Layer struct {
	Value admin.Scalar
	Label string // for example the variable or file name
}

// Stored is the writable layer of a group: the override values and the keys whose change is not applied yet.
type Stored struct {
	Over    map[string]admin.Scalar
	Pending map[string]bool
}

// Storage persists a group's Stored. Load reads outside a transaction; LoadTx and Save run inside the writer.
type Storage interface {
	Load(ctx context.Context, q Querier) (Stored, error)
	Save(ctx context.Context, tx *sql.Tx, s Stored) error
}

// Querier is the read side shared by *sql.DB and *sql.Tx.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Group is one registered settings group.
type Group struct {
	ID, Label  string
	Fields     []admin.Field
	Precedence []admin.SourceKind // highest first; default override, env, file, default
	ReadOnly   bool               // no update or reset
	// Operator lists the keys that need an operator-token session to change.
	Operator map[string]struct{}
	// Layers reports the current env and file values per key; nil means none.
	Layers func(ctx context.Context) map[string]map[admin.SourceKind]Layer
	// Storage defaults to one JSON row in app_settings under "settings.<id>".
	Storage Storage
	// Validate checks the whole resolved candidate (cross-field rules); optional.
	Validate admin.SemanticValidator
	// Applied runs after a committed change with the changed keys (live apply); optional. It must not block.
	Applied func(ctx context.Context, changed []string)
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// Registry holds the groups and builds the library registry for a request.
type Registry struct {
	DB *localstore.Store

	mu     sync.RWMutex
	groups []*Group
}

// Register adds a group. It refuses a duplicate id, a bad id or key, and a field the library would reject.
func (r *Registry) Register(g Group) error {
	if !idPattern.MatchString(g.ID) || strings.TrimSpace(g.Label) == "" || len(g.Fields) == 0 {
		return fmt.Errorf("settingsreg: group %q needs a lowercase id, a label and fields", g.ID)
	}
	keys := map[string]bool{}
	for _, f := range g.Fields {
		if !idPattern.MatchString(f.Key) || keys[f.Key] {
			return fmt.Errorf("settingsreg: group %s has a bad or repeated key %q", g.ID, f.Key)
		}
		keys[f.Key] = true
	}
	for k := range g.Operator {
		if !keys[k] {
			return fmt.Errorf("settingsreg: group %s names operator key %q that is not a field", g.ID, k)
		}
	}
	if len(g.Precedence) == 0 {
		g.Precedence = []admin.SourceKind{admin.OverrideSource, admin.EnvSource, admin.FileSource, admin.DefaultSource}
	}
	if g.Storage == nil {
		g.Storage = AppSettings{Key: "settings." + g.ID}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.groups {
		if x.ID == g.ID {
			return fmt.Errorf("settingsreg: group %s is already registered", g.ID)
		}
	}
	// Prove the declaration now, so a bad group fails at start-up and not on the first request.
	if _, err := build(append(slices.Clone(r.groups), &g), nil, false); err != nil {
		return fmt.Errorf("settingsreg: group %s: %w", g.ID, err)
	}
	r.groups = append(r.groups, &g)
	return nil
}

// Groups lists the registered group ids in registration order.
func (r *Registry) Groups() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.groups))
	for _, g := range r.groups {
		out = append(out, g.ID)
	}
	return out
}

// Group returns a registered group by id.
func (r *Registry) Group(id string) (*Group, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, g := range r.groups {
		if g.ID == id {
			return g, true
		}
	}
	return nil, false
}

// NeedsOperator reports the first of keys that needs an operator session in the group.
func (r *Registry) NeedsOperator(group string, keys []string) (string, bool) {
	g, ok := r.Group(group)
	if !ok {
		return "", false
	}
	for _, k := range keys {
		if _, hit := g.Operator[k]; hit {
			return k, true
		}
	}
	return "", false
}

// Revision is a hash of every declaration: it changes when a group or field is added, removed or reshaped.
func (r *Registry) Revision() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return revision(r.groups)
}

func revision(groups []*Group) string {
	h := sha256.New()
	ordered := slices.Clone(groups)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, g := range ordered {
		b, _ := json.Marshal(struct {
			ID, Label  string
			Fields     []admin.Field
			Precedence []admin.SourceKind
			ReadOnly   bool
			Operator   []string
		}{g.ID, g.Label, g.Fields, g.Precedence, g.ReadOnly, operatorKeys(g)})
		h.Write(b)
	}
	return "r-" + hex.EncodeToString(h.Sum(nil))[:16]
}

func operatorKeys(g *Group) []string {
	out := make([]string, 0, len(g.Operator))
	for k := range g.Operator {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Build returns the caller's library registry; operator says whether the session may change operator fields.
func (r *Registry) Build(operator bool) (*admin.Registry, error) {
	r.mu.RLock()
	groups := slices.Clone(r.groups)
	r.mu.RUnlock()
	return build(groups, r, operator)
}

func build(groups []*Group, r *Registry, operator bool) (*admin.Registry, error) {
	def := admin.Definition{App: admin.App{ID: settingsapi.AppID, Label: "Tachyon"}, Revision: revision(groups), BasePath: settingsapi.Root}
	for _, g := range groups {
		grp := admin.Group{ID: g.ID, Label: g.Label, Scope: admin.Scope{Kind: "app", ID: settingsapi.AppID}, Fields: g.Fields,
			Resolution: admin.Resolution{Precedence: g.Precedence}, SemanticValidation: g.Validate}
		grp.Capabilities = admin.Capabilities{CanRead: true, CanValidate: true, CanUpdate: !g.ReadOnly, CanReset: !g.ReadOnly}
		if !g.ReadOnly {
			grp.Resolution.WriteLayer = admin.OverrideSource
		}
		if r != nil {
			grp.Backend = &backend{reg: r, g: g, revision: def.Revision, operator: operator}
		} else {
			grp.Backend = noBackend{}
		}
		def.Groups = append(def.Groups, grp)
	}
	return admin.New(def)
}

// noBackend lets Register validate a declaration before any request exists.
type noBackend struct{}

func (noBackend) Read(context.Context) (admin.State, error) {
	return admin.State{}, errors.New("not ready")
}
func (noBackend) Preview(context.Context, admin.Changes) (admin.State, error) {
	return admin.State{}, errors.New("not ready")
}
func (noBackend) WithTransaction(context.Context, func(admin.GroupTransaction) error) error {
	return errors.New("not ready")
}

// AppSettings is the default Storage: one JSON row {"over":{...},"pending":[...]} in app_settings.
type AppSettings struct{ Key string }

type row struct {
	Over    map[string]admin.Scalar `json:"over"`
	Pending []string                `json:"pending,omitempty"`
}

func (a AppSettings) Load(ctx context.Context, q Querier) (Stored, error) {
	s := Stored{Over: map[string]admin.Scalar{}, Pending: map[string]bool{}}
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, a.Key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	var r row
	if json.Unmarshal([]byte(raw), &r) != nil {
		return s, nil // a damaged row reads as nothing saved; the next save replaces it
	}
	for k, v := range r.Over {
		s.Over[k] = v
	}
	for _, k := range r.Pending {
		s.Pending[k] = true
	}
	return s, nil
}

func (a AppSettings) Save(ctx context.Context, tx *sql.Tx, s Stored) error {
	r := row{Over: s.Over}
	for k := range s.Pending {
		r.Pending = append(r.Pending, k)
	}
	sort.Strings(r.Pending)
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO app_settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, a.Key, string(b))
	return err
}
