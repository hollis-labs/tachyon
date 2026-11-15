// Package settings reads the app's own "tachyon" block from the manifest config. The framework
// modules read theirs; this block holds what only Tachyon has: the hosts it answers to, where its
// upstreams are and which credential each one uses, its policy files, and the switches that let it
// reach out to anything at all.
//
//	tachyon:
//	  allowed_hosts: []            # extra Host values (besides loopback) served behind a local TLS proxy
//	  cache_dir: cache             # relative: anchored in the data directory (AfterConfig)
//	  workspace_dir: workspace     # chat workspace files; relative: anchored in the data directory
//	  chat_sync_interval: 5s       # the Tether chat sync loop (features.chat_sync), 1s to 1h
//	  upstreams:                   # every address empty by default; each credential is a secrets reference
//	    tether:          { url: "", token: "" }
//	    tether_control:  { url: "", token: "" }   # builder changes; url empty = tether.url
//	    tether_messages: { token: "" }             # forge assign, alert sink, card delivery; token empty = tether.token
//	    events:          { url: "", token: "" }   # Hollis events hub (card events)
//	    torque:          { url: "", token: "" }
//	    tesseract:       { url: "", token: "" }
//	    cerberus:        { url: "", token: "" }   # http(s) or unix:/path
//	    fragments:       { url: "", token: "" }   # Fragments Engine (transcripts)
//	    activity:        { url: "" }              # activity monitor, no credential
//	    caster:          { url: "", token: "" }
//	    app_agent:       { url: "", token: "", label: "", session: {} }   # Nanite app agent; session: Nanite's session body (pins, no credentials)
//	    github:          { url: "https://api.github.com", org: "", token: "" }
//	    action_hub:      { addr: "", user: "", key: "", known_hosts: "" }   # SSH hub for approved actions
//	  features:                    # every outbound loop, all off by default (the cutover turns them on)
//	    object_sync: false
//	    ...
//	  files:                       # policy and config files; absolute paths, empty = not configured
//	    approval_producer: ""
//	    ...
//	  alerts: { notify_to: "", public_url: "" }
//	  transcripts: { max_sessions: 5000 }   # sessions in one transcript snapshot, 200 to 20000
//
// Parse refuses an unknown key, a value of the wrong type and a malformed address or secrets
// reference, and names the key in the error, so a typo stops the start instead of silently turning a
// protection or an integration off. The manifest (cmd/tachyon/substrate.yaml) carries the defaults;
// the operator overrides them in the file named by SUBSTRATE_CONFIG, which merges deeply.
package settings

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	libsecrets "github.com/hollis-labs/libs/security/secrets"
)

// Key is the name of the config block.
const Key = "tachyon"

// Upstream is one service Tachyon calls: where it is and which secrets reference holds the
// credential. Either may be empty; an empty URL means the integration is not configured.
type Upstream struct {
	URL   string
	Token string // a secrets reference (file://, keychain://, op://, helper://, env://), never a value
}

// AppAgent is the Nanite app agent: an upstream, the label Tachyon shows for it, and the session
// body Tachyon posts to create its sessions (definition and host pins; never credentials).
type AppAgent struct {
	Upstream
	Label   string
	Session json.RawMessage // nil when not set
}

// GitHub is the forge upstream.
type GitHub struct {
	Upstream
	Org string
}

// ActionHub is the SSH hub that runs approved actions.
type ActionHub struct {
	Addr       string // host:port
	User       string
	Key        string // a secrets reference to the private key
	KnownHosts string // absolute path of the known_hosts file
}

// Upstreams are every service Tachyon reaches.
type Upstreams struct {
	Tether         Upstream
	TetherControl  Upstream
	TetherMessages Upstream // URL unused: messages go to Tether.URL
	Events         Upstream
	Torque         Upstream
	Tesseract      Upstream
	Cerberus       Upstream
	Fragments      Upstream
	Activity       Upstream // Token unused
	Caster         Upstream
	AppAgent       AppAgent
	GitHub         GitHub
	ActionHub      ActionHub
}

// Features are the outbound switches. Each one, when false, keeps Tachyon from sending or polling
// anything for that feature. All default to false: a new build talks to nobody until the operator
// says so, which is what the cutover relies on (plan section 8).
type Features struct {
	ObjectSync        bool // scheduler dispatch of object-sync jobs (Tether, Cerberus, GitHub reads)
	ChatSync          bool // the Tether chat sync loop
	AgentsCollector   bool // the Tether environment stream read model, and the ceremony meter's hourly read
	AgentsControls    bool // agent controls and launches over Tether: nudge, interrupt, restart, launch
	CatalogIndex      bool // the Tether catalog poller
	BuilderChanges    bool // builder changes, the launch action, registry writes and team operations over Tether
	CardEvents        bool // publish card.* events to the events hub
	CardEffectsTorque bool // approved cards may change Torque
	CardDelivery      bool // push card outcomes to the asker over Tether messages
	AlertSources      bool // the alert adapters (activity, agents bus, Torque events, Tether poll)
	AlertSink         bool // send alerts over Tether messages
	ForgeIndex        bool // the GitHub pull-request poller
	PlannerEvents     bool // the planner's Torque event ingest
	Transcripts       bool // Fragments Engine transcript snapshots
	ActionRuns        bool // run approved actions over the SSH hub
}

// Files are the policy and config files Tachyon reads. Empty means not configured.
type Files struct {
	ApprovalProducer  string // producer trust (Tether workload JWT); empty = /api/approvals/v1 answers 503
	ApprovalAssurance string // assurance table override
	CachePolicy       string
	TerminalPolicy    string
	CardDelivery      string
	SecretKey         string // a secrets reference: the AES key of approval secrets
}

// Transcripts are the transcript bridge's limits.
type Transcripts struct {
	// MaxSessions caps the sessions in one snapshot (TACHYON_TRANSCRIPT_MAX_SESSIONS), 200 to 20000.
	MaxSessions int
}

// Alerts are the alert sink's addressing.
type Alerts struct {
	NotifyTo  string // a Tether message address
	PublicURL string // the URL put in alert messages
}

// Settings are the parsed block.
type Settings struct {
	AllowedHosts []string
	CacheDir     string
	WorkspaceDir string // chat workspace file bytes (the old build's $TACHYON_DATA_DIR/workspace)
	// ChatSyncInterval is how often the Tether chat sync loop runs (TACHYON_CHAT_SYNC_INTERVAL).
	ChatSyncInterval time.Duration
	Upstreams        Upstreams
	Features         Features
	Files            Files
	Alerts           Alerts
	Transcripts      Transcripts
}

// featureKeys is the one list of feature switches: the config key and the field it sets.
func featureKeys(f *Features) []struct {
	Key string
	Dst *bool
} {
	return []struct {
		Key string
		Dst *bool
	}{
		{"object_sync", &f.ObjectSync},
		{"chat_sync", &f.ChatSync},
		{"agents_collector", &f.AgentsCollector},
		{"agents_controls", &f.AgentsControls},
		{"catalog_index", &f.CatalogIndex},
		{"builder_changes", &f.BuilderChanges},
		{"card_events", &f.CardEvents},
		{"card_effects_torque", &f.CardEffectsTorque},
		{"card_delivery", &f.CardDelivery},
		{"alert_sources", &f.AlertSources},
		{"alert_sink", &f.AlertSink},
		{"forge_index", &f.ForgeIndex},
		{"planner_events", &f.PlannerEvents},
		{"transcripts", &f.Transcripts},
		{"action_runs", &f.ActionRuns},
	}
}

// FeatureKeys lists every feature switch's config key, in a fixed order.
func FeatureKeys() []string {
	var out []string
	for _, k := range featureKeys(&Features{}) {
		out = append(out, k.Key)
	}
	return out
}

// Enabled reports the switch named by its config key; ok is false for an unknown key.
func (f Features) Enabled(key string) (on, ok bool) {
	for _, k := range featureKeys(&f) {
		if k.Key == key {
			return *k.Dst, true
		}
	}
	return false, false
}

// Parse reads the block (nil for a missing one, which gives every default).
func Parse(raw any) (Settings, error) {
	var s Settings
	if raw == nil {
		return s, nil
	}
	root, err := object(Key, raw)
	if err != nil {
		return s, err
	}
	if s.AllowedHosts, err = root.hosts("allowed_hosts"); err != nil {
		return s, err
	}
	if s.CacheDir, err = root.str("cache_dir"); err != nil {
		return s, err
	}
	if s.WorkspaceDir, err = root.str("workspace_dir"); err != nil {
		return s, err
	}
	if s.ChatSyncInterval, err = root.duration("chat_sync_interval", 5*time.Second, time.Second, time.Hour); err != nil {
		return s, err
	}
	if err = parseUpstreams(root, &s.Upstreams); err != nil {
		return s, err
	}
	feats, err := root.child("features")
	if err != nil {
		return s, err
	}
	for _, k := range featureKeys(&s.Features) {
		if *k.Dst, err = feats.boolean(k.Key); err != nil {
			return s, err
		}
	}
	if err = feats.done(); err != nil {
		return s, err
	}
	files, err := root.child("files")
	if err != nil {
		return s, err
	}
	for _, f := range []struct {
		key string
		dst *string
	}{
		{"approval_producer", &s.Files.ApprovalProducer},
		{"approval_assurance", &s.Files.ApprovalAssurance},
		{"cache_policy", &s.Files.CachePolicy},
		{"terminal_policy", &s.Files.TerminalPolicy},
		{"card_delivery", &s.Files.CardDelivery},
	} {
		if *f.dst, err = files.absPath(f.key); err != nil {
			return s, err
		}
	}
	if s.Files.SecretKey, err = files.secretRef("secret_key"); err != nil {
		return s, err
	}
	if err = files.done(); err != nil {
		return s, err
	}
	alerts, err := root.child("alerts")
	if err != nil {
		return s, err
	}
	if s.Alerts.NotifyTo, err = alerts.str("notify_to"); err != nil {
		return s, err
	}
	if s.Alerts.PublicURL, err = alerts.httpURL("public_url"); err != nil {
		return s, err
	}
	if err = alerts.done(); err != nil {
		return s, err
	}
	tr, err := root.child("transcripts")
	if err != nil {
		return s, err
	}
	if s.Transcripts.MaxSessions, err = tr.intRange("max_sessions", 5000, 200, 20000); err != nil {
		return s, err
	}
	if err = tr.done(); err != nil {
		return s, err
	}
	return s, root.done()
}

func parseUpstreams(root *block, u *Upstreams) error {
	ups, err := root.child("upstreams")
	if err != nil {
		return err
	}
	plain := []struct {
		key     string
		dst     *Upstream
		unix    bool // the address may be unix:/path
		noToken bool
		noURL   bool
	}{
		{key: "tether", dst: &u.Tether},
		{key: "tether_control", dst: &u.TetherControl, unix: true},
		{key: "tether_messages", dst: &u.TetherMessages, noURL: true},
		{key: "events", dst: &u.Events},
		{key: "torque", dst: &u.Torque},
		{key: "tesseract", dst: &u.Tesseract},
		{key: "cerberus", dst: &u.Cerberus, unix: true},
		{key: "fragments", dst: &u.Fragments},
		{key: "activity", dst: &u.Activity, noToken: true},
		{key: "caster", dst: &u.Caster},
	}
	for _, p := range plain {
		b, cerr := ups.child(p.key)
		if cerr != nil {
			return cerr
		}
		if err = b.upstream(p.dst, !p.noURL, !p.noToken, p.unix); err != nil {
			return err
		}
		if err = b.done(); err != nil {
			return err
		}
	}

	agent, err := ups.child("app_agent")
	if err != nil {
		return err
	}
	if err = agent.upstream(&u.AppAgent.Upstream, true, true, false); err != nil {
		return err
	}
	if u.AppAgent.Label, err = agent.str("label"); err != nil {
		return err
	}
	if u.AppAgent.Session, err = agent.jsonObject("session"); err != nil {
		return err
	}
	if err = agent.done(); err != nil {
		return err
	}

	gh, err := ups.child("github")
	if err != nil {
		return err
	}
	if err = gh.upstream(&u.GitHub.Upstream, true, true, false); err != nil {
		return err
	}
	if u.GitHub.Org, err = gh.str("org"); err != nil {
		return err
	}
	if err = gh.done(); err != nil {
		return err
	}

	hub, err := ups.child("action_hub")
	if err != nil {
		return err
	}
	if u.ActionHub.Addr, err = hub.hostPort("addr"); err != nil {
		return err
	}
	if u.ActionHub.User, err = hub.str("user"); err != nil {
		return err
	}
	if u.ActionHub.Key, err = hub.secretRef("key"); err != nil {
		return err
	}
	if u.ActionHub.KnownHosts, err = hub.absPath("known_hosts"); err != nil {
		return err
	}
	if err = hub.done(); err != nil {
		return err
	}
	return ups.done()
}

// block is one map in the config tree. It remembers which keys were read, so done can name the
// ones nobody asked for.
type block struct {
	path string
	m    map[string]any
	seen map[string]bool
}

func object(path string, raw any) (*block, error) {
	if raw == nil {
		return &block{path: path, m: map[string]any{}, seen: map[string]bool{}}, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a map", path)
	}
	return &block{path: path, m: m, seen: map[string]bool{}}, nil
}

func (b *block) name(k string) string { return b.path + "." + k }

func (b *block) get(k string) any {
	b.seen[k] = true
	return b.m[k]
}

func (b *block) child(k string) (*block, error) { return object(b.name(k), b.get(k)) }

// done refuses every key that was not read.
func (b *block) done() error {
	var unknown []string
	for k := range b.m {
		if !b.seen[k] {
			unknown = append(unknown, b.name(k))
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("unknown config key(s): %s", strings.Join(unknown, ", "))
}

func (b *block) str(k string) (string, error) {
	switch v := b.get(k).(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(v), nil
	default:
		return "", fmt.Errorf("%s must be a string", b.name(k))
	}
}

// duration reads a Go duration string within [lo, hi]; absent is def.
func (b *block) duration(k string, def, lo, hi time.Duration) (time.Duration, error) {
	v, err := b.str(k)
	if err != nil || v == "" {
		return def, err
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < lo || d > hi {
		return 0, fmt.Errorf("%s must be a duration from %s to %s", b.name(k), lo, hi)
	}
	return d, nil
}

// intRange reads a whole number within [lo, hi]; absent is def.
func (b *block) intRange(k string, def, lo, hi int) (int, error) {
	var n float64
	switch v := b.get(k).(type) {
	case nil:
		return def, nil
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	case uint64:
		n = float64(v)
	case float64:
		n = v
	default:
		return 0, fmt.Errorf("%s must be a whole number from %d to %d", b.name(k), lo, hi)
	}
	if n != float64(int(n)) || int(n) < lo || int(n) > hi {
		return 0, fmt.Errorf("%s must be a whole number from %d to %d", b.name(k), lo, hi)
	}
	return int(n), nil
}

// jsonObject reads a map and returns it as JSON; absent or empty is nil.
func (b *block) jsonObject(k string) (json.RawMessage, error) {
	switch v := b.get(k).(type) {
	case nil:
		return nil, nil
	case map[string]any:
		if len(v) == 0 {
			return nil, nil
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", b.name(k), err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("%s must be a map", b.name(k))
	}
}

func (b *block) boolean(k string) (bool, error) {
	switch v := b.get(k).(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	default:
		return false, fmt.Errorf("%s must be true or false", b.name(k))
	}
}

func (b *block) absPath(k string) (string, error) {
	v, err := b.str(k)
	if err != nil || v == "" {
		return v, err
	}
	if !filepath.IsAbs(v) {
		return "", fmt.Errorf("%s: %q is not an absolute path", b.name(k), v)
	}
	return v, nil
}

// secretRef accepts only a secrets reference: a credential value in the config file is refused,
// so it can never be committed or logged by accident.
func (b *block) secretRef(k string) (string, error) {
	v, err := b.str(k)
	if err != nil || v == "" {
		return v, err
	}
	if _, err := libsecrets.ParseRef(v); err != nil {
		return "", fmt.Errorf("%s must be a secrets reference (file://, keychain://, op://, helper://, env://), not a value", b.name(k))
	}
	return v, nil
}

func (b *block) httpURL(k string) (string, error) {
	v, err := b.str(k)
	if err != nil || v == "" {
		return v, err
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("%s: %q is not an http(s) URL", b.name(k), v)
	}
	return v, nil
}

// address is an http(s) URL or, where allowed, unix:/absolute/socket/path.
func (b *block) address(k string, unix bool) (string, error) {
	v, err := b.str(k)
	if err != nil || v == "" {
		return v, err
	}
	if unix && strings.HasPrefix(v, "unix:") {
		if p := strings.TrimPrefix(v, "unix:"); filepath.IsAbs(p) {
			return v, nil
		}
		return "", fmt.Errorf("%s: %q: a unix address is unix:/absolute/path", b.name(k), v)
	}
	return b.httpURL(k)
}

func (b *block) hostPort(k string) (string, error) {
	v, err := b.str(k)
	if err != nil || v == "" {
		return v, err
	}
	if _, port, err := net.SplitHostPort(v); err != nil || port == "" {
		return "", fmt.Errorf("%s: %q is not host:port", b.name(k), v)
	}
	return v, nil
}

func (b *block) upstream(dst *Upstream, withURL, withToken, unix bool) error {
	var err error
	if withURL {
		if dst.URL, err = b.address("url", unix); err != nil {
			return err
		}
	}
	if withToken {
		if dst.Token, err = b.secretRef("token"); err != nil {
			return err
		}
	}
	return nil
}

func (b *block) hosts(k string) ([]string, error) {
	var out []string
	switch hosts := b.get(k).(type) {
	case nil:
	case []any:
		for _, h := range hosts {
			t, ok := h.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a list of strings", b.name(k))
			}
			t = strings.ToLower(strings.TrimSpace(t))
			if t == "" || strings.ContainsAny(t, "/@ ") {
				return nil, fmt.Errorf("%s: %q is not a host name", b.name(k), t)
			}
			if _, _, err := net.SplitHostPort(t); err != nil && strings.Contains(t, ":") {
				return nil, fmt.Errorf("%s: %q is not a host name", b.name(k), t)
			}
			out = append(out, t)
		}
	default:
		return nil, fmt.Errorf("%s must be a list of strings", b.name(k))
	}
	return out, nil
}
