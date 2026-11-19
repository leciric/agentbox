package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/hostos"
	"agentbox/internal/pressure"
	"agentbox/internal/state"
)

// Continue agents after restarts. The chats record each turn while it runs
// (running_turns, internal/chat/restart.go), and one that AgentBox stopping
// cut short — an update, a crash, a quit, a reboot — is still recorded when
// the next daemon starts. continueTurns carries those turns on, and boots
// the machines a daemon before it left On hold for memory (admission.go):
// their machines started the way a message to a stopped agent starts one
// (wake.go).
//
// One at a time, through memory admission: the projects' chats and the Home
// chat first, since they run beside the daemon, then agents oldest first,
// each waiting for the one before to settle (waitSettled: its memory shows
// in a reading, the VM's memory stall is calm, and a boot fits) before it
// starts. On 18 Nov 2026 every resume at once — ten machines and eight
// Claude sessions after each VM start — ran the user's computer out of
// memory, which took the VM down and started it all again, six times.
//
// Nothing resumes by itself after AgentBox stopped unexpectedly (a crash,
// the VM or the computer going down with it running: the crash marker,
// report.Crashes.Unclean), or when the VM's supervisor says this boot is
// after a crash loop (api.VMBoot.Safe). Its agents are On hold
// (state.HoldRestart) and its turns held, each chat saying so, until the
// user's Resume all (POST /v1/restart-hold/resume) carries them on, in the
// same order, one at a time. Nor does one agent's work once AgentBox has
// carried it on maxAutoResumes times in a row, counted across restarts: a
// run ends once AgentBox stays up for autoResumeRun.
//
// It leaves alone what somebody chose: a turn the user stopped ended on its
// own and isn't recorded, a paused or retired agent stays as it is, and an
// agent on its command line has no chat to resume. A turn already resumed
// maxTurnResumes times isn't again, so a turn that crashes AgentBox doesn't
// keep restarting it.

// maxTurnResumes is how many times one turn's work is carried on across
// restarts before AgentBox gives up on it.
const maxTurnResumes = 3

// maxAutoResumes is how many times in a row AgentBox carries one agent's
// work on by itself as it starts, whatever its turns: past it, the agent
// waits for the user. autoResumeRun is how long AgentBox runs after one for
// the next to start a new run.
const (
	maxAutoResumes = 3
	autoResumeRun  = time.Hour
)

// resumeSettle is the least time after one resume before the next: a
// machine's boot and a chat's session take most of their memory in it, for
// the reading after it to show them. resumeSteadyFor is the most the next
// waits on top of it for the VM's memory to stop falling; a memory stall,
// it waits out however long it lasts.
const (
	resumeSettle    = 15 * time.Second
	resumeSteadyFor = 2 * time.Minute
)

// incusWaitLimit is how long continueTurns waits for Incus to answer before
// it gives up on the agents' turns.
const incusWaitLimit = 10 * time.Minute

// Why a turn waits for the user, as its chat says it.
const (
	whyUnexpected = "AgentBox stopped unexpectedly"
	whyTooOften   = "AgentBox restarted %d times in a row while it worked"
)

// resumeState is the line of resumes, and why nothing resumes by itself.
type resumeState struct {
	mu sync.Mutex
	// unexpected is set as the daemon starts when the last one didn't stop
	// as it should, or the VM's supervisor says so (VMBoot.Safe), with
	// why its account.
	unexpected bool
	why        string
	queue      []resumeItem
	running    bool
	// settle waits until the resume begun at since has settled (zero:
	// before the first); waitSettled, or a test's.
	settle func(ctx context.Context, since time.Time) error
	// readBoot is api.ReadVMBoot of this VM's disks, or a test's.
	readBoot func() api.VMBoot
}

func newResumeState() *resumeState {
	return &resumeState{readBoot: func() api.VMBoot { return api.ReadVMBoot(hostos.VMDisks()) }}
}

// resumeItem is one thing to carry on: a chat's turn, or a machine On hold
// for memory with none.
type resumeItem struct {
	ref  string
	lead bool // a project's chat or the Home chat: no machine
	// made orders agents, oldest first: the agent's CreatedAt (a lead's,
	// its turn's start).
	made  time.Time
	turn  *state.RunningTurn
	agent state.Agent // the agent, for a machine with no turn
	// byUser is the user's Resume all: not counted, nor capped.
	byUser bool
}

// sortResumes is the order they go in: the chats first, then agents oldest
// first.
func sortResumes(items []resumeItem) {
	slices.SortStableFunc(items, func(a, b resumeItem) int {
		if a.lead != b.lead {
			if a.lead {
				return -1
			}
			return 1
		}
		return cmp.Or(a.made.Compare(b.made), cmp.Compare(a.ref, b.ref))
	})
}

// noteLastStop reads, as the daemon starts, how the last one stopped: from
// unclean, the crash marker, and the VM's supervisor.
func (s *Server) noteLastStop(unclean bool, started time.Time) {
	r := s.resumes
	boot := r.readBoot()
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case boot.Safe:
		r.unexpected, r.why = true, boot.Why
		s.logf("continue after restart: the VM is after a crash (%s): nothing resumes by itself", cmp.Or(boot.Why, "no reason given"))
	case unclean:
		r.unexpected = true
		s.logf("continue after restart: the daemon started %s didn't stop as it should: nothing resumes by itself", started.Format(time.RFC3339))
	}
}

// continueTurns carries on, one at a time, the turns the last daemon left
// running and the machines it left On hold for memory; or holds them for
// the user, after AgentBox stopped unexpectedly; or forgets the turns when
// the setting is off. It returns once every one has started or been held.
func (s *Server) continueTurns(ctx context.Context) {
	turns, err := s.store.RunningTurns(ctx)
	if err != nil {
		s.logf("continue after restart: %v", err)
		return
	}
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		s.logf("continue after restart: %v", err)
		return
	}
	on := true
	if len(turns) > 0 {
		if on, err = s.store.FlagOn(ctx, state.SettingContinueAfterRestart); err != nil {
			s.logf("continue after restart: reading the setting: %v", err)
		}
	}
	byRef := map[string]state.Agent{}
	for _, a := range agents {
		byRef[a.Ref()] = a
	}
	var items []resumeItem
	resuming := map[string]bool{}
	for _, rt := range turns {
		resuming[rt.Ref()] = true
		if rt.Held {
			continue // it waits for the user still
		}
		if !on {
			s.notResumed(ctx, rt, `"Continue agents after restarts" is off in Settings`)
			continue
		}
		it := resumeItem{ref: rt.Ref(), lead: rt.Kind != state.TurnOfAgent, made: rt.StartedAt, turn: &rt}
		if a, ok := byRef[rt.Ref()]; ok && !it.lead {
			it.made, it.agent = a.CreatedAt, a
		} else if !it.lead {
			// Destroyed while AgentBox was down: nothing to carry on.
			_ = s.store.EndRunningTurn(ctx, rt.Project, rt.Agent, rt.Turn)
			continue
		}
		items = append(items, it)
	}
	for _, a := range agents {
		if a.Hold == state.HoldMemory && !a.IsLead() && a.Status == state.AgentReady && !resuming[a.Ref()] {
			items = append(items, resumeItem{ref: a.Ref(), made: a.CreatedAt, agent: a})
		}
	}
	if len(items) == 0 {
		return
	}
	r := s.resumes
	r.mu.Lock()
	unexpected, why := r.unexpected, r.why
	r.mu.Unlock()
	if unexpected {
		s.holdForUser(ctx, items, true, why)
		return
	}
	s.resumeInTurn(ctx, items)
}

// resumeInTurn adds items to the line and, unless it is already being gone
// through, goes through it, one at a time, until it is empty.
func (s *Server) resumeInTurn(ctx context.Context, items []resumeItem) {
	r := s.resumes
	r.mu.Lock()
	for _, it := range items {
		if !slices.ContainsFunc(r.queue, func(q resumeItem) bool { return q.ref == it.ref }) {
			r.queue = append(r.queue, it)
		}
		if !it.lead && it.turn != nil {
			// Until its turn runs again, it is resuming, not idle: it may
			// wait a while for its turn, and its machine to start.
			s.setOutcome(it.agent, api.AgentOutcomeResuming, "")
		}
	}
	sortResumes(r.queue)
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()
	s.logf("continue after restart: carrying on %d chat(s) and agent(s), one at a time", len(items))
	incus := false
	var last time.Time
	for {
		r.mu.Lock()
		if len(r.queue) == 0 || ctx.Err() != nil {
			r.mu.Unlock()
			return
		}
		it := r.queue[0]
		r.queue = r.queue[1:]
		r.mu.Unlock()
		if !it.lead && !incus {
			if err := s.waitForIncus(ctx); err != nil {
				s.giveUpOn(ctx, it, err.Error())
				continue
			}
			incus = true
		}
		settle := r.settle
		if settle == nil {
			settle = s.waitSettled
		}
		if err := settle(ctx, last); err != nil {
			return // stopping: the next daemon finds the rest where they are
		}
		if s.resumeOne(ctx, it) {
			last = time.Now()
		}
	}
}

// giveUpOn is a resume that can't go: its turn isn't carried on.
func (s *Server) giveUpOn(ctx context.Context, it resumeItem, why string) {
	if it.turn != nil {
		s.notResumed(ctx, *it.turn, why)
		return
	}
	s.logf("continue after restart: not starting %s's machine: %s", it.ref, why)
}

// resumeOne carries one on, and reports whether it started anything.
func (s *Server) resumeOne(ctx context.Context, it resumeItem) bool {
	switch {
	case it.turn == nil:
		return s.wakeHeld(ctx, it)
	case it.lead:
		return s.continueLead(ctx, it)
	default:
		return s.continueAgent(ctx, it)
	}
}

// autoResume is it about to start, by itself: it reports whether it may,
// and counts it. One past maxAutoResumes in a row waits for the user.
func (s *Server) autoResume(ctx context.Context, it resumeItem) bool {
	if it.byUser {
		return true
	}
	now := time.Now()
	project, name := splitRef(it.ref)
	n, err := s.store.AutoResumes(ctx, project, name, now.Add(-autoResumeRun))
	if err != nil {
		s.logf("continue after restart: %s: %v", it.ref, err)
	}
	if n >= maxAutoResumes {
		s.holdForUser(ctx, []resumeItem{it}, false, fmt.Sprintf(whyTooOften, n))
		return false
	}
	// Counted before it starts: a start that takes AgentBox down counts too.
	if err := s.store.CountAutoResume(ctx, project, name, now, now.Add(-autoResumeRun)); err != nil {
		s.logf("continue after restart: %s: counting the resume: %v", it.ref, err)
	}
	return true
}

func splitRef(ref string) (project, name string) {
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == '/' {
			return ref[:i], ref[i+1:]
		}
	}
	return "", ref
}

// continueLead carries on a project chat's turn, or the Home chat's.
func (s *Server) continueLead(ctx context.Context, it resumeItem) bool {
	rt := *it.turn
	if rt.Resumes >= maxTurnResumes {
		s.notResumed(ctx, rt, tooManyResumes(rt))
		return false
	}
	if !s.autoResume(ctx, it) {
		return false
	}
	m := s.manager(s.cfg.Log)
	var a state.Agent
	var err error
	if rt.Kind == state.TurnOfHome {
		a, err = m.EnsureHomeSession(ctx, sessionOfName(rt.Agent))
	} else if a, err = m.LeadSession(ctx, rt.Project, sessionOfName(rt.Agent)); err == nil {
		// What a turn's start does (ensureLeadFromPath): the lead reads its
		// branch as it is now.
		a, err = m.SyncLead(ctx, a)
	}
	if err != nil {
		s.logf("continue after restart: %s: %v", rt.Ref(), err)
		_ = s.store.EndRunningTurn(ctx, rt.Project, rt.Agent, rt.Turn)
		return false
	}
	return s.resumeChat(a, rt)
}

// continueAgent carries on an agent's turn, starting its machine first when
// it isn't running: its boot waits its turn for memory, On hold, as any
// other boot does (admission.go).
func (s *Server) continueAgent(ctx context.Context, it resumeItem) bool {
	rt := *it.turn
	a, err := s.store.Agent(ctx, rt.Project, rt.Agent)
	if err != nil {
		// Destroyed while AgentBox was down: nothing to carry on.
		_ = s.store.EndRunningTurn(ctx, rt.Project, rt.Agent, rt.Turn)
		return false
	}
	s.setOutcome(a, api.AgentOutcomeResuming, "")
	switch {
	case a.Status != state.AgentReady:
		s.notResumed(ctx, rt, "the agent is "+a.Status)
		return false
	case a.Interface == state.InterfaceCLI:
		s.notResumed(ctx, rt, "the agent works on its command line now")
		return false
	case !a.PausedAt.IsZero():
		s.notResumed(ctx, rt, "the agent was paused")
		return false
	case rt.Resumes >= maxTurnResumes:
		s.notResumed(ctx, rt, tooManyResumes(rt))
		return false
	case !s.autoResume(ctx, it):
		return false // it waits for you
	}
	unlock := s.wakes.lock(a.Ref())
	inst, err := s.cfg.Incus.Instance(ctx, a.Instance)
	if err != nil {
		unlock()
		s.notResumed(ctx, rt, fmt.Sprintf("its machine couldn't be found (%v)", err))
		return false
	}
	switch inst.Status {
	case "Running":
		unlock()
		// What its boot would have taken it off: running, it is held for
		// nothing (a Resume all's place in line, a daemon that died alone).
		s.unhold(ctx, a, "")
	case "Frozen":
		unlock()
		s.notResumed(ctx, rt, "the agent is paused")
		return false
	default:
		if err := s.diskPausedRefusal(a.Ref()); err != nil {
			unlock()
			s.notResumed(ctx, rt, err.Error())
			return false
		}
		if s.bootWaits() {
			// Its boot waits its turn for memory, On hold (admission.go):
			// messages to it meanwhile don't wait behind it.
			unlock()
			return s.continueOnHold(ctx, a, rt)
		}
		err := s.startMachine(ctx, s.manager(s.cfg.Log), a, "a turn to carry on after a restart")
		unlock()
		if err != nil {
			s.notResumed(ctx, rt, fmt.Sprintf("its machine couldn't be started (%v)", err))
			return false
		}
		s.refreshAgents(ctx)
	}
	return s.resumeChat(a, rt)
}

// continueOnHold starts an agent's machine once admission lets it boot,
// then carries on its turn.
func (s *Server) continueOnHold(ctx context.Context, a state.Agent, rt state.RunningTurn) bool {
	s.mu.Lock()
	if s.waking == nil {
		s.waking = map[string]bool{}
	}
	already := s.waking[a.Ref()]
	s.waking[a.Ref()] = true
	s.mu.Unlock()
	if already {
		return false
	}
	defer func() {
		s.mu.Lock()
		delete(s.waking, a.Ref())
		s.mu.Unlock()
	}()
	if err := s.startMachine(ctx, s.manager(s.cfg.Log), a, "a turn to carry on after a restart, its turn for memory come"); err != nil {
		s.notResumed(ctx, rt, fmt.Sprintf("its machine couldn't be started (%v)", err))
		return false
	}
	s.refreshAgents(ctx)
	return s.resumeChat(a, rt)
}

// wakeHeld boots a machine a daemon before this one left On hold for
// memory, with no turn to carry on: it waits its turn in admission again.
// Those on hold for being idle stay stopped until touched.
func (s *Server) wakeHeld(ctx context.Context, it resumeItem) bool {
	project, name := splitRef(it.ref)
	a, err := s.store.Agent(ctx, project, name)
	if err != nil || a.Hold != state.HoldMemory || a.Status != state.AgentReady {
		return false // started, stopped or gone meanwhile
	}
	if !s.autoResume(ctx, it) {
		return false
	}
	return s.wakeNow(ctx, s.manager(s.cfg.Log), a, "its turn, on hold for memory before AgentBox restarted")
}

// resumeChat hands the turn to the agent's chat, and reports whether it
// carried it on.
func (s *Server) resumeChat(a state.Agent, rt state.RunningTurn) bool {
	resumed, err := s.chat.Resume(a, rt)
	if !resumed {
		// Newer work runs on it, or nothing could: whatever it is doing, it
		// isn't resuming.
		s.mu.Lock()
		if s.outcomes[a.Ref()].kind == api.AgentOutcomeResuming {
			delete(s.outcomes, a.Ref())
		}
		s.mu.Unlock()
	}
	switch {
	case err != nil:
		s.logf("continue after restart: %s: %v", a.Ref(), err)
		_ = s.store.EndRunningTurn(context.Background(), rt.Project, rt.Agent, rt.Turn)
	case resumed:
		// Whether the project's chat waited on this agent died with the
		// daemon that knew (leadWaits lives in memory). A turn cut short in
		// the middle of the work is most likely the work it asked for, so
		// the finish that follows the resume reaches it as it would have.
		s.leadAsked(a)
		s.logf("%s: carried on the turn AgentBox's restart cut short (resume %d of %d)", a.Ref(), rt.Resumes+1, maxTurnResumes)
	}
	return resumed
}

// waitSettled waits until the resume begun at since has settled, for the
// next to start: resumeSettle after it, a reading of the VM past that whose
// memory stall is calm, with room for a machine's boot and no builds held
// for memory, and, for up to resumeSteadyFor, its memory no longer falling.
// A daemon with no pressure loop (a test's) doesn't wait.
func (s *Server) waitSettled(ctx context.Context, since time.Time) error {
	if s.pressureEvery <= 0 {
		return nil
	}
	start := time.Now()
	told := ""
	for {
		h := s.heavy
		h.mu.Lock()
		reading, trend, at := h.reading, h.trend, h.readAt
		h.mu.Unlock()
		now := time.Now()
		why := "a reading of the VM's memory"
		if !at.IsZero() && (since.IsZero() || at.After(since.Add(resumeSettle))) {
			why = s.unsettled(reading, trend, now.Sub(start) < resumeSteadyFor, now)
		}
		if why == "" {
			return nil
		}
		if why != told && now.Sub(start) > resumeSettle {
			s.logf("continue after restart: the next waits for %s", why)
			told = why
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.pressureEvery):
		}
	}
}

// unsettled is pressure.Policy.Unsettled, with admission's say on whether a
// boot fits and builds held for memory.
func (s *Server) unsettled(reading pressure.Sample, trend pressure.Trend, steady bool, now time.Time) string {
	if why := pressure.DefaultPolicy.Unsettled(reading, trend, steady); why != "" {
		return why
	}
	if s.buildsHeld() {
		return "builds held for memory"
	}
	ad := s.admit
	ad.mu.Lock()
	fits := ad.a.Fits(reading.Mem, now)
	ad.mu.Unlock()
	if !fits {
		return "the memory to boot a machine"
	}
	return ""
}

// notResumed forgets a turn that isn't carried on, saying why in its chat.
func (s *Server) notResumed(ctx context.Context, rt state.RunningTurn, why string) {
	s.logf("continue after restart: not resuming %s's turn: %s", rt.Ref(), why)
	if rt.Kind == state.TurnOfAgent {
		s.mu.Lock()
		s.outcomes[rt.Ref()] = agentOutcome{kind: api.AgentOutcomeFailed, note: "AgentBox restarted mid-turn and didn't resume it: " + why}
		s.mu.Unlock()
	}
	a, err := s.turnAgent(ctx, rt)
	if err != nil {
		_ = s.store.EndRunningTurn(ctx, rt.Project, rt.Agent, rt.Turn)
		return
	}
	s.chat.NoteNotResumed(a, rt, why)
}

// turnAgent is the agent whose chat rt is.
func (s *Server) turnAgent(ctx context.Context, rt state.RunningTurn) (state.Agent, error) {
	if rt.Kind == state.TurnOfHome {
		return s.manager(nil).HomeSession(sessionOfName(rt.Agent)), nil
	}
	return s.store.Agent(ctx, rt.Project, rt.Agent)
}

func tooManyResumes(rt state.RunningTurn) string {
	return fmt.Sprintf("it was already resumed %d times, and AgentBox stopped again each time", rt.Resumes)
}

// restartHoldRecord is SettingRestartHold.
type restartHoldRecord struct {
	Unexpected bool      `json:"unexpected,omitempty"`
	Why        string    `json:"why,omitempty"`
	Since      time.Time `json:"since"`
}

// holdForUser holds items for the user's Resume all: each agent On hold
// (HoldRestart), each turn held with its chat saying why. unexpected says
// AgentBox stopped unexpectedly, with why the VM's account of it; otherwise
// why is the reason.
func (s *Server) holdForUser(ctx context.Context, items []resumeItem, unexpected bool, why string) {
	said := why
	if unexpected {
		said = whyUnexpected
		if why != "" {
			said += " (" + why + ")"
		}
	}
	for _, it := range items {
		s.logf("continue after restart: %s waits for you: %s", it.ref, said)
		if !it.lead {
			project, name := splitRef(it.ref)
			if err := s.store.SetHold(ctx, project, name, state.HoldRestart); err != nil {
				s.logf("continue after restart: putting %s on hold: %v", it.ref, err)
			}
			s.mu.Lock()
			if s.outcomes[it.ref].kind == api.AgentOutcomeResuming {
				delete(s.outcomes, it.ref)
			}
			s.mu.Unlock()
		}
		if it.turn == nil {
			continue
		}
		rt := *it.turn
		if err := s.store.HoldRunningTurn(ctx, rt.Project, rt.Agent, rt.Turn, true); err != nil {
			s.logf("continue after restart: holding %s's turn: %v", rt.Ref(), err)
		}
		if a, err := s.turnAgent(ctx, rt); err == nil {
			s.chat.NoteHeld(a, rt, said)
		}
	}
	rec := restartHoldRecord{Unexpected: unexpected, Why: why, Since: time.Now()}
	if !unexpected {
		rec.Why = ""
	}
	if v, err := s.store.Setting(ctx, state.SettingRestartHold); err == nil && v != "" {
		var was restartHoldRecord
		if json.Unmarshal([]byte(v), &was) == nil {
			rec.Unexpected = rec.Unexpected || was.Unexpected
			rec.Why = cmp.Or(rec.Why, was.Why)
			if !was.Since.IsZero() {
				rec.Since = was.Since
			}
		}
	}
	b, _ := json.Marshal(rec)
	if err := s.store.SetSetting(ctx, state.SettingRestartHold, string(b)); err != nil {
		s.logf("continue after restart: %v", err)
	}
	s.refreshAgents(ctx)
	s.publishRestartHold(ctx)
}

// heldForUser is what waits for the user's Resume all, as items to resume.
func (s *Server) heldForUser(ctx context.Context) ([]resumeItem, error) {
	turns, err := s.store.RunningTurns(ctx)
	if err != nil {
		return nil, err
	}
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		return nil, err
	}
	byRef := map[string]state.Agent{}
	for _, a := range agents {
		byRef[a.Ref()] = a
	}
	var items []resumeItem
	seen := map[string]bool{}
	for _, rt := range turns {
		if !rt.Held {
			continue
		}
		it := resumeItem{ref: rt.Ref(), lead: rt.Kind != state.TurnOfAgent, made: rt.StartedAt, turn: &rt, byUser: true}
		if !it.lead {
			a, ok := byRef[rt.Ref()]
			if !ok {
				_ = s.store.EndRunningTurn(ctx, rt.Project, rt.Agent, rt.Turn)
				continue
			}
			it.made, it.agent = a.CreatedAt, a
		}
		seen[it.ref] = true
		items = append(items, it)
	}
	for _, a := range agents {
		if a.Hold == state.HoldRestart && !a.IsLead() && !seen[a.Ref()] {
			items = append(items, resumeItem{ref: a.Ref(), made: a.CreatedAt, agent: a, byUser: true})
		}
	}
	sortResumes(items)
	return items, nil
}

// restartHold is what waits for the user, as the API has it.
func (s *Server) restartHold(ctx context.Context) (api.RestartHold, error) {
	items, err := s.heldForUser(ctx)
	if err != nil {
		return api.RestartHold{}, err
	}
	out := api.RestartHold{Agents: []string{}, Chats: []string{}}
	for _, it := range items {
		if it.lead {
			out.Chats = append(out.Chats, it.ref)
		} else {
			out.Agents = append(out.Agents, it.ref)
		}
	}
	if len(items) == 0 {
		return out, nil
	}
	if v, err := s.store.Setting(ctx, state.SettingRestartHold); err == nil && v != "" {
		var rec restartHoldRecord
		if json.Unmarshal([]byte(v), &rec) == nil {
			out.Unexpected, out.Why, out.Since = rec.Unexpected, rec.Why, rec.Since
		}
	}
	return out, nil
}

// publishRestartHold tells the apps what waits for the user now.
func (s *Server) publishRestartHold(ctx context.Context) {
	if hold, err := s.restartHold(ctx); err == nil {
		s.events.publish(api.EventRestartHold, hold)
	}
}

// resumeAll is the user's Resume all: everything held for them goes in
// line, uncounted, and is carried on one at a time.
func (s *Server) resumeAll(ctx context.Context) (api.ResumeAllResult, error) {
	items, err := s.heldForUser(ctx)
	if err != nil {
		return api.ResumeAllResult{}, err
	}
	var out api.ResumeAllResult
	for i, it := range items {
		project, name := splitRef(it.ref)
		if err := s.store.ForgetAutoResumes(ctx, project, name); err != nil {
			s.logf("resume all: %s: %v", it.ref, err)
		}
		if it.turn != nil {
			if err := s.store.HoldRunningTurn(ctx, it.turn.Project, it.turn.Agent, it.turn.Turn, false); err != nil {
				return out, err
			}
		}
		if it.lead {
			out.Chats++
			continue
		}
		out.Agents++
		// Waiting its turn in line, as one On hold for memory does: a daemon
		// that stops meanwhile boots it as it starts.
		if err := s.store.SetHold(ctx, project, name, state.HoldMemory); err != nil {
			return out, err
		}
		items[i].agent.Hold = state.HoldMemory
	}
	if err := s.store.DeleteSetting(ctx, state.SettingRestartHold); err != nil {
		s.logf("resume all: %v", err)
	}
	s.logf("resume all: %d chat(s) and %d agent(s), one at a time", out.Chats, out.Agents)
	s.refreshAgents(ctx)
	s.publishRestartHold(ctx)
	if len(items) > 0 {
		run := s.runCtx // the daemon's: the line stops with it
		if run == nil {
			run = context.WithoutCancel(ctx)
		}
		go s.resumeInTurn(run, items)
	}
	return out, nil
}

func (s *Server) getRestartHold(w http.ResponseWriter, r *http.Request) error {
	hold, err := s.restartHold(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, hold)
}

func (s *Server) postResumeAll(w http.ResponseWriter, r *http.Request) error {
	out, err := s.resumeAll(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
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
