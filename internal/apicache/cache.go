package apicache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Identity resolves server-side upstream configuration and credential identity.
// Raw credentials are never saved as keys or returned to browsers.
type Identity func(scope string) (string, error)

type Cache struct {
	store     *localstore.Store
	settings  Settings
	identity  Identity
	now       func() time.Time
	locks     [64]sync.Mutex
	mutation  sync.RWMutex
	unhealthy atomic.Bool
}

func New(store *localstore.Store, settings Settings, identity Identity) *Cache {
	return &Cache{store: store, settings: settings, identity: identity, now: time.Now}
}

func Digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// Scope is an allowlist of non-destructive resource reads. Unknown/new APIs
// default to bypass until their semantics are known; streams never enter here.
func Scope(path string) string {
	if strings.HasPrefix(path, "/api/plugins/console/v1/chat-turns/") {
		return "chat-turns"
	}
	if strings.HasPrefix(path, "/api/plugins/console/v1/chat/") {
		return "chat"
	}
	if path == "/api/plugins/planner/v1/planner" || strings.HasPrefix(path, "/api/plugins/planner/v1/planner/") {
		return "planner"
	}
	if strings.HasPrefix(path, "/api/plugins/console/v1/catalog/") {
		return "catalog"
	}
	if path == "/api/plugins/console/v1/activity/agents" {
		return "activity"
	}
	if strings.HasPrefix(path, "/api/plugins/console/v1/forge/") {
		return "forge"
	}
	for _, plugin := range []string{"console", "planner"} {
		if rest, ok := strings.CutPrefix(path, "/api/plugins/"+plugin+"/v1/"); ok {
			root, _, _ := strings.Cut(rest, "/")
			switch root {
			case "tasks", "projects", "epics", "sprints", "collections", "tags", "runs", "artifacts", "templates", "planner", "scheduler", "models", "flags":
				return "torque"
			}
		}
	}
	return ""
}

func (c *Cache) PolicyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(c.settings)
}

type entry struct {
	body        []byte
	contentType string
	updated     int64
}

func (c *Cache) get(ctx context.Context, key string) (entry, error) {
	var e entry
	err := c.store.Reader.QueryRowContext(ctx, "SELECT body,content_type,updated_at FROM api_cache WHERE key=?", key).Scan(&e.body, &e.contentType, &e.updated)
	return e, err
}

func (c *Cache) save(ctx context.Context, key, scope, identity string, e entry) error {
	return c.store.Writer.Submit(ctx, "cache_snapshot", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO api_cache(key,scope,identity,body,content_type,updated_at) VALUES(?,?,?,?,?,?)
   ON CONFLICT(key) DO UPDATE SET body=excluded.body,content_type=excluded.content_type,updated_at=excluded.updated_at`, key, scope, identity, e.body, e.contentType, e.updated)
		if err != nil {
			return err
		}
		// Oldest snapshots leave first. Bounds cover all credential generations and
		// all resources, so rotation and high-cardinality filters cannot grow forever.
		_, err = tx.ExecContext(ctx, `DELETE FROM api_cache WHERE updated_at < ? OR key IN (
   SELECT key FROM (SELECT key,ROW_NUMBER() OVER (ORDER BY updated_at DESC,key) AS n,
    SUM(length(body)) OVER (ORDER BY updated_at DESC,key) AS bytes FROM api_cache)
   WHERE n > ? OR bytes > ?
  )`, c.now().Add(-time.Duration(c.settings.RetentionSeconds)*time.Second).UnixMilli(), c.settings.MaxEntries, c.settings.MaxBytes)
		return err
	})
}

func (c *Cache) invalidate(ctx context.Context, scope string) error {
	return c.store.Writer.Submit(ctx, "cache_invalidate", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM api_cache WHERE scope = ? OR (? IN ('planner','torque','activity') AND scope IN ('planner','torque','activity'))", scope, scope)
		return err
	})
}

// Wrap leaves response JSON contracts intact; freshness travels in headers.
func (c *Cache) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope := Scope(r.URL.Path)
		if r.Method != http.MethodGet && scope == "" && strings.HasPrefix(r.URL.Path, "/api/plugins/") {
			scope = "torque"
		}
		if r.Method != http.MethodGet {
			// Drain older readers before a mutation, then invalidate before new readers.
			// Demonstration saves never send a request and never enter this branch.
			if r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			c.mutation.Lock()
			defer c.mutation.Unlock()
			out := newCapture()
			next.ServeHTTP(out, r)
			if out.status >= 200 && out.status < 400 {
				if err := c.invalidate(r.Context(), scope); err != nil {
					out.header.Set("X-Tachyon-Cache-Error", "storage")
					c.unhealthy.Store(true)
				}
			}
			out.send(w)
			return
		}
		if scope == "" {
			next.ServeHTTP(w, r)
			return
		}
		c.mutation.RLock()
		defer c.mutation.RUnlock()
		p := c.settings.Policies[scope]
		if c.unhealthy.Load() || strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
			p.Enabled = false
		}
		identity, err := c.identity(scope)
		if err != nil {
			failure(w, scope, "configuration", "Upstream configuration or credential file is invalid; check the Tachyon host settings.")
			return
		}
		if scope == "chat-turns" {
			cookie, e := r.Cookie("tachyon_chat_turns")
			if e != nil || cookie.Value == "" {
				next.ServeHTTP(w, r)
				return
			}
			identity = Digest(identity + "\n" + cookie.Value)
		}
		// The fixed host credential is the current single-user identity boundary.
		// User-scoped credentials must extend this key when the account module lands.
		key := Digest(identity + "\n" + r.URL.Path + "?" + r.URL.Query().Encode())
		bypass := !p.Enabled || strings.Contains(r.Header.Get("Cache-Control"), "no-cache") || strings.Contains(r.Header.Get("Cache-Control"), "no-store")
		preferStale := strings.Contains(r.Header.Get("Prefer"), "stale-while-revalidate")
		fast := func(e entry, err error) bool {
			if bypass || err != nil {
				return false
			}
			age := c.now().Sub(time.UnixMilli(e.updated))
			if age < 0 {
				return false
			}
			if age < time.Duration(p.FreshSeconds)*time.Second {
				serveEntry(w, scope, "cached", e, p, "")
				return true
			}
			if preferStale && p.RevalidateSeconds > 0 && age < time.Duration(p.FreshSeconds+p.RevalidateSeconds)*time.Second {
				serveEntry(w, scope, "refreshing", e, p, "")
				return true
			}
			return false
		}
		cached, readErr := c.get(r.Context(), key)
		if fast(cached, readErr) {
			return
		}
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(key))
		lock := &c.locks[hash.Sum32()%uint32(len(c.locks))]
		lock.Lock()
		defer lock.Unlock()
		cached, readErr = c.get(r.Context(), key)
		if fast(cached, readErr) {
			return
		}
		exists := readErr == nil
		age := c.now().Sub(time.UnixMilli(cached.updated))
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(p.TimeoutSeconds)*time.Second)
		defer cancel()
		out := newCapture()
		next.ServeHTTP(out, r.WithContext(ctx))
		if r.Context().Err() != nil {
			return
		}
		if ctx.Err() != nil {
			out = errorCapture(http.StatusGatewayTimeout, "timeout", "Remote request timed out.")
		}
		after, identityErr := c.identity(scope)
		if scope == "chat-turns" {
			cookie, _ := r.Cookie("tachyon_chat_turns") // admitted above
			after = Digest(after + "\n" + cookie.Value)
		}
		if identityErr != nil || after != identity {
			failure(w, scope, "configuration", "Upstream credential changed during refresh; retry the request.")
			return
		}
		code := classify(out)
		if code == "" && out.status == 200 && json.Valid(out.body.Bytes()) && strings.Contains(out.header.Get("Content-Type"), "application/json") {
			fresh := entry{body: bytes.Clone(out.body.Bytes()), contentType: out.header.Get("Content-Type"), updated: c.now().UnixMilli()}
			storageError := ""
			if c.unhealthy.Load() {
				storageError = "storage"
			}
			if p.Enabled && int64(len(fresh.body)) <= c.settings.MaxBytes {
				if err := c.save(r.Context(), key, scope, identity, fresh); err != nil {
					storageError = "storage"
				}
			} else if p.Enabled {
				storageError = "oversized"
			}
			serveEntry(w, scope, "live", fresh, p, storageError)
			return
		}
		if code == "" && out.status == 200 {
			code = "invalid_response"
			out = errorCapture(http.StatusBadGateway, code, "Remote service returned an invalid JSON response.")
		}
		// A denied credential never falls back to previously authorized data. A
		// deleted resource must not reappear from an old successful response either.
		if exists && p.Enabled && (code == "unreachable" || code == "timeout" || code == "remote" || code == "rate_limited" || code == "invalid_response") && age >= 0 && age <= time.Duration(p.FreshSeconds+p.StaleSeconds)*time.Second {
			serveEntry(w, scope, "stale", cached, p, code)
			return
		}
		if code == "authentication" || out.status == 404 || out.status == 410 {
			err := c.store.Writer.Submit(r.Context(), "cache_forget", func(ctx context.Context, tx *sql.Tx) error {
				if code == "authentication" {
					_, e := tx.ExecContext(ctx, "DELETE FROM api_cache WHERE identity=?", identity)
					return e
				}
				_, e := tx.ExecContext(ctx, "DELETE FROM api_cache WHERE key=?", key)
				return e
			})
			if err != nil {
				c.unhealthy.Store(true)
			}
		}
		if code == "" {
			code = "remote"
		}
		out.header.Set("X-Tachyon-Source", scope)
		out.header.Set("X-Tachyon-Cache", "error")
		out.header.Set("X-Tachyon-Cache-Error", code)
		out.header.Set("Cache-Control", "no-store")
		if exists {
			out.header.Set("X-Tachyon-Updated-At", time.UnixMilli(cached.updated).UTC().Format(time.RFC3339Nano))
		}
		// Disk failures are visible even if there was no usable stored snapshot.
		if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
			out.header.Set("X-Tachyon-Storage-Error", "storage")
		}
		out.send(w)
	})
}

func serveEntry(w http.ResponseWriter, scope, state string, e entry, p Policy, problem string) {
	w.Header().Set("Content-Type", e.contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Tachyon-Source", scope)
	w.Header().Set("X-Tachyon-Cache", state)
	w.Header().Set("X-Tachyon-Updated-At", time.UnixMilli(e.updated).UTC().Format(time.RFC3339Nano))
	w.Header().Set("X-Tachyon-Fresh-Seconds", fmt.Sprint(p.FreshSeconds))
	w.Header().Set("X-Tachyon-Revalidate-Seconds", fmt.Sprint(p.RevalidateSeconds))
	w.Header().Set("X-Tachyon-Cache-Enabled", fmt.Sprint(p.Enabled))
	if problem != "" {
		w.Header().Set("X-Tachyon-Cache-Error", problem)
	}
	if state == "stale" {
		w.Header().Set("Warning", `110 tachyon "Response is stale"`)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(e.body)
}

func classify(out *capture) string {
	var b struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(out.body.Bytes(), &b)
	if out.status == 401 || out.status == 403 {
		return "authentication"
	}
	if out.status == 429 {
		return "rate_limited"
	}
	switch b.Code {
	case "configuration", "authentication", "timeout", "unreachable", "invalid_response":
		return b.Code
	case "unavailable":
		return "unreachable"
	}
	if out.status >= 500 {
		return "remote"
	}
	return ""
}

func failure(w http.ResponseWriter, scope, code, message string) {
	out := errorCapture(503, code, message)
	out.header.Set("X-Tachyon-Source", scope)
	out.header.Set("X-Tachyon-Cache", "error")
	out.header.Set("X-Tachyon-Cache-Error", code)
	out.header.Set("Cache-Control", "no-store")
	out.send(w)
}

type capture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newCapture() *capture                { return &capture{header: make(http.Header), status: 200} }
func (c *capture) Header() http.Header    { return c.header }
func (c *capture) WriteHeader(status int) { c.status = status }
func (c *capture) Write(b []byte) (int, error) {
	if c.body.Len()+len(b) > 8<<20 {
		return 0, errors.New("response exceeds cache buffer")
	}
	return c.body.Write(b)
}
func (c *capture) send(w http.ResponseWriter) {
	for k, v := range c.header {
		w.Header()[k] = v
	}
	w.WriteHeader(c.status)
	_, _ = w.Write(c.body.Bytes())
}
func errorCapture(status int, code, message string) *capture {
	c := newCapture()
	c.status = status
	c.header.Set("Content-Type", "application/json")
	_ = json.NewEncoder(&c.body).Encode(map[string]string{"error": message, "code": code})
	return c
}
