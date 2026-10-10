package daemon

import (
	"context"
	"fmt"
	"time"

	"agentbox/internal/state"
)

// Continue agents after restarts. The chats record each turn while it runs
// (running_turns, internal/chat/restart.go), and one that AgentBox stopping
// cut short — an update, a crash, a quit, a reboot — is still recorded when
// the next daemon starts. continueTurns carries those turns on: leads and the
// Home chat at once, since they run beside the daemon, and agents once Incus
// answers, their machines started the way a message to a stopped agent
// starts one (wake.go).
//
// It leaves alone what somebody chose: a turn the user stopped ended on its
// own and isn't recorded, a paused or retired agent stays as it is, and an
// agent on its command line has no chat to resume. A turn already resumed
// maxTurnResumes times in a row isn't again, so a turn that crashes AgentBox
// doesn't keep restarting it.

// maxTurnResumes is how many times one turn's work is carried on across
// restarts before AgentBox gives up on it.
const maxTurnResumes = 3

// incusWaitLimit is how long continueTurns waits for Incus to answer before
// it gives up on the agents' turns.
const incusWaitLimit = 10 * time.Minute

// continueTurns carries on the turns the last daemon left running, or
// forgets them when the setting is off.
func (s *Server) continueTurns(ctx context.Context) {
	turns, err := s.store.RunningTurns(ctx)
	if err != nil {
		s.logf("continue after restart: %v", err)
		return
	}
	if len(turns) == 0 {
		return
	}
	on, err := s.store.FlagOn(ctx, state.SettingContinueAfterRestart)
	if err != nil {
		s.logf("continue after restart: reading the setting: %v", err)
	}
	var agents []state.RunningTurn
	for _, rt := range turns {
		if !on {
			s.notResumed(ctx, rt, `"Continue agents after restarts" is off in Settings`)
			continue
		}
		if rt.Kind == state.TurnOfAgent {
			agents = append(agents, rt)
			continue
		}
		s.continueLead(ctx, rt)
	}
	if len(agents) == 0 {
		return
	}
	if err := s.waitForIncus(ctx); err != nil {
		for _, rt := range agents {
			s.notResumed(ctx, rt, err.Error())
		}
		return
	}
	for _, rt := range agents {
		if ctx.Err() != nil {
			return
		}
		s.continueAgent(ctx, rt)
	}
}

// continueLead carries on a project chat's turn, or the Home chat's.
func (s *Server) continueLead(ctx context.Context, rt state.RunningTurn) {
	if rt.Resumes >= maxTurnResumes {
		s.notResumed(ctx, rt, tooManyResumes(rt))
		return
	}
	m := s.manager(s.cfg.Log)
	var a state.Agent
	var err error
	if rt.Kind == state.TurnOfHome {
		a, err = m.EnsureHome(ctx)
	} else if a, err = m.Lead(ctx, rt.Project); err == nil {
		// What a turn's start does (ensureLeadFromPath): the lead reads its
		// branch as it is now.
		a, err = m.SyncLead(ctx, a)
	}
	if err != nil {
		s.logf("continue after restart: %s: %v", rt.Ref(), err)
		_ = s.store.EndRunningTurn(ctx, rt.Project, rt.Agent, rt.Turn)
		return
	}
	s.resumeChat(a, rt)
}

// continueAgent carries on an agent's turn, starting its machine first when
// it isn't running, or holding the turn until the VM has the memory to.
func (s *Server) continueAgent(ctx context.Context, rt state.RunningTurn) {
	a, err := s.store.Agent(ctx, rt.Project, rt.Agent)
	if err != nil {
		// Destroyed while AgentBox was down: nothing to carry on.
		_ = s.store.EndRunningTurn(ctx, rt.Project, rt.Agent, rt.Turn)
		return
	}
	switch {
	case a.Status != state.AgentReady:
		s.notResumed(ctx, rt, "the agent is "+a.Status)
		return
	case a.Interface == state.InterfaceCLI:
		s.notResumed(ctx, rt, "the agent works on its command line now")
		return
	case !a.PausedAt.IsZero():
		s.notResumed(ctx, rt, "the agent was paused")
		return
	case rt.Resumes >= maxTurnResumes:
		s.notResumed(ctx, rt, tooManyResumes(rt))
		return
	}
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	inst, err := s.cfg.Incus.Instance(ctx, a.Instance)
	if err != nil {
		s.notResumed(ctx, rt, fmt.Sprintf("its machine couldn't be found (%v)", err))
		return
	}
	switch inst.Status {
	case "Running":
	case "Frozen":
		s.notResumed(ctx, rt, "the agent is paused")
		return
	default:
		if err := s.diskPausedRefusal(a.Ref()); err != nil {
			s.notResumed(ctx, rt, err.Error())
			return
		}
		if err := s.startMachine(ctx, s.manager(s.cfg.Log), a); err != nil {
			s.notResumed(ctx, rt, fmt.Sprintf("its machine couldn't be started (%v)", err))
			return
		}
		s.refreshAgents(ctx)
	}
	s.resumeChat(a, rt)
}

// resumeChat hands the turn to the agent's chat.
func (s *Server) resumeChat(a state.Agent, rt state.RunningTurn) {
	resumed, err := s.chat.Resume(a, rt)
	switch {
	case err != nil:
		s.logf("continue after restart: %s: %v", a.Ref(), err)
		_ = s.store.EndRunningTurn(context.Background(), rt.Project, rt.Agent, rt.Turn)
	case resumed:
		s.logf("%s: carried on the turn AgentBox's restart cut short (resume %d of %d)", a.Ref(), rt.Resumes+1, maxTurnResumes)
	}
}

// notResumed forgets a turn that isn't carried on, saying why in its chat.
func (s *Server) notResumed(ctx context.Context, rt state.RunningTurn, why string) {
	s.logf("continue after restart: not resuming %s's turn: %s", rt.Ref(), why)
	var a state.Agent
	var err error
	switch rt.Kind {
	case state.TurnOfHome:
		a = s.manager(nil).Home()
	default:
		a, err = s.store.Agent(ctx, rt.Project, rt.Agent)
	}
	if err != nil {
		_ = s.store.EndRunningTurn(ctx, rt.Project, rt.Agent, rt.Turn)
		return
	}
	s.chat.NoteNotResumed(a, rt, why)
}

func tooManyResumes(rt state.RunningTurn) string {
	return fmt.Sprintf("it was already resumed %d times, and AgentBox stopped again each time", rt.Resumes)
}

// waitForIncus waits until Incus lists the machines, or gives up.
func (s *Server) waitForIncus(ctx context.Context) error {
	deadline := time.Now().Add(incusWaitLimit)
	for {
		_, err := s.cfg.Incus.Instances(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("incus didn't answer for %s (%v)", incusWaitLimit, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
