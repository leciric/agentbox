package daemon

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Admission across projects (agent.Admit). Every create, whoever made it, is
// checked against the VM's memory before its machine starts: one that fits
// starts now, as it always did, and one that doesn't joins its project's
// queue, whatever the create or the project's AlwaysQueue asked, and starts
// from there when it fits. The queue loop (queue.go) starts queued agents
// the same way, in the order they joined across every project, and this is
// what it works from.
//
// Admission doesn't depend on the agent queue's switch: off, nothing queues
// for slots, but an agent the VM has no memory for still waits rather than
// overcommit it.

// pendingCreate is a create admitted straight to its machine whose job hasn't
// ended: it holds its reservation until then. Its agent's row, once the job
// has made it, is "initializing", and isn't counted again (holders).
type pendingCreate struct {
	project  string
	reserved int64
	// wake is a stopped agent's machine starting (wake.go), not a create:
	// it has no "creating" row to stand in for.
	wake bool
}

// admission is what admitting works from: the memory agents share, what
// holds it, and who waits, in order, with what each would be told.
type admission struct {
	// total is the memory agents share; capacity what admission fills with
	// baselines, total less the burst pool's floor, except for whatever of
	// the floor nobody actually leases (agent.Capacity).
	total    int64
	capacity int64
	holders  []agent.Holder
	// instances are the holders' machines, by ref, for memory.high.
	instances map[string]string
	waiting   []agent.Waiter
	verdicts  map[string]agent.Verdict
	// slots is each project's number of slots, for a wait on them.
	slots map[string]int
	// queued are the queued agents, by ref.
	queued map[string]state.QueuedAgent
	// waking are the stopped agents waiting for memory to start their
	// machines, with a message held for them (wake.go), by ref.
	waking map[string]bool
}

// message is why ref waits, in one line, or "" when it starts.
func (a admission) message(ref string) string {
	v, ok := a.verdicts[ref]
	if !ok || v.Start {
		return ""
	}
	var need int64
	var project string
	for _, w := range a.waiting {
		if w.Ref == ref {
			need, project = w.Need, w.Project
		}
	}
	msg := agent.WaitMessage(v, a.capacity, need, a.holders, a.slots[project])
	if a.waking[ref] {
		return wakeMessage(msg)
	}
	return msg
}

// shapes gives each project's learned baseline and burst, looking each one
// up once.
type shapes struct {
	s      *Server
	ctx    context.Context
	shapes map[string]agent.Shape
}

func newShapes(s *Server, ctx context.Context) *shapes {
	return &shapes{s: s, ctx: ctx, shapes: map[string]agent.Shape{}}
}

func (r *shapes) of(project string) (agent.Shape, error) {
	shape, ok := r.shapes[project]
	if !ok {
		var err error
		if shape, err = r.s.projectShape(r.ctx, project); err != nil {
			return agent.Shape{}, err
		}
		r.shapes[project] = shape
	}
	return shape, nil
}

// planAdmission works out what holds memory and who would start now. extra is
// a create not queued yet, which waits behind everyone already queued; nil
// for none. It decides nothing: the caller starts whom it says.
func (s *Server) planAdmission(ctx context.Context, extra *agent.Waiter) (admission, error) {
	status, err := s.slotStatus(ctx)
	if err != nil {
		return admission{}, err
	}
	statuses, err := s.manager(nil).List(ctx, "")
	if err != nil {
		return admission{}, err
	}
	queue, err := s.store.Queue(ctx, "")
	if err != nil {
		return admission{}, err
	}
	res := newShapes(s, ctx)
	total := max(status.Budget-status.Reserve, 0)
	out := admission{
		total:     total,
		instances: map[string]string{},
		slots:     map[string]int{},
		queued:    map[string]state.QueuedAgent{},
		waking:    map[string]bool{},
	}
	s.mu.Lock()
	pending := make([]pendingCreate, 0, len(s.pendingCreates))
	pendingIn := map[string]bool{}
	for _, p := range s.pendingCreates {
		pending = append(pending, p)
		if !p.wake {
			pendingIn[p.project] = true
		}
	}
	s.mu.Unlock()
	for _, st := range statuses {
		if st.IsLead() {
			continue
		}
		starting := s.queueStarting(st.Ref())
		switch {
		case st.Status == state.AgentQueued && !starting:
			continue
		case st.Status == state.AgentCreating && !starting && pendingIn[st.Project]:
			continue // its create holds its reservation (pending)
		case !starting && !holdsSlot(st.State):
			continue
		}
		shape, err := res.of(st.Project)
		if err != nil {
			return admission{}, err
		}
		h := agent.Holder{Ref: st.Ref(), Project: st.Project, Reserved: shape.Baseline, Lease: s.burst.leaseOf(st.Ref())}
		if u, ok := s.lastUsage(st.Ref()); ok {
			h.Using = u.Memory
		}
		out.holders = append(out.holders, h)
		out.instances[h.Ref] = st.Instance
	}
	for i, p := range pending {
		out.holders = append(out.holders, agent.Holder{Ref: fmt.Sprintf("(creating %d)", i), Project: p.project, Reserved: p.reserved})
	}
	out.capacity = agent.Capacity(total, out.holders)

	var free map[string]int
	if status.Enabled {
		free = map[string]int{}
	}
	for _, p := range status.Projects {
		out.slots[p.Project] = p.Slots
		if free != nil {
			free[p.Project] = p.Slots - p.Running
		}
	}
	for _, q := range joinOrder(queue) {
		if s.queueStarting(q.Ref()) {
			continue
		}
		shape, err := res.of(q.Project)
		if err != nil {
			return admission{}, err
		}
		out.queued[q.Ref()] = q
		out.waiting = append(out.waiting, agent.Waiter{Ref: q.Ref(), Project: q.Project, Need: shape.Baseline, Since: q.QueuedAt})
	}
	// Behind the queue: a stopped agent waits the way a queued one does,
	// from when it was first told something.
	waking, err := s.wakingWaiters(res)
	if err != nil {
		return admission{}, err
	}
	for _, w := range waking {
		out.waking[w.Ref] = true
	}
	out.waiting = append(out.waiting, waking...)
	if extra != nil {
		out.waiting = append(out.waiting, *extra)
	}
	out.verdicts = agent.Admit(out.capacity, out.holders, out.waiting, free, time.Now(), s.memAvailable())
	return out, nil
}

// joinOrder is the queue across every project in the order its agents joined
// it, keeping each project's own order: of the agents at the front of each
// project's line, the one that joined first goes next.
func joinOrder(queue []state.QueuedAgent) []state.QueuedAgent {
	lines := map[string][]state.QueuedAgent{}
	var projects []string
	for _, q := range queue {
		if _, ok := lines[q.Project]; !ok {
			projects = append(projects, q.Project)
		}
		lines[q.Project] = append(lines[q.Project], q)
	}
	sort.Strings(projects)
	for _, p := range projects {
		line := lines[p]
		sort.SliceStable(line, func(i, j int) bool { return line[i].Position < line[j].Position })
	}
	out := make([]state.QueuedAgent, 0, len(queue))
	for len(out) < len(queue) {
		var next string
		for _, p := range projects {
			if len(lines[p]) == 0 {
				continue
			}
			if next == "" || lines[p][0].QueuedAt.Before(lines[next][0].QueuedAt) {
				next = p
			}
		}
		out = append(out, lines[next][0])
		lines[next] = lines[next][1:]
	}
	return out
}

// admitCreate decides whether a create that didn't ask to queue may start
// its machine now. When it may, its reservation is held until done is
// called, which the create's job does when it ends; when it may not, it
// says why, and the create joins the queue instead.
func (s *Server) admitCreate(ctx context.Context, req api.CreateAgentRequest) (done func(), wait string, err error) {
	shape, err := newShapes(s, ctx).of(req.Project)
	if err != nil {
		return nil, "", err
	}
	reserved := shape.Baseline
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	const ref = "(new)"
	plan, err := s.planAdmission(ctx, &agent.Waiter{Ref: ref, Project: req.Project, Need: reserved, Since: time.Now(), AnySlot: true})
	if err != nil {
		return nil, "", err
	}
	if !plan.verdicts[ref].Start {
		return nil, plan.message(ref), nil
	}
	s.mu.Lock()
	s.pendingSeq++
	id := s.pendingSeq
	s.pendingCreates[id] = pendingCreate{project: req.Project, reserved: reserved}
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.pendingCreates, id)
		s.mu.Unlock()
		s.kickQueue()
	}, "", nil
}

// setWaiting keeps what each queued agent was last told about why it waits,
// for Agent.Waiting, drops what no longer waits, and says whether any of it
// changed.
func (s *Server) setWaiting(plan admission) bool {
	waiting := map[string]string{}
	for ref := range plan.queued {
		if msg := plan.message(ref); msg != "" {
			waiting[ref] = msg
		}
	}
	for ref := range plan.waking {
		if msg := plan.message(ref); msg != "" {
			waiting[ref] = msg
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := !maps.Equal(s.waitReasons, waiting)
	s.waitReasons = waiting
	return changed
}

// waitingFor is why a queued agent waits, as last worked out.
func (s *Server) waitingFor(ref string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waitReasons[ref]
}

// tightenMemory sets each running agent's memory.high from what the VM has
// left (agent.MemoryHighs): none while there's room, and, as it runs short,
// down toward their reservation for the agents furthest over theirs.
func (s *Server) tightenMemory(ctx context.Context) {
	s.queueMu.Lock()
	plan, err := s.planAdmission(ctx, nil)
	s.queueMu.Unlock()
	if err != nil {
		s.logf("memory.high: %v", err)
		return
	}
	for ref, high := range agent.MemoryHighs(plan.total, plan.holders) {
		instance, ok := plan.instances[ref]
		if !ok {
			continue // a create still making its machine
		}
		if err := s.setMemoryHigh(instance, high); err != nil {
			s.logf("memory.high: %s: %v", ref, err)
		}
	}
}
