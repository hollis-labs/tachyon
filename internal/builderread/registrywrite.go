package builderread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/hollis-labs/tachyon/internal/builderapi"
	"github.com/hollis-labs/tachyon/internal/catalogindex"
)

// ProjectWriter updates a Tether registry project profile (needs the registry.write scope).
type ProjectWriter interface {
	Patch(ctx context.Context, urn string, patch map[string]any) error
}

// WriteError carries Tether's HTTP status for a refused registry write.
type WriteError struct{ Status int }

func (e *WriteError) Error() string { return fmt.Sprintf("tether answered HTTP %d", e.Status) }

type tetherWriter struct {
	base   string
	client *http.Client
}

// NewProjectWriter writes with an already authenticated Tether client (the control client).
func NewProjectWriter(base string, client *http.Client) ProjectWriter {
	return &tetherWriter{base: strings.TrimRight(base, "/"), client: client}
}

func (t *tetherWriter) Patch(ctx context.Context, urn string, patch map[string]any) error {
	raw, _ := json.Marshal(patch)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, t.base+"/registry/projects/"+url.PathEscape(urn), bytes.NewReader(raw))
	if err != nil {
		return errors.New("Tether unreachable")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := t.client.Do(req)
	if err != nil {
		return errors.New("Tether unreachable")
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		return &WriteError{Status: res.StatusCode}
	}
	return nil
}

const updatedBy = "tachyon:operator-browser"

// MountWrites registers the registry project writes behind guard (RequireOperator). Without a writer they answer
// 501 so the client can show that Tether write access is missing.
func (r *Reader) MountWrites(mux *http.ServeMux, guard func(http.Handler) http.Handler) {
	b := builderapi.RegistryPrefix + "/projects/{id}"
	mux.Handle("PATCH "+b, guard(http.HandlerFunc(r.registryPatch)))
	mux.Handle("POST "+b+"/link", guard(http.HandlerFunc(r.registryLink)))
	mux.Handle("DELETE "+b+"/link", guard(http.HandlerFunc(r.registryUnlink)))
}

func (r *Reader) writeFailed(w http.ResponseWriter, err error) {
	var we *WriteError
	switch {
	case errors.As(err, &we) && (we.Status == 401 || we.Status == 403):
		fail(w, 403, "tether_write_denied", "Tachyon's Tether token does not hold registry.write.")
	case errors.As(err, &we) && we.Status == 404:
		fail(w, 404, "not_found", "No such project.")
	case errors.As(err, &we) && we.Status == 409:
		fail(w, 409, "conflict", "The registry changed; read and retry.")
	case errors.As(err, &we) && we.Status == 400:
		fail(w, 400, "invalid", "Tether refused the values.")
	default:
		fail(w, 502, "registry_unavailable", "The Tether registry could not be written.")
	}
}

func (r *Reader) project(w http.ResponseWriter, req *http.Request) (*regView, map[string]file, bool) {
	if r.Writer == nil {
		fail(w, 501, "registry_write_unavailable", "Tachyon holds no Tether credential that can write the registry.")
		return nil, nil, false
	}
	views, catalog, ok := r.registry(w, req)
	if !ok {
		return nil, nil, false
	}
	for i := range views {
		if views[i].id == req.PathValue("id") {
			return &views[i], catalog, true
		}
	}
	fail(w, 404, "not_found", "No such project.")
	return nil, nil, false
}

func (r *Reader) reset() {
	if c, ok := r.Projects.(*cachedProjects); ok {
		c.mu.Lock()
		c.at = c.at.Add(-24 * 3600e9)
		c.mu.Unlock()
	}
}

func decodeObject(w http.ResponseWriter, req *http.Request) (map[string]any, bool) {
	var in map[string]any
	b, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 64<<10))
	if err != nil || json.Unmarshal(b, &in) != nil || len(in) == 0 {
		fail(w, 400, "invalid", "Body must be a JSON object with at least one field.")
		return nil, false
	}
	return in, true
}

func stringList(v any, n, max int) ([]string, bool) {
	a, ok := v.([]any)
	if !ok || len(a) > n {
		return nil, false
	}
	out := make([]string, 0, len(a))
	for _, e := range a {
		s, ok := e.(string)
		if !ok || strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > max {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func (r *Reader) registryPatch(w http.ResponseWriter, req *http.Request) {
	v, _, ok := r.project(w, req)
	if !ok {
		return
	}
	in, ok := decodeObject(w, req)
	if !ok {
		return
	}
	patch := map[string]any{"last_updated_by": updatedBy}
	for k, val := range in {
		allowed := false
		for _, f := range builderapi.ProjectWriteFields {
			allowed = allowed || f == k
		}
		bad := func() {
			fail(w, 400, "invalid", "Field "+fmt.Sprintf("%q", k)+" is not writable, or has the wrong type or size.")
		}
		if !allowed || val == nil {
			bad()
			return
		}
		switch k {
		case "display_name", "description", "guidelines":
			s, isStr := val.(string)
			max := map[string]int{"display_name": 120, "description": 2000, "guidelines": 8000}[k]
			if !isStr || utf8.RuneCountInString(s) > max || (k == "display_name" && strings.TrimSpace(s) == "") {
				bad()
				return
			}
			patch[k] = s
		case "tags", "entry_points":
			n, max := 20, 40
			if k == "entry_points" {
				max = 200
			}
			l, isList := stringList(val, n, max)
			if !isList {
				bad()
				return
			}
			patch[k] = map[string]any{"mode": "replace", "value": l}
		case "status":
			s, _ := val.(string)
			switch s {
			case "active":
				patch[k] = "active"
			case "archived":
				patch[k] = "deprecated" // Tether's soft delete: still listed, excluded from default search
			default:
				bad()
				return
			}
		}
	}
	if err := r.Writer.Patch(req.Context(), v.p.URN, patch); err != nil {
		r.writeFailed(w, err)
		return
	}
	r.reset()
	r.registryItem(w, req)
}

func (r *Reader) registryLink(w http.ResponseWriter, req *http.Request) {
	v, catalog, ok := r.project(w, req)
	if !ok {
		return
	}
	in, ok := decodeObject(w, req)
	if !ok {
		return
	}
	cid, _ := in["catalog_id"].(string)
	if _, known := catalog[cid]; !known || len(in) != 1 {
		fail(w, 400, "invalid", "catalog_id must name a catalog project.")
		return
	}
	r.setLink(w, req, v, cid)
}

func (r *Reader) registryUnlink(w http.ResponseWriter, req *http.Request) {
	v, _, ok := r.project(w, req)
	if !ok {
		return
	}
	r.setLink(w, req, v, "")
}

func (r *Reader) setLink(w http.ResponseWriter, req *http.Request, v *regView, cid string) {
	patch := map[string]any{"last_updated_by": updatedBy, "props": map[string]string{"catalog_id": cid}}
	if err := r.Writer.Patch(req.Context(), v.p.URN, patch); err != nil {
		r.writeFailed(w, err)
		return
	}
	r.reset()
	r.registryItem(w, req)
}

var _ = catalogindex.KindProject
