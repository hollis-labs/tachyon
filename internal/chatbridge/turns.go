// Package chatbridge owns Tachyon's durable chat turns and Tether transport.
package chatbridge

import (
	"encoding/json"
	"net/http"
	"regexp"
)

const Prefix = "/api/plugins/console/v1/chat-turns"

var replyPath = regexp.MustCompile(`^/items/[a-zA-Z0-9_-]+/reply$`)

func fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}
