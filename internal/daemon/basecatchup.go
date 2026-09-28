package daemon

import (
	"fmt"
	"strings"
	"sync"
)

// catchUpTail is how much of what a catch-up ran is kept for the agent to
// read when it fails: enough for apt's or mise's error and what led to it.
const catchUpTail = 4 << 10

// catchUpNote is what the agent refreshing a project base reads above its task
// about the catch-up AgentBox ran on its machine (agent.Manager.CatchUp): what
// it brought the machine up to, or what it tried and how it failed, with the
// last of what it ran. "" when there was nothing to catch up.
func catchUpNote(what string, err error, ran string) string {
	switch {
	case err != nil:
		note := "Before this message, AgentBox tried to catch this machine up with the base image it makes now"
		if what != "" {
			note += " (" + what + ")"
		}
		note += fmt.Sprintf(", and it failed: %v.", err)
		if ran = strings.TrimSpace(ran); ran != "" {
			note += "\n\nThe last of what it ran:\n\n```\n" + ran + "\n```"
		}
		return note + "\n\nIf the cause is on this machine — an apt source that no longer answers, a package held back, a full disk — fix it, " +
			"and say what it was in your summary. The base saved from this machine will still show as behind the image, " +
			"and refreshing it again runs the catch-up again."
	case what != "":
		return "Before this message, AgentBox caught this machine up with the base image it makes now: " + what + ". " +
			"The base saved from this machine records that. Check that the project still installs, builds and runs on them as part of the task below."
	}
	return ""
}

// tailWriter keeps the last max bytes written to it, from the start of a line
// where it can.
type tailWriter struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[over:]
		if i := strings.IndexByte(string(t.buf), '\n'); i >= 0 && i < len(t.buf)-1 {
			t.buf = t.buf[i+1:]
		}
	}
	return len(p), nil
}

func (t *tailWriter) String() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
