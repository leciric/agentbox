package daemon

import (
	"context"
	"time"

	"agentbox/internal/state"
)

// Stopping idle leads' AI tools. A project's chat keeps Claude Code running on
// the VM, some 200–230 MB each, for a chat that is idle most of the day; with
// "stop idle chats" on (state.SettingLeadSleep, on by default) the chat stops
// it once it has sat idle a while, and its next turn — a message, an agent's
// finish, a schedule, a project's message — resumes the same session
// (internal/chat/idle.go).
//
// Claude Code only. claude-agent-acp's session/resume is what a reload of the
// tools and an account switch already rely on; Codex's and OpenCode's adapters
// advertise a way to resume too, but nobody has seen it carry a real session
// on, and a lead that wakes up without its conversation is far worse than the
// memory it would save. Agents are left alone: their machines have idle
// reclaim of their own (idlereclaim.go).

// leadSleeps are the AI tools whose leads' chats may sleep.
var leadSleeps = map[string]bool{"claude": true}

// leadDueMargin is how soon a schedule has to be due for an idle lead to stay
// up for it: a wake costs a few seconds, so this only spares stopping a tool
// that would be started again moments later.
const leadDueMargin = 5 * time.Minute

// leadSleepAfter is the chat's IdleStop: how long a's chat may sit idle
// before its AI tool stops, and whether it ever does.
func (s *Server) leadSleepAfter(a state.Agent) (time.Duration, bool) {
	if !a.IsLead() || !leadSleeps[a.AI] {
		return 0, false
	}
	on, after, err := s.store.LeadSleep(context.Background())
	if err != nil {
		s.logf("reading %s: %v", state.SettingLeadSleep, err)
		return 0, true
	}
	if !on {
		return 0, true
	}
	return after, true
}

// leadDueSoon is the chat's StayUp: one of the lead's project's schedules is
// due within leadDueMargin.
func (s *Server) leadDueSoon(a state.Agent) bool {
	schedules, err := s.store.Schedules(context.Background(), a.Project)
	if err != nil {
		return false
	}
	soon := time.Now().Add(leadDueMargin)
	for _, sc := range schedules {
		if !sc.Paused && !sc.NextRun.IsZero() && sc.NextRun.Before(soon) {
			return true
		}
	}
	return false
}
