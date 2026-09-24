package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The project memory client. The same calls are served on three surfaces, so
// they are written once and scoped by where the client points:
//
//	c.ProjectMemory("pawly") // the user's, on the daemon's socket
//	c.LeadMemory()           // a project chat's, on its own project's socket
//	c.SelfMemory()           // an agent's own, on the in-agent socket
//
// What each surface allows is the daemon's business, not this client's: an
// agent that tries to curate a project's memories is refused by the route,
// not here.

// MemoryClient reaches one project's memory through one of the three surfaces.
type MemoryClient struct {
	c    *Client
	base string
}

// ProjectMemory is a project's memory on the daemon's own socket.
func (c *Client) ProjectMemory(project string) *MemoryClient {
	return &MemoryClient{c: c, base: "/v1/projects/" + url.PathEscape(project) + "/memory"}
}

// LeadMemory is the memory of the project behind a lead socket. Nothing here
// names a project, because a project's chat can't choose one.
func (c *Client) LeadMemory() *MemoryClient {
	return &MemoryClient{c: c, base: "/v1/project/memory"}
}

// SelfMemory is the memory of the project an agent belongs to, reached from
// inside that agent. It is the same memory: an agent reads what every other
// agent of the project has written.
func (c *Client) SelfMemory() *MemoryClient {
	return &MemoryClient{c: c, base: "/v1/self/memory"}
}

// EventQuery narrows a listing of events. A zero query is the project's most
// recent events.
type EventQuery struct {
	Agent string
	Types []string
	Since time.Time
	Limit int
}

func (q EventQuery) values() string {
	v := url.Values{}
	if q.Agent != "" {
		v.Set("agent", q.Agent)
	}
	for _, t := range q.Types {
		v.Add("type", t)
	}
	if !q.Since.IsZero() {
		v.Set("since", strconv.FormatInt(q.Since.UnixMilli(), 10))
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// Events are the project's, newest first.
func (m *MemoryClient) Events(ctx context.Context, q EventQuery) ([]MemoryEvent, error) {
	var out []MemoryEvent
	return out, m.c.do(ctx, http.MethodGet, m.base+"/events"+q.values(), nil, &out)
}

// AppendEvent records one thing that happened.
func (m *MemoryClient) AppendEvent(ctx context.Context, req AddMemoryEventRequest) (MemoryEvent, error) {
	var out MemoryEvent
	return out, m.c.do(ctx, http.MethodPost, m.base+"/events", req, &out)
}

// Memories are the project's live memories, of the kinds asked for; no kinds
// is every kind. What another memory has superseded is left out.
func (m *MemoryClient) Memories(ctx context.Context, kinds ...string) ([]Memory, error) {
	path := m.base + "/memories"
	if len(kinds) > 0 {
		path += "?kind=" + url.QueryEscape(strings.Join(kinds, ","))
	}
	var out []Memory
	return out, m.c.do(ctx, http.MethodGet, path, nil, &out)
}

// AddMemory writes one down.
func (m *MemoryClient) AddMemory(ctx context.Context, req AddMemoryRequest) (Memory, error) {
	var out Memory
	return out, m.c.do(ctx, http.MethodPost, m.base+"/memories", req, &out)
}

// Search looks through the project's memories, events and reports.
func (m *MemoryClient) Search(ctx context.Context, query string, limit int) (MemorySearchResults, error) {
	var out MemorySearchResults
	return out, m.c.do(ctx, http.MethodPost, m.base+"/search", MemorySearchRequest{Query: query, Limit: limit}, &out)
}

// WorkingMemory is what the project is doing right now.
func (m *MemoryClient) WorkingMemory(ctx context.Context) (WorkingMemory, error) {
	var out WorkingMemory
	return out, m.c.do(ctx, http.MethodGet, m.base+"/working", nil, &out)
}

// SetWorkingMemory changes the fields the patch names and leaves the rest.
func (m *MemoryClient) SetWorkingMemory(ctx context.Context, patch WorkingMemoryPatch) (WorkingMemory, error) {
	var out WorkingMemory
	return out, m.c.do(ctx, http.MethodPatch, m.base+"/working", patch, &out)
}

// Artifacts are the references the project has recorded, newest first.
func (m *MemoryClient) Artifacts(ctx context.Context) ([]MemoryArtifact, error) {
	var out []MemoryArtifact
	return out, m.c.do(ctx, http.MethodGet, m.base+"/artifacts", nil, &out)
}

// AddArtifact records a reference to something produced.
func (m *MemoryClient) AddArtifact(ctx context.Context, req AddArtifactRequest) (MemoryArtifact, error) {
	var out MemoryArtifact
	return out, m.c.do(ctx, http.MethodPost, m.base+"/artifacts", req, &out)
}

// Reports are what the project's agents said as they finished, newest first;
// one agent's when agent isn't empty.
func (m *MemoryClient) Reports(ctx context.Context, agent string) ([]AgentReport, error) {
	path := m.base + "/reports"
	if agent != "" {
		path += "?agent=" + url.QueryEscape(agent)
	}
	var out []AgentReport
	return out, m.c.do(ctx, http.MethodGet, path, nil, &out)
}

// AddReport files one.
func (m *MemoryClient) AddReport(ctx context.Context, req AddReportRequest) (AgentReport, error) {
	var out AgentReport
	return out, m.c.do(ctx, http.MethodPost, m.base+"/reports", req, &out)
}

// Context builds what the project knows about a query, bounded to a budget
// (D75). It is the same builder the lead's recap and a worker's brief go
// through, so what a tool asks for here is what an agent is given.
func (m *MemoryClient) Context(ctx context.Context, req ContextRequest) (ContextResult, error) {
	var out ContextResult
	return out, m.c.do(ctx, http.MethodPost, m.base+"/context", req, &out)
}

// ContextStats is what this project's context builds have cost since the
// daemon started.
func (m *MemoryClient) ContextStats(ctx context.Context) (ContextAccount, error) {
	var out ContextAccount
	return out, m.c.do(ctx, http.MethodGet, m.base+"/context/stats", nil, &out)
}

// TaskQuery narrows a listing of tasks. A zero query is the project's whole
// graph, what is in the way first.
type TaskQuery struct {
	Agent    string
	Statuses []string
	Parent   string
	OpenOnly bool
	Limit    int
}

func (q TaskQuery) values() string {
	v := url.Values{}
	if q.Agent != "" {
		v.Set("agent", q.Agent)
	}
	for _, status := range q.Statuses {
		v.Add("status", status)
	}
	if q.Parent != "" {
		v.Set("parent", q.Parent)
	}
	if q.OpenOnly {
		v.Set("open", "true")
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// Tasks are the project's, what is in the way first (D77).
func (m *MemoryClient) Tasks(ctx context.Context, q TaskQuery) ([]Task, error) {
	var out []Task
	return out, m.c.do(ctx, http.MethodGet, m.base+"/tasks"+q.values(), nil, &out)
}

// Task is one by id, with the tasks it waits on and the tasks waiting on it.
func (m *MemoryClient) Task(ctx context.Context, id string) (Task, error) {
	var out Task
	return out, m.c.do(ctx, http.MethodGet, m.base+"/tasks/"+url.PathEscape(id), nil, &out)
}

// AddTask writes one down. Not available to a worker agent: creating work for
// somebody else needs every agent in view.
func (m *MemoryClient) AddTask(ctx context.Context, req AddTaskRequest) (Task, error) {
	var out Task
	return out, m.c.do(ctx, http.MethodPost, m.base+"/tasks", req, &out)
}

// UpdateTask changes what the request names. A worker may do this to its own
// task's status and detail, and to nothing else.
func (m *MemoryClient) UpdateTask(ctx context.Context, id string, req UpdateTaskRequest) (Task, error) {
	var out Task
	return out, m.c.do(ctx, http.MethodPatch, m.base+"/tasks/"+url.PathEscape(id), req, &out)
}

// LinkTasks says one task is waiting on another. A cycle is refused, with the
// edge that would have closed it named.
func (m *MemoryClient) LinkTasks(ctx context.Context, task, dependsOn string) error {
	return m.c.do(ctx, http.MethodPost, m.base+"/tasks/link",
		LinkTasksRequest{TaskID: task, DependsOnID: dependsOn}, nil)
}

// UnlinkTasks takes that back.
func (m *MemoryClient) UnlinkTasks(ctx context.Context, task, dependsOn string) error {
	return m.c.do(ctx, http.MethodPost, m.base+"/tasks/unlink",
		LinkTasksRequest{TaskID: task, DependsOnID: dependsOn}, nil)
}

// ResolveMemory closes a memory with nothing put in its place: an issue that
// stopped being true. Superseding is for a memory that is wrong; this is for
// one that is over ([D76](../../docs/implementation/decisions.md#d76)).
func (m *MemoryClient) ResolveMemory(ctx context.Context, id, why string) (Memory, error) {
	var out Memory
	return out, m.c.do(ctx, http.MethodPost, m.base+"/resolve", ResolveMemoryRequest{ID: id, Why: why}, &out)
}

// Consolidation is what the project's consolidation has done and what is
// waiting: the numbers a compression ratio is made of.
func (m *MemoryClient) Consolidation(ctx context.Context) (MemoryConsolidation, error) {
	var out MemoryConsolidation
	return out, m.c.do(ctx, http.MethodGet, m.base+"/consolidation", nil, &out)
}

// Duplicates are the near-duplicate memory pairs the last mechanical pass
// flagged, closest first. Nothing has been merged: these are pairs somebody
// might decide about.
func (m *MemoryClient) Duplicates(ctx context.Context) ([]MemoryDuplicate, error) {
	var out []MemoryDuplicate
	return out, m.c.do(ctx, http.MethodGet, m.base+"/duplicates", nil, &out)
}

// Consolidate runs the project's consolidation now. With distil it also asks
// the project's chat to turn the events since the watermark into memories,
// which spends the project's tokens; without it, only the mechanical pass
// runs, which costs nothing.
func (m *MemoryClient) Consolidate(ctx context.Context, distil bool) ([]ConsolidationPass, error) {
	var out []ConsolidationPass
	return out, m.c.do(ctx, http.MethodPost, m.base+"/consolidate", ConsolidateRequest{Distil: distil}, &out)
}
