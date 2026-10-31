package agentsderive

import (
	"path"
	"regexp"
	"strings"

	"github.com/hollis-labs/libs/security/redact"
)

const MaxOneLiner = 160

// OneLiner is the derived "what is it doing" line.
type OneLiner struct {
	Verb   string `json:"verb"`
	Target string `json:"target"`
	Tool   string `json:"tool"`
}

var (
	taskID    = regexp.MustCompile(`^[A-Z]{2,4}-\d{8}-\d{4}$`)
	taskState = regexp.MustCompile(`^[a-z_]{2,20}$`)
	nsOK      = regexp.MustCompile(`^[A-Za-z0-9._/@-]{1,120}$`)
	cmdOK     = regexp.MustCompile(`^[A-Za-z0-9._/+-]{1,40}$`)
	agentOK   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,60}$`)
)

func str(in map[string]any, key string) string {
	s, _ := in[key].(string)
	return s
}

func dirOf(p string) string {
	if p == "" {
		return ""
	}
	d := path.Base(path.Dir(p))
	if d == "." || d == "/" {
		return ""
	}
	return d + "/"
}

// selfDir names a path that is already a directory.
func selfDir(p string) string {
	if p == "" {
		return ""
	}
	d := path.Base(p)
	if d == "." || d == "/" {
		return ""
	}
	return d + "/"
}

func clipRunes(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// ToolName strips an MCP server prefix: mcp__tether__torque_task_get -> torque_task_get.
func ToolName(name string) string {
	if strings.HasPrefix(name, "mcp__") {
		if i := strings.LastIndex(name, "__"); i > 4 {
			return name[i+2:]
		}
	}
	return name
}

// Describe maps a tool call to its one-liner. The map is the only place a tool argument is read, and
// each row reads named fields only: Bash yields its first token and nothing else (no arguments, no
// body, and no token that looks like an assignment). The result passes the secret redactor and is cut
// to 160 characters.
func Describe(tool string, in map[string]any) OneLiner {
	name := ToolName(tool)
	o := OneLiner{Tool: clipRunes(name, 60)}
	switch name {
	case "Read", "NotebookRead":
		o.Verb, o.Target = "Reading", dirOf(str(in, "file_path"))
	case "Grep":
		o.Verb = "Searching"
		o.Target = selfDir(str(in, "path"))
		if q := clipRunes(str(in, "pattern"), 60); q != "" {
			o.Target = strings.TrimSpace(o.Target + ` for "` + q + `"`)
		}
	case "Glob":
		o.Verb, o.Target = "Listing", selfDir(str(in, "path"))
	case "Edit", "MultiEdit", "NotebookEdit":
		o.Verb, o.Target = "Editing", path.Base(str(in, "file_path"))
	case "Write":
		o.Verb, o.Target = "Writing", path.Base(str(in, "file_path"))
	case "Bash":
		o.Verb = "Running"
		o.Target = shellTarget(strings.Fields(str(in, "command")))
	case "WebFetch":
		o.Verb, o.Target = "Fetching", "a web page"
	case "WebSearch":
		o.Verb, o.Target = "Searching", "the web"
	case "Task", "Agent":
		o.Verb, o.Target = "Delegating", ""
	case "TodoWrite":
		o.Verb, o.Target = "Updating", "its todo list"
	case "AskUserQuestion":
		o.Verb, o.Target = "Asking", "you"
	case "torque_task_transition":
		o.Verb = "Updating"
		if id, st := str(in, "id"), str(in, "status"); taskID.MatchString(id) && taskState.MatchString(st) {
			o.Target = "task " + id + " to " + st
		} else {
			o.Target = "a task"
		}
	case "tether_message_send", "tether_message_reply":
		o.Verb = "Sent mail"
		if to := str(in, "to"); to != "" {
			if a := agentOK.FindString(path.Base(to)); a != "" && a == path.Base(to) {
				o.Target = "to " + a
			}
		}
	default:
		switch {
		case strings.HasPrefix(name, "tesseract_") || strings.Contains(name, "memory_") || strings.Contains(name, "workspace_") || strings.Contains(name, "knowledge_"):
			o.Verb = "Reading"
			if strings.Contains(name, "write") || strings.Contains(name, "promote") {
				o.Verb = "Writing"
			}
			o.Target = "memory"
			if ns := str(in, "namespace"); nsOK.MatchString(ns) {
				o.Target += " " + ns
			}
		default:
			o.Verb, o.Target = mcpLine(name)
		}
	}
	o.Target = strings.TrimSpace(redact.Text(o.Target))
	for len([]rune(o.Verb+" "+o.Target)) > MaxOneLiner {
		o.Target = string([]rune(o.Target)[:len([]rune(o.Target))-1])
	}
	return o
}

var shellPrefix = map[string]bool{"sudo": true, "env": true, "time": true, "nohup": true, "command": true, "exec": true}
var shellBlock = map[string]string{"for": "a shell loop", "while": "a shell loop", "until": "a shell loop", "if": "a shell script", "case": "a shell script",
	"do": "a shell script", "then": "a shell script", "{": "a shell script", "(": "a shell script", "[": "a shell script", "[[": "a shell script", "function": "a shell script"}

// shellTarget names a Bash command by its first real word: wrappers (sudo, env, time) and a leading cd are
// skipped, a shell keyword reads as a script or loop, and anything unusual reads as "a command".
func shellTarget(f []string) string {
	for len(f) > 0 && shellPrefix[f[0]] {
		f = f[1:]
	}
	if len(f) > 2 && f[0] == "cd" {
		for i, w := range f {
			if w == "&&" || w == ";" {
				f = f[i+1:]
				break
			}
		}
	}
	if len(f) == 0 {
		return "a command"
	}
	if b, ok := shellBlock[f[0]]; ok {
		return b
	}
	if cmdOK.MatchString(f[0]) && !strings.Contains(f[0], "=") {
		return path.Base(f[0])
	}
	return "a command"
}

var mcpServers = map[string]bool{"tether": true, "torque": true, "tangent": true, "tesseract": true}
var readWords = map[string]bool{"get": true, "list": true, "show": true, "read": true, "search": true, "recall": true, "inbox": true, "status": true, "history": true, "lookup": true, "view": true, "peek": true, "wait": true, "await": true, "health": true, "whoami": true, "facets": true}
var writeWords = map[string]bool{"add": true, "create": true, "send": true, "write": true, "post": true, "enqueue": true, "emit": true, "reply": true, "notify": true, "register": true, "launch": true, "open": true}
var changeWords = map[string]bool{"update": true, "set": true, "edit": true, "transition": true, "assign": true, "mark": true, "move": true, "promote": true, "touch": true, "renew": true, "ack": true, "consume": true}
var removeWords = map[string]bool{"delete": true, "remove": true, "archive": true, "cancel": true, "stop": true, "revoke": true, "withdraw": true, "close": true, "purge": true}

// mcpLine turns an MCP tool name into verb + short target ("torque_comment_add" -> "Writing", "comment"),
// never the raw tool name. A name it cannot read becomes "Using a tool".
func mcpLine(name string) (string, string) {
	words := strings.Split(strings.ToLower(name), "_")
	if len(words) > 1 && mcpServers[words[0]] {
		words = words[1:]
	}
	verb, vi := "", -1
	for i, w := range words {
		switch {
		case changeWords[w]:
			verb, vi = "Updating", i
		case writeWords[w]:
			verb, vi = "Writing", i
		case removeWords[w]:
			verb, vi = "Removing", i
		case readWords[w] && vi < 0:
			verb, vi = "Reading", i
		}
		if verb == "Updating" || verb == "Removing" {
			break
		}
	}
	var noun []string
	for i, w := range words {
		if i != vi && !(verb == "Updating" && (readWords[w] || writeWords[w])) && w != "" {
			noun = append(noun, w)
		}
	}
	if verb == "" || len(noun) == 0 {
		return "Using", "a tool"
	}
	return verb, strings.Join(noun, " ")
}
