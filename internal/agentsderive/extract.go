package agentsderive

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
)

const maxOutputBytes = 1 << 20

// Usage is one API call's token counts (Claude message usage).
type Usage struct {
	Input, Output, CacheRead, CacheWrite int64
}

// Context is used + window for the context meter.
func (u Usage) ContextUsed() int64 { return u.Input + u.CacheRead + u.CacheWrite }

// Extracted is everything derived from one session.output frame. Nothing in it is raw text.
type Extracted struct {
	Tools   []OneLiner
	Usage   *Usage // the last API call seen in the frame
	Window  int64  // contextWindow from a result frame
	Model   string
	Compact *int64 // compact_boundary pre_tokens
	Billed  *Usage // summed final usage of the API calls that finished in the frame (message_delta)
}

type claudeLine struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Message struct {
		Model   string `json:"model"`
		Usage   *usage `json:"usage"`
		Content []struct {
			Type  string         `json:"type"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
	} `json:"message"`
	Event struct {
		Type  string `json:"type"`
		Usage *usage `json:"usage"`
	} `json:"event"`
	ModelUsage map[string]struct {
		ContextWindow int64 `json:"contextWindow"`
	} `json:"modelUsage"`
	Compact struct {
		Pre int64 `json:"pre_tokens"`
	} `json:"compact_metadata"`
}

type usage struct {
	In  int64 `json:"input_tokens"`
	CW  int64 `json:"cache_creation_input_tokens"`
	CR  int64 `json:"cache_read_input_tokens"`
	Out int64 `json:"output_tokens"`
}

func (u *usage) get() *Usage {
	if u == nil || (u.In == 0 && u.CW == 0 && u.CR == 0 && u.Out == 0) {
		return nil
	}
	return &Usage{Input: u.In, Output: u.Out, CacheRead: u.CR, CacheWrite: u.CW}
}

// ExtractOutput reads a session.output payload IN MEMORY and returns derived fields only. The payload
// is either {"data":"<base64 of JSON lines>"} or the JSON lines themselves; anything else, anything
// too large, and any line that is not a recognised Claude stream-json object yields nothing. A result
// frame cut short by Tether's payload truncation is tolerated: the lines before the cut still count.
func ExtractOutput(payload []byte) Extracted {
	var out Extracted
	if len(payload) == 0 || len(payload) > maxOutputBytes {
		return out
	}
	body := payload
	var w struct {
		Data string `json:"data"`
		B64  string `json:"b64"`
	}
	if json.Unmarshal(payload, &w) == nil && (w.Data != "" || w.B64 != "") {
		raw := w.Data
		if raw == "" {
			raw = w.B64
		}
		dec, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			dec, err = base64.RawStdEncoding.DecodeString(raw)
		}
		if err != nil {
			return out
		}
		body = dec
	}
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var l claudeLine
		if json.Unmarshal(line, &l) != nil {
			continue // a truncated or foreign line
		}
		switch l.Type {
		case "assistant":
			for _, c := range l.Message.Content {
				if c.Type == "tool_use" && c.Name != "" {
					out.Tools = append(out.Tools, Describe(c.Name, c.Input))
				}
			}
			if u := l.Message.Usage.get(); u != nil {
				out.Usage = u
			}
			if l.Message.Model != "" {
				out.Model = clipRunes(l.Message.Model, 80)
			}
		case "stream_event":
			if l.Event.Type == "message_delta" {
				if u := l.Event.Usage.get(); u != nil {
					out.Usage = u
					if out.Billed == nil {
						out.Billed = &Usage{}
					}
					out.Billed.Input += u.Input
					out.Billed.Output += u.Output
					out.Billed.CacheRead += u.CacheRead
					out.Billed.CacheWrite += u.CacheWrite
				}
			}
		case "result":
			for m, mu := range l.ModelUsage {
				if mu.ContextWindow > 0 {
					out.Window, out.Model = mu.ContextWindow, clipRunes(m, 80)
				}
			}
		case "system":
			if l.Subtype == "compact_boundary" && l.Compact.Pre > 0 {
				p := l.Compact.Pre
				out.Compact = &p
			}
		}
	}
	return out
}
