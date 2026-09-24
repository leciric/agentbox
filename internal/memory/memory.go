// Package memory is a project's brain: what happened, what was learned from
// it, what the project is doing right now, and what it produced.
//
// It is AgentBox's, not a model's ([D72](../../docs/implementation/decisions.md#d72)).
// Nothing here calls an LLM, embeds anything or talks to a vector database:
// the whole of it is SQLite tables in the state database, searched with FTS5,
// and every agent of every AI tool reaches the same rows through the same
// surfaces. A project is the scope — memory outlives the agent that wrote it,
// and never crosses into another project.
//
// Six things are kept, and they are deliberately not the same thing:
//
//   - Events are raw history, appended and never rewritten.
//   - Memories are what somebody decided is worth keeping, written on purpose.
//     A memory that another one supersedes stops coming back from search.
//   - Working memory is one small document per project: what it is doing now.
//   - Tasks are the project's state as a graph: what is to be done, who is on
//     it, what contains it and what it is waiting on
//     ([D77](../../docs/implementation/decisions.md#d77)). They are the one
//     thing here that is closed rather than superseded, because a task is the
//     work and a memory is the judgement that outlives it.
//   - Artifacts are references to what was produced — a path, a branch, a pull
//     request — never the thing itself.
//   - Reports are what an agent said when it finished, in a shape that can be
//     read back without asking a model to parse prose.
//
// The tables are migrated by package state, with every other table
// ([`migrations`](../state/state.go)); this package only queries them.
package memory

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a project has nothing under the id asked for,
// including an id that belongs to another project: memory is scoped, so from
// here the two are the same thing. The daemon's writeError turns it into a
// 404, beside state's own.
var ErrNotFound = errors.New("not found")

// Store reads and writes a project's memory. It takes the database package
// state opened, so memory lives beside the projects and agents it is about,
// in one file that backs up and migrates as a whole.
type Store struct {
	db *sql.DB
}

// New wraps an open state database. Pass state.Store.DB(): the schema is
// already migrated by the time that handle exists.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Memory kinds. Which one a memory is decides how long it is expected to
// matter, and that is the whole of the distinction.
const (
	// KindProject is a long-lived fact about the project itself:
	// architecture, a convention it keeps, a configuration value, a
	// constraint it works under. True until the project changes.
	KindProject = "project"
	// KindEpisodic is something that happened, kept because it explains
	// later things: a migration ran, a dependency was upgraded, a demo was
	// recorded.
	KindEpisodic = "episodic"
	// KindDecision is a choice and its reason, so the next agent doesn't
	// re-litigate it or quietly undo it.
	KindDecision = "decision"
	// KindDiscovery is something found out the hard way: a gotcha, a
	// non-obvious cause, a command that isn't in the README.
	KindDiscovery = "discovery"
	// KindIssue is a known problem nobody has fixed yet.
	KindIssue = "issue"
)

// Kinds are the memory kinds, in the order they are worth reading.
var Kinds = []string{KindProject, KindDecision, KindDiscovery, KindIssue, KindEpisodic}

// Report statuses: how an agent's task ended.
const (
	StatusDone    = "done"    // finished, nothing left
	StatusPartial = "partial" // some of it is done, the rest is in RemainingIssues
	StatusBlocked = "blocked" // stopped on something it couldn't get past
	StatusFailed  = "failed"  // it didn't work
)

// Statuses are the report statuses a report may carry.
var Statuses = []string{StatusDone, StatusPartial, StatusBlocked, StatusFailed}

// Importance bounds. 3 is ordinary: what a memory gets when nothing says
// otherwise. The number orders what a context builder keeps when it can't keep
// everything; it is not a search weight.
const (
	MinImportance     = 1
	MaxImportance     = 5
	DefaultImportance = 3
)

// EventConversationCompacted is the event type the daemon appends when it
// folds a chat's conversation into memory and carries it on in a fresh session
// (D73). Every memory that compaction writes names that event as its source,
// which is what LatestFrom finds the narrative by. It is a constant here, and
// not a string in the daemon, because the half that writes those rows and the
// half that reads them back are different packages and have to agree.
const EventConversationCompacted = "conversation_compacted"

// EventMemoryConsolidated is the event type a distillation appends when it
// folds a stretch of raw events into memories (D76). It holds the model's raw
// answer, and every memory the pass writes names it as its source. Like
// EventConversationCompacted it is a constant here because the half that
// writes those rows and the half that reads them back are different packages.
//
// A distillation never reads its own events: they are AgentBox's bookkeeping,
// not the project's history, and folding them back in would have every pass
// summarise the last one.
const EventMemoryConsolidated = "memory_consolidated"

// Field bounds, so one runaway tool call can't write a megabyte into a table
// everything else has to read past. They are generous: a memory is a
// paragraph, not a document.
const (
	MaxTitleLen   = 200
	MaxContentLen = 16 << 10
	MaxPayloadLen = 64 << 10
)

// Event is one thing that happened, exactly as it happened. Payload is
// whatever shape that kind of event has; nothing here interprets it.
type Event struct {
	ID      string          `json:"id"`
	Project string          `json:"project"`
	Agent   string          `json:"agent,omitempty"`   // empty for the project itself
	Session string          `json:"session,omitempty"` // the AI tool session it came from
	At      time.Time       `json:"at"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	// ArtifactID names the artifact this event is about, when it is about one.
	ArtifactID string `json:"artifactId,omitempty"`
}

// EventFilter narrows Events. A zero filter is every event of the project,
// newest first, up to DefaultLimit.
type EventFilter struct {
	Agent string    // one agent's events; empty is every agent's
	Types []string  // only these types; empty is every type
	Since time.Time // at or after this moment
	Limit int       // at most this many, newest first; 0 is DefaultLimit
}

// Memory is something worth keeping, written on purpose.
type Memory struct {
	ID         string    `json:"id"`
	Project    string    `json:"project"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Content    string    `json:"content"`
	Importance int       `json:"importance"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	// SupersedesID is the memory this one replaces, when it replaces one.
	SupersedesID string `json:"supersedesId,omitempty"`
	// SourceEventID is the event this was learned from, when it came from one.
	SourceEventID string `json:"sourceEventId,omitempty"`
	// Superseded is true when a later memory names this one. It is derived,
	// not stored: supersedes_id is the only record, so the two can't disagree.
	Superseded bool `json:"superseded,omitempty"`
	// ResolvedAt is when this memory was closed with nothing replacing it —
	// an issue that stopped being true (D76). A resolved memory is as gone
	// from listings and searches as a superseded one, and is still readable
	// by id. Unlike Superseded this is stored, because it is the only record
	// of its own fact: there is no reverse lookup it could disagree with.
	ResolvedAt time.Time `json:"resolvedAt,omitzero,omitempty"`
	// ResolvedBy is who or what closed it, and why, in a line.
	ResolvedBy string `json:"resolvedBy,omitempty"`
	// ReferencedAt is the last time something put this memory in front of a
	// model: a recap, a brief, a distillation's window. It is what keeps
	// importance decay off the memories that are actually being read.
	ReferencedAt time.Time `json:"referencedAt,omitzero,omitempty"`
	// DecayedAt is the last time the mechanical pass took a point off this
	// memory's importance, so a pass that runs hourly doesn't decay it hourly.
	DecayedAt time.Time `json:"decayedAt,omitzero,omitempty"`
}

// Resolved reports whether this memory was closed without a replacement.
func (m Memory) Resolved() bool { return !m.ResolvedAt.IsZero() }

// Live reports whether this memory still counts: nothing has superseded it
// and nobody has resolved it. It is what Memories and Search return.
func (m Memory) Live() bool { return !m.Superseded && !m.Resolved() }

// Artifact is a reference to something an agent produced, never its contents:
// a path in a worktree, a branch, a pull request, a media item. What it points
// at may be gone; the reference says what was made and by whom.
type Artifact struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Agent   string `json:"agent,omitempty"`
	// Type is what kind of thing it is: file, branch, pull_request, media,
	// url — a word, not a closed set, because what agents produce isn't one.
	Type      string          `json:"type"`
	Path      string          `json:"path"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

// Report is what an agent said when it finished: the same thing its final
// message says, in a shape that can be read back without a model.
type Report struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Agent     string    `json:"agent"`
	CreatedAt time.Time `json:"createdAt"`
	Task      string    `json:"task"`
	Status    string    `json:"status"`
	Summary   string    `json:"summary"`
	// Discoveries is what it found out that wasn't asked for.
	Discoveries []string `json:"discoveries,omitempty"`
	// Decisions is what it chose, and why.
	Decisions []string `json:"decisions,omitempty"`
	// RemainingIssues is what it knows is still wrong or unfinished. This is
	// the list that makes a "partial" or "blocked" report worth having.
	RemainingIssues []string `json:"remainingIssues,omitempty"`
	// Artifacts are artifact ids, or plain references for anything that
	// wasn't recorded as one.
	Artifacts []string `json:"artifacts,omitempty"`
}

// DefaultLimit is how many rows a listing or a search returns when the caller
// names no number. It is small on purpose: everything here is read into a
// model's context.
const DefaultLimit = 20

// MaxLimit caps what a caller may ask for.
const MaxLimit = 200

func limitOf(n int) int {
	switch {
	case n <= 0:
		return DefaultLimit
	case n > MaxLimit:
		return MaxLimit
	}
	return n
}

// newID is a short, readable id with a prefix that says what it names, so a
// memory id and an event id can't be mistaken for each other in a tool call.
func newID(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("reading random bytes: %v", err))
	}
	return prefix + "_" + hex.EncodeToString(b)
}

// millis stores a time; a zero time becomes 0, and reads back as a zero time.
func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// stamp is a time as it will read back: the tables keep milliseconds, so what
// a write returns is what a later read returns, to the same precision.
func stamp(t time.Time) time.Time { return attime(millis(t)) }

func attime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// text trims a field and refuses one that is longer than max.
func text(what, s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return "", fmt.Errorf("%s is %d bytes, longer than the %d allowed: write less, or put the rest in an artifact", what, len(s), max)
	}
	return s, nil
}

// requireProject is the one thing every call needs. A project-less row would
// be visible to every project, which is the boundary this package keeps.
func requireProject(project string) error {
	if strings.TrimSpace(project) == "" {
		return errors.New("a project is required: memory is scoped to one project")
	}
	return nil
}

// jsonDocument normalises a JSON column: empty becomes {}, and anything that
// isn't valid JSON is refused here rather than stored and failing on read.
func jsonDocument(what string, raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	if len(raw) > MaxPayloadLen {
		return "", fmt.Errorf("%s is %d bytes, longer than the %d allowed", what, len(raw), MaxPayloadLen)
	}
	if !json.Valid(raw) {
		return "", fmt.Errorf("%s isn't valid JSON", what)
	}
	return string(raw), nil
}

// jsonList stores a list of strings, dropping the empty ones so a model that
// sends ["", ""] doesn't fill a report with blanks.
func jsonList(items []string) (string, error) {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it = strings.TrimSpace(it); it != "" {
			out = append(out, it)
		}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func readList(raw string) []string {
	var out []string
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// oneOf checks a value against a closed set, and says what the set is.
func oneOf(what, value string, allowed []string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return fmt.Errorf("%s is %q: it is one of %s", what, value, strings.Join(allowed, ", "))
}
