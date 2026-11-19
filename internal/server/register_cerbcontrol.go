package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/actionrun"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/cerbcontrol"
)

func init() { registerServerMount(mountCerberusControl) }

// CerberusPrefix is the Cerberus control surface under the console API.
const CerberusPrefix = "/api/plugins/console/v1/cerberus"

type cerberusRoutes struct {
	s       *Server
	mode    string
	client  *cerbcontrol.Client
	cerr    error
	journal cerbcontrol.Journal
}

// mountCerberusControl serves list/status/logs/health reads and lifecycle verbs for Cerberus resources.
// TACHYON_CERBERUS_CONTROL=direct (default, dev) calls the Cerberus daemon with acknowledged:true;
// approve-and-run turns each verb into an Approve-and-Run request the owner approves.
func mountCerberusControl(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	c := &cerberusRoutes{s: s, mode: cerbcontrol.ParseMode(os.Getenv("TACHYON_CERBERUS_CONTROL"))}
	c.client, c.cerr = cerbcontrol.New(s.cfg.Upstreams.CerberusAddr, s.cfg.Upstreams.CerberusTokenFile)
	guard := func(h http.HandlerFunc) http.Handler { return operator.RequireOperator(h) }
	// Dev-open: lifecycle writes need a same-origin request but not operator elevation. Set
	// TACHYON_CERBERUS_ELEVATION=required to demand the elevated operator session like other Tachyon writes.
	write := func(h http.HandlerFunc) http.Handler { return sameOriginWrite(h) }
	if strings.EqualFold(os.Getenv("TACHYON_CERBERUS_ELEVATION"), "required") {
		write = guard
	}
	mux.Handle("GET "+CerberusPrefix+"/control", guard(c.control))
	mux.Handle("GET "+CerberusPrefix+"/health", guard(c.health))
	mux.Handle("GET "+CerberusPrefix+"/resources", guard(c.list))
	mux.Handle("GET "+CerberusPrefix+"/resources/{id}/status", guard(c.read("status")))
	mux.Handle("GET "+CerberusPrefix+"/resources/{id}/inspect", guard(c.read("inspect")))
	mux.Handle("GET "+CerberusPrefix+"/resources/{id}/doctor", guard(c.read("doctor")))
	mux.Handle("GET "+CerberusPrefix+"/resources/{id}/logs", guard(c.logs))
	mux.Handle("POST "+CerberusPrefix+"/resources/{id}/{verb}", write(c.verb))
}

// sameOriginWrite refuses cross-site and foreign-origin writes (CSRF) without requiring elevation.
func sameOriginWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o, err := url.Parse(r.Header.Get("Origin"))
		if err != nil || o.Host != r.Host || (o.Scheme != "https" && o.Scheme != "http") || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeJSON(w, 403, map[string]string{"code": "origin_denied", "message": "Use the Tachyon browser origin (send an Origin header matching the host)."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (c *cerberusRoutes) fail(w http.ResponseWriter, err error) {
	var se *cerbcontrol.StatusError
	switch {
	case errors.Is(err, cerbcontrol.ErrInvalid):
		writeJSON(w, 400, map[string]string{"code": "invalid", "message": err.Error()})
	case errors.As(err, &se):
		writeJSON(w, 502, map[string]any{"code": "cerberus_error", "cerberus_status": se.Status, "cerberus": json.RawMessage(rawOrQuote(se.Body))})
	default:
		writeJSON(w, 502, map[string]string{"code": "cerberus_unreachable", "message": err.Error()})
	}
}

func rawOrQuote(s string) string {
	if json.Valid([]byte(s)) {
		return s
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func (c *cerberusRoutes) ready(w http.ResponseWriter) bool {
	if c.cerr != nil {
		writeJSON(w, 503, map[string]string{"code": "cerberus_not_configured", "message": c.cerr.Error()})
		return false
	}
	return true
}

func (c *cerberusRoutes) control(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"mode": c.mode, "setting": "TACHYON_CERBERUS_CONTROL", "modes": []string{cerbcontrol.ModeDirect, cerbcontrol.ModeApproveAndRun},
		"configured": c.cerr == nil, "verbs": cerbcontrol.Verbs(), "recent": c.journal.Recent()})
}

func (c *cerberusRoutes) health(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w) {
		return
	}
	out, err := c.client.Health(r.Context(), r.URL.Query().Get("resource_id"))
	if err != nil {
		c.fail(w, err)
		return
	}
	writeRaw(w, out)
}

func (c *cerberusRoutes) list(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w) {
		return
	}
	q := r.URL.Query()
	out, err := c.client.List(r.Context(), q.Get("project_id"), q.Get("connector"), q.Get("tag"))
	if err != nil {
		c.fail(w, err)
		return
	}
	writeRaw(w, out)
}

func (c *cerberusRoutes) read(view string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !c.ready(w) {
			return
		}
		out, err := c.client.Read(r.Context(), r.PathValue("id"), view)
		if err != nil {
			c.fail(w, err)
			return
		}
		writeRaw(w, out)
	}
}

func (c *cerberusRoutes) logs(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w) {
		return
	}
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	out, err := c.client.Logs(r.Context(), r.PathValue("id"), lines, r.URL.Query().Get("stream"))
	if err != nil {
		c.fail(w, err)
		return
	}
	writeRaw(w, out)
}

func writeRaw(w http.ResponseWriter, raw json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

func (c *cerberusRoutes) verb(w http.ResponseWriter, r *http.Request) {
	id, name := r.PathValue("id"), r.PathValue("verb")
	verb, ok := cerbcontrol.VerbByName(name)
	if !ok {
		writeJSON(w, 404, map[string]string{"code": "unknown_verb", "message": "verbs: start, restart, reload, stop, deploy, apply, sync, remove"})
		return
	}
	actor := strings.TrimSpace(r.Header.Get("X-Actor"))
	op := cerbcontrol.Op{At: time.Now().UTC(), Mode: c.mode, Verb: verb.Name, Resource: id, Actor: actor, Destructive: verb.Destructive}
	record := func(outcome string, status int) {
		op.Outcome, op.Status = outcome, status
		c.journal.Add(op)
		slog.Info("cerberus control", "mode", op.Mode, "verb", op.Verb, "resource", id, "actor", actor, "destructive", op.Destructive, "outcome", outcome)
	}
	if c.mode == cerbcontrol.ModeApproveAndRun {
		c.viaApproval(w, r, verb, id, record)
		return
	}
	if !c.ready(w) {
		record("not_configured", 503)
		return
	}
	out, err := c.client.Mutate(r.Context(), verb, id)
	if err != nil {
		var se *cerbcontrol.StatusError
		if errors.As(err, &se) {
			record("error", se.Status)
		} else {
			record("error", 0)
		}
		c.fail(w, err)
		return
	}
	record("done", 200)
	writeJSON(w, 200, map[string]any{"mode": c.mode, "verb": verb.Name, "resource_id": id, "destructive": verb.Destructive, "result": out})
}

// viaApproval files the verb as an Approve-and-Run request (same registry, approval card and run route).
func (c *cerberusRoutes) viaApproval(w http.ResponseWriter, r *http.Request, verb cerbcontrol.Verb, id string, record func(string, int)) {
	if c.s.actions == nil {
		record("unavailable", 503)
		writeJSON(w, 503, map[string]string{"code": "actions_unavailable", "message": "Approve-and-Run is not enabled on this host."})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body)
	if strings.TrimSpace(body.Reason) == "" {
		body.Reason = "Cerberus " + verb.Name + " " + id + " requested from Tachyon"
	}
	h, err := c.s.actions.Request(r.Context(), actionrun.RequestInput{Machine: actionrun.MachineAgentOS, Action: "cerberus.resource." + verb.Route,
		Params: map[string]string{"resource_id": id}, Reason: body.Reason, AgentID: "tachyon-operator"})
	if err != nil {
		var verr *actionrun.ValidationError
		record("refused", 400)
		if errors.As(err, &verr) {
			writeJSON(w, 400, map[string]string{"code": "invalid", "message": verr.Msg})
		} else {
			writeJSON(w, 500, map[string]string{"code": "unavailable", "message": "Could not file the request."})
		}
		return
	}
	record("pending_approval", 202)
	writeJSON(w, 202, map[string]any{"mode": c.mode, "verb": verb.Name, "resource_id": id, "destructive": verb.Destructive, "run": h,
		"next": "Nothing has run. Approve the request in Tachyon, then read " + actionrun.Prefix + "/" + h.RunID})
}
