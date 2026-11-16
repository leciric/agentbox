package chat

// Attention is what a chat waits on the user for, and whether it is stuck,
// as the sessions list shows each of a project's chat sessions
// (daemon/sessionstatus.go).
type Attention struct {
	State string // as State
	// Permissions is its own permission requests waiting for an answer.
	Permissions int
	// AgentPermissions is the agents' requests its chat shows as cards
	// (ShowAgentPermission) still waiting for one.
	AgentPermissions int
	// Stalled says its running turn was marked stalled (SetStalled).
	Stalled bool
}

// Closing says the daemon is shutting its chats down.
func (m *Manager) Closing() bool { return m.closing.Load() }

// Attention says what ref's chat waits on the user for. A conversation not
// loaded since the daemon started waits on nothing: its requests ended with
// the daemon that asked them.
func (m *Manager) Attention(ref string) Attention {
	c := m.existing(ref)
	if c == nil {
		return Attention{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	a := Attention{State: c.session.State, Stalled: c.session.StalledSince != nil}
	if c.turn != nil {
		a.Permissions = len(c.pending)
	}
	for _, it := range c.items {
		if p := it.AgentPermission; p != nil && p.Permission.Outcome == "" {
			a.AgentPermissions++
		}
	}
	return a
}
