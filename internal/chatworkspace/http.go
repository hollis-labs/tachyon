package chatworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/hollis-labs/tachyon/internal/chatapi"
)

// Routes are the explicit patterns the server mounts under chatapi.Prefix, each behind
// RequireOperator. ContentRoute is mounted separately behind RequireOperatorReads.
var Routes = []string{
	"GET /threads/{ref}/workspace", "POST /threads/{ref}/workspace",
	"PATCH /threads/{ref}/workspace/{id}", "DELETE /threads/{ref}/workspace/{id}",
	"GET /threads/{ref}/scratchpad", "PUT /threads/{ref}/scratchpad",
	"GET /settings", "PUT /settings",
}

const ContentRoute = "GET " + chatapi.WorkspaceContentPath

var refPattern = regexp.MustCompile(`^[a-z][a-z0-9+.-]{0,31}:[^\s]{1,480}$`)

// deniedSchemes are never valid as a link or ref target.
var deniedSchemes = map[string]bool{"javascript": true, "data": true, "file": true, "vbscript": true, "blob": true}

type Handler struct {
	Store *Store
	// Exists reports whether the thread exists (nil error) so rows never attach to a ghost.
	Exists func(ctx context.Context, ref string) error
	// Published is called after a successful change with the event fields; may be nil.
	Published func(ctx context.Context, thread, op, id string)
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	reply(w, status, map[string]string{"code": code, "message": msg})
}

func (h *Handler) problem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		fail(w, 404, "not_found", "No such workspace item.")
	case errors.Is(err, ErrConflict):
		fail(w, 409, "conflict", "The scratchpad changed since you loaded it. Reload and merge.")
	case errors.Is(err, ErrLimit):
		fail(w, 413, "limit_reached", "This chat's workspace is full (100 items or 250 MiB).")
	case errors.Is(err, ErrTooLarge):
		fail(w, 413, "too_large", "Files can be at most 25 MiB.")
	case errors.Is(err, ErrType):
		fail(w, 415, "type_not_allowed", "This file type is not allowed. Text, JSON, PDF, PNG, JPEG, GIF, WebP and SVG can be added.")
	default:
		fail(w, 500, "internal", "The workspace could not complete the request.")
	}
}

func (h *Handler) thread(w http.ResponseWriter, r *http.Request) (string, bool) {
	t, err := chatapi.ParseRef(r.PathValue("ref"))
	if err != nil {
		fail(w, 400, "invalid", err.Error())
		return "", false
	}
	if h.Exists != nil {
		if err := h.Exists(r.Context(), t.String()); err != nil {
			fail(w, 404, "not_found", "No such thread.")
			return "", false
		}
	}
	return t.String(), true
}

func (h *Handler) publish(ctx context.Context, thread, op, id string) {
	if h.Published != nil {
		h.Published(context.WithoutCancel(ctx), thread, op, id)
	}
}

// Mux returns the handler for Routes mounted under chatapi.Prefix.
func (h *Handler) Mux() http.Handler {
	m := http.NewServeMux()
	handlers := map[string]http.HandlerFunc{
		"GET /threads/{ref}/workspace": h.list, "POST /threads/{ref}/workspace": h.add,
		"PATCH /threads/{ref}/workspace/{id}": h.patch, "DELETE /threads/{ref}/workspace/{id}": h.remove,
		"GET /threads/{ref}/scratchpad": h.getPad, "PUT /threads/{ref}/scratchpad": h.putPad,
		"GET /settings": h.getSettings, "PUT /settings": h.putSettings,
	}
	for _, r := range Routes {
		method, path, _ := strings.Cut(r, " ")
		m.HandleFunc(method+" "+chatapi.Prefix+path, handlers[r])
	}
	return m
}

// Content serves file bytes. It is mounted behind RequireOperatorReads.
func (h *Handler) Content() http.Handler { return http.HandlerFunc(h.content) }

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	items, lim, err := h.Store.List(r.Context(), t)
	if err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 200, map[string]any{"items": items, "limits": lim})
}

func (h *Handler) add(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	var it Item
	var err error
	ct, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	switch strings.TrimSpace(ct) {
	case "multipart/form-data":
		it, err = h.receiveFile(w, r, t)
	case "application/json":
		it, err = h.parseLink(w, r)
	default:
		fail(w, 415, "type_not_allowed", "Send multipart/form-data with a file, or JSON with a link or ref.")
		return
	}
	if err != nil {
		h.problem(w, err)
		return
	}
	if it.ID == "" && it.Name == "" { // an error response was already written
		return
	}
	it.AddedBy = "user"
	saved, err := h.Store.Add(r.Context(), t, it)
	if err != nil {
		if it.Type == "file" || it.Type == "image" {
			_ = os.Remove(h.Store.Path(t, it.ID))
		}
		h.problem(w, err)
		return
	}
	h.publish(r.Context(), t, "add", saved.ID)
	reply(w, 201, saved)
}

func (h *Handler) parseLink(w http.ResponseWriter, r *http.Request) (Item, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var in struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		URL    string `json:"url"`
		Ref    string `json:"ref"`
		Shared bool   `json:"shared"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	name := ""
	if err := dec.Decode(&in); err == nil {
		name = CleanName(in.Name)
	}
	if name == "" {
		fail(w, 400, "invalid", "A link or ref needs a name.")
		return Item{}, nil
	}
	it := Item{Type: in.Type, Name: name, Shared: in.Shared}
	switch in.Type {
	case "link":
		u, err := url.Parse(in.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(in.URL) > 2048 {
			fail(w, 400, "invalid", "A link needs an http or https URL.")
			return Item{}, nil
		}
		it.URL = &in.URL
	case "ref":
		scheme, _, _ := strings.Cut(in.Ref, ":")
		if !refPattern.MatchString(in.Ref) || deniedSchemes[strings.ToLower(scheme)] {
			fail(w, 400, "invalid", "A ref looks like scheme:target, for example torque:task/CW-1.")
			return Item{}, nil
		}
		it.Ref = &in.Ref
	default:
		fail(w, 400, "invalid", "type must be link or ref for JSON; upload files as multipart.")
		return Item{}, nil
	}
	return it, nil
}

func (h *Handler) receiveFile(w http.ResponseWriter, r *http.Request, thread string) (Item, error) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxFileBytes+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		fail(w, 400, "invalid", "Malformed multipart body.")
		return Item{}, nil
	}
	var it Item
	var haveFile bool
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return Item{}, ErrTooLarge
			}
			fail(w, 400, "invalid", "Malformed multipart body.")
			return Item{}, nil
		}
		switch p.FormName() {
		case "shared":
			b, _ := io.ReadAll(io.LimitReader(p, 16))
			it.Shared = strings.TrimSpace(string(b)) == "true"
		case "file":
			if haveFile {
				fail(w, 400, "invalid", "Send one file at a time.")
				return Item{}, nil
			}
			if err := h.storeFile(p, thread, &it); err != nil {
				return Item{}, err
			}
			haveFile = true
		}
	}
	if !haveFile {
		fail(w, 400, "invalid", "A file part named \"file\" is required.")
		return Item{}, nil
	}
	return it, nil
}

func (h *Handler) storeFile(p *multipart.Part, thread string, it *Item) error {
	name := CleanName(p.FileName())
	if name == "" {
		name = "file"
	}
	tmp, head, size, sum, err := h.Store.Receive(thread, p)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			err = ErrTooLarge
		}
		return err
	}
	mime, typ, err := Classify(name, head, size <= int64(len(head)))
	if err == nil && mime == "application/json" && !validJSON(tmp) {
		err = ErrType
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	it.ID = firstID("")
	if err := os.Rename(tmp, h.Store.Path(thread, it.ID)); err != nil {
		os.Remove(tmp)
		return err
	}
	it.Type, it.Name, it.Mime, it.SizeBytes, it.SHA256 = typ, name, &mime, &size, &sum
	return nil
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var in struct {
		Name   *string `json:"name"`
		Shared *bool   `json:"shared"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || (in.Name == nil && in.Shared == nil) {
		fail(w, 400, "invalid", "Send name and/or shared.")
		return
	}
	if in.Name != nil {
		n := CleanName(*in.Name)
		if n == "" {
			fail(w, 400, "invalid", "A name cannot be empty.")
			return
		}
		in.Name = &n
	}
	it, err := h.Store.Patch(r.Context(), t, r.PathValue("id"), in.Name, in.Shared)
	if err != nil {
		h.problem(w, err)
		return
	}
	if in.Shared != nil {
		op := "unshare"
		if *in.Shared {
			op = "share"
		}
		h.publish(r.Context(), t, op, it.ID)
	}
	reply(w, 200, it)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	if err := h.Store.Delete(r.Context(), t, r.PathValue("id")); err != nil {
		h.problem(w, err)
		return
	}
	h.publish(r.Context(), t, "remove", r.PathValue("id"))
	reply(w, 200, map[string]bool{"ok": true})
}

func (h *Handler) content(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	it, err := h.Store.Get(r.Context(), t, r.PathValue("id"))
	if err != nil {
		h.problem(w, err)
		return
	}
	if it.Type != "file" && it.Type != "image" {
		fail(w, 404, "not_found", "This item has no file content.")
		return
	}
	f, err := os.Open(h.Store.Path(t, it.ID))
	if err != nil {
		fail(w, 404, "not_found", "The file is no longer available.")
		return
	}
	defer f.Close()
	ctype := "application/octet-stream"
	if it.Mime != nil {
		switch *it.Mime {
		case "text/plain", "text/markdown", "text/csv", "application/pdf", "image/png", "image/jpeg", "image/gif", "image/webp":
			ctype = *it.Mime
		}
	}
	hd := w.Header()
	hd.Set("Content-Type", ctype)
	hd.Set("Content-Disposition", `attachment; filename*=UTF-8''`+url.PathEscape(it.Name))
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	hd.Set("Cache-Control", "no-store")
	if it.SizeBytes != nil {
		hd.Set("Content-Length", strconv.FormatInt(*it.SizeBytes, 10))
	}
	_, _ = io.Copy(w, f)
}

func (h *Handler) getPad(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	sp, err := h.Store.Scratchpad(r.Context(), t)
	if err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 200, sp)
}

func (h *Handler) putPad(w http.ResponseWriter, r *http.Request) {
	t, ok := h.thread(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxScratchpad+(8<<10))
	var in struct {
		Body     *string `json:"body"`
		Expected *int64  `json:"expected_revision"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			fail(w, 413, "too_large", "The scratchpad holds at most 64 KiB.")
			return
		}
		fail(w, 400, "invalid", "Send body and expected_revision.")
		return
	}
	if in.Body == nil || in.Expected == nil || *in.Expected < 0 {
		fail(w, 400, "invalid", "Send body and expected_revision.")
		return
	}
	if len(*in.Body) > MaxScratchpad {
		fail(w, 413, "too_large", "The scratchpad holds at most 64 KiB.")
		return
	}
	sp, err := h.Store.SaveScratchpad(r.Context(), t, *in.Body, *in.Expected)
	if err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 200, sp)
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.Store.Settings(r.Context())
	if err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 200, s)
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var in struct {
		ScratchpadPlacement     *string `json:"scratchpad_placement"`
		TerminalCloseOnComplete *bool   `json:"terminal_close_on_complete"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || (in.ScratchpadPlacement == nil && in.TerminalCloseOnComplete == nil) ||
		(in.ScratchpadPlacement != nil && *in.ScratchpadPlacement != "drawer" && *in.ScratchpadPlacement != "aside") {
		fail(w, 400, "invalid", "Send scratchpad_placement (drawer or aside) and/or terminal_close_on_complete (true or false).")
		return
	}
	// A field left out keeps its saved value.
	cur, err := h.Store.Settings(r.Context())
	if err != nil {
		h.problem(w, err)
		return
	}
	if in.ScratchpadPlacement != nil {
		cur.ScratchpadPlacement = *in.ScratchpadPlacement
	}
	if in.TerminalCloseOnComplete != nil {
		cur.TerminalCloseOnComplete = *in.TerminalCloseOnComplete
	}
	if err := h.Store.SaveSettings(r.Context(), cur); err != nil {
		h.problem(w, err)
		return
	}
	reply(w, 200, cur)
}
