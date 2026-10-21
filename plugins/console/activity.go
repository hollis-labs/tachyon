package main

import "regexp"

var activityRecipient = regexp.MustCompile(`^msg://(?:agent|session)/[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]{1,128}$`)

// Reviewed metadata families. Session output, prompts, tool arguments and
// transcripts are deliberately not an activity feed.
var activityEventKinds = []string{
	"tool.call.started", "tool.call.finished", "progress.checkpoint",
	"session.state_changed", "session.launch_failed", "session.turn_output",
	"session.turn_routed", "session.stall_detected", "session.stall_cleared",
	"message.sent", "message.queued", "message.delivered", "message.read",
	"message.consumed", "message.failed", "message.dead_lettered", "message.redriven",
	"message.canceled", "message.wake.outcome",
	"fleet.node.down", "fleet.node.up", "fleet.node.degraded",
}
