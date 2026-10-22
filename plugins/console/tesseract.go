package main

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
	"github.com/hollis-labs/tachyon/pkg/pluginkit"
)

const tesseractPrefix = "/api/plugins/console/v1/tesseract"

// tesseractReads is the whole reviewed read surface: an item by id, its
// history, and a memory revision by id. Nothing else reaches Tesseract.
var tesseractReads = regexp.MustCompile(`^/(items/[A-Za-z0-9_-]{1,64}(/history)?|memory/revisions/[A-Za-z0-9_-]{1,64})$`)

// TesseractHandler is a GET-only window onto Tesseract so the operator can
// preview and open the ids and keys agents reference. The credential is a
// Tesseract token holding tesseract.read only; it stays in the host's private
// token file and is never sent to the browser.
type TesseractHandler struct {
	proxy *pluginkit.Proxy
	err   error
}

func NewTesseractHandler(addr, tokenFile string) *TesseractHandler {
	h := &TesseractHandler{}
	addr = strings.TrimRight(strings.TrimSpace(addr), "/")
	if addr == "" {
		return h
	}
	rt, err := upstreamauth.Wrap(http.DefaultTransport.(*http.Transport).Clone(), addr, tokenFile)
	if err != nil {
		h.err = err
		return h
	}
	h.proxy = pluginkit.NewProxy("console", pluginkit.Route{Prefix: "/v1/tesseract", Upstream: addr, Target: "/v1"})
	h.proxy.Client = &http.Client{Transport: rt, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return h
}

func (*TesseractHandler) Match(path string) bool { return strings.HasPrefix(path, tesseractPrefix+"/") }

func (h *TesseractHandler) Handle(ctx context.Context, req subprocess.HTTPRequest) subprocess.HTTPResponse {
	if req.Method != http.MethodGet {
		return forgeErr(405, "read_only", "Tesseract access is read-only")
	}
	if !tesseractReads.MatchString(strings.TrimPrefix(req.Path, tesseractPrefix)) {
		return forgeErr(404, "not_found", "Unknown Tesseract read")
	}
	if h.err != nil {
		return forgeErr(503, "configuration", "Tesseract address or credential transport is invalid")
	}
	if h.proxy == nil {
		return forgeErr(503, "configuration", "Configure TESSERACT_URL and TESSERACT_TOKEN_FILE on the Tachyon host")
	}
	res, _ := h.proxy.HTTPHandle(ctx, req)
	res.Headers["Cache-Control"] = "no-store"
	return res
}
