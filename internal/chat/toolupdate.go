package chat

// ToolUpdated has every agent's session on tool (an AI tool, "claude") restart
// on the version AgentBox now runs before its next turn: a turn running
// carries on with the one it started on, and the restart resumes the
// conversation, as a changed context window's does. Leads are left alone:
// they run the host's tools, not the base image's.
func (m *Manager) ToolUpdated(tool string) {
	m.mu.Lock()
	convs := make([]*conversation, 0, len(m.convs))
	for _, c := range m.convs {
		convs = append(convs, c)
	}
	m.mu.Unlock()
	for _, c := range convs {
		c.mu.Lock()
		if !c.gone && c.adapter != nil && c.agent.AI == tool && !c.agent.IsLead() {
			c.versionRestart = true
		}
		c.mu.Unlock()
	}
}

// Relaunch has the chat ref restart its adapter before its next turn, which
// resumes the conversation: the launcher would now start it somewhere else,
// as the Home chat's moves between the user's computer and the VM.
func (m *Manager) Relaunch(ref string) {
	c := m.existing(ref)
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.gone && c.adapter != nil {
		c.versionRestart = true
	}
}
