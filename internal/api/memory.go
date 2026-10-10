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
	// Global is true for an AgentBox-wide memory, which every project reads
	// beside its own; its Project is "*".
	Global bool `json:"global,omitempty"`
	// Origin is where an AgentBox-wide memory was written: the project whose
	// chat or user wrote it, "_home" for the Home chat, empty for the user's
	// own from the app.
	Origin string `json:"origin,omitempty"`
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
	// Confirmations is how many times this memory was written down again and
	// the restatement merged into it. Search and agents' briefs weigh it.
	Confirmations int `json:"confirmations,omitempty"`
	// Promotion is where it stands as a candidate project note: "" until the
	// lead is offered it, then "offered", "promoted" (it is a note now, and
	// no brief serves it again) or "dismissed" (never offered again).
	Promotion   string    `json:"promotion,omitempty"`
	PromotionAt time.Time `json:"promotionAt,omitzero,omitempty"`
}

// MemoryFeedbackRequest is a reader's verdict on a memory it was handed:
// "wrong" and "stale" drop it to the lowest importance at once (stale also
// closes an open item), "helpful" raises it a point, up to 4.
type MemoryFeedbackRequest struct {
	// Memory is its id, or its title exactly as it was shown.
	Memory  string `json:"memory"`
	Verdict string `json:"verdict"`
	// Why is what the reader found, in a line; required unless helpful.
	Why string `json:"why,omitempty"`
}

// MemoryFeedbackResult is what feedback did to the memory.
type MemoryFeedbackResult struct {
	Memory Memory `json:"memory"` // as it is now
	// Was is its importance before.
	Was int `json:"was"`
	// Resolved is true when the feedback closed it.
	Resolved bool `json:"resolved,omitempty"`
}

// NoteSuggestion is a memory enough of a project's agents were handed in their
// briefs to be offered to its lead as a note.
type NoteSuggestion struct {
	Memory Memory `json:"memory"`
	// Agents is how many different agents it was served to.
	Agents int `json:"agents"`
}

// PromoteMemoryRequest makes a memory a project note.
type PromoteMemoryRequest struct {
	ID string `json:"id"`
	// Text is the note as every agent should read it; the memory's title and
	// content when empty.
	Text string `json:"text,omitempty"`
}

// PromoteMemoryResult is the memory, marked promoted, and the notes with it in.
type PromoteMemoryResult struct {
	Memory Memory `json:"memory"`
	Notes  Notes  `json:"notes"`
}

// DismissPromotionRequest keeps a memory a memory: it is never offered as a
// note again.
type DismissPromotionRequest struct {
	ID string `json:"id"`
}

// ResolveMemoryRequest closes a memory without replacing it.
type ResolveMemoryRequest struct {
	ID string `json:"id"`
	// Why is what closed it — the pull request that fixed it, the test that
	// was deleted, the fact that it stopped mattering.
	Why string `json:"why,omitempty"`
}

// TidyMemoryRequest cleans a project's open items in one go: every live
// issue (or item waiting on something) last mentioned before the cutoff is
// resolved as "tidied", and what is left is merged by topic. Before that,
// secrets are removed from every row stored before memory removed them on
// the way in. No model is asked.
type TidyMemoryRequest struct {
	// OlderThanHours is the cutoff; 0 is seven days.
	OlderThanHours int `json:"olderThanHours,omitempty"`
	// Apply writes the plan. Without it the answer is what would happen.
	Apply bool `json:"apply,omitempty"`
}

// TidyMemoryResult is what a tidy did, or would do.
type TidyMemoryResult struct {
	Applied bool `json:"applied"`
	// Resolved are the open items old enough to close, as they were.
	Resolved []Memory `json:"resolved"`
	// Merged are the duplicates folded into another item.
	Merged []MemoryMerge `json:"merged"`
	// Kept is how many open items are left.
	Kept int `json:"kept"`
	// Scrubbed is how many stored rows of each kind still held a secret.
	Scrubbed MemoryScrub `json:"scrubbed"`
}

// MemoryScrub counts the rows a tidy removed secrets from, or would.
type MemoryScrub struct {
	Events        int `json:"events"`
	Memories      int `json:"memories"`
	Reports       int `json:"reports"`
	Artifacts     int `json:"artifacts"`
	WorkingMemory int `json:"workingMemory"`
}

// MemoryMerge is one open item folded into another that says the same thing.
type MemoryMerge struct {
	Memory Memory  `json:"memory"`
	Into   Memory  `json:"into"`
	Score  float64 `json:"score"`
	Why    string  `json:"why"`
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
	// Scope is MemoryScopeProject (the default) or MemoryScopeAll, for a
	// memory every project reads: a preference the user asked to hold
	// everywhere. A supersedes names a memory of the same scope.
	Scope string `json:"scope,omitempty"`
}

// Memory scopes: where AddMemoryRequest writes.
const (
	MemoryScopeProject = "project"
	MemoryScopeAll     = "all"
)

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
