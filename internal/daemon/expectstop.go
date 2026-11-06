package daemon

// An agent that AgentBox or the user stops, pauses, retires or destroys often
// has a turn open as its machine goes down: a finished agent waiting on its
// pull request's checks, a lead retiring it while it still talks. Its AI tool
// then exits mid-turn, or the turn is cancelled, and without this the daemon
// would read it as a crash: the outcome "failed" over the agent's real finish,
// an AgentFailed or AgentChatLost event, an error in the Inbox and the lead
// woken to see to it. expectStop marks the agent before its machine goes down,
// and the turn's end is then left alone: the agent keeps the outcome its last
// real turn gave it (done, with its pull request), and nobody is told.
//
// The mark lasts until the agent comes back: its machine started or resumed,
// or its AI tool launched again. A crash after that reports as ever.

// expectStop marks that a stop somebody asked for is taking ref's machine
// down, and what happens to its turn is no failure.
func (s *Server) expectStop(ref string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping == nil {
		s.stopping = map[string]bool{}
	}
	s.stopping[ref] = true
}

// stopOnPurpose runs stop, which takes ref's machine down, with expectStop's
// mark set, and clears the mark when stop fails: a machine still up after a
// failed pause or stop hasn't been taken down, and a crash of it later must
// still report. The mark would otherwise last until its AI tool next
// launches, which a paused-in-vain agent's never does.
func (s *Server) stopOnPurpose(ref string, stop func() error) error {
	s.expectStop(ref)
	if err := stop(); err != nil {
		s.agentBack(ref)
		return err
	}
	return nil
}

// agentBack clears expectStop's mark: ref's machine or AI tool is running
// again, and a turn that dies now is a real failure.
func (s *Server) agentBack(ref string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.stopping, ref)
}

// stopExpected is whether ref is being stopped on purpose (expectStop).
func (s *Server) stopExpected(ref string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopping[ref]
}
