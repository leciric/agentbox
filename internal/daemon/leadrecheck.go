package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/gitrepo"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// "Lead rechecks agents": while on, every SettingLeadRecheckMinutes the daemon
// looks at each project, and when there is something its chat could act on —
// agents are queued, or an agent has sat idle for a whole recheck — it wakes
// the chat with a short status: each running agent's idle time, last report,
// whether it finished, its pull request, what it uses now and at its peak, and
// the queue's length and free slots. The chat may then retire a finished
// agent that was never stopped, which frees its slot for the next queued one;
// its brief says so, and that it must never retire one with uncommitted or
// unpushed work.
//
// The status is re-sent on every step of the turn it starts, so it is kept to
// a line an agent. A project with nothing to act on costs nothing: no turn is
// started. Nor is one started for the same state twice — an idle agent the
// chat chose to keep isn't brought up again every twenty minutes, only when
// something about it or the queue changes.

// recheckAgent is one running agent, as the recheck reports it.
type recheckAgent struct {
	Name, Title string
	Busy        bool          // a turn in progress, a job, a question waiting
	IdleFor     time.Duration // since it last did anything; 0 while busy
	Finished    bool          // its last event is a finish
	Report      string        // its last report: status and the start of its summary
	PR          string        // its pull request, like "#12 checks passing"
	Dirty       bool          // uncommitted work
	Unpushed    bool          // commits that are neither merged nor on a remote
	Memory      int64
	MemoryPeak  int64
	CPU         float64
	CPUPeak     float64
}

// recheckInput is one project, as the recheck sees it.
type recheckInput struct {
	Queued, Slots, Free int
	Agents              []recheckAgent
}

// recheckNote decides whether a project's chat is worth waking, and with
// what. idleAfter is how long an agent must have been idle to count. key sums
// up what there is to act on — not how long for, which only grows — so the
// same state isn't sent twice.
func recheckNote(in recheckInput, idleAfter time.Duration) (note, key string, wake bool) {
	var idle []recheckAgent
	for _, a := range in.Agents {
		if !a.Busy && a.IdleFor >= idleAfter {
			idle = append(idle, a)
		}
	}
	if in.Queued == 0 && len(idle) == 0 {
		return "", "", false
	}
	var k strings.Builder
	fmt.Fprintf(&k, "q%d f%d", in.Queued, in.Free)
	for _, a := range idle {
		fmt.Fprintf(&k, " %s:%t:%t:%t:%s", a.Name, a.Finished, a.Dirty, a.Unpushed, a.PR)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[recheck] Queue: %d waiting, %d of %d slots free.", in.Queued, in.Free, in.Slots)
	for _, a := range in.Agents {
		fmt.Fprintf(&b, "\n- %s", a.Name)
		var parts []string
		switch {
		case a.Busy:
			parts = append(parts, "working")
		case a.Finished:
			parts = append(parts, "finished, idle "+shortDuration(a.IdleFor))
		default:
			parts = append(parts, "idle "+shortDuration(a.IdleFor))
		}
		if a.Report != "" {
			parts = append(parts, "report: "+a.Report)
		}
		if a.PR != "" {
			parts = append(parts, "PR "+a.PR)
		}
		switch {
		case a.Dirty:
			parts = append(parts, "UNCOMMITTED work")
		case a.Unpushed:
			parts = append(parts, "UNPUSHED commits")
		}
		parts = append(parts, fmt.Sprintf("mem %s/%s peak, cpu %.0f%%/%.0f%%", gibs(a.Memory), gibs(a.MemoryPeak), a.CPU, a.CPUPeak))
		b.WriteString(" " + strings.Join(parts, "; "))
	}
	b.WriteString("\nRetire (stop) a finished agent still holding its machine, if nothing more is needed from it; never one with uncommitted or unpushed work. Nothing to do: say nothing.")
	return b.String(), k.String(), true
}

// shortDuration is a duration like "45m" or "3h10m".
func shortDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return idleDurationWords(d)
}

// gibs is a size in GiB with one decimal.
func gibs(n int64) string { return fmt.Sprintf("%.1fG", float64(n)/(1<<30)) }

// recheckLeads rechecks every project whose recheck is due, as of now.
func (s *Server) recheckLeads(ctx context.Context, now time.Time) {
	on, every, err := s.store.LeadRecheck(ctx)
	if err != nil || !on {
		return
	}
	projects, err := s.store.Projects(ctx)
	if err != nil {
		s.logf("lead recheck: %v", err)
		return
	}
	var slots *api.QueueStatus
	for _, p := range projects {
		s.mu.Lock()
		due := now.Sub(s.recheckedAt[p.Name]) >= every
		if due {
			s.recheckedAt[p.Name] = now
		}
		s.mu.Unlock()
		if !due {
			continue
		}
		if _, err := s.manager(nil).Lead(ctx, p.Name); err != nil {
			continue // no chat to wake: a project never chatted with costs nothing
		}
		if slots == nil {
			st, err := s.slotStatus(ctx)
			if err != nil {
				s.logf("lead recheck: %v", err)
				return
			}
			slots = &st
		}
		in, err := s.recheckInput(ctx, p, *slots, now)
		if err != nil {
			s.logf("lead recheck of %s: %v", p.Name, err)
			continue
		}
		note, key, wake := recheckNote(in, every)
		s.mu.Lock()
		same := key == s.recheckedWhat[p.Name]
		s.recheckedWhat[p.Name] = key
		s.mu.Unlock()
		if !wake || same {
			continue
		}
		s.recheckTell(ctx, p.Name, note)
	}
}

// recheckInput gathers what the recheck reports about one project.
func (s *Server) recheckInput(ctx context.Context, p state.Project, slots api.QueueStatus, now time.Time) (recheckInput, error) {
	var in recheckInput
	for _, ps := range slots.Projects {
		if ps.Project == p.Name {
			in.Queued, in.Slots, in.Free = ps.Queued, ps.Slots, max(ps.Slots-ps.Running, 0)
		}
	}
	m := s.manager(nil)
	statuses, err := m.List(ctx, p.Name)
	if err != nil {
		return in, err
	}
	questions, err := s.store.Questions(ctx, p.Name, true)
	if err != nil {
		return in, err
	}
	waiting := map[string]bool{}
	for _, q := range questions {
		if q.Waiting() {
			waiting[q.Agent] = true
		}
	}
	finished := map[string]bool{}
	if events, err := s.store.AgentEvents(ctx, p.Name); err == nil {
		seen := map[string]bool{}
		for _, ev := range events { // newest first
			if seen[ev.Agent] {
				continue
			}
			seen[ev.Agent] = true
			var e api.AgentEvent
			if json.Unmarshal(ev.Data, &e) == nil && e.Kind == api.AgentFinished {
				finished[ev.Agent] = true
			}
		}
	}
	prs := map[string]string{}
	if watches, err := s.store.PRWatches(ctx, p.Name); err == nil {
		for _, w := range watches {
			pr := fmt.Sprintf("#%d", w.Number)
			if w.Checks != "" {
				pr += " checks " + w.Checks
			}
			if w.Conflict {
				pr += ", conflicts"
			}
			prs[w.Agent] = pr
		}
	}
	peaks, _ := s.store.UsagePeaks(ctx, p.Name)
	repo, repoErr := gitrepo.Open(p.Root)
	for _, st := range statuses {
		if st.IsLead() || !holdsSlot(st.State) {
			continue
		}
		a := recheckAgent{Name: st.Name, Title: st.Title, Finished: finished[st.Name], PR: prs[st.Name],
			MemoryPeak: peaks[st.Name].Memory, CPUPeak: peaks[st.Name].CPU}
		since, busy := s.autoStopIdleSince(ctx, m, st, waiting[st.Name], now)
		a.Busy = busy || st.State != "running"
		if !a.Busy && !since.IsZero() {
			a.IdleFor = now.Sub(since)
		}
		if reports, err := s.memory().Reports(ctx, p.Name, st.Name); err == nil && len(reports) > 0 {
			a.Report = reportLine(reports[0])
		}
		if !a.Busy {
			changes := changesOf(st.Agent)
			a.Dirty = changes.Dirty
			a.Unpushed = repoErr == nil && changes.Files > 0 && !agent.BranchDisposable(repo, st.Agent)
		}
		if u, ok := s.lastUsage(st.Ref()); ok {
			a.Memory, a.CPU = u.Memory, u.CPU
		}
		in.Agents = append(in.Agents, a)
	}
	sort.Slice(in.Agents, func(i, j int) bool { return in.Agents[i].Name < in.Agents[j].Name })
	return in, nil
}

// reportLine is a report in a few words: its status and the start of its
// summary.
func reportLine(r memory.Report) string {
	summary := strings.Join(strings.Fields(r.Summary), " ")
	if len([]rune(summary)) > 60 {
		summary = string([]rune(summary)[:59]) + "…"
	}
	if summary == "" {
		return r.Status
	}
	return fmt.Sprintf("%s %q", r.Status, summary)
}
