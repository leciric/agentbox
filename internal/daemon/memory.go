package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/memory"
)

// The project memory API (D72).
// One set of handlers serves three surfaces, because they are one memory read
// from three places:
//
//   - /v1/projects/{project}/memory/… — the user, on the daemon's socket.
//   - /v1/project/memory/…            — a project's chat, on its own socket,
//     which is what decides the project (D42).
//   - /v1/self/memory/…               — an agent, on its own socket, which is
//     what decides both the project and the agent (D17).
//
// An agent may read all of it and add to the append-only parts. It may not
// curate: writing a memory down, and saying what the project is doing now, are
// the user's and the lead's. An agent says what it found and what it did, and
// something with the whole project in view decides what that means.

// memory is the store of what a project remembers, on the database package
// state opened.
func (s *Server) memory() *memory.Store { return memory.New(s.store.DB()) }

// memoryScope is whose memory a request is about. project is always set;
// agent is set only inside an agent, where the socket says which one is
// asking and nothing it sends can claim to be another.
type memoryScope struct {
	project string
	agent   string
}

// projectMemoryScope reads the project from the route, and checks it exists so
// a typo answers 404 rather than an empty list.
func (s *Server) projectMemoryScope(r *http.Request) (memoryScope, error) {
	project := r.PathValue("project")
	if _, err := s.store.Project(r.Context(), project); err != nil {
		return memoryScope{}, err
	}
	return memoryScope{project: project}, nil
}

// agentMemoryScope is the agent behind a socket, and its project.
func (s *Server) agentMemoryScope(instance string) func(*http.Request) (memoryScope, error) {
	return func(r *http.Request) (memoryScope, error) {
		a, err := s.store.AgentByInstance(r.Context(), instance)
		if err != nil {
			return memoryScope{}, err
		}
		return memoryScope{project: a.Project, agent: a.Name}, nil
	}
}

// memoryRoutes are the memory surface. inAgent marks the ones an agent gets on
// its own socket too: everything that reads, and everything that appends.
var memoryRoutes = []struct {
	method  string
	path    string
	action  string
	inAgent bool
}{
	{http.MethodGet, "/events", "events", true},
	{http.MethodPost, "/events", "append-event", true},
	{http.MethodGet, "/memories", "memories", true},
	{http.MethodPost, "/memories", "add-memory", false},
	{http.MethodPost, "/search", "search", true},
	{http.MethodGet, "/working", "working", true},
	{http.MethodPatch, "/working", "set-working", false},
	{http.MethodGet, "/artifacts", "artifacts", true},
	{http.MethodPost, "/artifacts", "add-artifact", true},
	{http.MethodGet, "/reports", "reports", true},
	{http.MethodPost, "/reports", "add-report", true},
	{http.MethodPost, "/context", "context", true},
	{http.MethodGet, "/context/stats", "context-stats", true},
	// Consolidation (D76). An agent reads how its project's memory is being
	// kept, the same way it reads the memory itself; closing an issue and
	// spending the project's tokens on a distillation are curation, and stay
	// with the user and the lead.
	{http.MethodGet, "/consolidation", "consolidation", true},
	{http.MethodGet, "/duplicates", "duplicates", true},
	{http.MethodPost, "/resolve", "resolve-memory", false},
	{http.MethodPost, "/consolidate", "consolidate", false},
	// The task graph (D77). A worker reads the whole of it — what everybody
	// is on and what is waiting on what is exactly the thing it needs before
	// it touches the same files — and updates its own task, which is the one
	// row it is the authority on. Writing work down for somebody else,
	// moving a task under another and drawing a blocking edge are curating
	// the plan, and need every agent in view.
	{http.MethodGet, "/tasks", "tasks", true},
	{http.MethodGet, "/tasks/{task}", "task", true},
	{http.MethodPost, "/tasks", "add-task", false},
	{http.MethodPatch, "/tasks/{task}", "update-task", true},
	{http.MethodPost, "/tasks/link", "link-tasks", false},
	{http.MethodPost, "/tasks/unlink", "unlink-tasks", false},
}

// memoryHandler is one route of the memory surface, for whichever scope the
// socket it arrived on decides.
func (s *Server) memoryHandler(action string, scope func(*http.Request) (memoryScope, error)) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		who, err := scope(r)
		if err != nil {
			return err
		}
		m := s.memory()
		ctx := r.Context()
		switch action {
		case "events":
			filter, err := eventFilter(r)
			if err != nil {
				return err
			}
			events, err := m.Events(ctx, who.project, filter)
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, apiEvents(events))

		case "append-event":
			var req api.AddMemoryEventRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			e := memory.Event{
				Project: who.project, Agent: agentOf(who, req.Agent), Session: req.Session,
				Type: req.Type, Payload: req.Payload, ArtifactID: req.ArtifactID,
			}
			if req.At != nil {
				e.At = *req.At
			}
			out, err := m.AppendEvent(ctx, e)
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusCreated, apiEvent(out))

		case "memories":
			memories, err := m.Memories(ctx, who.project, kindsOf(r))
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, apiMemories(memories))

		case "add-memory":
			var req api.AddMemoryRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			out, err := m.AddMemory(ctx, memory.Memory{
				Project: who.project, Kind: req.Kind, Title: req.Title, Content: req.Content,
				Importance: req.Importance, SupersedesID: req.SupersedesID, SourceEventID: req.SourceEventID,
			})
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusCreated, apiMemory(out))

		case "search":
			var req api.MemorySearchRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			results, err := m.Search(ctx, who.project, req.Query, req.Limit)
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, api.MemorySearchResults{
				Memories: apiMemories(results.Memories),
				Events:   apiEvents(results.Events),
				Reports:  apiReports(results.Reports),
			})

		case "working":
			working, err := m.WorkingMemory(ctx, who.project)
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, apiWorking(working))

		case "set-working":
			var req api.WorkingMemoryPatch
			if err := readJSON(r, &req); err != nil {
				return err
			}
			working, err := m.SetWorkingMemory(ctx, who.project, memory.WorkingMemoryPatch{
				Goal: req.Goal, CurrentTask: req.CurrentTask, ActiveAgents: req.ActiveAgents,
				Blockers: req.Blockers, Notes: req.Notes,
			})
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, apiWorking(working))

		case "artifacts":
			artifacts, err := m.Artifacts(ctx, who.project)
			if err != nil {
				return err
			}
			out := make([]api.MemoryArtifact, 0, len(artifacts))
			for _, a := range artifacts {
				out = append(out, apiArtifact(a))
			}
			return writeJSON(w, http.StatusOK, out)

		case "add-artifact":
			var req api.AddArtifactRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			out, err := m.AddArtifact(ctx, memory.Artifact{
				Project: who.project, Agent: agentOf(who, req.Agent),
				Type: req.Type, Path: req.Path, Metadata: req.Metadata,
			})
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusCreated, apiArtifact(out))

		case "reports":
			agent := r.URL.Query().Get("agent")
			reports, err := m.Reports(ctx, who.project, agent)
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, apiReports(reports))

		case "consolidation":
			p, err := s.store.Project(ctx, who.project)
			if err != nil {
				return err
			}
			stats, err := m.ConsolidationStats(ctx, who.project)
			if err != nil {
				return err
			}
			stats.Setting = p.Consolidation
			recent, err := m.Passes(ctx, who.project, consolidationHistory)
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, apiConsolidation(stats, recent))

		case "duplicates":
			pairs, err := m.Duplicates(ctx, who.project)
			if err != nil {
				return err
			}
			out := make([]api.MemoryDuplicate, 0, len(pairs))
			for _, d := range pairs {
				out = append(out, api.MemoryDuplicate{
					Memory: apiMemory(d.Memory), Of: apiMemory(d.Of),
					Similarity: d.Similarity, FoundAt: d.FoundAt,
				})
			}
			return writeJSON(w, http.StatusOK, out)

		case "resolve-memory":
			var req api.ResolveMemoryRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			out, err := m.ResolveMemory(ctx, who.project, req.ID, req.Why)
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, apiMemory(out))

		case "consolidate":
			return s.consolidateProject(w, r, who.project)

		case "add-report":
			var req api.AddReportRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			out, err := m.AddReport(ctx, memory.Report{
				Project: who.project, Agent: agentOf(who, req.Agent), Task: req.Task, Status: req.Status,
				Summary: req.Summary, Discoveries: req.Discoveries, Decisions: req.Decisions,
				RemainingIssues: req.RemainingIssues, Artifacts: req.Artifacts,
			})
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusCreated, apiReport(out))

		case "context":
			var req api.ContextRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			built, err := s.buildContext(ctx, m, who, req)
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, built)

		case "context-stats":
			return writeJSON(w, http.StatusOK, apiContextAccount(memory.ContextBuilds(who.project)))

		case "tasks":
			filter, err := taskFilter(r)
			if err != nil {
				return err
			}
			tasks, err := m.Tasks(ctx, who.project, filter)
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, apiTasks(tasks))

		case "task":
			task, err := m.Task(ctx, who.project, r.PathValue("task"))
			if err != nil {
				return err
			}
			return writeJSON(w, http.StatusOK, apiTask(task))

		case "add-task":
			var req api.AddTaskRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			out, err := m.AddTask(ctx, memory.Task{
				Project: who.project, Agent: strings.TrimSpace(req.Agent), ParentID: req.ParentID,
				Status: req.Status, Goal: req.Goal, Detail: req.Detail,
			})
			if err != nil {
				return err
			}
			// A task written down with its blockers already named is one
			// call, not three. An edge that would close a cycle is refused
			// and the task stays: the plan gained a row, not a loop.
			for _, on := range req.DependsOn {
				if err := m.LinkTasks(ctx, who.project, out.ID, on); err != nil {
					return err
				}
			}
			if out, err = m.Task(ctx, who.project, out.ID); err != nil {
				return err
			}
			s.captureTaskCreated(ctx, out)
			if len(out.DependsOn) > 0 {
				s.captureTaskBlocked(ctx, who.project, out, out.DependsOn)
			}
			return writeJSON(w, http.StatusCreated, apiTask(out))

		case "update-task":
			var req api.UpdateTaskRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			id := r.PathValue("task")
			was, err := m.Task(ctx, who.project, id)
			if err != nil {
				return err
			}
			patch, err := taskPatch(who, was, req)
			if err != nil {
				return err
			}
			out, err := m.UpdateTask(ctx, who.project, id, patch)
			if err != nil {
				return err
			}
			if out.Status != was.Status {
				s.captureTaskStatus(ctx, out, was.Status)
			}
			return writeJSON(w, http.StatusOK, apiTask(out))

		case "link-tasks", "unlink-tasks":
			var req api.LinkTasksRequest
			if err := readJSON(r, &req); err != nil {
				return err
			}
			if action == "unlink-tasks" {
				if err := m.UnlinkTasks(ctx, who.project, req.TaskID, req.DependsOnID); err != nil {
					return err
				}
			} else {
				if err := m.LinkTasks(ctx, who.project, req.TaskID, req.DependsOnID); err != nil {
					return err
				}
			}
			task, err := m.Task(ctx, who.project, req.TaskID)
			if err != nil {
				return err
			}
			if action == "link-tasks" {
				s.captureTaskBlocked(ctx, who.project, task, []string{req.DependsOnID})
			}
			return writeJSON(w, http.StatusOK, apiTask(task))
		}
		return fmt.Errorf("unknown memory action %q", action)
	}
}

// buildContext answers POST /context: what the project knows about a query,
// bounded to a budget (D75). It is the same builder the lead's recap and a
// worker's brief go through, which is the point of the route — the app, a
// future tool and an agent's AI tool all get one slice rather than three.
//
// The budget defaults to the project's own setting, and an agent asking for
// one gets a worker's share of it: an agent can ask for more, up to the bounds
// the builder clamps to, because a worker that has read its brief and wants
// the whole of what the project knows is a worker doing the right thing.
func (s *Server) buildContext(ctx context.Context, m *memory.Store, who memoryScope, req api.ContextRequest) (api.ContextResult, error) {
	audience := memory.Audience(req.For)
	if audience == "" && who.agent != "" {
		audience = memory.ForAgent
	}
	budget := req.Budget
	if budget == 0 {
		p, err := s.store.Project(ctx, who.project)
		if err != nil {
			return api.ContextResult{}, err
		}
		budget = p.ContextBudget
		if who.agent != "" {
			budget = memory.AgentBudget(budget)
		}
	}
	built, err := m.BuildContext(ctx, memory.ContextRequest{
		Project: who.project, Query: req.Query, Budget: budget, For: audience,
	})
	if err != nil {
		return api.ContextResult{}, err
	}
	return apiContext(built), nil
}

// taskPatch is what a caller is allowed to change about a task. From the
// user's routes or a project chat's it is the whole of the request: curating
// the plan is what those two are for. Inside an agent it is the status and
// the detail of *its own* task and nothing else — a worker knows how its own
// work is going better than anything else does, and knows nothing about
// whether it should still be the one doing it, what contains it, or what it
// is now called.
func taskPatch(who memoryScope, task memory.Task, req api.UpdateTaskRequest) (memory.TaskPatch, error) {
	patch := memory.TaskPatch{Status: req.Status, Goal: req.Goal, Detail: req.Detail,
		Agent: req.Agent, ParentID: req.ParentID}
	if who.agent == "" {
		return patch, nil
	}
	if task.Agent != who.agent {
		whose := "nobody's yet"
		if task.Agent != "" {
			whose = task.Agent + "'s"
		}
		return memory.TaskPatch{}, fmt.Errorf("%s is %s task, not yours: say how your own is going, and ask the project's chat to change the plan",
			task.ID, whose)
	}
	if req.Goal != nil || req.Agent != nil || req.ParentID != nil {
		return memory.TaskPatch{}, errors.New("an agent may say how its own task is going — its status and its detail — " +
			"and not what the task is, who it belongs to or what contains it: that is the project chat's to decide")
	}
	return memory.TaskPatch{Status: req.Status, Detail: req.Detail}, nil
}

// taskFilter reads the query parameters of a task listing. ?open=true is the
// common one: a plan is what is left to do.
func taskFilter(r *http.Request) (memory.TaskFilter, error) {
	q := r.URL.Query()
	f := memory.TaskFilter{Agent: q.Get("agent"), Statuses: q["status"], Parent: q.Get("parent")}
	if raw := q.Get("open"); raw != "" {
		open, err := strconv.ParseBool(raw)
		if err != nil {
			return f, fmt.Errorf("invalid open %q: it is true or false", raw)
		}
		f.OpenOnly = open
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return f, fmt.Errorf("invalid limit %q", raw)
		}
		f.Limit = n
	}
	return f, nil
}

func apiTask(t memory.Task) api.Task {
	return api.Task{
		ID: t.ID, Project: t.Project, Agent: t.Agent, ParentID: t.ParentID, Status: t.Status,
		Goal: t.Goal, Detail: t.Detail, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		ClosedAt: t.ClosedAt, DependsOn: t.DependsOn, Blocks: t.Blocks,
	}
}

func apiTasks(tasks []memory.Task) []api.Task {
	out := make([]api.Task, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, apiTask(t))
	}
	return out
}

// agentOf is which agent a written row belongs to. Inside an agent it is the
// one behind the socket, whatever the body says: an agent can't file a report
// in another's name. Elsewhere the caller names it, or names nobody.
func agentOf(who memoryScope, named string) string {
	if who.agent != "" {
		return who.agent
	}
	return strings.TrimSpace(named)
}

// eventFilter reads the query parameters of a listing. An agent reads its
// project's whole history, not only its own: what another agent hit yesterday
// is the thing worth knowing.
func eventFilter(r *http.Request) (memory.EventFilter, error) {
	q := r.URL.Query()
	f := memory.EventFilter{Agent: q.Get("agent"), Types: q["type"]}
	if raw := q.Get("since"); raw != "" {
		ms, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return f, fmt.Errorf("invalid since %q: it is a Unix time in milliseconds", raw)
		}
		f.Since = time.UnixMilli(ms)
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return f, fmt.Errorf("invalid limit %q", raw)
		}
		f.Limit = n
	}
	return f, nil
}

// kindsOf reads ?kind=decision,issue — one parameter or several, either way.
func kindsOf(r *http.Request) []string {
	var kinds []string
	for _, raw := range r.URL.Query()["kind"] {
		for _, kind := range strings.Split(raw, ",") {
			if kind = strings.TrimSpace(kind); kind != "" {
				kinds = append(kinds, kind)
			}
		}
	}
	return kinds
}

func apiEvent(e memory.Event) api.MemoryEvent {
	return api.MemoryEvent{
		ID: e.ID, Project: e.Project, Agent: e.Agent, Session: e.Session,
		At: e.At, Type: e.Type, Payload: e.Payload, ArtifactID: e.ArtifactID,
	}
}

func apiEvents(events []memory.Event) []api.MemoryEvent {
	out := make([]api.MemoryEvent, 0, len(events))
	for _, e := range events {
		out = append(out, apiEvent(e))
	}
	return out
}

func apiMemory(m memory.Memory) api.Memory {
	return api.Memory{
		ID: m.ID, Project: m.Project, Kind: m.Kind, Title: m.Title, Content: m.Content,
		Importance: m.Importance, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
		SupersedesID: m.SupersedesID, SourceEventID: m.SourceEventID, Superseded: m.Superseded,
		ResolvedAt: m.ResolvedAt, ResolvedBy: m.ResolvedBy,
		ReferencedAt: m.ReferencedAt, DecayedAt: m.DecayedAt,
	}
}

// consolidationHistory is how many passes the consolidation route carries.
// Enough to see a trend, few enough that the response stays one screen.
const consolidationHistory = 10

func apiPass(p memory.Pass) api.ConsolidationPass {
	return api.ConsolidationPass{
		ID: p.ID, Project: p.Project, Kind: p.Kind, At: p.At, DurationMS: p.Duration.Milliseconds(),
		EventsRead: p.EventsRead, MemoriesWritten: p.MemoriesWritten,
		MemoriesSuperseded: p.MemoriesSuperseded, MemoriesResolved: p.MemoriesResolved,
		MemoriesDecayed: p.MemoriesDecayed, DuplicatesFound: p.DuplicatesFound,
		InputBytes: p.InputBytes, OutputBytes: p.OutputBytes, Model: p.Model,
		ThroughEventID: p.ThroughEventID, ThroughAt: p.ThroughAt, Error: p.Error,
	}
}

func apiConsolidation(s memory.Stats, recent []memory.Pass) api.MemoryConsolidation {
	out := api.MemoryConsolidation{
		Project: s.Project, Setting: s.Setting,
		Events: s.Events, Memories: s.Memories, Superseded: s.Superseded, Resolved: s.Resolved,
		Duplicates: s.Duplicates, Pending: s.Pending,
		Passes: s.Passes, EventsRead: s.EventsRead, MemoriesWritten: s.MemoriesWritten,
		MemoriesSuperseded: s.MemoriesSuperseded, MemoriesResolved: s.MemoriesResolved,
		MemoriesDecayed: s.MemoriesDecayed, InputBytes: s.InputBytes, OutputBytes: s.OutputBytes,
		LastPass: s.LastPass, Watermark: s.Watermark,
		Recent: make([]api.ConsolidationPass, 0, len(recent)),
	}
	for _, p := range recent {
		out.Recent = append(out.Recent, apiPass(p))
	}
	return out
}

func apiMemories(memories []memory.Memory) []api.Memory {
	out := make([]api.Memory, 0, len(memories))
	for _, m := range memories {
		out = append(out, apiMemory(m))
	}
	return out
}

func apiWorking(w memory.WorkingMemory) api.WorkingMemory {
	return api.WorkingMemory{
		Goal: w.Goal, CurrentTask: w.CurrentTask, ActiveAgents: w.ActiveAgents,
		Blockers: w.Blockers, Notes: w.Notes, UpdatedAt: w.UpdatedAt,
	}
}

func apiArtifact(a memory.Artifact) api.MemoryArtifact {
	return api.MemoryArtifact{
		ID: a.ID, Project: a.Project, Agent: a.Agent, Type: a.Type,
		Path: a.Path, Metadata: a.Metadata, CreatedAt: a.CreatedAt,
	}
}

func apiReport(r memory.Report) api.AgentReport {
	return api.AgentReport{
		ID: r.ID, Project: r.Project, Agent: r.Agent, CreatedAt: r.CreatedAt,
		Task: r.Task, Status: r.Status, Summary: r.Summary, Discoveries: r.Discoveries,
		Decisions: r.Decisions, RemainingIssues: r.RemainingIssues, Artifacts: r.Artifacts,
	}
}

func apiReports(reports []memory.Report) []api.AgentReport {
	out := make([]api.AgentReport, 0, len(reports))
	for _, r := range reports {
		out = append(out, apiReport(r))
	}
	return out
}

func apiContext(c memory.Context) api.ContextResult {
	sections := make([]api.ContextSection, 0, len(c.Sections))
	for _, sec := range c.Sections {
		sections = append(sections, api.ContextSection{
			Kind: sec.Kind, Title: sec.Title, Rows: sec.Rows, Tokens: sec.Tokens,
		})
	}
	return api.ContextResult{Text: c.Text, Sections: sections, Stats: apiContextStats(c.Stats)}
}

func apiContextStats(st memory.ContextStats) api.ContextStats {
	return api.ContextStats{
		Project: st.Project, For: string(st.For), At: st.At, Query: st.Query,
		Budget: st.Budget, Tokens: st.Tokens, Rows: st.Rows,
		ConsideredRows: st.Considered, DroppedRows: st.Dropped,
		DroppedSections: st.DroppedSections, Truncated: st.Truncated,
		CorpusTokens: st.CorpusTokens, Ratio: st.Ratio,
	}
}

func apiContextAccount(a memory.ContextAccount) api.ContextAccount {
	recent := make([]api.ContextStats, 0, len(a.Recent))
	for _, st := range a.Recent {
		recent = append(recent, apiContextStats(st))
	}
	return api.ContextAccount{Builds: a.Builds, Tokens: a.Tokens, DroppedRows: a.DroppedRows, Recent: recent}
}
