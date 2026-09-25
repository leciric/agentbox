package api

import (
	"encoding/json"
	"time"
)

// The project memory API: what a project remembers, over and above the
// conversation any one agent is having (D72).
// The same shapes serve three surfaces — the user's routes under
// /v1/projects/{project}/memory, a project chat's under /v1/project/memory,
// and an agent's own under /v1/self/memory — because they are the same memory,
// reached from three places that are allowed to do different amounts to it.
//
// Nothing here is a model's format. The daemon stores these rows itself, and
// they read back the same whichever AI tool wrote them.

// MemoryEvent is one thing that happened: raw history, appended and never
// rewritten.
type MemoryEvent struct {
	ID      string    `json:"id"`
	Project string    `json:"project"`
	Agent   string    `json:"agent,omitempty"`
	Session string    `json:"session,omitempty"`
	At      time.Time `json:"at"`
	Type    string    `json:"type"`
	// Payload is whatever shape that kind of event has. AgentBox stores it and
	// searches its text; nothing interprets it.
	Payload    json.RawMessage `json:"payload,omitempty"`
	ArtifactID string          `json:"artifactId,omitempty"`
}

// AddMemoryEventRequest appends one event. The project comes from the route,
// and on an agent's own routes so does the agent.
type AddMemoryEventRequest struct {
	Type       string          `json:"type"`
	Agent      string          `json:"agent,omitempty"`
	Session    string          `json:"session,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	ArtifactID string          `json:"artifactId,omitempty"`
	At         *time.Time      `json:"at,omitempty"` // when it happened; now by default
}

// Memory is something worth keeping, written on purpose rather than captured.
type Memory struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	// Kind is project, episodic, decision, discovery or issue.
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Content    string    `json:"content"`
	Importance int       `json:"importance"` // 1 to 5
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	// SupersedesID is the memory this one replaces, when it replaces one.
	SupersedesID string `json:"supersedesId,omitempty"`
	// SourceEventID is the event it was learned from, when it came from one.
	SourceEventID string `json:"sourceEventId,omitempty"`
	// Superseded is true when a later memory replaced this one. A superseded
	// memory is still readable by id, and never comes back from a search.
	Superseded bool `json:"superseded,omitempty"`
	// ResolvedAt is when this memory was closed with nothing put in its
	// place: an issue that stopped being true (D76). Like a superseded
	// memory it is still readable by id and never comes back from a search.
	ResolvedAt time.Time `json:"resolvedAt,omitzero,omitempty"`
	// ResolvedBy is what closed it, in a line.
	ResolvedBy string `json:"resolvedBy,omitempty"`
	// ReferencedAt is the last time something put this memory in front of a
	// model. It is what keeps importance decay off what is being read.
	ReferencedAt time.Time `json:"referencedAt,omitzero,omitempty"`
	// DecayedAt is the last time a consolidation took a point off this
	// memory's importance.
	DecayedAt time.Time `json:"decayedAt,omitzero,omitempty"`
}

// ResolveMemoryRequest closes a memory without replacing it.
type ResolveMemoryRequest struct {
	ID string `json:"id"`
	// Why is what closed it — the pull request that fixed it, the test that
	// was deleted, the fact that it stopped mattering.
	Why string `json:"why,omitempty"`
}

// ConsolidateRequest runs a project's consolidation now, whatever its
// schedule would have decided.
type ConsolidateRequest struct {
	// Distil also asks the project's chat to turn the events since the
	// watermark into memories. It spends the project's tokens, so it is off
	// unless asked for; the mechanical pass runs either way.
	Distil bool `json:"distil,omitempty"`
}

// ConsolidationPass is what one consolidation did: what it read, what it
// wrote, what it cost (D76).
type ConsolidationPass struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	// Kind is "mechanical" (no model, and no cost) or "distill".
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`
	// DurationMS is how long it took. For a distillation, mostly thinking.
	DurationMS int64 `json:"durationMs"`

	EventsRead         int `json:"eventsRead"`
	MemoriesWritten    int `json:"memoriesWritten"`
	MemoriesSuperseded int `json:"memoriesSuperseded"`
	MemoriesResolved   int `json:"memoriesResolved"`
	MemoriesDecayed    int `json:"memoriesDecayed"`
	DuplicatesFound    int `json:"duplicatesFound"`

	// InputBytes and OutputBytes are what a distillation sent and what came
	// back: AgentBox never sees a bill, so the size of the prompt and the
	// answer is the honest measure of what a pass cost. Both are 0 for a
	// mechanical pass.
	InputBytes  int `json:"inputBytes"`
	OutputBytes int `json:"outputBytes"`
	// Model is what a distillation really ran on, which is not always what
	// the project asked for: a cheap model the account won't run falls back
	// to the chat's own session rather than losing the pass (D78). Empty for
	// a mechanical pass, and for a distillation on the chat's own model.
	Model string `json:"model,omitempty"`

	// ThroughEventID and ThroughAt are how far a distillation read. The
	// newest successful one is the project's watermark.
	ThroughEventID string    `json:"throughEventId,omitempty"`
	ThroughAt      time.Time `json:"throughAt,omitzero,omitempty"`
	// Error is why the pass gave up, when it did.
	Error string `json:"error,omitempty"`
}

// MemoryConsolidation is a project's consolidation as a whole: what is
// configured, what has been done, and the two numbers a compression ratio is
// made of — how much raw history there is, and how few memories it became.
type MemoryConsolidation struct {
	Project string `json:"project"`
	// Setting is how many new events the project gathers before its chat is
	// asked to distil them; 0 means consolidation is switched off.
	Setting int `json:"setting"`

	Events     int `json:"events"`     // every event the project has recorded
	Memories   int `json:"memories"`   // live memories now
	Superseded int `json:"superseded"` // memories a later one replaced
	Resolved   int `json:"resolved"`   // memories closed with no replacement
	Duplicates int `json:"duplicates"` // near-duplicate pairs waiting on a decision
	Pending    int `json:"pending"`    // events since the watermark, not yet distilled

	Passes             int `json:"passes"`
	EventsRead         int `json:"eventsRead"`
	MemoriesWritten    int `json:"memoriesWritten"`
	MemoriesSuperseded int `json:"memoriesSuperseded"`
	MemoriesResolved   int `json:"memoriesResolved"`
	MemoriesDecayed    int `json:"memoriesDecayed"`
	InputBytes         int `json:"inputBytes"`
	OutputBytes        int `json:"outputBytes"`

	LastPass  time.Time `json:"lastPass,omitzero,omitempty"`
	Watermark time.Time `json:"watermark,omitzero,omitempty"`
	// Recent are the last few passes, newest first.
	Recent []ConsolidationPass `json:"recent,omitempty"`
}

// MemoryDuplicate is one pair of live memories of a kind whose titles say
// close to the same thing. Nothing is merged on this: it is a pair somebody,
// or a distillation, might decide about.
type MemoryDuplicate struct {
	Memory Memory `json:"memory"` // the newer of the two
	Of     Memory `json:"of"`
	// Similarity is the normalised word overlap of the two titles, 0 to 1.
	Similarity float64   `json:"similarity"`
	FoundAt    time.Time `json:"foundAt"`
}

// The consolidation pass kinds.
const (
	ConsolidationMechanical = "mechanical"
	ConsolidationDistill    = "distill"
)

// AddMemoryRequest writes one down.
type AddMemoryRequest struct {
	Kind          string `json:"kind,omitempty"` // project by default
	Title         string `json:"title"`
	Content       string `json:"content,omitempty"`
	Importance    int    `json:"importance,omitempty"` // 3 by default
	SupersedesID  string `json:"supersedesId,omitempty"`
	SourceEventID string `json:"sourceEventId,omitempty"`
}

// MemorySearchRequest looks through a project's memories, events and reports.
type MemorySearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"` // per kind of result; 20 by default
}

// MemorySearchResults are the three kinds of match, each ranked on its own.
// They are separate lists because a relevance score only compares within one
// index, and because what a caller wants from a memory and from an event is
// not the same thing.
type MemorySearchResults struct {
	Memories []Memory      `json:"memories,omitempty"`
	Events   []MemoryEvent `json:"events,omitempty"`
	Reports  []AgentReport `json:"reports,omitempty"`
}

// WorkingMemory is what a project is doing right now: one small document,
// kept current rather than appended to.
type WorkingMemory struct {
	Goal         string    `json:"goal,omitempty"`
	CurrentTask  string    `json:"currentTask,omitempty"`
	ActiveAgents []string  `json:"activeAgents,omitempty"`
	Blockers     []string  `json:"blockers,omitempty"`
	Notes        string    `json:"notes,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt,omitzero,omitempty"`
}

// WorkingMemoryPatch changes what it names and leaves the rest alone. An empty
// string or an empty list clears its field.
type WorkingMemoryPatch struct {
	Goal         *string   `json:"goal,omitempty"`
	CurrentTask  *string   `json:"currentTask,omitempty"`
	ActiveAgents *[]string `json:"activeAgents,omitempty"`
	Blockers     *[]string `json:"blockers,omitempty"`
	Notes        *string   `json:"notes,omitempty"`
}

// MemoryArtifact is a reference to something an agent produced — never its
// contents. A file stays in its worktree, a recording stays in media, a pull
// request stays on GitHub.
type MemoryArtifact struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Agent   string `json:"agent,omitempty"`
	// Type is what kind of thing it is: file, branch, pull_request, media, url.
	Type      string          `json:"type"`
	Path      string          `json:"path"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

// AddArtifactRequest records one.
type AddArtifactRequest struct {
	Type     string          `json:"type"`
	Path     string          `json:"path"`
	Agent    string          `json:"agent,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// AgentReport is what an agent said when it finished, in a shape that can be
// read back without asking a model to parse prose.
type AgentReport struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Agent     string    `json:"agent"`
	CreatedAt time.Time `json:"createdAt"`
	Task      string    `json:"task,omitempty"`
	// Status is done, partial, blocked or failed.
	Status          string   `json:"status"`
	Summary         string   `json:"summary"`
	Discoveries     []string `json:"discoveries,omitempty"`
	Decisions       []string `json:"decisions,omitempty"`
	RemainingIssues []string `json:"remainingIssues,omitempty"`
	Artifacts       []string `json:"artifacts,omitempty"`
}

// AddReportRequest files one. On an agent's own routes the agent is the one
// behind the socket, and the field is ignored.
type AddReportRequest struct {
	Agent           string   `json:"agent,omitempty"`
	Task            string   `json:"task,omitempty"`
	Status          string   `json:"status,omitempty"` // done by default
	Summary         string   `json:"summary"`
	Discoveries     []string `json:"discoveries,omitempty"`
	Decisions       []string `json:"decisions,omitempty"`
	RemainingIssues []string `json:"remainingIssues,omitempty"`
	Artifacts       []string `json:"artifacts,omitempty"`
}

// The memory kinds, as the API names them.
const (
	MemoryKindProject   = "project"
	MemoryKindEpisodic  = "episodic"
	MemoryKindDecision  = "decision"
	MemoryKindDiscovery = "discovery"
	MemoryKindIssue     = "issue"
)

// The report statuses.
const (
	ReportDone    = "done"
	ReportPartial = "partial"
	ReportBlocked = "blocked"
	ReportFailed  = "failed"
)

// Project state as a graph (D77):
// the tasks a project has, who is on each, and what each is waiting on. It is
// beside memory rather than inside it — a memory is a judgement that outlives
// the work, a task is the work — and it is what answers "what is blocked on
// what", which working memory's list of agent names cannot.

// Task is one piece of work a project has.
type Task struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	// Agent is the agent on it, when one is.
	Agent string `json:"agent,omitempty"`
	// ParentID is the task this one is part of, when it is part of one.
	ParentID string `json:"parentId,omitempty"`
	// Status is open, active, blocked, done or abandoned.
	Status string `json:"status"`
	// Goal is what is to be done, in a line; Detail is everything else.
	Goal      string    `json:"goal"`
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// ClosedAt is when it stopped being open, and absent while it still is.
	ClosedAt time.Time `json:"closedAt,omitzero,omitempty"`
	// DependsOn are the tasks this one is waiting on, and Blocks the ones
	// waiting on it. Neither is a column: they are the blocking edges, read
	// both ways.
	DependsOn []string `json:"dependsOn,omitempty"`
	Blocks    []string `json:"blocks,omitempty"`
}

// AddTaskRequest writes a task down. The project comes from the route.
type AddTaskRequest struct {
	Goal     string `json:"goal"`
	Detail   string `json:"detail,omitempty"`
	Agent    string `json:"agent,omitempty"`
	ParentID string `json:"parentId,omitempty"`
	Status   string `json:"status,omitempty"` // open by default
	// DependsOn are the tasks it is blocked on from the start, linked as it
	// is created. A cycle is refused, and the task is still written down.
	DependsOn []string `json:"dependsOn,omitempty"`
}

// UpdateTaskRequest changes what it names and leaves the rest. On an agent's
// own routes only Status and Detail are allowed, and only on its own task:
// restructuring the plan needs every agent in view.
type UpdateTaskRequest struct {
	Status   *string `json:"status,omitempty"`
	Goal     *string `json:"goal,omitempty"`
	Detail   *string `json:"detail,omitempty"`
	Agent    *string `json:"agent,omitempty"`
	ParentID *string `json:"parentId,omitempty"`
}

// LinkTasksRequest says one task is waiting on another, or takes that back.
type LinkTasksRequest struct {
	TaskID      string `json:"taskId"`
	DependsOnID string `json:"dependsOnId"`
}

// The task statuses.
const (
	TaskOpen      = "open"
	TaskActive    = "active"
	TaskBlocked   = "blocked"
	TaskDone      = "done"
	TaskAbandoned = "abandoned"
)

// The context builder's surface (D75).
// POST /context is the stable way to ask for what a project knows, bounded to
// a budget, so the app, a brief and any future tool all get the same slice —
// and so Claude Code, Codex and OpenCode agents are given the same thing
// rather than three approximations of it.

// ContextRequest asks for a context built from a project's memory.
type ContextRequest struct {
	// Query is what the consumer is about to do, as words: a task, a question,
	// a filename. Empty falls back to what the project says it is working on.
	Query string `json:"query,omitempty"`
	// Budget is how many estimated tokens the context may cost. 0 is the
	// project's own setting; anything out of bounds is clamped, not refused.
	Budget int `json:"budget,omitempty"`
	// For is who is reading it: "lead", "agent" or "tool". It changes one
	// heading's wording and how the build is accounted for, nothing else.
	For string `json:"for,omitempty"`
}

// ContextResult is one built context: the markdown a model reads, what went
// into it, and what it cost.
type ContextResult struct {
	Text     string           `json:"text"`
	Sections []ContextSection `json:"sections,omitempty"`
	Stats    ContextStats     `json:"stats"`
}

// ContextSection is one part of a built context. The text itself is in
// ContextResult.Text: this is the accounting of what is in it.
type ContextSection struct {
	// Kind is working, story, open, knowledge, events, reports or artifacts.
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Rows   int    `json:"rows"`
	Tokens int    `json:"tokens"`
}

// ContextStats is what one build cost and what it left out.
type ContextStats struct {
	Project string    `json:"project"`
	For     string    `json:"for"`
	At      time.Time `json:"at"`
	Query   string    `json:"query,omitempty"`
	Budget  int       `json:"budget"`
	Tokens  int       `json:"tokens"`
	// Rows is what it kept, ConsideredRows what it weighed, DroppedRows the
	// difference.
	Rows            int      `json:"rows"`
	ConsideredRows  int      `json:"consideredRows"`
	DroppedRows     int      `json:"droppedRows"`
	DroppedSections []string `json:"droppedSections,omitempty"`
	// Truncated is true when even what a build never drops overflowed the
	// budget and the text was cut.
	Truncated bool `json:"truncated,omitempty"`
	// CorpusTokens is everything the project remembers, estimated the same
	// way, and Ratio is Tokens against it: 0.04 is a context a twenty-fifth
	// the size of the memory it came from.
	CorpusTokens int     `json:"corpusTokens"`
	Ratio        float64 `json:"ratio"`
}

// ContextAccount is what a project's context builds have cost since the daemon
// started. The counters are in the process, not in a table: they are about
// AgentBox's own behaviour rather than something the project remembers.
type ContextAccount struct {
	Builds      int `json:"builds"`
	Tokens      int `json:"tokens"`
	DroppedRows int `json:"droppedRows"`
	// Recent are the last builds, newest first.
	Recent []ContextStats `json:"recent,omitempty"`
}

// The audiences a context can be built for.
const (
	ContextForLead  = "lead"
	ContextForAgent = "agent"
	ContextForTool  = "tool"
)
