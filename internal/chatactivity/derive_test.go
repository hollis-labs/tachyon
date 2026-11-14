package chatactivity

import (
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	for tool, want := range map[string]string{
		"mcp__tether__tether_message_send": TypeMessageSent, "tether_message_reply": TypeMessageSent, "mcp__tether__tether_message_inbox": TypeMessageRead,
		"mcp__tether__torque_task_get": TypeTaskChecked, "torque_task_list": TypeTaskChecked, "mcp__tether__torque_comment_add": TypeTaskUpdated,
		"CronCreate": TypeReminder, "ScheduleWakeup": TypeReminder, "Monitor": TypeWait,
	} {
		d, ok := Classify(tool)
		if !ok || d.Type != want || !Types[d.Type] {
			t.Errorf("%s: %v %v, want %s", tool, d, ok, want)
		}
	}
	for _, noise := range []string{"Read", "Edit", "Bash", "Grep", "mcp__tesseract__tesseract_recall", "torque_task_delete", ""} {
		if d, ok := Classify(noise); ok {
			t.Errorf("%q is noise but classified as %v", noise, d)
		}
	}
}

func TestThrottleCollapsesRepeats(t *testing.T) {
	th := &Throttle{Window: 10 * time.Second}
	now := time.Now()
	if !th.Allow("tether:s1", TypeTaskChecked, now) || th.Allow("tether:s1", TypeTaskChecked, now.Add(5*time.Second)) {
		t.Fatal("a repeat inside the window must be dropped")
	}
	if !th.Allow("tether:s1", TypeMessageSent, now) || !th.Allow("tether:s2", TypeTaskChecked, now) {
		t.Fatal("another type or thread is independent")
	}
	if !th.Allow("tether:s1", TypeTaskChecked, now.Add(11*time.Second)) {
		t.Fatal("after the window it is allowed again")
	}
}
