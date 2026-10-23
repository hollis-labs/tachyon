package actionrun

import (
	"regexp"
	"sync"
)

const (
	// MaxStoredOutput bounds what is persisted and returned to the agent.
	MaxStoredOutput = 64 << 10
	// replayBytes bounds what a reattaching terminal can replay.
	replayBytes = 256 << 10
)

var (
	ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][A-Za-z0-9]`)
	// Known secret shapes. Redaction is a backstop; the real protection is that
	// prompts for secrets do not echo and input is never recorded.
	secretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(pass(?:word|phrase)|secret|token|api[_-]?key)([ \t]*[:=][ \t]*)\S+`),
		regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{8,}`),
		regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
		regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(-----END [A-Z ]*PRIVATE KEY-----|$)`),
		regexp.MustCompile(`\b(?:ghp|gho|ghs|github_pat|sk|xox[bp])[-_][A-Za-z0-9_-]{16,}`),
	}
)

// Redact strips terminal control sequences and known secret shapes.
func Redact(s string) string {
	s = ansi.ReplaceAllString(s, "")
	for i, re := range secretPatterns {
		if i == 0 {
			s = re.ReplaceAllString(s, "${1}${2}[redacted]")
		} else {
			s = re.ReplaceAllString(s, "[redacted]")
		}
	}
	return s
}

// transcript collects the persisted copy of an unattended run's output. Only
// actions that take no input are recorded; interactive terminals never are.
type transcript struct {
	mu      sync.Mutex
	buf     []byte
	dropped bool
}

func (t *transcript) output(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4*MaxStoredOutput {
		t.buf = t.buf[len(t.buf)-4*MaxStoredOutput:]
		t.dropped = true
	}
}

// text returns the bounded, redacted transcript (the tail when it is long).
func (t *transcript) text() string {
	t.mu.Lock()
	raw := string(t.buf)
	dropped := t.dropped
	t.mu.Unlock()
	s := Redact(raw)
	if len(s) > MaxStoredOutput {
		s = s[len(s)-MaxStoredOutput:]
		dropped = true
	}
	if dropped {
		s = "[earlier output truncated]\n" + s
	}
	return s
}

// ring is the in-memory replay buffer for reattaching terminals.
type ring struct {
	buf []byte
}

func (r *ring) add(p []byte) {
	r.buf = append(r.buf, p...)
	if len(r.buf) > replayBytes {
		r.buf = append([]byte(nil), r.buf[len(r.buf)-replayBytes:]...)
	}
}
