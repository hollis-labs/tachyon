package dataverbs

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/hollis-labs/libs/core/util/svcerr"
)

// Prefix stays inside the existing operator cookie's path. This is a host-owned
// route shared by every UI plugin, independent of the console subprocess.
const Prefix = "/api/plugins/console/v1/data"

type providerView struct {
	ID    string `json:"id"`
	Verbs []Verb `json:"verbs"`
}
type response struct {
	Version  int             `json:"version"`
	Provider string          `json:"provider"`
	Verb     string          `json:"verb"`
	Data     json.RawMessage `json:"data"`
}

// NewHTTP mounts explicitly selected providers. The host supplies its live
// mutation authorization middleware; nil fails closed. Read verbs are GET-only
// and follow the host's existing read-access boundary. POST can only call writes.
func NewHTTP(providers []Provider, authorize func(http.Handler) http.Handler) http.Handler {
	return newHTTP(Prefix, providers, authorize)
}

func newHTTP(prefix string, providers []Provider, authorize func(http.Handler) http.Handler) http.Handler {
	registered := map[string]Provider{}
	verbs := map[string]map[string]Verb{}
	for _, p := range providers {
		if p == nil || p.ID() == "" || strings.Contains(p.ID(), "/") {
			panic("invalid data provider")
		}
		if _, ok := registered[p.ID()]; ok {
			panic("duplicate data provider")
		}
		registered[p.ID()] = p
		verbs[p.ID()] = map[string]Verb{}
		for _, v := range p.Verbs() {
			if v.Name == "" || strings.Contains(v.Name, "/") {
				panic("invalid data verb")
			}
			if _, ok := verbs[p.ID()][v.Name]; ok {
				panic("duplicate data verb")
			}
			verbs[p.ID()][v.Name] = v
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == prefix+"/providers" {
			if r.Method != http.MethodGet {
				methodError(w, "GET")
				return
			}
			if r.URL.RawQuery != "" {
				writeError(w, invalid("Discovery does not accept query parameters."))
				return
			}
			entries := make([]providerView, 0, len(registered))
			for _, p := range registered {
				entries = append(entries, providerView{ID: p.ID(), Verbs: p.Verbs()})
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
			writeJSON(w, map[string]any{"version": ContractVersion, "providers": entries})
			return
		}
		rel, ok := strings.CutPrefix(r.URL.Path, prefix+"/providers/")
		parts := strings.Split(rel, "/")
		if !ok || len(parts) != 3 || parts[1] != "verbs" {
			writeError(w, svcerr.New(svcerr.CodeNotFound, "Unknown data route."))
			return
		}
		p, ok := registered[parts[0]]
		if !ok {
			writeError(w, svcerr.New(svcerr.CodeNotFound, "Unknown data provider."))
			return
		}
		v, ok := verbs[parts[0]][parts[2]]
		if !ok {
			writeError(w, svcerr.New(svcerr.CodeNotFound, "Unknown data verb."))
			return
		}
		method := "GET"
		if v.Mutates {
			method = "POST"
		}
		if r.Method != method {
			methodError(w, method)
			return
		}
		call := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, err := requestInput(w, r, v.Mutates)
			if err != nil {
				writeError(w, err)
				return
			}
			actor := ""
			if v.Mutates {
				actor = "operator:browser"
			}
			result, err := p.Invoke(r.Context(), v.Name, raw, actor)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, response{Version: ContractVersion, Provider: p.ID(), Verb: v.Name, Data: result})
		})
		if !v.Mutates {
			call.ServeHTTP(w, r)
			return
		}
		if authorize == nil {
			writeError(w, svcerr.New(svcerr.CodePermission, "Operator authorization is unavailable."))
			return
		}
		authorize(call).ServeHTTP(w, r)
	})
}
func requestInput(w http.ResponseWriter, r *http.Request, mutation bool) (json.RawMessage, error) {
	if !mutation {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			return nil, invalid("Read verbs use the input query parameter, not a body.")
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return nil, invalid("Invalid query encoding.")
		}
		for key, values := range query {
			if key != "input" || len(values) != 1 {
				return nil, invalid("Only one input query parameter is supported.")
			}
		}
		raw := query.Get("input")
		if len(raw) > 16<<10 {
			return nil, svcerr.New("too_large", "Read input exceeds 16 KiB.", svcerr.WithStatus(413))
		}
		return json.RawMessage(raw), nil
	}
	if r.URL.RawQuery != "" {
		return nil, invalid("Write verbs accept a JSON body without query parameters.")
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, svcerr.New("unsupported_media_type", "Use application/json.", svcerr.WithStatus(415))
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxInputBytes))
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return nil, svcerr.New("too_large", "Data command exceeds the size limit.", svcerr.WithStatus(413))
		}
		return nil, invalid("Could not read data command.")
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, invalid("A JSON command body is required.")
	}
	return raw, nil
}
func methodError(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, svcerr.New("method_not_allowed", "Use the verb's declared HTTP method.", svcerr.WithStatus(405)))
}
func writeError(w http.ResponseWriter, err error) {
	// Match the host's existing flat error envelope while sharing svcerr semantics.
	var typed *svcerr.Error
	code, message := "internal", "Local data operation failed."
	if errors.As(err, &typed) {
		code, message = string(typed.Code), typed.Message
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(svcerr.StatusFor(err, 500))
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
