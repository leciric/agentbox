package chat

import (
	"errors"
	"fmt"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Reloading a chat's tools. Every AI tool reads its MCP servers — AgentBox's
// own and the project's connectors, written into its configuration by
// agent.SyncConnectors — once, when its adapter starts. So a connector added
// mid-chat reaches the chat only through a new adapter, and that adapter
// resumes the same session (session/resume, else session/load: connect), so
// the model keeps its context and the transcript stays. An adapter that can
// resume neither starts a new session, which the chat says before and after.
//
// A change to the project's connectors marks every running chat it reaches
// (ToolsChanged), and the next turn restarts the adapter before it begins, the
// way a new context window does (window.go): cheap, since a chat nobody writes
// to again never restarts. The composer shows the change meanwhile, with
// "Reload tools" to do it now.

// ReloadTools restarts a chat's adapter now, resuming its session with the
// MCP servers its tool's configuration has now. Refused while the session is
// busy: a turn, a hidden prompt, or background work a restart would end.
func (m *Manager) ReloadTools(a state.Agent) (api.ChatSession, error) {
	c, err := m.conversation(a)
	if err != nil {
		return api.ChatSession{}, err
	}
	defer c.mu.Unlock()
	if err := c.reloadRefused(); err != nil {
		return api.ChatSession{}, err
	}
	ad := c.adapter
	if ad == nil {
		// Nothing running: the adapter that starts next reads them anyway.
		c.toolsChanged(false)
		c.markSession()
		c.flush(false)
		return clone(c.session), nil
	}
	tool := ToolNames[c.agent.AI]
	it := c.add("notice", c.lastTurn())
	if ad.resumable {
		it.Text = fmt.Sprintf("Reloading the tools: %s restarts with the MCP servers it has now and resumes this conversation.", tool)
	} else {
		it.Text = fmt.Sprintf("Reloading the tools: %s restarts with the MCP servers it has now, in a new session, as its adapter can't resume one.", tool)
	}
	c.restartForTools()
	c.flush(true)
	return clone(c.session), nil
}

// reloadRefused says why the tools can't be reloaded now, or nil. Caller
// holds c.mu.
func (c *conversation) reloadRefused() error {
	tool := ToolNames[c.agent.AI]
	switch {
	case c.turn != nil:
		return fmt.Errorf("%s is in the middle of a turn: reload its tools once it ends", tool)
	case c.rolling || c.compaction != nil:
		return errors.New("the session is being compacted: reload its tools once that ends")
	case len(c.session.Background) > 0:
		return fmt.Errorf("%s has work running in the background (%s), which a restart would end: reload its tools once it finishes", tool, strings.Join(c.session.Background, ", "))
	case c.adapter != nil && !c.adapter.ready:
		return fmt.Errorf("%s is still starting, and gets the tools it has now", tool)
	}
	return nil
}

// restartForTools stops the adapter and starts another, which resumes the
// session (connect). Caller holds c.mu, and no turn runs.
func (c *conversation) restartForTools() {
	c.stopAdapter()
	c.toolsChanged(false)
	c.startAdapter()
}

// toolsChanged sets or clears ToolsChanged. Caller holds c.mu.
func (c *conversation) toolsChanged(changed bool) {
	c.toolsRestart = changed
	c.session.ToolsChanged = changed
}

// ToolsChanged is told that the MCP servers of a project's chats changed: of
// every chat of the project, its own included, when agent is "", one agent's
// otherwise. Call it once their configuration is rewritten. A chat whose
// adapter runs is marked, and reloads before its next turn when its adapter
// can resume; one that runs none reads them when it starts.
func (m *Manager) ToolsChanged(project, agent string) {
	m.mu.Lock()
	var convs []*conversation
	for _, c := range m.convs {
		convs = append(convs, c)
	}
	m.mu.Unlock()
	for _, c := range convs {
		c.mu.Lock()
		if c.agent.Project == project && (agent == "" || c.agent.Name == agent) && c.adapter != nil && !c.gone {
			c.toolsChanged(true)
			c.markSession()
			c.flush(false)
		}
		c.mu.Unlock()
	}
}

// reloadBeforeTurn restarts the adapter before a turn begins, when the tools
// changed since it started and it can resume. Caller holds c.mu.
func (c *conversation) reloadBeforeTurn() bool {
	ad := c.adapter
	if !c.toolsRestart || ad == nil || !ad.ready || !ad.resumable || c.rolling || len(c.session.Background) > 0 {
		return false
	}
	c.add("notice", c.turn.id).Text = fmt.Sprintf("The tools changed: %s restarts with the new MCP servers and resumes this conversation.", ToolNames[c.agent.AI])
	c.stopAdapter()
	c.toolsChanged(false)
	return true
}
