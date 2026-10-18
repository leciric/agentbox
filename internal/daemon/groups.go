package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// Groups: one task run on several agents at once, each perhaps on another AI
// tool or model, so the user can compare what each made of it and keep one
// (state/groups.go). Orca's answer to the same wish is a recipe: make the
// worktrees by hand, paste the prompt into each and diff them. Here a group
// is one request:
//
//   - fanOut checks every agent the group asks for as create_agent would
//     (checkCreate), then starts them all, each on a branch of the same slug
//     with its letter after it, and each told it is one of several, to commit
//     and show its work but not open a pull request until it is picked.
//   - Each member's finish is recorded once (GroupMemberFinished). A member's
//     own finish doesn't wake the lead; the last one's does, with a notice
//     the app shows as the group (api.ChatItem.Group).
//   - getGroup compares them: state, diff, pull request and checks, tokens
//     and cost, time, media and report, for the app's Compare view and the
//     lead's compare_group.
//   - keepGroup settles it on one: the others are retired (stopped, their
//     branches and worktrees kept) and the kept one is told to open its pull
//     request, after which it goes to review like any agent.
//   - judgeGroup asks the lead to compare them and recommend one with
//     recommend_agent; the user still decides.

// groupLetters label a group's agents in the order it asked for them.
const groupLetters = "ABCDEF"

// recentGroups is how long a settled group stays listed: long enough to find
// the one just decided, not so long the sidebar fills with old comparisons.
const recentGroups = 7 * 24 * time.Hour

func groupLabel(position int) string {
	if position < 1 || position > len(groupLetters) {
		return fmt.Sprint(position)
	}
	return groupLetters[position-1 : position]
}

// groupBranch is a member's branch slug: the group's, then its letter.
func groupBranch(base string, position int) string {
	suffix := "-" + strings.ToLower(groupLabel(position))
	if len(base)+len(suffix) > agent.MaxBranchSlugLen {
		base = strings.TrimRight(base[:agent.MaxBranchSlugLen-len(suffix)], "-")
	}
	return base + suffix
}

// groupTask is what each member is given: the task, and what being one of
// several changes about finishing it.
func groupTask(task string, label string, size int) string {
	return strings.TrimSpace(task) + "\n\n---\n\n" + fmt.Sprintf(
		"You are agent %s of %d given this same task at once, so the user can compare what each of you makes of it and keep one. "+
			"Work on it as you would alone, without looking for the others. Commit your work on your branch and record your proof "+
			"(recordings, screenshots) as usual, and say in your report which tests you ran and whether they passed. "+
			"Don't push or open a pull request yet: the one the user keeps is told to, and the others are retired with their branches kept.",
		label, size)
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) error {
	var req api.FanOutRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	out, err := s.fanOut(r.Context(), req, false)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, out)
}

func (s *Server) leadFanOut(w http.ResponseWriter, r *http.Request) error {
	var req api.FanOutRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	req.Project = r.PathValue("project")
	out, err := s.fanOut(r.Context(), req, true)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, out)
}

// fanOut makes a group and starts its agents. Every one of them is checked
// before the first starts, so a model one of them can't run is refused with
// nothing made rather than leaving half a group behind.
func (s *Server) fanOut(ctx context.Context, req api.FanOutRequest, byLead bool) (api.FanOutResult, error) {
	req.Title, req.Task = strings.TrimSpace(req.Title), strings.TrimSpace(req.Task)
	if req.Title == "" || req.Task == "" {
		return api.FanOutResult{}, errors.New("a group needs a title and a task")
	}
	if n := len(req.Variants); n < 2 || n > state.MaxGroupSize {
		return api.FanOutResult{}, fmt.Errorf("a group runs its task on 2 to %d agents, not %d", state.MaxGroupSize, n)
	}
	if err := agent.CheckBranchSlug(req.Branch); err != nil {
		return api.FanOutResult{}, err
	}
	base := req.Branch
	if base == "" {
		base = agent.SlugForBranch(req.Title)
	}
	if base == "" {
		base = "group"
	}
	reqs := make([]api.CreateAgentRequest, len(req.Variants))
	members := make([]state.GroupMember, len(req.Variants))
	for i, v := range req.Variants {
		label := groupLabel(i + 1)
		one := api.CreateAgentRequest{
			Project: req.Project, Title: req.Title + " · " + label, Branch: groupBranch(base, i+1), From: req.From,
			Task: groupTask(req.Task, label, len(req.Variants)),
			AI:   v.AI, Model: v.Model, Effort: v.Effort, ContextWindow: v.ContextWindow,
			ClaudeAccount: v.ClaudeAccount, CodexAccount: v.CodexAccount, OpenCodeAccounts: v.OpenCodeAccounts, CursorAccount: v.CursorAccount,
		}
		checked, err := s.checkCreate(ctx, one, byLead)
		if err != nil {
			return api.FanOutResult{}, fmt.Errorf("agent %s: %w", label, err)
		}
		reqs[i] = checked
		members[i] = state.GroupMember{AI: checked.AI, Model: deref(v.Model), Effort: deref(v.Effort)}
	}
	g, err := s.store.AddGroup(ctx, state.Group{Project: reqs[0].Project, Title: req.Title, Task: req.Task, ByLead: byLead, Judge: req.Judge, Members: members})
	if err != nil {
		return api.FanOutResult{}, err
	}
	out := api.FanOutResult{Jobs: []api.Job{}}
	for i, one := range reqs {
		position := i + 1
		j, err := s.launchJob("create", one.Project, s.groupCreateJob(g.ID, position, one, byLead))
		if err != nil {
			_ = s.store.SetGroupMember(ctx, g.ID, position, "", "", err.Error())
			continue
		}
		_ = s.store.SetGroupMember(ctx, g.ID, position, j.ID, "", "")
		out.Jobs = append(out.Jobs, j)
	}
	s.countFeature(api.FeatureGroupFanOut)
	if g, err = s.store.GroupByID(ctx, g.ID); err != nil {
		return api.FanOutResult{}, err
	}
	out.Group = groupInfo(g)
	s.publishGroup(g)
	// The project's chat hears of a group the user made, so it knows these
	// agents are being compared rather than each one's to follow up; the one
	// it made itself it already knows of. The notice shows as the group.
	// The project's chat shows the group from the start, as a card, whoever
	// made it; a group the user made is news to the lead too, so it knows
	// these agents are being compared rather than each one's to follow up.
	who := "You ran"
	if !byLead {
		who = "The user ran"
	}
	s.tellLeadOf(ctx, g.Project, fmt.Sprintf("%s one task on %d agents at once, to compare them and keep one (group %s, %q): %s. "+
		"They are told not to open pull requests until one is kept; you'll hear when all of them have finished. Task:\n\n%s",
		who, len(g.Members), g.ID, g.Title, memberList(g), g.Task), chat.NoticeOptions{Group: g.ID})
	return out, nil
}

// groupCreateJob is createJob for one member of a group, recording which
// agent it made, or why it couldn't.
func (s *Server) groupCreateJob(group string, position int, req api.CreateAgentRequest, byLead bool) func(context.Context, io.Writer) (any, error) {
	create := s.createJob(req, byLead, "")
	return func(ctx context.Context, log io.Writer) (any, error) {
		out, err := create(ctx, log)
		record := context.WithoutCancel(ctx)
		if err != nil {
			_ = s.store.SetGroupMember(record, group, position, "", "", err.Error())
			s.groupMoved(record, group)
			return out, err
		}
		if a, ok := out.(api.Agent); ok {
			_ = s.store.SetGroupMember(record, group, position, "", a.Name, "")
		}
		s.groupMoved(record, group)
		return out, nil
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// groupInfo is a group as GET /v1/groups lists it, without comparing.
func groupInfo(g state.Group) api.Group {
	out := api.Group{
		ID: g.ID, Project: g.Project, Title: g.Title, Task: g.Task, ByLead: g.ByLead, Kept: g.Kept,
		Recommended: g.Recommended, Recommendation: g.Recommendation, Judge: g.Judge, Judging: g.Judging,
		CreatedAt: g.CreatedAt, Members: []api.GroupMember{},
	}
	if g.Settled() {
		at := g.SettledAt
		out.SettledAt = &at
	}
	for _, m := range g.Members {
		row := api.GroupMember{Label: groupLabel(m.Position), AI: m.AI, Model: m.Model, Effort: m.Effort, Job: m.Job, Agent: m.Agent, Error: m.Error}
		if m.Agent != "" {
			row.Ref = g.Project + "/" + m.Agent
		}
		if !m.FinishedAt.IsZero() {
			at := m.FinishedAt
			row.FinishedAt = &at
		}
		if !m.FinishedAt.IsZero() || m.Error != "" {
			out.Finished++
		}
		out.Members = append(out.Members, row)
	}
	return out
}

func (s *Server) publishGroup(g state.Group) {
	s.events.publish(api.EventGroup, api.GroupChange{ID: g.ID, Project: g.Project})
}

// groupMoved publishes a group's change, and tells the lead once every one of
// its agents has finished or couldn't be made.
func (s *Server) groupMoved(ctx context.Context, id string) {
	g, err := s.store.GroupByID(ctx, id)
	if err != nil {
		return
	}
	s.publishGroup(g)
	s.groupMaybeDone(ctx, g)
}

// groupMemberFinished records an agent's first finish in a group still being
// compared, and reports the group, or "" for an agent in none or a finish
// after the first. That first finish's own notice then waits rather than
// waking the lead, which hears once, when the last one finishes
// (groupMaybeDone); a later one, after a word from the lead, is news as any
// agent's is.
func (s *Server) groupMemberFinished(ctx context.Context, a state.Agent) string {
	first, err := s.store.GroupMemberFinished(ctx, a.Project, a.Name, time.Now())
	if err != nil || !first {
		return ""
	}
	g, ok, err := s.store.GroupOf(ctx, a.Project, a.Name)
	if err != nil || !ok {
		return ""
	}
	return g.ID
}

// groupMaybeDone tells the lead a group's agents have all finished, once. It
// starts a turn when the lead made the group, or was asked to judge it.
func (s *Server) groupMaybeDone(ctx context.Context, g state.Group) {
	if g.Settled() {
		return
	}
	info := groupInfo(g)
	if info.Finished < len(g.Members) {
		return
	}
	s.mu.Lock()
	if s.groupsDone == nil {
		s.groupsDone = map[string]bool{}
	}
	told := s.groupsDone[g.ID]
	s.groupsDone[g.ID] = true
	s.mu.Unlock()
	if told {
		return
	}
	text := fmt.Sprintf("All %d agents of group %s (%q) have finished: %s. The user compares them in the app and keeps one.",
		len(g.Members), g.ID, g.Title, memberList(g))
	act := g.ByLead
	if g.Judge {
		_ = s.store.SetGroupJudging(ctx, g.ID, true)
		text += " " + judgeAsk(g)
		act = true
	} else if g.ByLead {
		text += " Tell the user they are ready to compare, in a line; don't keep one yourself."
	}
	s.tellLeadOf(ctx, g.Project, text, chat.NoticeOptions{Act: act, Group: g.ID})
	if g.Judge {
		s.publishGroup(g)
	}
}

func memberList(g state.Group) string {
	parts := make([]string, 0, len(g.Members))
	for _, m := range g.Members {
		who := m.Agent
		if who == "" {
			who = "not made: " + headWords(m.Error, 80)
		}
		what := m.AI
		if m.Model != "" {
			what += " " + m.Model
		}
		parts = append(parts, fmt.Sprintf("%s = %s (%s)", groupLabel(m.Position), who, what))
	}
	return strings.Join(parts, ", ")
}

// judgeAsk is what asks the lead to judge a group. Where the agents agree is
// worth saying: the same change made independently is likely right.
func judgeAsk(g state.Group) string {
	return fmt.Sprintf("You were asked to judge them: read compare_group %s, look at their diffs (agent_diff) and what they showed, "+
		"and call recommend_agent with the one you would keep and why, in two or three sentences: what it got right that the others "+
		"didn't, what they all agree on, and anything the pick still lacks. Then say the same to the user in a line. Don't keep or retire "+
		"any of them: the user decides.", g.ID)
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) error {
	groups, err := s.store.Groups(r.Context(), r.URL.Query().Get("project"), recentGroups)
	if err != nil {
		return err
	}
	out := make([]api.Group, 0, len(groups))
	for _, g := range groups {
		out = append(out, groupInfo(g))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) leadListGroups(w http.ResponseWriter, r *http.Request) error {
	r.URL.RawQuery = "project=" + r.PathValue("project")
	return s.listGroups(w, r)
}

// groupFromPath is the group a request names, held to the lead's own project
// on its socket.
func (s *Server) groupFromPath(r *http.Request) (state.Group, error) {
	g, err := s.store.GroupByID(r.Context(), r.PathValue("id"))
	if err != nil {
		return state.Group{}, err
	}
	if project := r.PathValue("project"); project != "" && g.Project != project {
		return state.Group{}, fmt.Errorf("group %q: %w", r.PathValue("id"), state.ErrNotFound)
	}
	return g, nil
}

func (s *Server) getGroup(w http.ResponseWriter, r *http.Request) error {
	g, err := s.groupFromPath(r)
	if err != nil {
		return err
	}
	out, err := s.compareGroup(r.Context(), g)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, out)
}

// compareGroup fills in everything the Compare view sets side by side, from
// what the daemon already keeps: the fleet's diff and pull request, the token
// ledger, media and reports. A member whose machine is gone is read from the
// archive.
func (s *Server) compareGroup(ctx context.Context, g state.Group) (api.Group, error) {
	out := groupInfo(g)
	p, err := s.store.Project(ctx, g.Project)
	if err != nil {
		return out, err
	}
	statuses, err := s.manager(nil).List(ctx, g.Project)
	if err != nil {
		return out, err
	}
	live := map[string]agent.Status{}
	var agents []state.Agent
	for _, st := range statuses {
		if _, ok := g.Member(st.Name); ok {
			live[st.Name] = st
			agents = append(agents, st.Agent)
		}
	}
	var prs map[string]*api.PullRequest
	if len(agents) > 0 {
		if _, entry, heads, _, err := s.projectPulls(p, agents); err == nil {
			prs = entry.byAgent(heads)
		}
	}
	changes := agentChanges(agents, changesOf)
	changed := map[string]api.AgentChanges{}
	for i, a := range agents {
		changed[a.Name] = changes[i]
	}
	ids := map[string]string{}
	for i := range out.Members {
		m := &out.Members[i]
		if m.Agent == "" {
			m.State = "creating"
			if m.Error != "" {
				m.State = "failed"
			}
			continue
		}
		if st, ok := live[m.Agent]; ok {
			info := s.agentInfo(st)
			created := st.CreatedAt
			m.StartedAt, m.Title, m.Branch, m.State = &created, info.Title, info.Branch, info.State
			m.Busy = s.chatBusy(st.Ref())
			c := changed[m.Agent]
			m.Changes = &c
			if pr := prs[m.Agent]; pr != nil {
				s.prWatch.overlay(g.Project, pr)
				m.PR = pr
			}
			ids[st.ID] = m.Agent
		} else if a, err := s.store.ArchivedAgentByName(ctx, g.Project, m.Agent); err == nil {
			created := a.CreatedAt
			m.StartedAt, m.Title, m.Branch, m.State = &created, a.Title, a.Branch, "archived"
			if a.PR.Number > 0 {
				m.PR = &api.PullRequest{Number: a.PR.Number, Title: a.PR.Title, URL: a.PR.URL, State: a.PR.State}
			}
			ids[a.ID] = m.Agent
		}
		if items, err := s.store.Media(ctx, g.Project, m.Agent); err == nil {
			m.Media = len(items)
		}
		if reports, err := s.memory().Reports(ctx, g.Project, m.Agent); err == nil && len(reports) > 0 {
			m.ReportStatus, m.ReportSummary = reports[0].Status, reports[0].Summary
		}
	}
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	if sums, err := s.store.ArchivedTokens(ctx, keys); err == nil {
		for i := range out.Members {
			m := &out.Members[i]
			for id, name := range ids {
				if name == m.Agent {
					m.Tokens, m.CostUSD = sums[id].Tokens, sums[id].CostUSD
				}
			}
		}
	}
	return out, nil
}

func (s *Server) keepGroup(w http.ResponseWriter, r *http.Request) error {
	var req api.KeepRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	g, err := s.groupFromPath(r)
	if err != nil {
		return err
	}
	if req.Agent == "" {
		return errors.New("name the agent to keep")
	}
	g, err = s.settleGroup(r.Context(), g, req.Agent)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, groupInfo(g))
}

func (s *Server) closeGroup(w http.ResponseWriter, r *http.Request) error {
	g, err := s.groupFromPath(r)
	if err != nil {
		return err
	}
	g, err = s.settleGroup(r.Context(), g, "")
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, groupInfo(g))
}

// settleGroup keeps one of a group's agents, or none: every other one still
// holding a machine is retired the way the app's Retire does it (stopped, its
// worktree and branch kept), and the kept one is told to open its pull
// request.
func (s *Server) settleGroup(ctx context.Context, g state.Group, keep string) (state.Group, error) {
	if keep != "" {
		if _, ok := g.Member(keep); !ok {
			return g, fmt.Errorf("%s isn't one of this group's agents", keep)
		}
		if _, err := s.store.Agent(ctx, g.Project, keep); err != nil {
			return g, fmt.Errorf("%s can't be kept: its machine is gone (its branch is still there)", keep)
		}
		if err := s.store.KeepInGroup(ctx, g.ID, keep); err != nil {
			return g, err
		}
	} else if err := s.store.CloseGroup(ctx, g.ID); err != nil {
		return g, err
	}
	m := s.manager(s.cfg.Log)
	var retired []string
	for _, member := range g.Members {
		if member.Agent == "" || member.Agent == keep {
			continue
		}
		a, err := s.store.Agent(ctx, g.Project, member.Agent)
		if err != nil {
			continue // already destroyed: its branch is what is left
		}
		if err := s.retireOne(ctx, m, a, api.RetireStop); err != nil {
			s.logf("retiring %s with its group: %v", a.Ref(), err)
			continue
		}
		s.recordAgentFinished(ctx, a, "retired")
		retired = append(retired, a.Name)
	}
	s.refreshAgents(ctx)
	text := fmt.Sprintf("The user closed group %s (%q) without keeping any of its agents", g.ID, g.Title)
	if keep != "" {
		text = fmt.Sprintf("The user kept %s from group %s (%q)", keep, g.ID, g.Title)
		if a, err := s.store.Agent(ctx, g.Project, keep); err == nil {
			if _, err := s.tellAgent(ctx, a, "You were picked: the user compared the agents given this task and kept your work. "+
				"Push your branch and open your pull request now, as you would have, with your proof."); err != nil {
				s.logf("telling %s it was kept: %v", a.Ref(), err)
			}
		}
	}
	if len(retired) > 0 {
		text += fmt.Sprintf("; %s retired, their branches kept.", strings.Join(retired, ", "))
	} else {
		text += "."
	}
	s.tellLeadOf(ctx, g.Project, text, chat.NoticeOptions{Group: g.ID})
	s.countFeature(api.FeatureGroupKeep)
	g, err := s.store.GroupByID(ctx, g.ID)
	if err != nil {
		return g, err
	}
	s.publishGroup(g)
	return g, nil
}

// judgeGroup asks the project's chat to compare a group's agents and
// recommend one. It can be asked again, after another look.
func (s *Server) judgeGroup(w http.ResponseWriter, r *http.Request) error {
	g, err := s.groupFromPath(r)
	if err != nil {
		return err
	}
	if g.Settled() {
		return errors.New("this group was already decided")
	}
	if _, err := s.manager(nil).Lead(r.Context(), g.Project); err != nil {
		return fmt.Errorf("this project has no chat to judge with yet: %w", err)
	}
	if err := s.store.SetGroupJudging(r.Context(), g.ID, true); err != nil {
		return err
	}
	text := fmt.Sprintf("The user asks you to judge group %s (%q): %s. %s", g.ID, g.Title, memberList(g), judgeAsk(g))
	if info := groupInfo(g); info.Finished < len(g.Members) {
		text += fmt.Sprintf(" Only %d of %d have finished: judge what is there, and say which are still working.", info.Finished, len(g.Members))
	}
	s.tellLeadOf(r.Context(), g.Project, text, chat.NoticeOptions{Act: true, Group: g.ID})
	if g, err = s.store.GroupByID(r.Context(), g.ID); err != nil {
		return err
	}
	s.publishGroup(g)
	return writeJSON(w, http.StatusOK, groupInfo(g))
}

func (s *Server) leadRecommend(w http.ResponseWriter, r *http.Request) error {
	var req api.RecommendRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	g, err := s.groupFromPath(r)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Why) == "" {
		return errors.New("say why, in two or three sentences")
	}
	if err := s.store.RecommendInGroup(r.Context(), g.ID, req.Agent, strings.TrimSpace(req.Why)); err != nil {
		return err
	}
	if g, err = s.store.GroupByID(r.Context(), g.ID); err != nil {
		return err
	}
	s.publishGroup(g)
	return writeJSON(w, http.StatusOK, groupInfo(g))
}
