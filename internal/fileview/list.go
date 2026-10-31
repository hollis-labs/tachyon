package fileview

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/tachyon/internal/fileviewapi"
)

// maxListEntries bounds one directory answer; the tree view asks again per folder.
const maxListEntries = 1000

// Entry is one item of GET /list.
type Entry struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"` // dir | file
	Size  *int64 `json:"size,omitempty"`
	MTime *int64 `json:"mtime,omitempty"`
}

// list answers GET /list?root=<root_id>&path=<rel>: the entries of one directory under an allow-listed root.
// Every entry goes through the guard, so secret names and links that leave the root are omitted.
func (f *Files) list(w http.ResponseWriter, r *http.Request) {
	roots, _, err := f.Guard.Svc.Effective(r.Context())
	if err != nil {
		jsonErr(w, 500, "internal", "Settings are unavailable.")
		return
	}
	q := r.URL.Query()
	var root *EffectiveRoot
	for i := range roots {
		if roots[i].ID == q.Get("root") {
			root = &roots[i]
		}
	}
	rel := filepath.ToSlash(filepath.Clean("/" + q.Get("path")))
	if strings.ContainsRune(q.Get("path"), 0) || strings.Contains("/"+q.Get("path")+"/", "/../") {
		jsonErr(w, 400, "invalid", "path must be relative to the root and contain no '..'.")
		return
	}
	if root == nil {
		jsonErr(w, 404, fileviewapi.DenyNotFoundHidden, "No such root.")
		return
	}
	dir := filepath.Join(root.Path, filepath.FromSlash(rel))
	res, den := f.Guard.Resolve(r.Context(), dir)
	if den != nil || !res.Info.IsDir() {
		f.auditDenied(r, "stat", dir, fileviewapi.DenyNotFoundHidden)
		jsonErr(w, 404, fileviewapi.DenyNotFoundHidden, "No such directory.")
		return
	}
	des, err := os.ReadDir(res.Path)
	if err != nil {
		jsonErr(w, 404, fileviewapi.DenyNotFoundHidden, "No such directory.")
		return
	}
	entries := make([]Entry, 0, len(des))
	truncated := false
	for _, de := range des {
		if len(entries) >= maxListEntries {
			truncated = true
			break
		}
		cr, cden := f.Guard.Resolve(r.Context(), filepath.Join(res.Path, de.Name()))
		if cden != nil {
			continue
		}
		switch {
		case cr.Info.IsDir():
			entries = append(entries, Entry{Name: de.Name(), Kind: "dir"})
		case cr.Info.Mode().IsRegular():
			entries = append(entries, Entry{Name: de.Name(), Kind: "file", Size: pointer(cr.Info.Size()), MTime: pointer(cr.Info.ModTime().UnixMilli())})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if (entries[i].Kind == "dir") != (entries[j].Kind == "dir") {
			return entries[i].Kind == "dir"
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	if f.Audit != nil {
		f.Audit.RecordAsync(AuditEntry{Principal: f.principal(r), Kind: "stat", Source: "host", PathOrRef: dir, Outcome: "ok", Count: len(entries)})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"root": root.ID, "path": strings.TrimPrefix(rel, "/"), "entries": entries, "truncated": truncated})
}
