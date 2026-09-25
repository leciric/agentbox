package agent

import (
	"context"
	"encoding/json"
	"strings"

	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// What AgentBox tells a model about its project before the model has said
// anything: the recap a project's chat is given in place of the conversation it
// can no longer see, and the slice of the same memory a worker gets in its
// brief.
//
// Neither is assembled here. Both are [memory.Store.BuildContext]
// (D75), asked for a different
// audience with a different budget — one builder, so what a worker is told
// about its project and what its chat is told can't drift apart. This file is
// only the two consumers: where the query comes from, and how much each may
// spend.

// leadRecap builds the "Where we are" section of a project chat's brief out of
// what the project remembers: what it is doing now, the narrative of the
// conversation so far, what is still open, and whatever memory says about the
// goal and the current task.
//
// The lead spends the project's whole context budget, because its recap stands
// in for a conversation that is gone; a worker's is a quarter of it, because a
// worker still has the project to read and search_memory to ask.
//
// It is rendered on every configureLead, not only just after a rollover, so a
// daemon restarted afterwards — which rewrites the brief from the store — hands
// the chat the same recap rather than an empty one.
//
// A project whose memory is empty gets "", and the section isn't rendered at
// all. That is every project before its first compaction.
func (m *Manager) leadRecap(ctx context.Context, project string) (string, error) {
	built, err := m.buildContext(ctx, project, "", memory.ForLead)
	if err != nil {
		return "", err
	}
	return built.Text, nil
}

// projectKnowledge is the "What the project knows" section of a worker's brief:
// the same memory the lead reads, cut to a worker's share of the budget and
// picked out by what this agent was asked to do.
//
// The query is the agent's title and its task. A task is not a column on the
// agent — it is a message the daemon sends once the machine is up — so the
// brief written while an agent is being created is given it directly, and every
// rewrite afterwards recovers it from the agent_created event the daemon
// captured. An agent whose task nobody recorded falls back to its title, and
// then to what the project says it is working on.
func (m *Manager) projectKnowledge(ctx context.Context, a state.Agent, task string) (string, error) {
	if task == "" {
		task = m.recordedTask(ctx, a)
	}
	built, err := m.buildContext(ctx, a.Project, joinWords(a.Title, task), memory.ForAgent)
	if err != nil {
		return "", err
	}
	return built.Text, nil
}

// recordedTask is what this agent was asked to do, as the daemon wrote it down
// when the agent was created. It is best-effort: a brief is worth writing
// without it, and every agent made before capture existed has none.
func (m *Manager) recordedTask(ctx context.Context, a state.Agent) string {
	events, err := m.memory().Events(ctx, a.Project, memory.EventFilter{
		Agent: a.Name, Types: []string{"agent_created"}, Limit: 1,
	})
	if err != nil || len(events) == 0 {
		return ""
	}
	return payloadString(events[0].Payload, "task")
}

// buildContext is one build against the project's own budget, or a worker's
// share of it. The share is applied here rather than by the caller so that
// every brief AgentBox writes spends the same way.
func (m *Manager) buildContext(ctx context.Context, project, query string, who memory.Audience) (memory.Context, error) {
	budget, err := m.contextBudget(ctx, project)
	if err != nil {
		return memory.Context{}, err
	}
	if who == memory.ForAgent {
		budget = memory.AgentBudget(budget)
	}
	return m.memory().BuildContext(ctx, memory.ContextRequest{
		Project: project, Query: query, Budget: budget, For: who,
	})
}

func (m *Manager) contextBudget(ctx context.Context, project string) (int, error) {
	p, err := m.Store.Project(ctx, project)
	if err != nil {
		return 0, err
	}
	return p.ContextBudget, nil
}

// memory is the project memory, on the database package state opened.
func (m *Manager) memory() *memory.Store { return memory.New(m.Store.DB()) }

// joinWords makes one query out of several pieces, skipping the empty ones.
func joinWords(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " ")
}

// payloadString reads one string field out of an event's payload, or "" when
// the payload isn't an object or hasn't got it. An event's shape is its own
// business (D72), so nothing here insists on one.
func payloadString(payload json.RawMessage, field string) string {
	var doc map[string]any
	if len(payload) == 0 || json.Unmarshal(payload, &doc) != nil {
		return ""
	}
	s, _ := doc[field].(string)
	return strings.TrimSpace(s)
}

// ProjectKnowledge is the memory section of an agent's brief, for a brief that
// is being shown rather than written: the app's preview asks for the brief an
// agent made now would read, and an agent that doesn't exist yet has a name
// and a project and nothing else.
func (m *Manager) ProjectKnowledge(ctx context.Context, a state.Agent) (string, error) {
	return m.projectKnowledge(ctx, a, "")
}
