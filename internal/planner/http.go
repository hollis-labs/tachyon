package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

const Prefix = "/api/plugins/console/v1/planner"
const LegacyPrefix = "/api/plugins/planner/v1/planner"

type HTTP struct {
	Owned *Owned
	Tasks *Tasks
	Store *localstore.Store
}

func NewHTTP(store *localstore.Store, tasks *Tasks) *HTTP {
	return &HTTP{Owned: &Owned{Objects: objects.New(store)}, Tasks: tasks, Store: store}
}
func send(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (h *HTTP) fail(w http.ResponseWriter, r *http.Request, db, id string, err error) {
	status, code, message := 500, "internal", "Planner operation failed."
	switch {
	case errors.Is(err, ErrOperatorRequired):
		status, code, message = 403, "operator_required", err.Error()
	case errors.Is(err, ErrCycle):
		status, code, message = 422, "cycle", "That link would make a cycle."
	case errors.Is(err, ErrRule):
		status, code, message = 422, "invalid", err.Error()
	case errors.Is(err, objects.ErrInvalid):
		status, code, message = 400, "invalid", err.Error()
	case errors.Is(err, objects.ErrConflict):
		status, code, message = 409, "conflict", "Record changed; read and reconcile before retrying."
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/pin") {
			code = "revision_conflict"
		}
	case errors.Is(err, objects.ErrNotFound):
		status, code, message = 404, "not_found", "Planner item or route not found."
	case errors.Is(err, ErrTaskSource):
		status, code, message = 502, "source_unavailable", err.Error()
	}
	body := Item{"error": message, "code": code}
	if status == 409 && db != "" && id != "" {
		if v, e := h.Owned.Get(r.Context(), db, id); e == nil {
			body["details"] = Item{"current": v}
		}
	}
	send(w, status, body)
}

var actorSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// requestActor names who made a browser write. The browser may send X-Planner-Actor (its
// operator profile slug); anything else, or no header, is the shared "operator:browser". The
// prefix is fixed, so a header cannot turn an actor into anything but an operator label.
func requestActor(r *http.Request) string {
	if v := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Planner-Actor"))); actorSlug.MatchString(v) {
		return "operator:" + v
	}
	return "operator:browser"
}
func readBody(w http.ResponseWriter, r *http.Request, allowed ...string) (Item, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 3<<20)
	defer r.Body.Close()
	d := json.NewDecoder(r.Body)
	d.UseNumber()
	var m Item
	if err := d.Decode(&m); err != nil || m == nil {
		return nil, objects.ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, objects.ErrInvalid
	}
	for k := range m {
		ok := false
		for _, a := range allowed {
			if k == a {
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("%w: unknown field %s", objects.ErrInvalid, k)
		}
	}
	return m, nil
}
func expected(m Item) (int64, error) {
	n, ok := m["expected_revision"].(json.Number)
	if !ok {
		return 0, fmt.Errorf("%w: expected_revision required", objects.ErrInvalid)
	}
	rev, err := n.Int64()
	if err != nil || rev < 1 {
		return 0, objects.ErrInvalid
	}
	return rev, nil
}
func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	path, ok := strings.CutPrefix(r.URL.Path, Prefix)
	if !ok {
		path, ok = strings.CutPrefix(r.URL.Path, LegacyPrefix)
	}
	if !ok {
		h.fail(w, r, "", "", objects.ErrNotFound)
		return
	}
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	ctx := r.Context()
	var out any
	var err error
	status := 200
	db, id := "", ""
	if len(parts) > 0 {
		db = CanonicalDB(parts[0])
	}
	if len(parts) > 1 {
		id = parts[1]
	}
	if r.Method == http.MethodGet {
		switch path {
		case "resolve":
			out, err = h.Owned.ResolveReference(ctx, r.URL.Query().Get("ref"))
		case "databases":
			rows := []Item{}
			for _, name := range portfolioDatabases {
				items, e := h.Owned.List(ctx, name, nil, "")
				if e != nil {
					err = e
					break
				}
				updated := ""
				for _, v := range items {
					if textField(v, "updated") > updated {
						updated = textField(v, "updated")
					}
				}
				rows = append(rows, Item{"name": PublicDB(name), "prefix": "", "schema": "tachyon/planner/" + PublicDB(name) + "@1", "count": len(items), "updated": updated})
			}
			out = rows
		case "search":
			hits := []Item{}
			query := strings.ToLower(r.URL.Query().Get("q"))
			names := portfolioDatabases
			if name := r.URL.Query().Get("db"); name != "" {
				names = []string{CanonicalDB(name)}
			}
			for _, name := range names {
				items, e := h.Owned.List(ctx, name, nil, "")
				if e != nil {
					err = e
					break
				}
				for _, v := range items {
					if strings.Contains(strings.ToLower(string(encode(v))), query) {
						hits = append(hits, Item{"db": PublicDB(name), "id": v["id"], "title": v["title"], "status": v["status"]})
					}
				}
			}
			out = hits
		case "board":
			var o BoardOptions
			o.Scope = r.URL.Query().Get("scope")
			o.IncludeProposed = r.URL.Query().Get("include_proposed") == "true"
			for _, param := range []struct {
				name string
				dest *float64
			}{{"recent_hours", &o.RecentHours}, {"active_hours", &o.ActiveHours}} {
				if value := r.URL.Query().Get(param.name); value != "" {
					*param.dest, err = strconv.ParseFloat(value, 64)
					if err != nil || math.IsNaN(*param.dest) || math.IsInf(*param.dest, 0) {
						err = objects.ErrInvalid
						break
					}
				}
			}
			if value := r.URL.Query().Get("limit"); value != "" && err == nil {
				o.Limit, err = strconv.Atoi(value)
				if err != nil {
					err = objects.ErrInvalid
				}
			}
			if err == nil {
				out, err = h.Owned.Board(ctx, h.Tasks, o)
			}
		case "torque-titles":
			ids := []string{}
			for _, v := range strings.Split(r.URL.Query().Get("ids"), ",") {
				if v = strings.TrimSpace(v); v != "" {
					ids = append(ids, v)
				}
			}
			out, err = h.Tasks.Titles(ctx, ids)
		case "events":
			h.Events(w, r)
			return
		default:
			switch {
			case len(parts) == 2 && db == "schema":
				out, err = Schema(id)
			case len(parts) == 3 && parts[2] == "links" && db != "schema":
				out, err = h.Owned.Links(ctx, db, id)
			case len(parts) == 3 && db == "workstreams" && parts[2] == "tasks":
				var row Item
				row, err = h.Owned.Get(ctx, db, id)
				if err == nil {
					out, err = h.Tasks.ForWorkstream(ctx, row)
				}
			case len(parts) == 1:
				filters := map[string]string{}
				for k, v := range r.URL.Query() {
					if k != "sort" && len(v) > 0 {
						filters[k] = v[0]
					}
				}
				out, err = h.Owned.List(ctx, db, filters, r.URL.Query().Get("sort"))
			case len(parts) == 2:
				out, err = h.Owned.Get(ctx, db, id)
			default:
				err = objects.ErrNotFound
			}
		}
	} else {
		actor := requestActor(r)
		switch {
		case (r.Method == http.MethodPost || r.Method == http.MethodDelete) && path == "relate":
			var b Item
			b, err = readBody(w, r, "from", "to", "expected_revision", "field")
			if err == nil {
				var rev int64
				rev, err = expected(b)
				if err == nil {
					from, to := object(b["from"]), object(b["to"])
					out, err = h.Owned.Relate(ctx, RelationRequest{From: PortfolioRef{textField(from, "db"), textField(from, "id")}, To: PortfolioRef{textField(to, "db"), textField(to, "id")}, ExpectedRevision: rev, Field: textField(b, "field"), Add: r.Method == http.MethodPost}, actor)
				}
			}
		case r.Method == http.MethodPut && len(parts) == 3 && parts[2] == "pin":
			var b Item
			b, err = readBody(w, r, "pinned", "expected_revision")
			if err == nil {
				pinned, isBool := b["pinned"].(bool)
				var rev int64
				if rev, err = expected(b); err == nil && !isBool {
					err = fmt.Errorf("%w: pinned (boolean) is required", objects.ErrInvalid)
				}
				if err == nil {
					var row Item
					if row, err = h.Owned.Pin(ctx, db, id, rev, pinned, actor); err == nil {
						out = Item{"item": Item{"id": row["id"], "db": PublicDB(db), "rev": row["rev"], "pinned": row["pinned"] == true, "pinned_at": row["pinned_at"]}}
					}
				}
			}
		case (r.Method == http.MethodPost || r.Method == http.MethodDelete) && len(parts) == 3 && parts[2] == "relations":
			var b Item
			b, err = readBody(w, r, "kind", "to", "expected_revision")
			if err == nil {
				var rev int64
				if rev, err = expected(b); err == nil {
					to := object(b["to"])
					from, target := PortfolioRef{DB: db, ID: id}, PortfolioRef{DB: textField(to, "db"), ID: textField(to, "id")}
					var row Item
					if r.Method == http.MethodPost {
						row, err = h.Owned.AddRelation(ctx, from, textField(b, "kind"), target, rev, actor)
					} else {
						row, err = h.Owned.RemoveRelation(ctx, from, textField(b, "kind"), target, rev, actor)
					}
					if err == nil {
						var view Item
						if view, err = h.Owned.Links(ctx, db, id); err == nil {
							out = Item{"item": Item{"id": row["id"], "db": PublicDB(db), "rev": row["rev"], "relations_count": row["relations_count"]}, "links_view": view}
						}
					}
				}
			}
		case r.Method == http.MethodPost && len(parts) == 1:
			var b Item
			b, err = readBody(w, r, "id", "fields")
			if err == nil {
				out, err = h.Owned.Create(ctx, db, textField(b, "id"), object(b["fields"]), actor)
				status = 201
			}
		case r.Method == http.MethodPatch && len(parts) == 2:
			var b Item
			b, err = readBody(w, r, "expected_revision", "patch")
			if err == nil {
				var rev int64
				rev, err = expected(b)
				if err == nil {
					if _, ok := b["patch"].(map[string]any); !ok {
						err = objects.ErrInvalid
					} else {
						out, err = h.Owned.Patch(ctx, db, id, rev, object(b["patch"]), actor)
					}
				}
			}
		case r.Method == http.MethodPost && len(parts) == 2 && id == "reorder":
			var b Item
			b, err = readBody(w, r, "items")
			if err == nil {
				var items []OrderChange
				d := json.NewDecoder(strings.NewReader(string(encode(b["items"]))))
				d.DisallowUnknownFields()
				if d.Decode(&items) != nil {
					err = objects.ErrInvalid
				} else {
					out, err = h.Owned.Reorder(ctx, db, items, actor)
				}
			}
		case (r.Method == http.MethodPost || r.Method == http.MethodDelete) && len(parts) == 3:
			action := parts[2]
			allowed := []string{"expected_revision"}
			switch action {
			case "comments":
				allowed = append(allowed, "text", "kind")
			case "links":
				if r.Method == http.MethodPost {
					allowed = append(allowed, "link")
					action = "link-add"
				} else {
					allowed = append(allowed, "kind", "ref")
					action = "link-remove"
				}
			case "decide":
				allowed = append(allowed, "option", "comment")
			case "defer", "dismiss":
				allowed = append(allowed, "note")
			case "promote":
				allowed = append(allowed, "to_db", "target_id", "fields")
			case "reopen":
			case "ready", "approve", "park", "unpark", "finish", "archive", "verify":
				allowed = append(allowed, "reason", "note", "position")
			case "resequence":
				allowed = append(allowed, "position")
			case "append_state":
				allowed = append(allowed, "text")
			default:
				err = objects.ErrNotFound
			}
			if r.Method == http.MethodDelete && action != "link-remove" {
				err = objects.ErrInvalid
			}
			if err == nil {
				var b Item
				b, err = readBody(w, r, allowed...)
				if err == nil {
					var rev int64
					rev, err = expected(b)
					if err == nil {
						if action == "promote" {
							switch db {
							case "inbox":
								out, err = h.Owned.Promote(ctx, id, rev, textField(b, "to_db"), textField(b, "target_id"), object(b["fields"]), actor)
							case "ideas":
								out, err = h.Owned.PromoteIdea(ctx, id, rev, textField(b, "to_db"), textField(b, "target_id"), object(b["fields"]), actor)
							default:
								err = objects.ErrInvalid
							}
						} else {
							out, err = h.Owned.Action(ctx, db, id, rev, action, b, actor)
						}
					}
				}
			}
		default:
			w.Header().Set("Allow", "GET, POST, PUT, PATCH, DELETE")
			send(w, 405, Item{"code": "method_not_allowed", "error": "Unsupported Planner method/route."})
			return
		}
	}
	if err != nil {
		h.fail(w, r, db, id, err)
		return
	}
	send(w, status, out)
}

// Events is a durable invalidation cursor over owned object revisions. Polling
// observes writes made by the CLI and other transports, not just this handler.
func (h *HTTP) Events(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		send(w, 405, Item{"code": "method_not_allowed"})
		return
	}
	last := int64(-1)
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			send(w, 400, Item{"code": "invalid", "error": "Invalid event cursor"})
			return
		}
		last = n
	}
	current := func(ctx context.Context) (int64, error) {
		var n int64
		err := h.Store.Reader.QueryRowContext(ctx, `SELECT coalesce(max(r.rowid),0) FROM object_revisions r JOIN objects o ON o.id=r.object_id WHERE o.ownership='tachyon' AND o.kind IN ('strategic_priority','workstream','roadmap_item','idea','decision','risk','inbox_item')`).Scan(&n)
		return n, err
	}
	n, err := current(r.Context())
	if err != nil {
		h.fail(w, r, "", "", err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	emit := func(n int64) error {
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, err := fmt.Fprintf(w, "id: %d\nevent: planner.changed\ndata: {\"source\":\"tachyon\",\"refresh\":true}\n\n", n)
		if err == nil {
			err = controller.Flush()
		}
		return err
	}
	if n != last {
		if emit(n) != nil {
			return
		}
		last = n
	} else {
		_, _ = io.WriteString(w, ": connected\n\n")
		if controller.Flush() != nil {
			return
		}
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			n, err := current(r.Context())
			if err != nil {
				return
			}
			if n != last {
				if emit(n) != nil {
					return
				}
				last = n
			}
		case <-heartbeat.C:
			_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			if controller.Flush() != nil {
				return
			}
		}
	}
}
