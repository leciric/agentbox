package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// Is a cheaper model's distillation still worth having? (D78)
//
// PR #70 built a retrieval harness for search: a hand-labelled fixture corpus
// and a score that a change to ranking has to beat. The shape half fits here
// and the other half doesn't, and it is worth being plain about which.
//
// What fits: a fixture window of history, hand-labelled with what a pass over
// it *should* end up remembering, and a score for an answer against it —
// compression, coverage, and how much junk it left behind.
//
// What doesn't: the thing being scored. Search is a SQL query, so the harness
// can run the real one. A distillation's answer comes from a model, and
// running one in `go test` would mean a login, a network, a bill and a
// different answer every time — CI has none of those and shouldn't. So the
// answers here are recorded: four hand-written ones standing for the ways a
// model really answers this prompt, from a careful one to a model that
// invents memory ids, and what is scored is what AgentBox *does* with each.
//
// That makes this a harness for the half that matters when the model gets
// cheaper: the failure modes. A pass that can't be read must cost nothing, an
// answer that names memories that don't exist must not lose the judgement in
// it, and a lazy pass must be visible in the pass log and undoable by
// superseding, because those are what a user needs when they turn the cheap
// model on and want to know whether to turn it off again.
//
// Run `go test ./internal/daemon/ -run TestDistillationQuality -v` for the
// table. The subtests are named short on purpose: a test's name is in the
// temporary directory a test daemon's unix socket lives under, and 107 bytes
// is all a unix socket path has.

// qualityFixture is the window a pass reads, and the ids an answer needs to
// name to be right about it.
type qualityFixture struct {
	stale  string // a memory this stretch of history makes wrong
	fixed  string // an open issue this stretch of history closes
	known  string // something the project already remembers, correctly
	events int
}

// seedQualityWindow writes what a fortnight on a small project looks like:
// three memories it already has, and a run of events that between them make
// one of those wrong, close another, and establish two new things.
func seedQualityWindow(t *testing.T, d testDaemon) qualityFixture {
	t.Helper()
	ctx := context.Background()
	store := d.srv.memory()
	var f qualityFixture

	add := func(kind, title, content string) string {
		t.Helper()
		m, err := store.AddMemory(ctx, memory.Memory{
			Project: "hello-stack", Kind: kind, Title: title, Content: content, Importance: 3,
		})
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	f.stale = add(memory.KindProject, "The API listens on 7777",
		"internal/api/server.go binds :7777.")
	f.fixed = add(memory.KindIssue, "The count query is unindexed",
		"The dashboard's count query does a table scan on every load.")
	f.known = add(memory.KindProject, "Every agent branches from main",
		"Agents are created detached from main; nothing branches from a branch.")

	at := time.Now().Add(-14 * 24 * time.Hour)
	event := func(kind, agent string, payload map[string]any) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendEvent(ctx, memory.Event{
			Project: "hello-stack", Agent: agent, Type: kind, At: at, Payload: raw,
		}); err != nil {
			t.Fatal(err)
		}
		at = at.Add(20 * time.Minute)
		f.events++
	}
	// The port move, said three times the way history says everything: once
	// as work, once as a merge, once as a question.
	event("agent_created", "agent-02", map[string]any{"task": "Move the API off 7777 onto 8080"})
	event("agent_finished", "agent-02", map[string]any{"status": "done", "summary": "internal/api/server.go now binds :8080; the compose file and the README follow it."})
	event("pr_merged", "agent-02", map[string]any{"number": 78, "title": "Serve the API on 8080"})
	event("question_asked", "agent-03", map[string]any{"question": "Is anything still hard-coding 7777?"})
	event("question_answered", "agent-03", map[string]any{"answer": "No. 8080 everywhere since #78."})
	// The index, which closes the open issue.
	event("agent_created", "agent-04", map[string]any{"task": "Index the dashboard count query"})
	event("agent_finished", "agent-04", map[string]any{"status": "done", "summary": "Added an index on events(project, created_at); the count query is 40ms now, down from 3s."})
	event("pr_merged", "agent-04", map[string]any{"number": 81, "title": "Index the dashboard count query"})
	// Something new nobody knew: the seed script.
	event("agent_created", "agent-05", map[string]any{"task": "Find out why the dashboard is slow in development"})
	event("agent_finished", "agent-05", map[string]any{"status": "partial", "summary": "The seed script writes 10,000 rows, which is why development looks slow; production has 200."})
	// And a fortnight of noise, which is most of what a window is.
	for i := range 15 {
		event("artifact_created", "agent-05", map[string]any{"name": fmt.Sprintf("screenshot-%d", i), "kind": "screenshot"})
	}
	return f
}

// qualityCandidate is one recorded answer and what should become of it.
type qualityCandidate struct {
	name string
	// what this answer stands for: which model behaviour it is a recording of.
	stands string
	answer func(f qualityFixture) string

	wantPass bool // the pass succeeds and the watermark moves
	// maxWritten bounds the new memories a pass of this kind may write. It is
	// the compression the prompt asks for, as a number.
	maxWritten int
	// covers are terms at least one live memory must mention afterwards: the
	// things this window genuinely established.
	covers                       []string
	wantSuperseded, wantResolved int
}

func qualityCandidates() []qualityCandidate {
	return []qualityCandidate{{
		name:   "careful",
		stands: "a model that reads what the project already knows before writing anything",
		answer: func(f qualityFixture) string {
			return `{
			  "memories": [
			    {"kind": "discovery", "title": "The seed script writes 10,000 rows",
			     "detail": "Which is why the dashboard looks slow in development; production has 200.", "importance": 3}
			  ],
			  "supersede": [
			    {"replaces": "` + f.stale + `", "kind": "project", "title": "The API listens on 8080",
			     "detail": "Moved off 7777 in #78; the compose file and the README follow it.", "importance": 4}
			  ],
			  "resolve": [
			    {"id": "` + f.fixed + `", "why": "Indexed in #81; the count query is 40ms."}
			  ]
			}`
		},
		wantPass: true, maxWritten: 2, covers: []string{"seed script", "8080"},
		wantSuperseded: 1, wantResolved: 1,
	}, {
		name:   "lazy",
		stands: "a model that summarises each event instead of the stretch, which is the cheap model's likeliest failure",
		answer: func(qualityFixture) string {
			var items []string
			for i := range 12 {
				items = append(items, fmt.Sprintf(
					`{"kind": "episodic", "title": "Screenshot %d was produced", "detail": "agent-05 saved a screenshot.", "importance": 2}`, i))
			}
			return `{"memories": [` + strings.Join(items, ",") + `], "supersede": [], "resolve": []}`
		},
		// Nothing refuses it: it is a valid answer, and the point of the
		// harness is what the user can see and undo afterwards.
		wantPass: true, maxWritten: 12,
	}, {
		name:   "ids",
		stands: "a model that gets the judgement right and the bookkeeping wrong",
		answer: func(qualityFixture) string {
			return `{
			  "memories": [
			    {"kind": "fact", "title": "The seed script writes 10,000 rows", "detail": "Development only.", "importance": 9},
			    {"kind": "project", "title": "   ", "detail": "A memory with nothing to call it."}
			  ],
			  "supersede": [
			    {"replaces": "mem_neverexisted", "kind": "project", "title": "The API listens on 8080",
			     "detail": "Moved off 7777 in #78.", "importance": 4}
			  ],
			  "resolve": [
			    {"id": "mem_alsoneverexisted", "why": "I think this one is done."}
			  ]
			}`
		},
		wantPass: true, maxWritten: 2, covers: []string{"seed script", "8080"},
	}, {
		name:     "prose",
		stands:   "a model that answers the question instead of the shape, which a smaller one does more often",
		answer:   func(qualityFixture) string { return "Sure! Here's what I think this project should remember: …" },
		wantPass: false,
	}, {
		name:   "cut",
		stands: "an answer cut off mid-object, which is what a context that ran out looks like",
		answer: func(qualityFixture) string {
			return `{"memories": [{"kind": "discovery", "title": "The seed script writes 10,0`
		},
		wantPass: false,
	}}
}

// TestDistillationQuality scores each recorded answer
// over the same fixture window and logs the table, then holds each to what it
// should have left behind.
func TestDistillationQuality(t *testing.T) {
	t.Log("candidate          pass  read  written  superseded  resolved  covered  memories/event")
	for _, c := range qualityCandidates() {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			d, lead := consolidationProject(t)
			lead.AI = "claude"
			if _, err := d.client.SetConsolidation(ctx, "hello-stack", 20); err != nil {
				t.Fatal(err)
			}
			f := seedQualityWindow(t, d)
			store := d.srv.memory()

			answer := c.answer(f)
			d.srv.askAside = func(context.Context, state.Agent, string, string) (string, string, error) {
				return answer, "claude-haiku-4-5", nil
			}
			d.srv.askLead = func(context.Context, state.Agent, string) (string, error) {
				t.Error("the chat's own session was asked: the aside answered")
				return "", nil
			}

			ran := d.srv.distillIfDue(ctx, lead)
			pass := lastPass(t, d)
			live, err := store.Memories(ctx, "hello-stack", nil)
			if err != nil {
				t.Fatal(err)
			}
			covered := 0
			for _, term := range c.covers {
				if mentions(live, term) {
					covered++
				}
			}
			t.Logf("%-18s %-5v %5d %8d %11d %9d %8s %15.2f", c.name, ran, pass.EventsRead,
				pass.MemoriesWritten, pass.MemoriesSuperseded, pass.MemoriesResolved,
				fmt.Sprintf("%d/%d", covered, len(c.covers)),
				ratio(pass.MemoriesWritten, pass.EventsRead))

			if ran != c.wantPass {
				t.Fatalf("the pass %v, want %v (%s)", ran, c.wantPass, c.stands)
			}
			if !c.wantPass {
				// An answer nobody can read costs the project nothing: the
				// window is still there, and the next pass reads it again.
				if len(live) != 3 {
					t.Errorf("%d live memories, want the 3 the project started with: an unreadable answer wrote something", len(live))
				}
				if got, err := store.Watermark(ctx, "hello-stack"); err != nil || !got.IsZero() {
					t.Errorf("Watermark() = %v, %v; want it unmoved", got, err)
				}
				if pass.Error == "" {
					t.Error("the failed pass recorded no reason")
				}
				if pass.Model != "claude-haiku-4-5" {
					t.Errorf("the failed pass says it ran on %q: a failure is worth knowing the model of", pass.Model)
				}
				return
			}
			if got, err := store.Watermark(ctx, "hello-stack"); err != nil || got.IsZero() {
				t.Errorf("Watermark() = %v, %v; want a successful pass to move it", got, err)
			}
			if pass.MemoriesWritten > c.maxWritten {
				t.Errorf("%d memories written, want at most %d", pass.MemoriesWritten, c.maxWritten)
			}
			if pass.MemoriesSuperseded != c.wantSuperseded {
				t.Errorf("%d memories superseded, want %d", pass.MemoriesSuperseded, c.wantSuperseded)
			}
			if pass.MemoriesResolved != c.wantResolved {
				t.Errorf("%d memories resolved, want %d", pass.MemoriesResolved, c.wantResolved)
			}
			for _, term := range c.covers {
				if !mentions(live, term) {
					t.Errorf("nothing the project remembers mentions %q", term)
				}
			}
			// The fixture's third memory is already right, and no answer here
			// has anything to say about it: writing down what the project
			// already remembers is how a store doubles in size and every
			// future agent pays for it twice.
			if n := titled(live, "Every agent branches from main"); n != 1 {
				t.Errorf("%d live memories say what the project already knew, want the 1 it started with", n)
			}
			if _, err := store.Memory(ctx, "hello-stack", f.known); err != nil {
				t.Errorf("the memory the project already had: %v", err)
			}
			// Whatever a pass wrote, a person can put it right: every memory
			// it left is live, addressable and replaceable through the same
			// door anything else writes through. That is the undo.
			for _, m := range live {
				if m.SourceEventID == "" {
					continue // one of the three the project started with
				}
				if _, err := store.AddMemory(ctx, memory.Memory{
					Project: "hello-stack", Kind: m.Kind, Title: "Superseding " + m.Title,
					Content: "Put right by hand.", SupersedesID: m.ID,
				}); err != nil {
					t.Errorf("superseding %q, which this pass wrote: %v", m.Title, err)
				}
			}
		})
	}
}

// What each candidate leaves behind, in detail: the parts that are about
// AgentBox containing a model rather than about the model.
func TestDistillationContainsAWeakAnswer(t *testing.T) {
	ctx := context.Background()
	d, lead := consolidationProject(t)
	lead.AI = "claude"
	if _, err := d.client.SetConsolidation(ctx, "hello-stack", 20); err != nil {
		t.Fatal(err)
	}
	f := seedQualityWindow(t, d)
	store := d.srv.memory()

	var invented qualityCandidate
	for _, c := range qualityCandidates() {
		if c.name == "ids" {
			invented = c
		}
	}
	answer := invented.answer(f)
	d.srv.askAside = func(context.Context, state.Agent, string, string) (string, string, error) {
		return answer, "claude-haiku-4-5", nil
	}
	if !d.srv.distillIfDue(ctx, lead) {
		t.Fatal("an answer with invented ids in it lost a pass that was otherwise fine")
	}
	live, err := store.Memories(ctx, "hello-stack", nil)
	if err != nil {
		t.Fatal(err)
	}

	// The replacement named a memory that doesn't exist. What it said is kept
	// as a new memory — the judgement was right, only the bookkeeping wasn't —
	// and nothing is recorded as superseded, because nothing was.
	if !mentions(live, "8080") {
		t.Error("the replacement was dropped because it named an id that doesn't exist")
	}
	if pass := lastPass(t, d); pass.MemoriesSuperseded != 0 {
		t.Errorf("%d memories superseded by an id that names nothing", pass.MemoriesSuperseded)
	}
	// And the memory it claimed to replace is untouched: a model can't
	// retire a memory by guessing at its id.
	if _, err := store.Memory(ctx, "hello-stack", f.stale); err != nil {
		t.Errorf("the memory it claimed to replace: %v", err)
	}
	if !mentions(live, "listens on 7777") {
		t.Error("the stale memory stopped being live, on an answer that never named it")
	}

	for _, m := range live {
		if m.SourceEventID == "" {
			continue
		}
		// A kind nobody recognises is not a refusal: the fact is still worth
		// keeping, and an unlabelled fact is a project fact.
		if m.Kind == "fact" {
			t.Errorf("%q was written with the kind the model invented", m.Title)
		}
		// Importance is clamped rather than refused, so a 9 doesn't outrank
		// everything the project really depends on.
		if m.Importance > memory.MaxImportance {
			t.Errorf("%q has importance %d, over the maximum", m.Title, m.Importance)
		}
		if strings.TrimSpace(m.Title) == "" {
			t.Error("a memory with no title was written")
		}
	}
}

// titled counts the live memories with exactly this title, which is how a
// pass that rewrites what is already known shows up as a number.
func titled(live []memory.Memory, title string) int {
	n := 0
	for _, m := range live {
		if strings.EqualFold(strings.TrimSpace(m.Title), title) {
			n++
		}
	}
	return n
}

// mentions reports whether any live memory says term, in its title or its body.
func mentions(live []memory.Memory, term string) bool {
	for _, m := range live {
		if strings.Contains(strings.ToLower(m.Title+" "+m.Content), strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}
