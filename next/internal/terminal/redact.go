package terminal

import "regexp"

// Redaction of run output, carried from the old actionrun package (output.go), which the terminal shared.

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
