package daemon

import (
	"context"
	"time"

	"agentbox/internal/state"
)

// Stopping idle chats' AI tools. A chat keeps Claude Code and its ACP adapter
// running for as long as the daemon does (a lead's, on the VM) or its machine
// does (an agent's): 250–320 MB for Claude Code and 125–150 MB for the adapter
// on the VM of 23 Nov 2026, where twelve of them held about 1.5 GB, for chats
// idle most of the day. With "stop idle chats" on (state.SettingLeadSleep, on
// by default) the chat stops both once it has sat idle a while, and its next
// turn — a message, tell_agent, an agent's finish, a schedule, a project's
// message — resumes the same session (internal/chat/idle.go).
//
// Claude Code only. claude-agent-acp's session/resume is what a reload of the
// tools and an account switch already rely on; Codex's and OpenCode's adapters
// advertise a way to resume too, but nobody has seen it carry a real session
// on, and a chat that wakes up without its conversation is far worse than the
// memory it would save.
//
// Agents sleep as leads do. Their machines' idle reclaim (idlereclaim.go, off
// by default) freezes what an agent left running, but keeps its chat's
// processes running (agent.markKept), so it never gave this memory back. An
// agent's chat sleeps only once its turn has ended with nothing left going
// that would wake it (chat's sleepable): a command it backgrounded, a Monitor,
// a subagent, a timer in Claude Code's own process. Its browser is the
// machine's, which the Playwright MCP server reaches over CDP, so it outlives
// the chat's tools.

// chatSleeps are the AI tools whose chats may sleep.
var chatSleeps = map[string]bool{"claude": true}

// leadDueMargin is how soon a schedule has to be due for an idle lead to stay
// up for it: a wake costs a few seconds, so this only spares stopping a tool
// that would be started again moments later.
const leadDueMargin = 5 * time.Minute

// chatSleepAfter is the chat's IdleStop: how long a's chat may sit idle
// before its AI tool stops, and whether it ever does.
func (s *Server) chatSleepAfter(a state.Agent) (time.Duration, bool) {
	if !chatSleeps[a.AI] {
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

// leadDueSoon is the chat's StayUp: a is a lead, and one of its project's
// schedules is due within leadDueMargin. An agent's chat that a schedule
// tells something is woken by it like by any other message.
func (s *Server) leadDueSoon(a state.Agent) bool {
	if !a.IsLead() {
		return false
	}
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
