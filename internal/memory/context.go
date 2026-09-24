package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The context builder ([D75](../../docs/implementation/decisions.md#d75)).
//
// Everything AgentBox gives a model that isn't the conversation itself is a
// context: the recap a compacted chat starts its next session with (D73), the
// "What the project knows" section of a worker's brief, and whatever asks
// POST /context for one. They were going to be three hand-rolled assemblies of
// the same six queries, each with its own idea of what to keep. This is the
// one builder they all go through.
//
// It lives in package memory, rather than in an internal/context of its own,
// because the whole of it is queries over these tables and a budget: a package
// called context would also shadow the standard library's at every import site
// that needs both, which is all of them.
//
// What it does is bounded on purpose. A project's memory grows without limit
// and a model's context does not, so a build is given a number of tokens it
// may spend and gives things up until it fits, in the order the architecture
// asks for: working memory and the project's state always, then the knowledge
// that matches the query, then what happened, then what agents reported, then
// what they produced.

// Audience is who a context is being built for. Only one heading's wording and
// the accounting depend on it: the slice itself is the same memory, because a
// worker and a chat asking "what does this project know about OAuth" want the
// same answer.
type Audience string

const (
	// ForLead is a project's chat, which reads its context in place of a
	// conversation it can no longer see.
	ForLead Audience = "lead"
	// ForAgent is a worker agent, which reads it in its brief before it
	// starts, alongside its task.
	ForAgent Audience = "agent"
	// ForTool is anything else that asked for one over POST /context.
	ForTool Audience = "tool"
)

// Token budgets. A budget is estimated tokens (see EstimateTokens), not words
// and not bytes, because tokens are what a context window is measured in and
// what a build is trying not to waste.
const (
	// DefaultBudgetTokens is what a project spends on one context when it
	// hasn't said otherwise. It is the cap D73 set for the lead's recap —
	// 3,000 words, "roughly 4,000 tokens" — in the unit it was always
	// reasoning in. A fresh session has to fit the brief, the project's
	// notes and the turn itself beside this.
	DefaultBudgetTokens = 4000
	// MinBudgetTokens is the smallest budget worth setting. Below it a
	// context is working memory and a cut sentence, which is worse than
	// having none: it reads like the project's memory is broken.
	MinBudgetTokens = 500
	// MaxBudgetTokens is the largest. Past it a context stops being a summary
	// of what the project knows and becomes a dump of it, which is what
	// search_memory is for.
	MaxBudgetTokens = 32000
)

// How much of each kind a build weighs before the budget gets a say. These
// bound the queries, so a project with ten thousand memories costs the same to
// build a context for as one with fifty; the budget then decides how much of
// what they returned survives.
const (
	contextTasks      = 10 // open tasks: the plan, not the project's whole history of work
	contextIssues     = 8  // open issues: what is in the way, not every bug ever filed
	contextKnowledge  = 8  // high-importance project and decision memories
	contextSearchHits = 5  // per kind, for the query
	contextReports    = 5  // the newest reports, the way project_state shows them
	contextArtifacts  = 5  // the newest artifacts
)

// HighImportance is the importance a memory needs before it is worth putting
// in front of every consumer whether or not they searched for it. 4 and 5 are
// "the next agent will get this wrong without it"; 3 is ordinary, and ordinary
// belongs in a search result rather than in everybody's context.
const HighImportance = 4

// Section kinds, which are also the order a context is rendered in and — read
// backwards — the order it gives things up in.
const (
	SectionWorking   = "working"   // what the project is doing right now
	SectionTasks     = "tasks"     // the plan: open and blocked tasks, and their edges
	SectionStory     = "story"     // the newest narrative of the project's chat
	SectionOpen      = "open"      // the open issues
	SectionKnowledge = "knowledge" // what the project knows about the query
	SectionEvents    = "events"    // what happened, matching the query
	SectionReports   = "reports"   // what agents said as they finished
	SectionArtifacts = "artifacts" // what they produced
)

// sectionOrder is the order a context is rendered in, which is also its
// priority: when the budget won't hold everything, a build gives up the tail —
// artifacts first, then reports, then past events, then the knowledge that
// matched the query. What survives is always a prefix of this.
//
// The first keptSections of it are never given up for something below them:
// they are what the project is doing and where it stands, and a context
// without them is a list of facts nobody can place. "Never given up" is not
// "always fits" — when they overflow the budget on their own the rendered text
// is cut, and Stats.Truncated says so.
var sectionOrder = []string{
	SectionWorking, SectionTasks, SectionStory, SectionOpen,
	SectionKnowledge, SectionEvents, SectionReports, SectionArtifacts,
}

// keptSections is how many of that order are never given up: what the project
// is doing, the plan it is doing it under, the story of its chat, and what is
// in the way. D77 made it four rather than three — the task graph is project
// state, and the architecture puts state ahead of retrieved knowledge, not
// after it. All four are bounded queries, so the floor a build can't drop
// below is a fixed size rather than a growing one.
const keptSections = 4

// ContextRequest asks for a bounded context.
type ContextRequest struct {
	Project string
	// Query is what the consumer is about to do, as words: a task, a
	// question, a filename. It seeds the search, and nothing else. Empty
	// falls back to working memory's goal and current task, which is what the
	// lead's recap has always searched for.
	Query string
	// Budget is the estimated tokens the whole context may cost. 0 is
	// DefaultBudgetTokens; anything outside the bounds is clamped rather than
	// refused, because a caller that asked for too much wants a context, not
	// an error.
	Budget int
	// For is who is reading it. ForTool when unset.
	For Audience
}

// ContextSection is one part of a built context, and what it cost.
type ContextSection struct {
	Kind string `json:"kind"`
	// Title is the section's heading, rendered in bold; Aside follows it in
	// brackets, outside the bold.
	Title string `json:"title"`
	Aside string `json:"aside,omitempty"`
	Body  string `json:"body"`
	// Rows is how many rows of memory went into it: one per memory, event,
	// report or artifact, and one per line of working memory.
	Rows   int `json:"rows"`
	Tokens int `json:"tokens"`
	// Memories are the ids of the memory rows this section is made of, when
	// it is made of memories. They are neither rendered nor serialised: they
	// are what BuildContext marks as referenced once the budget has decided
	// which sections survive ([D76](../../docs/implementation/decisions.md#d76)),
	// so importance decay leaves alone what is actually being read.
	Memories []string `json:"-"`
}

func (s ContextSection) render() string {
	head := "**" + s.Title + "**"
	if s.Aside != "" {
		head += " (" + s.Aside + ")"
	}
	return head + "\n\n" + strings.TrimRight(s.Body, "\n")
}

// ContextStats is what one build cost and what it gave up. A few counters,
// kept so the app can show whether a project's memory is paying for itself —
// not a metrics framework.
type ContextStats struct {
	Project string    `json:"project"`
	For     Audience  `json:"for"`
	At      time.Time `json:"at"`
	Query   string    `json:"query,omitempty"`
	// Budget is the tokens the build was allowed, Tokens what it spent.
	Budget int `json:"budget"`
	Tokens int `json:"tokens"`
	// Rows is how many rows of memory it kept, Considered how many it
	// retrieved and weighed, Dropped the difference.
	Rows       int `json:"rows"`
	Considered int `json:"consideredRows"`
	Dropped    int `json:"droppedRows"`
	// DroppedSections are the kinds it gave up whole, in the order it gave
	// them up.
	DroppedSections []string `json:"droppedSections,omitempty"`
	// Truncated is true when even the sections it never drops overflowed the
	// budget, and the rendered text was cut to fit.
	Truncated bool `json:"truncated,omitempty"`
	// CorpusTokens is the whole of what this project remembers, estimated the
	// same way: every live memory, every event and every report.
	CorpusTokens int `json:"corpusTokens"`
	// Ratio is Tokens against CorpusTokens — the compression the build got.
	// 0.04 means the context is a twenty-fifth of everything the project
	// remembers. It is 0 for a project that remembers nothing.
	Ratio float64 `json:"ratio"`
}

// Context is one built context: the markdown a model reads, what went into it,
// and what it cost.
type Context struct {
	// Text is the rendered markdown, and the only thing a model sees. It is
	// built once, because a truncated context's text is shorter than its
	// sections say: Sections is the accounting of what went in, Text is what
	// came out.
	Text     string
	Sections []ContextSection
	Stats    ContextStats
	// Referenced are the ids of the memory rows the budget kept, gathered
	// from the sections that survived. A memory that was weighed and dropped
	// isn't in it: nothing was spent on it, so nothing has read it (D76).
	Referenced []string `json:"-"`
}

// Empty reports whether the project had nothing to say. A consumer renders no
// section at all for an empty context rather than an empty heading, which is
// every project before anything has been remembered.
func (c Context) Empty() bool { return strings.TrimSpace(c.Text) == "" }

// EstimateTokens is how many tokens a piece of text costs, near enough.
//
// It is an estimate and nothing else: AgentBox counts tokens without a
// tokenizer and without asking a model, because both would mean a dependency
// or a network call on the path that writes a brief. Four bytes to a token is
// the documented rule of thumb for English prose, and that is all this is.
//
// Where it is wrong: code, file paths, identifiers and JSON punctuation
// tokenize denser than prose, so this undercounts them — a payload of
// {"agent":"agent-04"} is more tokens than its bytes suggest. Text outside the
// Latin scripts is denser again, by a lot. It is counted in bytes rather than
// runes for exactly that reason: a multi-byte rune really does cost more than
// an ASCII one. Treat a budget as the right order of magnitude, not as a
// guarantee a provider would agree with.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + bytesPerToken - 1) / bytesPerToken
}

const bytesPerToken = 4

// BudgetTokens clamps what a caller asked for into the bounds, and turns 0 —
// "nobody said" — into the default.
func BudgetTokens(n int) int {
	switch {
	case n <= 0:
		return DefaultBudgetTokens
	case n < MinBudgetTokens:
		return MinBudgetTokens
	case n > MaxBudgetTokens:
		return MaxBudgetTokens
	}
	return n
}

// AgentBudget is a worker agent's share of its project's budget. A worker's
// brief already carries its machine, the project's notes and its task, and the
// agent has search_memory to go deeper the moment it needs to; the lead's
// context replaces a conversation it cannot get back. So the worker gets a
// quarter, and is told plainly that it is a summary.
func AgentBudget(projectBudget int) int {
	return BudgetTokens(BudgetTokens(projectBudget) / agentBudgetShare)
}

const agentBudgetShare = 4

// BuildContext assembles what a consumer should know before it starts, out of
// what the project remembers, inside a budget.
//
// A project that remembers nothing builds an empty context rather than an
// error: that is every project before its first memory, and the consumer
// renders no section for it.
func (s *Store) BuildContext(ctx context.Context, req ContextRequest) (Context, error) {
	if err := requireProject(req.Project); err != nil {
		return Context{}, err
	}
	if req.For == "" {
		req.For = ForTool
	}
	budget := BudgetTokens(req.Budget)

	working, err := s.WorkingMemory(ctx, req.Project)
	if err != nil {
		return Context{}, err
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		query = strings.TrimSpace(working.Goal + " " + working.CurrentTask)
	}
	sections, err := s.contextSections(ctx, req, working, query)
	if err != nil {
		return Context{}, err
	}
	corpus, err := s.corpusTokens(ctx, req.Project)
	if err != nil {
		return Context{}, err
	}

	built := fit(sections, budget)
	built.Stats.Project, built.Stats.For, built.Stats.Query = req.Project, req.For, query
	built.Stats.At = time.Now()
	built.Stats.Budget, built.Stats.CorpusTokens = budget, corpus
	if corpus > 0 {
		built.Stats.Ratio = float64(built.Stats.Tokens) / float64(corpus)
	}
	recordContextBuild(built.Stats)
	// Every memory the budget kept is about to be spent as some model's
	// context, which is what "referenced" means: it is what keeps importance
	// decay off the memories a project is actually reading (D76). A build is
	// worth more than its bookkeeping, so a failure here doesn't fail it.
	_ = s.MarkReferenced(ctx, req.Project, built.Referenced...)
	return built, nil
}

// contextSections gathers every section a build could keep, in render order.
// Each one is bounded by its own query, so what this returns is already small;
// fit then decides how much of it the budget holds.
func (s *Store) contextSections(ctx context.Context, req ContextRequest, working WorkingMemory, query string) ([]ContextSection, error) {
	var out []ContextSection
	add := func(kind, title, aside, body string, rows int, memories ...string) {
		if body = strings.TrimSpace(body); body == "" {
			return
		}
		section := ContextSection{Kind: kind, Title: title, Aside: aside, Body: body, Rows: rows, Memories: memories}
		section.Tokens = EstimateTokens(section.render())
		out = append(out, section)
	}

	// What it is doing now, which everything else is read against.
	lines, rows := workingLines(working)
	add(SectionWorking, "What this project is doing", "", lines, rows)

	// The plan: what is open, what is being worked on, and what is waiting on
	// what (D77). It sits directly under working memory because it is the
	// same question answered in a shape that has edges in it — "what is
	// blocked on what" is the thing prose can't say — and above everything
	// retrieved, because a context is read to decide what to do next.
	tasks, err := s.Tasks(ctx, req.Project, TaskFilter{OpenOnly: true, Limit: contextTasks})
	if err != nil {
		return nil, err
	}
	add(SectionTasks, "The plan", "", taskLines(tasks), len(tasks))

	// The newest narrative of the project's chat: the one memory that is a
	// story rather than a fact, superseded by each compaction so there is
	// only ever one (D73).
	story, err := s.LatestFrom(ctx, req.Project, KindEpisodic, EventConversationCompacted)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	seen := map[string]bool{story.ID: true}
	if text := strings.TrimSpace(story.Content); text != "" {
		add(SectionStory, storyTitle(req.For), story.CreatedAt.Format("2 January 2006"), text, 1, story.ID)
	}

	// What is in the way. Memories orders by importance, so a cut list keeps
	// the issues that matter rather than the newest ones.
	issues, err := s.Memories(ctx, req.Project, []string{KindIssue})
	if err != nil {
		return nil, err
	}
	issues = firstN(issues, contextIssues)
	for _, it := range issues {
		seen[it.ID] = true
	}
	add(SectionOpen, "Still open", "", memoryLines(issues), len(issues), MemoryIDs(issues)...)

	// What the project knows: the facts and decisions it can't be worked on
	// without, then whatever the query found, minus anything already said
	// above. A memory repeated twice is context spent twice.
	standing, err := s.Memories(ctx, req.Project, []string{KindProject, KindDecision})
	if err != nil {
		return nil, err
	}
	knowledge := make([]Memory, 0, contextKnowledge+contextSearchHits)
	for _, m := range standing {
		if len(knowledge) == contextKnowledge {
			break
		}
		if m.Importance >= HighImportance && !seen[m.ID] {
			knowledge, seen[m.ID] = append(knowledge, m), true
		}
	}

	// The search is the only part of a build that depends on what the
	// consumer is about to do. A search that fails costs the context its
	// query-shaped sections and nothing else: what the project is doing and
	// what it has decided is worth having on its own.
	found := s.contextSearch(ctx, req.Project, query)
	for _, m := range found.Memories {
		if !seen[m.ID] {
			knowledge, seen[m.ID] = append(knowledge, m), true
		}
	}
	add(SectionKnowledge, "What the project knows about it", "", memoryLines(knowledge), len(knowledge), MemoryIDs(knowledge)...)

	events := firstN(found.Events, contextSearchHits)
	add(SectionEvents, "What happened", "", eventLines(events), len(events))

	// What agents said as they finished: the newest, then anything the query
	// turned up that wasn't in them.
	reports, err := s.Reports(ctx, req.Project, "")
	if err != nil {
		return nil, err
	}
	reports = firstN(reports, contextReports)
	for _, r := range reports {
		seen[r.ID] = true
	}
	for _, r := range found.Reports {
		if !seen[r.ID] {
			reports, seen[r.ID] = append(reports, r), true
		}
	}
	add(SectionReports, "What agents reported", "", reportLines(reports), len(reports))

	// What agents produced is for an agent that may need to find it; the
	// lead never acts on a list of screenshots, and its recap is re-sent on
	// every call it makes (D90).
	if req.For != ForLead {
		artifacts, err := s.Artifacts(ctx, req.Project)
		if err != nil {
			return nil, err
		}
		artifacts = firstN(artifacts, contextArtifacts)
		add(SectionArtifacts, "What was produced", "", artifactLines(artifacts), len(artifacts))
	}

	return out, nil
}

// storyTitle is the one piece of wording that depends on who is reading. To a
// project's chat the narrative is its own conversation, and saying so is what
// makes a rolled-over session carry on instead of starting again; to anybody
// else it is the chat's, and calling it "the conversation" would leave an
// agent wondering which one it missed.
func storyTitle(who Audience) string {
	if who == ForLead {
		return "The conversation so far"
	}
	return "Where this project's chat had got to"
}

// contextSearch is what the project remembers about what the consumer is about
// to do. Nothing is searched for a consumer that said nothing and a project
// that isn't working on anything, and a search that fails returns nothing
// rather than failing the build.
func (s *Store) contextSearch(ctx context.Context, project, query string) Results {
	if query == "" {
		return Results{}
	}
	results, err := s.Search(ctx, project, query, contextSearchHits)
	if err != nil {
		return Results{}
	}
	return results
}

// fit spends the budget down the render order, which is also the priority
// order: a section that doesn't fit is given up, and so is everything below
// it, because everything below it is worth less. The sections that are never
// given up come first, and when those alone overflow, the text they render is
// cut on a word boundary and told to search for the rest.
func fit(sections []ContextSection, budget int) Context {
	var out Context
	out.Stats.Budget = budget
	for _, s := range sections {
		out.Stats.Considered += s.Rows
	}

	spent, dropping := 0, false
	for _, s := range sections {
		// Once something has been given up, everything below it goes too:
		// the order is the priority, so keeping a later section would mean
		// keeping the cheaper thing over the dearer one.
		if !neverDropped(s.Kind) && (dropping || spent+s.Tokens+separatorTokens > budget) {
			dropping = true
			out.Stats.DroppedSections = append(out.Stats.DroppedSections, s.Kind)
			continue
		}
		out.Sections = append(out.Sections, s)
		out.Referenced = append(out.Referenced, s.Memories...)
		out.Stats.Rows += s.Rows
		spent += s.Tokens + separatorTokens
	}
	out.Stats.Dropped = out.Stats.Considered - out.Stats.Rows

	parts := make([]string, 0, len(out.Sections))
	for _, s := range out.Sections {
		parts = append(parts, s.render())
	}
	out.Text = strings.TrimSpace(strings.Join(parts, "\n\n"))
	if EstimateTokens(out.Text) > budget {
		out.Text, out.Stats.Truncated = capTokens(out.Text, budget), true
	}
	out.Stats.Tokens = EstimateTokens(out.Text)
	return out
}

// separatorTokens is the blank line between two sections, counted so a
// context of many small sections doesn't quietly overrun its budget by the
// glue between them.
const separatorTokens = 1

func neverDropped(kind string) bool {
	for _, k := range sectionOrder[:keptSections] {
		if k == kind {
			return true
		}
	}
	return false
}

// capTokens cuts a context that has outgrown its budget, and says that it did:
// one that stops mid-sentence with no explanation reads like the memory itself
// is truncated rather than the slice of it.
func capTokens(text string, max int) string {
	// The note has to fit inside the budget too, so the cut leaves room for
	// it. A budget too small to hold the note is a budget too small to hold
	// anything, and MinBudgetTokens is what keeps that from happening.
	whole := EstimateTokens(text)
	allowance := max - EstimateTokens(cutNote(whole, whole))
	if allowance < 1 {
		allowance = 1
	}
	cut := cutAt(text, allowance*bytesPerToken)
	if cut < 0 {
		return text
	}
	return strings.TrimSpace(text[:cut]) + cutNote(EstimateTokens(text[:cut]), whole)
}

func cutNote(kept, whole int) string {
	return fmt.Sprintf("\n\n(Cut to fit: about %d of the %d tokens this project remembers about it are above. "+
		"The rest is in its memory — search it rather than asking for it again.)", kept, whole)
}

// cutAt is the byte offset to cut at: the end of the last whole word that fits
// in max bytes, or -1 when the text already does. It cuts the text itself
// rather than rebuilding it from its words, because a context is markdown and
// its line breaks are what make it readable.
func cutAt(text string, max int) int {
	if len(text) <= max {
		return -1
	}
	cut, boundary := -1, 0
	for i, r := range text {
		if i > max {
			break
		}
		boundary = i
		if unicode.IsSpace(r) {
			cut = i
		}
	}
	if cut <= 0 {
		// One long word, or text with no spaces at all: cut at the last rune
		// that fits rather than at the byte, so what comes out is still text.
		return boundary
	}
	return cut
}

func workingLines(w WorkingMemory) (string, int) {
	var lines []string
	add := func(label, value string) {
		if value = strings.TrimSpace(value); value != "" {
			lines = append(lines, "- "+label+": "+value)
		}
	}
	add("Goal", w.Goal)
	add("Working on", w.CurrentTask)
	add("Agents on it", strings.Join(w.ActiveAgents, ", "))
	add("In the way", strings.Join(w.Blockers, "; "))
	add("Notes", w.Notes)
	return strings.Join(lines, "\n"), len(lines)
}

// taskLines renders the plan as a list a model can act on: what it is, who is
// on it, and what it is waiting on, by id. The ids are in it because the next
// thing a reader does with a task is name it — to report on it, to link it,
// to close it — and a plan whose rows can't be named is a plan nobody can
// change.
func taskLines(tasks []Task) string {
	goals := make(map[string]string, len(tasks))
	for _, t := range tasks {
		goals[t.ID] = t.Goal
	}
	var lines []string
	for _, t := range tasks {
		line := "- **" + t.Goal + "** (" + t.Status
		if t.Agent != "" {
			line += ", " + t.Agent
		}
		line += ", " + t.ID + ")"
		if len(t.DependsOn) > 0 {
			line += " — waiting on " + strings.Join(taskNames(t.DependsOn, goals), "; ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// taskNames is what a blocking edge points at, by goal when that task is in
// the same slice and by id when it isn't: a closed task still blocks nothing,
// but a task left out by the bound is still worth naming.
func taskNames(ids []string, goals map[string]string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if goal := goals[id]; goal != "" {
			out = append(out, excerpt(goal, MaxTitleLen)+" ("+id+")")
			continue
		}
		out = append(out, id)
	}
	return out
}

func memoryLines(items []Memory) string {
	var lines []string
	for _, it := range items {
		line := "- **" + it.Title + "**"
		if content := strings.TrimSpace(it.Content); content != "" {
			line += " — " + strings.Join(strings.Fields(content), " ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// eventLines renders raw history for a reader rather than for a parser. The
// payload is whatever shape that kind of event has, so what goes in is a short
// excerpt of its text with the JSON punctuation taken out: enough to recognise
// which event it was, not enough for one noisy payload to spend the budget.
func eventLines(events []Event) string {
	var lines []string
	for _, e := range events {
		line := "- **" + e.Type + "**"
		if e.Agent != "" {
			line += " (" + e.Agent + ")"
		}
		if !e.At.IsZero() {
			line += ", " + e.At.Format("2 January 2006")
		}
		if excerpt := excerpt(payloadWords(e.Payload), maxEventExcerpt); excerpt != "" {
			line += " — " + excerpt
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func reportLines(reports []Report) string {
	var lines []string
	for _, r := range reports {
		line := "- **" + r.Agent + "** (" + r.Status
		if !r.CreatedAt.IsZero() {
			line += ", " + r.CreatedAt.Format("2 January 2006")
		}
		line += ")"
		if task := strings.TrimSpace(r.Task); task != "" {
			line += " " + excerpt(task, MaxTitleLen)
		}
		if summary := excerpt(r.Summary, maxReportExcerpt); summary != "" {
			line += " — " + summary
		}
		// What a report says is still wrong is the part of it the next agent
		// most needs, and the part a summary is likeliest to leave out.
		if len(r.RemainingIssues) > 0 {
			line += " Still wrong: " + excerpt(strings.Join(r.RemainingIssues, "; "), maxReportExcerpt) + "."
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func artifactLines(artifacts []Artifact) string {
	var lines []string
	for _, a := range artifacts {
		line := "- **" + a.Type + "** " + a.Path
		if a.Agent != "" {
			line += " (" + a.Agent + ")"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// A report's summary and an event's payload are bounded fields, not one-liners:
// 16 KiB and 64 KiB. These are how much of one is worth a line in a context.
const (
	maxReportExcerpt = 300
	maxEventExcerpt  = 160
)

// payloadWords is an event's payload as text: its values, with the braces,
// quotes and commas dropped. The keys go too — "title", "branch" and "how" say
// nothing a reader doesn't already know from the event's type.
func payloadWords(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var doc map[string]any
	if json.Unmarshal(payload, &doc) != nil {
		return strings.Join(strings.Fields(string(payload)), " ")
	}
	var parts []string
	for _, key := range sortedKeys(doc) {
		switch v := doc[key].(type) {
		case string:
			if v = strings.TrimSpace(v); v != "" {
				parts = append(parts, v)
			}
		case nil, bool:
		default:
			parts = append(parts, fmt.Sprint(v))
		}
	}
	return strings.Join(strings.Fields(strings.Join(parts, " · ")), " ")
}

// sortedKeys keeps an event rendering the same way twice: a map's order
// doesn't, and a context that changes between two builds of the same memory is
// one nobody can diff.
func sortedKeys(doc map[string]any) []string {
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// excerpt is the first max bytes of a field, on a word boundary, with its
// whitespace collapsed so a multi-line summary is one line in a list.
func excerpt(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	cut := cutAt(s, max)
	if cut < 0 {
		return s
	}
	return strings.TrimSpace(s[:cut]) + "…"
}

func firstN[T any](items []T, n int) []T {
	if len(items) > n {
		return items[:n]
	}
	return items
}

// corpusTokens is everything this project remembers, estimated the same way a
// context is: what a consumer would have had to read if nothing chose for it.
// It is the denominator of the compression ratio, and it is three SUMs over
// lengths rather than three listings — the rows are never loaded.
func (s *Store) corpusTokens(ctx context.Context, project string) (int, error) {
	var bytes int64
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT IFNULL(SUM(LENGTH(m.title) + LENGTH(m.content)), 0) FROM memories m
		  WHERE m.project = ? AND `+notSuperseded+`) +
		(SELECT IFNULL(SUM(LENGTH(type) + LENGTH(payload)), 0) FROM events WHERE project = ?) +
		(SELECT IFNULL(SUM(LENGTH(task) + LENGTH(summary) + LENGTH(discoveries) + LENGTH(decisions)
			+ LENGTH(remaining_issues)), 0) FROM agent_reports WHERE project = ?)`,
		project, project, project).Scan(&bytes)
	if err != nil {
		return 0, err
	}
	return int(bytes+bytesPerToken-1) / bytesPerToken, nil
}

// The accounting. Every build records what it cost here, so the memory routes
// can answer "is this paying for itself" without a build having to be asked
// for again to find out.
//
// It is in the process and not in a table on purpose: these are counters about
// AgentBox's own behaviour, not something the project remembers, and writing
// them to the events table would make every build grow the corpus the next one
// measures itself against. They start empty when the daemon does. It is
// package-level because a Store is a thin wrapper made fresh per call site
// over one database handle — memory.New(db) in the daemon's every request and
// in the brief writer — and the counters have to outlive those.
var builds struct {
	sync.Mutex
	byProject map[string]*projectBuilds
}

type projectBuilds struct {
	recent  []ContextStats
	builds  int
	tokens  int
	dropped int
}

// contextStatsKept is how many recent builds are remembered per project.
const contextStatsKept = 20

// ContextAccount is what a project's context builds have cost so far, since
// the daemon started.
type ContextAccount struct {
	// Builds is how many contexts were built, Tokens what they spent between
	// them and DroppedRows how many rows of memory they left out to fit.
	Builds      int `json:"builds"`
	Tokens      int `json:"tokens"`
	DroppedRows int `json:"droppedRows"`
	// Recent are the last builds, newest first.
	Recent []ContextStats `json:"recent,omitempty"`
}

func recordContextBuild(st ContextStats) {
	builds.Lock()
	defer builds.Unlock()
	if builds.byProject == nil {
		builds.byProject = map[string]*projectBuilds{}
	}
	p := builds.byProject[st.Project]
	if p == nil {
		p = &projectBuilds{}
		builds.byProject[st.Project] = p
	}
	p.builds++
	p.tokens += st.Tokens
	p.dropped += st.Dropped
	p.recent = append([]ContextStats{st}, firstN(p.recent, contextStatsKept-1)...)
}

// ContextBuilds is the accounting for one project.
func ContextBuilds(project string) ContextAccount {
	builds.Lock()
	defer builds.Unlock()
	p := builds.byProject[project]
	if p == nil {
		return ContextAccount{}
	}
	return ContextAccount{
		Builds: p.builds, Tokens: p.tokens, DroppedRows: p.dropped,
		Recent: append([]ContextStats(nil), p.recent...),
	}
}
