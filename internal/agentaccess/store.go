// Package agentaccess owns incoming service identities and credentials. Tokens
// are opaque, stored only as hashes, and checked against SQLite on every call.
package agentaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Every supported verb has authorization semantics in dataverbs.scopedLocal.
// Existing credentials retain their exact grants; adding support never expands them.
var readVerbs = []string{"planner.list", "objects.list", "objects.get", "objects.history", "objects.lookup_source"}
var portfolioReadVerbs = []string{"planner.portfolio.list", "planner.portfolio.get", "planner.portfolio.schema", "planner.portfolio.resolve"}
var portfolioWriteVerbs = []string{"planner.portfolio.create", "planner.portfolio.patch", "planner.portfolio.action", "planner.portfolio.relate", "planner.portfolio.reorder", "planner.portfolio.assign", "planner.portfolio.unassign"}

// TorqueVerbs read Torque's tasks and comments through Tachyon. They are NOT in
// readVerbs on purpose: existing planner-read credentials keep their exact grants.
// A credential gets them only from the torque-read or planner-torque-read preset.
var torqueVerbs = []string{"torque.task.get", "torque.task.comments", "torque.task.list"}

// TesseractVerbs read Tesseract (a search and an item read) through Tachyon. Like the
// Torque verbs they are not in readVerbs: only the presets below grant them. They name
// no object kinds, so a credential holding only these needs none.
var tesseractVerbs = []string{"tesseract.recall", "tesseract.get"}

// ChatWorkspaceVerbs read the items an operator shared in an app chat thread. Only the
// chat-workspace-read preset grants them; they name no object kinds.
var chatWorkspaceVerbs = []string{"chat.workspace.list", "chat.workspace.get"}

// ChatTopicVerbs let an agent add a topic (a named jump point) to an app chat thread. Only the
// chat-topic-add preset grants them; they name no object kinds.
var chatTopicVerbs = []string{"chat.topic.add"}

// ChatPlanVerbs read and write the working plan of an app chat thread. Only the chat-plan preset
// grants them; they name no object kinds.
var chatPlanVerbs = []string{"chat.plan.get", "chat.plan.write", "chat.plan.step"}

// TerminalVerbs let an agent propose a shell command for the operator to approve and read the run it caused.
// Only the terminal-propose preset grants them; they name no object kinds.
var terminalVerbs = []string{"terminal.machines", "terminal.propose", "terminal.run.get"}

// ChatActivityVerbs let an agent add to the activity feed of an app chat thread; ChatPanelVerbs let
// it ask the GUI to open a built-in panel. Each is its own preset and names no object kinds.
var chatActivityVerbs = []string{"chat.activity.append"}
var chatPanelVerbs = []string{"chat.panel.open"}

// ActionVerbs are the registered-action tools (request, then await the result).
// They name no object kinds: a credential holding only these needs none.
var actionVerbs = []string{"actions.catalog", "actions.request", "actions.await"}

// CardVerbs are the card tools: ask the operator, then read, await or withdraw
// the card. A credential only ever sees cards its own identity asked.
var cardVerbs = []string{"card.ask", "card.get", "card.await", "card.cancel"}
var plannerKinds = []string{"plan", "task", "epic", "sprint", "collection", "workstream", "inbox_item", "idea", "decision", "risk", "roadmap_item", "strategic_priority"}
var portfolioKinds = []string{"strategic_priority", "workstream", "roadmap_item", "idea", "decision", "risk", "inbox_item"}

type Grants struct {
	Verbs []string `json:"verbs"`
	Kinds []string `json:"kinds"`
}

func PlannerRead() Grants {
	return Grants{append([]string(nil), readVerbs...), append([]string(nil), plannerKinds...)}
}
func PortfolioRead() Grants {
	return Grants{append(append([]string(nil), readVerbs...), portfolioReadVerbs...), append([]string(nil), portfolioKinds...)}
}

// TorqueRead grants only the Torque read verbs, over the Torque row kinds
// (plans are task rows with kind=plan).
func TorqueRead() Grants {
	return Grants{append([]string(nil), torqueVerbs...), []string{"task", "plan", "epic", "sprint"}}
}

// PlannerTorqueRead is PlannerRead plus TorqueRead: what the in-app agent holds.
func PlannerTorqueRead() Grants {
	g := PlannerRead()
	g.Verbs = append(g.Verbs, torqueVerbs...)
	return g
}

// TesseractRead grants only the Tesseract read verbs.
func TesseractRead() Grants { return Grants{Verbs: append([]string(nil), tesseractVerbs...)} }

// AppAgentRead is what the in-app agent holds: Tachyon's own data, Torque and Tesseract, read-only.
func AppAgentRead() Grants {
	g := PlannerTorqueRead()
	g.Verbs = append(g.Verbs, tesseractVerbs...)
	return g
}
func PortfolioWrite() Grants {
	g := PortfolioRead()
	g.Verbs = append(g.Verbs, portfolioWriteVerbs...)
	return g
}

// ChatWorkspaceRead grants only the chat workspace read verbs.
func ChatWorkspaceRead() Grants       { return Grants{Verbs: append([]string(nil), chatWorkspaceVerbs...)} }
func ChatWorkspaceVerb(v string) bool { return contains(chatWorkspaceVerbs, v) }

// ChatTopicAdd grants only chat.topic.add.
func ChatTopicAdd() Grants        { return Grants{Verbs: append([]string(nil), chatTopicVerbs...)} }
func ChatTopicVerb(v string) bool { return contains(chatTopicVerbs, v) }

// ChatPlan grants only the chat plan verbs.
func ChatActivityAppend() Grants { return Grants{Verbs: append([]string(nil), chatActivityVerbs...)} }
func ChatPanelOpen() Grants      { return Grants{Verbs: append([]string(nil), chatPanelVerbs...)} }
func ChatActivityVerb(v string) bool {
	return contains(chatActivityVerbs, v) || contains(chatPanelVerbs, v)
}
func TerminalPropose() Grants    { return Grants{Verbs: append([]string(nil), terminalVerbs...)} }
func TerminalVerb(v string) bool { return contains(terminalVerbs, v) }
func ChatPlan() Grants           { return Grants{Verbs: append([]string(nil), chatPlanVerbs...)} }
func ChatPlanVerb(v string) bool { return contains(chatPlanVerbs, v) || ChatActivityVerb(v) }
func ActionRequest() Grants      { return Grants{Verbs: append([]string(nil), actionVerbs...)} }
func TorqueVerb(v string) bool   { return contains(torqueVerbs, v) }
func TesseractVerb(v string) bool {
	return contains(tesseractVerbs, v)
}
func ActionVerb(v string) bool { return contains(actionVerbs, v) }
func CardAsk() Grants          { return Grants{Verbs: append([]string(nil), cardVerbs...)} }
func CardVerb(v string) bool   { return contains(cardVerbs, v) }
func PlannerKinds() []string   { return append([]string(nil), plannerKinds...) }
func ReadVerb(v string) bool   { return contains(readVerbs, v) || contains(portfolioReadVerbs, v) }
func SupportedVerb(v string) bool {
	return ReadVerb(v) || contains(portfolioWriteVerbs, v) || contains(actionVerbs, v) || contains(cardVerbs, v) || contains(torqueVerbs, v) || contains(tesseractVerbs, v) || contains(chatWorkspaceVerbs, v) || contains(chatTopicVerbs, v) || contains(chatPlanVerbs, v) || ChatActivityVerb(v) || contains(terminalVerbs, v)
}
func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
func (g Grants) Validate() error {
	onlyActions := len(g.Verbs) > 0
	for _, v := range g.Verbs {
		onlyActions = onlyActions && (contains(actionVerbs, v) || contains(cardVerbs, v) || contains(tesseractVerbs, v) || contains(chatWorkspaceVerbs, v) || contains(chatTopicVerbs, v) || contains(chatPlanVerbs, v) || ChatActivityVerb(v) || contains(terminalVerbs, v))
	}
	if len(g.Verbs) == 0 || (len(g.Kinds) == 0 && !onlyActions) {
		return errors.New("explicit verbs and kinds are required")
	}
	for _, v := range g.Verbs {
		if !SupportedVerb(v) {
			return fmt.Errorf("unsupported service verb %q", v)
		}
	}
	for _, k := range g.Kinds {
		if !contains(plannerKinds, k) {
			return fmt.Errorf("unsupported service kind %q", k)
		}
	}
	return nil
}
func (g Grants) scopes() []string {
	out := []string{}
	for _, v := range g.Verbs {
		out = append(out, "verb:"+v)
	}
	for _, k := range g.Kinds {
		out = append(out, "kind:"+k)
	}
	return out
}
func Allows(p auth.Principal, category, value string) bool {
	return p.Verified && contains(p.Scopes, category+":"+value)
}

type Token struct {
	ID         string     `json:"id"`
	IdentityID string     `json:"identity_id"`
	Name       string     `json:"name"`
	Agents     []string   `json:"agents,omitempty"`
	Grants     Grants     `json:"grants"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
	// Proxy is true when this token's identity is the pinned Tether proxy.
	Proxy bool `json:"proxy,omitempty"`
}
type Store struct{ DB *localstore.Store }

// Issue creates a new identity, or adds a token to the exact existing identity.
// The caller must deliver the secret privately. Token metadata never includes it.
func (s *Store) Issue(ctx context.Context, identityID, name string, grants Grants, ttl time.Duration) (Token, string, error) {
	if err := grants.Validate(); err != nil {
		return Token{}, "", err
	}
	if ttl < time.Minute || ttl > 90*24*time.Hour {
		return Token{}, "", errors.New("token lifetime must be between 1 minute and 90 days")
	}
	if strings.TrimSpace(name) == "" || len(name) > 200 {
		return Token{}, "", errors.New("identity name is required (maximum 200 bytes)")
	}
	fresh := identityID == ""
	if fresh {
		identityID = uuid.NewString()
	} else if _, err := uuid.Parse(identityID); err != nil {
		return Token{}, "", errors.New("identity must be a UUID")
	}
	secret, err := tokenfile.Generate()
	if err != nil {
		return Token{}, "", err
	}
	now := time.Now().UTC().Truncate(time.Second)
	t := Token{ID: uuid.NewString(), IdentityID: identityID, Name: name, Grants: grants, CreatedAt: now, ExpiresAt: now.Add(ttl).Truncate(time.Second)}
	raw, _ := json.Marshal(grants)
	err = s.DB.Writer.Submit(ctx, "service-token.issue", func(ctx context.Context, tx *sql.Tx) error {
		if fresh {
			if _, err := tx.ExecContext(ctx, "INSERT INTO service_identities(id,name,created_at) VALUES(?,?,?)", identityID, name, now.Unix()); err != nil {
				return err
			}
		} else {
			var stored string
			if err := tx.QueryRowContext(ctx, "SELECT name FROM service_identities WHERE id=? AND disabled_at IS NULL", identityID).Scan(&stored); err != nil {
				return errors.New("active service identity not found")
			}
			if stored != name {
				return errors.New("identity name does not match")
			}
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO service_tokens(id,identity_id,token_hash,grants,created_at,expires_at) VALUES(?,?,?,?,?,?)", t.ID, identityID, auth.HashKey(secret), string(raw), now.Unix(), t.ExpiresAt.Unix())
		return err
	})
	if err != nil {
		return Token{}, "", err
	}
	return t, secret, nil
}

// AgentKey reduces an agent id or address to its bare lowercase id: "tether://agent/x",
// "msg://agent/agent-mux/x" and "x" all become "x" ("agent-mux/x" keeps its last path element).
func AgentKey(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.Index(v, "://"); i >= 0 {
		v = v[i+3:]
		v = strings.TrimPrefix(v, "agent/")
	}
	if i := strings.LastIndex(v, "/"); i >= 0 {
		v = v[i+1:]
	}
	return strings.ToLower(strings.TrimSpace(v))
}

// AgentScope is the principal scope an agent binding becomes: "agent:<key>".
const AgentScope = "agent"

// Bind records that an identity may act for the given agents (chat plan verbs check it).
// It adds to any existing bindings, applies to tokens already issued (Verify reads it on every
// call), and is idempotent. The identity must exist and be active.
func (s *Store) Bind(ctx context.Context, identityID string, agents []string) ([]string, error) {
	if _, err := uuid.Parse(identityID); err != nil {
		return nil, errors.New("identity must be a UUID")
	}
	keys := []string{}
	for _, a := range agents {
		k := AgentKey(a)
		if k == "" || len(k) > 200 {
			return nil, errors.New("agent is required (maximum 200 bytes)")
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil, errors.New("at least one agent is required")
	}
	now := time.Now().Unix()
	err := s.DB.Writer.Submit(ctx, "service-identity.bind", func(ctx context.Context, tx *sql.Tx) error {
		var one int
		if err := tx.QueryRowContext(ctx, "SELECT 1 FROM service_identities WHERE id=? AND disabled_at IS NULL", identityID).Scan(&one); err != nil {
			return errors.New("active service identity not found")
		}
		for _, k := range keys {
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO service_identity_agents(identity_id,agent_key,created_at) VALUES(?,?,?)", identityID, k, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Agents(ctx, identityID)
}

// Unbind removes one agent from an identity. It reports an error when nothing was bound.
func (s *Store) Unbind(ctx context.Context, identityID, agent string) error {
	k := AgentKey(agent)
	return s.DB.Writer.Submit(ctx, "service-identity.unbind", func(ctx context.Context, tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, "DELETE FROM service_identity_agents WHERE identity_id=? AND agent_key=?", identityID, k)
		if err != nil {
			return err
		}
		if n, err := r.RowsAffected(); err == nil && n == 0 {
			return errors.New("binding not found")
		}
		return nil
	})
}

// Agents lists the agents an identity is bound to, sorted.
func (s *Store) Agents(ctx context.Context, identityID string) ([]string, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, "SELECT agent_key FROM service_identity_agents WHERE identity_id=? ORDER BY agent_key", identityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ProxyScope is the principal scope carried only by the pinned Tether proxy identity: "proxy:tether".
// Handlers accept X-Tether-* actor headers when, and only when, the authenticated principal has it.
const (
	ProxyScope  = "proxy"
	ProxyTether = "tether"
)

// IsTetherProxy reports whether the verified principal is the pinned Tether proxy.
func IsTetherProxy(p auth.Principal) bool { return Allows(p, ProxyScope, ProxyTether) }

// PinProxy makes one active identity the trusted Tether proxy, replacing any previous pin.
// Like bindings it applies to the identity's existing tokens at once (Verify reads it on every call).
func (s *Store) PinProxy(ctx context.Context, identityID string) error {
	if _, err := uuid.Parse(identityID); err != nil {
		return errors.New("identity must be a UUID")
	}
	return s.DB.Writer.Submit(ctx, "service-proxy.pin", func(ctx context.Context, tx *sql.Tx) error {
		var one int
		if err := tx.QueryRowContext(ctx, "SELECT 1 FROM service_identities WHERE id=? AND disabled_at IS NULL", identityID).Scan(&one); err != nil {
			return errors.New("active service identity not found")
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO service_proxy_pin(id,identity_id,pinned_at) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET identity_id=excluded.identity_id, pinned_at=excluded.pinned_at", identityID, time.Now().Unix())
		return err
	})
}

// UnpinProxy removes the pin; it reports an error when nothing was pinned.
func (s *Store) UnpinProxy(ctx context.Context) error {
	return s.DB.Writer.Submit(ctx, "service-proxy.unpin", func(ctx context.Context, tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, "DELETE FROM service_proxy_pin WHERE id=1")
		if err != nil {
			return err
		}
		if n, err := r.RowsAffected(); err == nil && n == 0 {
			return errors.New("no proxy identity is pinned")
		}
		return nil
	})
}

// ProxyIdentity returns the pinned identity id, or "" when none.
func (s *Store) ProxyIdentity(ctx context.Context) (string, error) {
	var id string
	err := s.DB.Reader.QueryRowContext(ctx, "SELECT identity_id FROM service_proxy_pin WHERE id=1").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *Store) Verify(ctx context.Context, secret string) (auth.Principal, error) {
	if !tokenfile.ValidHex(secret) {
		return auth.Principal{}, auth.ErrInvalidToken
	}
	var id, identity, name, grants string
	var expiry int64
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT t.id,i.id,i.name,t.grants,t.expires_at FROM service_tokens t JOIN service_identities i ON i.id=t.identity_id WHERE t.token_hash=? AND t.revoked_at IS NULL AND i.disabled_at IS NULL`, auth.HashKey(secret)).Scan(&id, &identity, &name, &grants, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Principal{}, auth.ErrInvalidToken
	}
	if err != nil {
		return auth.Principal{}, auth.ErrUnavailable
	}
	if time.Now().Unix() >= expiry {
		return auth.Principal{}, auth.ErrInvalidToken
	}
	var g Grants
	if json.Unmarshal([]byte(grants), &g) != nil || g.Validate() != nil {
		return auth.Principal{}, auth.ErrInvalidToken
	}
	agents, err := s.Agents(ctx, identity)
	if err != nil {
		return auth.Principal{}, auth.ErrUnavailable
	}
	scopes := g.scopes()
	for _, a := range agents {
		scopes = append(scopes, AgentScope+":"+a)
	}
	pinned, err := s.ProxyIdentity(ctx)
	if err != nil {
		return auth.Principal{}, auth.ErrUnavailable
	}
	if pinned != "" && pinned == identity {
		scopes = append(scopes, ProxyScope+":"+ProxyTether)
	}
	return auth.Principal{Verified: true, ID: identity, Kind: "service", Display: name, TokenID: id, Scopes: scopes, ExpiresAt: time.Unix(expiry, 0).UTC()}, nil
}
func (s *Store) Revoke(ctx context.Context, id string, identity bool) error {
	table, field := "service_tokens", "revoked_at"
	if identity {
		table, field = "service_identities", "disabled_at"
	}
	return s.DB.Writer.Submit(ctx, "service-token.revoke", func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "UPDATE "+table+" SET "+field+"=COALESCE("+field+",?) WHERE id=?", time.Now().Unix(), id)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err == nil && n == 0 {
			return errors.New("credential or identity not found")
		}
		return err
	})
}
func (s *Store) List(ctx context.Context) ([]Token, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT t.id,i.id,i.name,t.grants,t.created_at,t.expires_at,t.revoked_at,i.disabled_at FROM service_tokens t JOIN service_identities i ON i.id=t.identity_id ORDER BY t.created_at,t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		var t Token
		var raw string
		var created, expires int64
		var revoked, disabled sql.NullInt64
		if err := rows.Scan(&t.ID, &t.IdentityID, &t.Name, &raw, &created, &expires, &revoked, &disabled); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &t.Grants); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(created, 0).UTC()
		t.ExpiresAt = time.Unix(expires, 0).UTC()
		if revoked.Valid {
			v := time.Unix(revoked.Int64, 0).UTC()
			t.RevokedAt = &v
		}
		if disabled.Valid {
			v := time.Unix(disabled.Int64, 0).UTC()
			t.DisabledAt = &v
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	pinned, err := s.ProxyIdentity(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Agents, err = s.Agents(ctx, out[i].IdentityID); err != nil {
			return nil, err
		}
		out[i].Proxy = pinned != "" && pinned == out[i].IdentityID
	}
	return out, nil
}
