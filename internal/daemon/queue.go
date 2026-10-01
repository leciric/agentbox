package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// The agent queue. A create asked to queue (CreateAgentRequest.Queue, or the
// project's AlwaysQueue) gets its agent's record, name, title, branch and task
// at once, but no machine: the agent waits, "Queued #N" in the sidebar, until
// one of its project's slots is free, and the daemon starts it then, the same
// way a create would have made it. Slots are agent.SplitSlots: auto, from the
// memory budget and each project's learned peak, or the user's own number.
//
// Only queued agents wait. A create that doesn't ask to queue makes its agent
// at once, exactly as before, and that agent then takes a slot like any other,
// so a project that never queues anything sees nothing change. Nothing
// running is ever stopped or paused to make room.
//
// The queue is looked at when anything might have freed a slot — an agent
// stopped, was destroyed or finished, a project's slots changed — and on a
// timer besides, which is also when each running agent's memory is sampled
// for its project's peak.

// queueInterval is how often the queue is looked at, and running agents'
// memory sampled, when nothing else has asked.
const queueInterval = 30 * time.Second

// maxPinnedSlots is the most slots a project can be pinned to.
const maxPinnedSlots = 64

// queuedRequest is what a queued agent is started from: the create request as
// it was made, and whether the project's chat made it, which the chat is still
// waiting to hear about.
type queuedRequest struct {
	Request api.CreateAgentRequest `json:"request"`
	ByLead  bool                   `json:"byLead,omitempty"`
}

// kickQueue asks the queue loop to look at the queue now, without waiting for
// it: asks made while it's busy fold into one.
func (s *Server) kickQueue() {
	select {
	case s.queueKick <- struct{}{}:
	default:
	}
}

// runQueue is the queue's loop: it starts queued agents into free slots when
// kicked and on a timer, and on the timer samples what running agents use and
// rechecks the leads that are due (leadrecheck.go). With both "agent queue"
// and "lead rechecks agents" off, the timer does nothing at all.
func (s *Server) runQueue(ctx context.Context) {
	if s.queueEvery <= 0 {
		return // a test drives admitQueued itself
	}
	tick := func() {
		queueOn, recheckOn := s.queueFeatures(ctx)
		if queueOn || recheckOn {
			s.sampleUsage(ctx)
		}
		s.admitQueued(ctx)
		if recheckOn {
			s.recheckLeads(ctx, time.Now())
		}
	}
	tick()
	ticker := time.NewTicker(s.queueEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		case <-s.queueKick:
			s.admitQueued(ctx)
		}
	}
}

// queueFeatures reads the two switches: "agent queue" and "lead rechecks
// agents".
func (s *Server) queueFeatures(ctx context.Context) (queue, recheck bool) {
	queue, _ = s.store.Flag(ctx, state.SettingAgentQueue)
	recheck, _, _ = s.store.LeadRecheck(ctx)
	return queue, recheck
}

// usageSample is how long each sample of the agents' CPU is taken over.
const usageSample = time.Second

// sampleUsage measures what every agent uses now, keeps it for the slot page
// and the recheck, and records each running agent's peaks.
func (s *Server) sampleUsage(ctx context.Context) {
	agents, err := s.manager(nil).SampleUsage(ctx, time.Now(), usageSample)
	if err != nil {
		s.logf("agent queue: sampling usage: %v", err)
	}
	now := make(map[string]agent.AgentUsage, len(agents))
	for _, a := range agents {
		now[a.Ref()] = a
	}
	s.mu.Lock()
	s.usageNow = now
	s.mu.Unlock()
}

// lastUsage is what an agent was using when last sampled.
func (s *Server) lastUsage(ref string) (agent.AgentUsage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.usageNow[ref]
	return u, ok
}

// holdsSlot reports whether an agent in this state holds one of its project's
// slots: it has a machine that holds memory, or is getting one.
func holdsSlot(state string) bool {
	switch state {
	case "running", "paused", "initializing":
		return true
	}
	return false
}

// slotStatus works out every project's slots as things stand now.
func (s *Server) slotStatus(ctx context.Context) (api.QueueStatus, error) {
	m := s.manager(nil)
	budget, err := s.slotBudget(ctx)
	if err != nil {
		return api.QueueStatus{}, err
	}
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return api.QueueStatus{}, err
	}
	statuses, err := m.List(ctx, "")
	if err != nil {
		return api.QueueStatus{}, err
	}
	queue, err := s.store.Queue(ctx, "")
	if err != nil {
		return api.QueueStatus{}, err
	}
	running := map[string]int{}
	for _, st := range statuses {
		if !st.IsLead() && (holdsSlot(st.State) || s.queueStarting(st.Ref())) {
			running[st.Project]++
		}
	}
	queued := map[string]int{}
	for _, q := range queue {
		if !s.queueStarting(q.Ref()) {
			queued[q.Project]++
		}
	}
	enabled, err := s.store.Flag(ctx, state.SettingAgentQueue)
	if err != nil {
		return api.QueueStatus{}, err
	}
	out := api.QueueStatus{Enabled: enabled, Budget: budget, Reserve: agent.SlotReserve(budget), Projects: []api.ProjectSlots{}, Queued: []api.QueuedAgent{}}
	var shares []agent.SlotProject
	for _, p := range projects {
		peak, learned, err := s.projectPeak(ctx, p.Name)
		if err != nil {
			return api.QueueStatus{}, err
		}
		shares = append(shares, agent.SlotProject{Name: p.Name, Peak: peak, Pinned: p.Slots, Running: running[p.Name], Queued: queued[p.Name]})
		out.Projects = append(out.Projects, api.ProjectSlots{
			Project: p.Name, Pinned: p.Slots, Peak: peak, PeakLearned: learned, Running: running[p.Name], Queued: queued[p.Name],
			Agents: s.slotAgents(ctx, p.Name, statuses),
		})
	}
	slots := agent.SplitSlots(max(budget-out.Reserve, 0), shares)
	for i := range out.Projects {
		out.Projects[i].Slots = slots[out.Projects[i].Project]
	}
	byRef := make(map[string]agent.Status, len(statuses))
	for _, st := range statuses {
		byRef[st.Ref()] = st
	}
	for _, q := range queue {
		a := byRef[q.Ref()]
		var req queuedRequest
		_ = json.Unmarshal(q.Request, &req)
		out.Queued = append(out.Queued, api.QueuedAgent{
			Ref: q.Ref(), Project: q.Project, Name: q.Name, Title: a.Title, Branch: a.Branch,
			Task: req.Request.Task, TaskID: req.Request.TaskID, Position: q.Position, QueuedAt: q.QueuedAt,
		})
	}
	return out, nil
}

// slotAgents are a project's agents that hold a slot, with what they use.
func (s *Server) slotAgents(ctx context.Context, project string, statuses []agent.Status) []api.SlotAgent {
	peaks, err := s.store.UsagePeaks(ctx, project)
	if err != nil {
		peaks = nil
	}
	out := []api.SlotAgent{}
	for _, st := range statuses {
		if st.Project != project || st.IsLead() || !holdsSlot(st.State) {
			continue
		}
		row := api.SlotAgent{Name: st.Name, Title: st.Title, State: st.State,
			MemoryPeak: peaks[st.Name].Memory, CPUPeak: peaks[st.Name].CPU}
		if u, ok := s.lastUsage(st.Ref()); ok {
			row.Memory, row.CPU = u.Memory, u.CPU
		}
		out = append(out, row)
	}
	return out
}

// admitQueued starts as many queued agents as there are free slots, each
// project's in its queue's order.
func (s *Server) admitQueued(ctx context.Context) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	queue, err := s.store.Queue(ctx, "")
	if err != nil {
		s.logf("agent queue: %v", err)
		return
	}
	if len(queue) == 0 {
		return
	}
	status, err := s.slotStatus(ctx)
	if err != nil {
		s.logf("agent queue: %v", err)
		return
	}
	free := map[string]int{}
	for _, p := range status.Projects {
		free[p.Project] = p.Slots - p.Running
		if !status.Enabled {
			// The queue is off: nothing waits, whatever the slots say.
			free[p.Project] = len(queue)
		}
	}
	started := 0
	for _, q := range queue {
		if s.queueStarting(q.Ref()) || free[q.Project] <= 0 {
			continue
		}
		if err := s.queueStart(ctx, q); err != nil {
			s.logf("agent queue: starting %s: %v", q.Ref(), err)
			continue
		}
		free[q.Project]--
		started++
	}
	if started > 0 {
		s.refreshAgents(ctx)
	}
}

// queueStarting reports whether a queued agent was handed to a create job
// that hasn't finished: it holds a slot, and mustn't be started twice.
func (s *Server) queueStarting(ref string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startingQueued[ref]
}

func (s *Server) setQueueStarting(ref string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		s.startingQueued[ref] = true
	} else {
		delete(s.startingQueued, ref)
	}
}

// startQueuedAgent starts the job that makes a queued agent, and tells the
// project's chat, in one line, that it's on its way.
func (s *Server) startQueuedAgent(ctx context.Context, q state.QueuedAgent) error {
	var req queuedRequest
	if err := json.Unmarshal(q.Request, &req); err != nil {
		return fmt.Errorf("reading its request: %w", err)
	}
	a, err := s.store.Agent(ctx, q.Project, q.Name)
	if err != nil {
		return err
	}
	ref := q.Ref()
	s.setQueueStarting(ref, true)
	create := s.createJob(req.Request, req.ByLead, q.Name)
	if _, err := s.jobs.start("create", q.Project, func(ctx context.Context, log io.Writer) (any, error) {
		defer func() {
			s.setQueueStarting(ref, false)
			s.kickQueue()
		}()
		s.logf("agent queue: starting %s", ref)
		out, err := create(ctx, log)
		if err != nil {
			// Failing before it left the queue — its tool isn't logged in,
			// the base image went — would fail again every time it was
			// tried, so it leaves the queue the way a failed create leaves
			// nothing behind; failing after, the create has removed it.
			if cur, getErr := s.store.Agent(ctx, q.Project, q.Name); getErr == nil && cur.Status == state.AgentQueued {
				_ = s.store.RemoveAgent(context.WithoutCancel(ctx), q.Project, q.Name)
			}
			s.releaseQueuedTasks(context.WithoutCancel(ctx), a)
		}
		return out, err
	}); err != nil {
		s.setQueueStarting(ref, false)
		return err
	}
	s.tellLead(ctx, q.Project, fmt.Sprintf("Queued agent %s%s is starting: a slot came free.", q.Name, withTitle(a.Title)), false)
	return nil
}

func withTitle(title string) string {
	if title == "" {
		return ""
	}
	return fmt.Sprintf(" (%q)", title)
}

// enqueueAgent queues the agent req asks for, and answers with a job that has
// already done so, so every caller waits for a queued agent the way it waits
// for any other: the job's result is the agent, in state "queued".
func (s *Server) enqueueAgent(w http.ResponseWriter, ctx context.Context, req api.CreateAgentRequest, byLead bool) error {
	request, err := json.Marshal(queuedRequest{Request: req, ByLead: byLead})
	if err != nil {
		return err
	}
	a, err := s.manager(nil).Enqueue(ctx, req.Project, agent.CreateOptions{
		Name:          req.Name,
		Branch:        req.Branch,
		Title:         req.Title,
		AI:            req.AI,
		Interface:     req.Interface,
		Autonomous:    req.Autonomous == nil || *req.Autonomous,
		Model:         req.Model,
		Effort:        req.Effort,
		From:          req.From,
		ClaudeAccount: req.ClaudeAccount,
		GitHubAccount: req.GitHubAccount,
		FinishNotice:  req.FinishNotice,
		Task:          strings.TrimSpace(req.Task),
	}, request)
	if err != nil {
		return err
	}
	if req.TaskID != "" {
		s.assignTask(ctx, a.Project, req.TaskID, a.Name)
	}
	s.captureEvent(ctx, a.Project, a.Name, "agent_queued", map[string]any{"title": a.Title, "task": strings.TrimSpace(req.Task), "branch": a.Branch}, "")
	s.refreshAgents(ctx)
	info, err := s.describe(ctx, a)
	if err != nil {
		return err
	}
	j, err := s.jobs.start("queue", req.Project, func(context.Context, io.Writer) (any, error) {
		return info, nil
	})
	if err != nil {
		return err
	}
	// It has nothing left to do: answer with it done, and the queued agent in
	// its result, so a caller can say where in line it is without asking.
	done, err := j.follow(ctx, 0, func(string) error { return nil })
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, done)
}

// taskForAgent fills in a create request made for a task of the plan: the
// task must be open and nobody's, and its words are the agent's task and
// title unless the request has its own.
func (s *Server) taskForAgent(ctx context.Context, req *api.CreateAgentRequest) error {
	t, err := s.memory().Task(ctx, req.Project, req.TaskID)
	if err != nil {
		return err
	}
	if !t.Open() {
		return fmt.Errorf("task %s is %s: only an open task can be given to an agent", t.ID, t.Status)
	}
	if t.Agent != "" {
		return fmt.Errorf("task %s is already %s's", t.ID, t.Agent)
	}
	if strings.TrimSpace(req.Task) == "" {
		req.Task = t.Goal
		if t.Detail != "" && t.Detail != t.Goal {
			req.Task += "\n\n" + t.Detail
		}
	}
	if strings.TrimSpace(req.Title) == "" {
		req.Title = titleFromTask(t.Goal)
	}
	return nil
}

// titleFromTask makes an agent's title from a task's words: its first line,
// cut to fit agent.CleanTitle's 80 characters at a word.
func titleFromTask(goal string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(goal), "\n")
	line = strings.Join(strings.Fields(line), " ")
	if len([]rune(line)) <= 80 {
		return line
	}
	r := []rune(line)[:79]
	if i := strings.LastIndex(string(r), " "); i > 40 {
		return string(r)[:i] + "…"
	}
	return string(r) + "…"
}

// assignTask gives a task of the plan to an agent. A failure only loses the
// link, which the task tab shows, so it's logged rather than failing a create.
func (s *Server) assignTask(ctx context.Context, project, id, agentName string) {
	if _, err := s.memory().UpdateTask(ctx, project, id, memory.TaskPatch{Agent: &agentName}); err != nil {
		s.logf("memory: giving task %s to %s/%s: %v", id, project, agentName, err)
	}
}

// releaseQueuedTasks hands back the open tasks a queued agent that's leaving
// the queue unstarted was given, so they can be queued again.
func (s *Server) releaseQueuedTasks(ctx context.Context, a state.Agent) {
	open, err := s.memory().Tasks(ctx, a.Project, memory.TaskFilter{Agent: a.Name, OpenOnly: true})
	if err != nil {
		return
	}
	nobody := ""
	for _, t := range open {
		if _, err := s.memory().UpdateTask(ctx, a.Project, t.ID, memory.TaskPatch{Agent: &nobody}); err != nil {
			s.logf("memory: handing back task %s: %v", t.ID, err)
		}
	}
}

// deleteTask takes one of the user's tasks off their list. A task that's
// queued takes its queued agent out of the queue with it: the user deleted the
// work, so nothing should start for it. An agent already running on it keeps
// running; only the row goes.
func (s *Server) deleteTask(ctx context.Context, project, id string) error {
	t, err := s.memory().Task(ctx, project, id)
	if err != nil {
		return err
	}
	if t.Agent != "" {
		if a, err := s.store.Agent(ctx, project, t.Agent); err == nil && a.Status == state.AgentQueued {
			s.queueMu.Lock()
			defer s.queueMu.Unlock()
			if s.queueStarting(a.Ref()) {
				return fmt.Errorf("%s is starting already", a.Ref())
			}
			if err := s.store.RemoveAgent(ctx, a.Project, a.Name); err != nil {
				return err
			}
			s.captureEvent(ctx, a.Project, a.Name, "agent_retired", map[string]any{"how": "unqueued", "branch": a.Branch}, "")
			defer s.refreshAgents(ctx)
		}
	}
	return s.memory().DeleteTask(ctx, project, id)
}

// HTTP

func (s *Server) getQueue(w http.ResponseWriter, r *http.Request) error {
	status, err := s.slotStatus(r.Context())
	if err != nil {
		return err
	}
	if project := r.URL.Query().Get("project"); project != "" {
		if _, err := s.store.Project(r.Context(), project); err != nil {
			return err
		}
		var queued []api.QueuedAgent
		for _, q := range status.Queued {
			if q.Project == project {
				queued = append(queued, q)
			}
		}
		status.Queued = append([]api.QueuedAgent{}, queued...)
	}
	return writeJSON(w, http.StatusOK, status)
}

// queuedFromPath is the queued agent a queue route names.
func (s *Server) queuedFromPath(r *http.Request) (state.Agent, error) {
	a, err := s.agentFromPath(r)
	if err != nil {
		return state.Agent{}, err
	}
	if a.Status != state.AgentQueued || s.queueStarting(a.Ref()) {
		return state.Agent{}, fmt.Errorf("%s isn't queued", a.Ref())
	}
	return a, nil
}

func (s *Server) moveQueued(w http.ResponseWriter, r *http.Request) error {
	a, err := s.queuedFromPath(r)
	if err != nil {
		return err
	}
	var req api.MoveQueuedRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := s.store.MoveQueued(r.Context(), a.Project, a.Name, req.Position); err != nil {
		return err
	}
	s.refreshAgents(r.Context())
	return s.getQueue(w, r)
}

func (s *Server) removeQueued(w http.ResponseWriter, r *http.Request) error {
	a, err := s.queuedFromPath(r)
	if err != nil {
		return err
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	if s.queueStarting(a.Ref()) {
		return fmt.Errorf("%s is starting already", a.Ref())
	}
	if err := s.store.RemoveAgent(r.Context(), a.Project, a.Name); err != nil {
		return err
	}
	s.releaseQueuedTasks(r.Context(), a)
	s.captureEvent(r.Context(), a.Project, a.Name, "agent_retired", map[string]any{"how": "unqueued", "branch": a.Branch}, "")
	s.refreshAgents(r.Context())
	w.WriteHeader(http.StatusNoContent)
	return nil
}
