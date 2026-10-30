// Package builderparts holds the Builder's Library parts (B5): prompts and policy bundles that Tachyon manages and
// compiles into agent files, with the references that say which agent carries which revision and the hash that
// detects a hand edit (drift). Tether has no native object for either yet (design request B4), so a part is
// compiled into the agent's own fields and the file stays the system of record.
package builderparts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	KindPrompt = "prompt"
	KindPolicy = "policy"
)

// PolicyFields are the agent fields a policy bundle sets, in editor-model terms.
var PolicyFields = []string{"permission_mode", "network", "default_sandbox", "mcp_scopes", "lifecycle", "recovery"}

// Part is a Library part. Prompts carry Text; policies carry Bundle (only the keys they set).
type Part struct {
	Kind, ID, Name, Summary, Text string
	Bundle                        map[string]any
	Revision                      int
	Archived                      bool
	CreatedNS, UpdatedNS          int64
}

type Ref struct {
	AgentID, Kind, PartID, Hash, PlanID string
	Rev                                 int
	AppliedNS                           int64
}

type Revision struct {
	Revision    int
	Actor, Plan string
	AtNS        int64
	Part        Part
}

type body struct {
	Text   string         `json:"text,omitempty"`
	Bundle map[string]any `json:"bundle,omitempty"`
}

func (p Part) bodyJSON() string {
	b, _ := json.Marshal(body{Text: p.Text, Bundle: p.Bundle})
	return string(b)
}

// Effects is what a change does to the parts tables when it is applied.
type Effects struct {
	Parts  []Part   `json:"parts"`
	Attach []Ref    `json:"attach"`
	Detach []RefKey `json:"detach"`
}

type RefKey struct{ AgentID, Kind, PartID string }

func (e *Effects) Empty() bool { return e == nil || len(e.Parts)+len(e.Attach)+len(e.Detach) == 0 }

// ---- reads (use the reader pool)

func scanPart(sc interface{ Scan(...any) error }) (Part, error) {
	var p Part
	var b string
	var arch int
	if err := sc.Scan(&p.Kind, &p.ID, &p.Name, &p.Summary, &b, &p.Revision, &arch, &p.CreatedNS, &p.UpdatedNS); err != nil {
		return p, err
	}
	var bd body
	_ = json.Unmarshal([]byte(b), &bd)
	p.Text, p.Bundle, p.Archived = bd.Text, bd.Bundle, arch == 1
	return p, nil
}

const partCols = `kind,id,name,summary,body_json,revision,archived,created_ns,updated_ns`

func Get(ctx context.Context, db *sql.DB, kind, id string) (Part, bool, error) {
	p, err := scanPart(db.QueryRowContext(ctx, `SELECT `+partCols+` FROM library_parts WHERE kind=? AND id=?`, kind, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Part{}, false, nil
	}
	return p, err == nil, err
}

func List(ctx context.Context, db *sql.DB, kind string) ([]Part, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+partCols+` FROM library_parts WHERE kind=? ORDER BY id`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Part
	for rows.Next() {
		p, err := scanPart(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func Counts(ctx context.Context, db *sql.DB) (prompts, policies int) {
	rows, err := db.QueryContext(ctx, `SELECT kind,COUNT(*) FROM library_parts WHERE archived=0 GROUP BY kind`)
	if err != nil {
		return 0, 0
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if rows.Scan(&k, &n) == nil {
			if k == KindPrompt {
				prompts = n
			} else {
				policies = n
			}
		}
	}
	return
}

func refs(ctx context.Context, db *sql.DB, where string, args ...any) ([]Ref, error) {
	rows, err := db.QueryContext(ctx, `SELECT agent_id,kind,part_id,part_revision,compiled_hash,plan_id,applied_ns FROM part_refs WHERE `+where+` ORDER BY agent_id,kind,part_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ref
	for rows.Next() {
		var r Ref
		if err := rows.Scan(&r.AgentID, &r.Kind, &r.PartID, &r.Rev, &r.Hash, &r.PlanID, &r.AppliedNS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RefsOfPart lists the agents that carry a part; RefsOfAgent lists the parts an agent carries.
func RefsOfPart(ctx context.Context, db *sql.DB, kind, id string) ([]Ref, error) {
	return refs(ctx, db, `kind=? AND part_id=?`, kind, id)
}
func RefsOfAgent(ctx context.Context, db *sql.DB, agent string) ([]Ref, error) {
	return refs(ctx, db, `agent_id=?`, agent)
}

func Revisions(ctx context.Context, db *sql.DB, kind, id string) ([]Revision, error) {
	rows, err := db.QueryContext(ctx, `SELECT revision,body_json,actor,plan_id,at_ns FROM library_part_revisions WHERE kind=? AND id=? ORDER BY revision DESC`, kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Revision
	for rows.Next() {
		var r Revision
		var b string
		if err := rows.Scan(&r.Revision, &b, &r.Actor, &r.Plan, &r.AtNS); err != nil {
			return nil, err
		}
		var bd body
		_ = json.Unmarshal([]byte(b), &bd)
		r.Part = Part{Kind: kind, ID: id, Text: bd.Text, Bundle: bd.Bundle, Revision: r.Revision}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- commit (inside the serialized writer)

// Commit writes a change's effects: part rows and revisions, then references.
func Commit(ctx context.Context, tx *sql.Tx, e *Effects, actor, planID string, atNS int64) error {
	if e == nil {
		return nil
	}
	for _, p := range e.Parts {
		arch := 0
		if p.Archived {
			arch = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_parts(`+partCols+`) VALUES(?,?,?,?,?,?,?,?,?)
 ON CONFLICT(kind,id) DO UPDATE SET name=excluded.name,summary=excluded.summary,body_json=excluded.body_json,revision=excluded.revision,archived=excluded.archived,updated_ns=excluded.updated_ns`,
			p.Kind, p.ID, p.Name, p.Summary, p.bodyJSON(), p.Revision, arch, atNS, atNS); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO library_part_revisions(kind,id,revision,body_json,actor,plan_id,at_ns) VALUES(?,?,?,?,?,?,?)`,
			p.Kind, p.ID, p.Revision, p.bodyJSON(), actor, planID, atNS); err != nil {
			return err
		}
	}
	for _, k := range e.Detach {
		if _, err := tx.ExecContext(ctx, `DELETE FROM part_refs WHERE agent_id=? AND kind=? AND part_id=?`, k.AgentID, k.Kind, k.PartID); err != nil {
			return err
		}
	}
	for _, r := range e.Attach {
		if _, err := tx.ExecContext(ctx, `INSERT INTO part_refs(agent_id,kind,part_id,part_revision,compiled_hash,plan_id,applied_ns) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(agent_id,kind,part_id) DO UPDATE SET part_revision=excluded.part_revision,compiled_hash=excluded.compiled_hash,plan_id=excluded.plan_id,applied_ns=excluded.applied_ns`,
			r.AgentID, r.Kind, r.PartID, r.Rev, r.Hash, planID, atNS); err != nil {
			return err
		}
	}
	return nil
}

// ---- compiling prompts into an agent body

const (
	markerOpen  = "<!-- tachyon:prompt "
	markerClose = "<!-- /tachyon:prompt -->"
)

func blockRE(id string) *regexp.Regexp {
	return regexp.MustCompile(`(?s)<!-- tachyon:prompt ` + regexp.QuoteMeta(id) + `@\d+ -->\n(.*?)\n?` + regexp.QuoteMeta(markerClose) + `\n?`)
}

func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// Block renders a part for an agent body.
func Block(id string, rev int, text string) string {
	return markerOpen + id + "@" + itoa(rev) + " -->\n" + strings.TrimRight(text, "\n") + "\n" + markerClose + "\n"
}

// PutBlock inserts or replaces the part's block in a body. Text outside blocks is never touched.
func PutBlock(body, id string, rev int, text string) string {
	re := blockRE(id)
	blk := Block(id, rev, text)
	if re.MatchString(body) {
		return re.ReplaceAllLiteralString(body, blk)
	}
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if body != "" {
		body += "\n"
	}
	return body + blk
}

// DropBlock removes the part's block.
func DropBlock(body, id string) string { return blockRE(id).ReplaceAllString(body, "") }

// TextHash is the compiled_hash of a prompt: the hash of its text as it sits in the block.
func TextHash(text string) string { return hash(strings.TrimRight(text, "\n")) }

// BlockText returns the text currently inside the part's block.
func BlockText(body, id string) (string, bool) {
	m := blockRE(id).FindStringSubmatch(body)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func itoa(n int) string { return strconv.Itoa(n) }

// ---- compiling policies into an agent file

// PolicyHash is the compiled_hash of a policy: the canonical JSON of the fields the bundle sets.
func PolicyHash(fields map[string]any) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		ordered = append(ordered, k, fields[k])
	}
	b, _ := json.Marshal(ordered)
	return hash(string(b))
}

// AgentFields reads the policy fields an agent file holds right now for the given keys.
func AgentFields(content string, keys []string) map[string]any {
	var d map[string]any
	if yaml.Unmarshal([]byte(content), &d) != nil {
		return nil
	}
	perm, _ := d["permissions"].(map[string]any)
	out := map[string]any{}
	for _, k := range keys {
		var v any
		switch k {
		case "permission_mode", "network", "default_sandbox":
			v = perm[k]
		default:
			v = d[k]
		}
		if v != nil {
			out[k] = normalize(v)
		}
	}
	return out
}

// normalize makes a decoded YAML value comparable with a decoded JSON one.
func normalize(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// Drifted reports whether an agent file no longer holds what the reference compiled in. bundleKeys are the keys
// the policy sets (from the part's current bundle).
func Drifted(content string, r Ref, bundleKeys []string) bool {
	if r.Kind == KindPrompt {
		var d map[string]any
		if yaml.Unmarshal([]byte(content), &d) != nil {
			return true
		}
		body, _ := d["body"].(string)
		text, ok := BlockText(body, r.PartID)
		return !ok || TextHash(text) != r.Hash
	}
	return PolicyHash(AgentFields(content, bundleKeys)) != r.Hash
}
