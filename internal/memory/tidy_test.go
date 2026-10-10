package memory_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/memory"
)

// The fixtures below are shaped like a real project's store, where 93 of 119
// issues were still open: the same problem written down three times under
// three titles, one title written twice, a list of pull requests awaiting
// merge restated every few days — and, beside them, issues that share words
// with those and are about something else.

var day = 24 * time.Hour

type fixture struct {
	key            string
	kind           string
	ago            time.Duration
	title, content string
}

var staleStore = []fixture{
	// One problem, three titles.
	{"names-restart", memory.KindIssue, 16 * day, "Agent names restart at agent-01 once every agent is gone",
		`After the last agent is retired, create_agent hands out agent-01 again, and a retire_agent("agent-01") meant for the old one destroys the new one.`},
	{"names-unfixed", memory.KindIssue, 15 * day, "Agent-name reuse bug unfixed",
		"create_agent reuses a freed agent-NN name as soon as the old agent is removed; retire_agent with the old name destroys the new agent. Needs a per-project counter."},
	{"names-reused", memory.KindIssue, 14 * day, "Agent names are reused as soon as an agent is gone; tell_agent can't wake a stopped machine",
		`2026-09-25: after merged agents were auto-removed (#23), the next create_agent reused the freed name (agent-02), and a retire_agent("agent-02") meant for the old one destroyed the just-created agent. tell_agent to a stopped agent fails instead of starting it.`},
	// One title, twice.
	{"model-help-1", memory.KindIssue, 12 * day, "Stale --model help text",
		"agentbox create --help still lists sonnet-4 and opus-4 as the model choices; the list is in internal/cli/agent.go."},
	{"model-help-2", memory.KindIssue, 5 * day, "Stale --model help text",
		"The --model flag's help names models that no longer exist: update the help string in internal/cli/agent.go."},
	// A list restated as it changed.
	{"awaiting-1", memory.KindIssue, 13 * day, "Open PRs awaiting the user's merge",
		"#98 GPU toggle skips the lead; #100 stale base / main sync; #101 desktop windows maximized; #102 Settings redesign."},
	{"awaiting-2", memory.KindIssue, 9 * day, "Open PRs awaiting the user's merge",
		"#186 SnapShots/cookie import, #187 env test, #193 queued model, #194 WSL pool, #195 media everywhere (CI unconfirmed at retire)."},
	{"awaiting-3", memory.KindProject, 1 * day, "PRs awaiting the user's merge",
		"#234 then #237 (stacked), #235, #238, #240 (then close #204), #241 then #242 (stacked)."},
	// Issues that share words with those, about other things.
	{"containers", memory.KindIssue, 10 * day, "Retired agents leave their containers RUNNING",
		"retire_agent(stop) sometimes leaves the incus container running; check incus list and stop ab-agentbox-agent-NNN by hand."},
	{"rail", memory.KindIssue, 3 * day, "Agent names are truncated in the rail",
		"Long agent titles overflow the sidebar rail; the row needs min-w-0 on its flex child."},
	{"flaky", memory.KindIssue, 8 * day, "Flaky TestTheLeadsSkillChangesWaitForTheUser",
		"The daemon test races syncSkills installs on stop; it fails about one run in ten on CI."},
	{"git-repo", memory.KindIssue, 2 * day, "Transient 'not a valid git repository' error",
		"Creating an agent sometimes fails with 'not a valid git repository' right after a project is added; retrying works."},
	{"windows", memory.KindIssue, 2 * day, "Windows skill import untested",
		"PR #237 translates Windows paths, but nobody has run it on real Windows yet."},
	// Facts, which are never merged or tidied however they read.
	{"fact-agents", memory.KindProject, 30 * day, "Agents are Incus containers",
		"Each agent is an Incus container with a git worktree on agentbox/<slug> and its own tmux terminal."},
	{"fact-names", memory.KindProject, 30 * day, "Agent names come from a per-project counter",
		"state.NextAgentName reserves agent-NN atomically, so a retired agent's name is never handed out again."},
	{"fact-merge", memory.KindDecision, 20 * day, "The user merges every PR",
		"Agents open pull requests; only the user merges them, after reading the description."},
}

// seed writes the fixtures, dated back from now, and answers them by key.
func seed(t *testing.T, s *memory.Store, now time.Time, fixtures []fixture) map[string]memory.Memory {
	t.Helper()
	out := map[string]memory.Memory{}
	for _, f := range fixtures {
		out[f.key] = add(t, s, memory.Memory{Kind: f.kind, Title: f.title, Content: f.content, CreatedAt: now.Add(-f.ago)})
	}
	return out
}

func keysOf(byKey map[string]memory.Memory, ids []string) []string {
	var out []string
	for _, id := range ids {
		for k, m := range byKey {
			if m.ID == id {
				out = append(out, k)
			}
		}
	}
	slices.Sort(out)
	return out
}

func TestSameTopicIssuesAreMergedByWhatTheySay(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Now()
	m := seed(t, s, now, staleStore)

	merges, err := s.MergeDuplicates(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, mg := range merges {
		got[keysOf(m, []string{mg.Memory.ID})[0]] = keysOf(m, []string{mg.Into.ID})[0]
		if mg.Why == "" {
			t.Errorf("a merge with no reason: %+v", mg)
		}
	}
	want := map[string]string{
		// The newest of each group stays.
		"names-restart": "names-reused", "names-unfixed": "names-reused",
		"model-help-1": "model-help-2",
		"awaiting-1":   "awaiting-3", "awaiting-2": "awaiting-3",
	}
	for k, into := range want {
		if got[k] != into {
			t.Errorf("%s merged into %q, want %q", k, got[k], into)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s was merged into %s, and is a different problem", k, got[k])
		}
	}

	// The merged ones are resolved and name what they were folded into; the
	// survivor is live and carries the highest importance of its group.
	gone, err := s.Memory(ctx, "pawly", m["names-unfixed"].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !gone.Resolved() || !strings.Contains(gone.ResolvedBy, m["names-reused"].ID) {
		t.Errorf("the duplicate should be resolved naming the survivor, got %+v", gone)
	}
	issues, err := s.Memories(ctx, "pawly", []string{memory.KindIssue})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 7 {
		t.Errorf("%d issues left live, want 7", len(issues))
	}

	// A second pass finds nothing more to do.
	again, err := s.MergeDuplicates(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Errorf("a second pass merged %d more", len(again))
	}
}

// The mechanical pass merges same-topic issues on its own, and counts them.
func TestMechanicalPassMergesSameTopicIssues(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	seed(t, s, time.Now(), staleStore)
	pass, err := s.Consolidate(ctx, "pawly", memory.ConsolidateOptions{DecayAfter: -1})
	if err != nil {
		t.Fatal(err)
	}
	if pass.MemoriesResolved != 5 {
		t.Errorf("the pass resolved %d duplicates, want 5", pass.MemoriesResolved)
	}
}

func TestAnchorsAreExtractedFromWhatAnOpenItemSays(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"#234 then #237 (stacked), #235, #240 (then close #204)",
			[]string{"pr:234", "pr:237", "pr:235", "pr:240", "pr:204"}},
		{"PR 236 merged; see https://github.com/leciric/agentbox/pull/241 too", []string{"pr:236", "pr:241"}},
		{"branch agentbox/fix-memory-stale-issues needs a rebase", []string{"branch:agentbox/fix-memory-stale-issues"}},
		{"agent-12's PR #12 on feat/one-account-per-project", []string{"pr:12", "branch:feat/one-account-per-project"}},
		{"agent-07 asks: which port? answer_question (id 1a2b3c4d)", []string{"question:1a2b3c4d"}},
		{"internal/agent/recap.go feeds them back via memory.BuildContext",
			[]string{"path:internal/agent/recap.go", "symbol:memory.BuildContext"}},
		// An import path is not a branch, and an agent's number is not a PR.
		{"agentbox/internal/memory is imported by agent-284", nil},
	}
	for _, c := range cases {
		var got []string
		for _, a := range memory.ExtractAnchors(c.text) {
			got = append(got, a.Kind+":"+a.Value)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("ExtractAnchors(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// Only open items are anchored: a fact that cites a pull request is not
// waiting on it.
func TestOnlyOpenItemsAreAnchored(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	fact := add(t, s, memory.Memory{Kind: memory.KindProject, Title: "Connectors are AgentBox-wide since #236"})
	waiting := add(t, s, memory.Memory{Kind: memory.KindProject, Title: "PRs awaiting the user's merge", Content: "#240, #241"})
	issue := add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "Reports tab crash", Content: "fixed by #238",
		Anchors: []memory.Anchor{{Kind: "PR", Value: "#300"}, {Kind: "nonsense", Value: "x"}}})
	for _, c := range []struct {
		m    memory.Memory
		want int
	}{{fact, 0}, {waiting, 2}, {issue, 2}} {
		got, err := s.Anchors(ctx, "pawly", c.m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != c.want {
			t.Errorf("%q has anchors %v, want %d", c.m.Title, got, c.want)
		}
	}
}

func TestAnchoredItemsCloseWhenEverythingTheyWaitOnIsOver(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Now()
	stacked := add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "PRs awaiting the user's merge",
		Content: "#234 then #237 (stacked)", CreatedAt: now.Add(-2 * day)})
	// Cites a pull request merged long before it was written: that one is
	// history, and only #250 is what it waits on.
	regression := add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "Search misses accents since #200",
		Content: "Fix is in #250.", CreatedAt: now.Add(-2 * day)})
	history := add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "Regression from #201",
		Content: "Nobody has looked at it.", CreatedAt: now.Add(-2 * day)})
	asked := add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "agent-90 is waiting on the user",
		Content: "Should the reminders page paginate? question 1a2b3c4d", CreatedAt: now.Add(-day)})
	add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "recap.go is too long", Content: "internal/agent/recap.go"})

	pr := func(n string) memory.Anchor { return memory.Anchor{Kind: memory.AnchorPR, Value: n} }
	closed := map[memory.Anchor]memory.Closed{
		pr("234"): {At: now.Add(-time.Hour), How: "merged"},
		pr("200"): {At: now.Add(-30 * day), How: "merged"},
		pr("201"): {At: now.Add(-30 * day), How: "merged"},
	}
	resolved, err := s.ResolveAnchored(ctx, "pawly", closed)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 0 {
		t.Fatalf("resolved %v with #237 and #250 still open", resolved)
	}

	closed[pr("237")] = memory.Closed{At: now, How: "merged"}
	closed[pr("250")] = memory.Closed{At: now, How: "closed"}
	closed[memory.Anchor{Kind: memory.AnchorQuestion, Value: "1a2b3c4d"}] = memory.Closed{At: now, How: "answered"}
	resolved, err = s.ResolveAnchored(ctx, "pawly", closed)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range resolved {
		ids = append(ids, m.ID)
	}
	slices.Sort(ids)
	want := []string{stacked.ID, regression.ID, asked.ID}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Errorf("resolved %v, want %v (and never the one only citing history, %s)", ids, want, history.ID)
	}
	got, err := s.Memory(ctx, "pawly", stacked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ResolvedBy != "auto: #234 merged, #237 merged" {
		t.Errorf("resolved_by = %q", got.ResolvedBy)
	}
}

// An open issue nobody has brought up in three weeks drops out of the lead's
// "Still open", and is still found by a search. One an agent's event named
// recently stays, however old it is.
func TestQuietIssuesDropOutOfTheRecap(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Now()
	add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "Stale --model help text",
		Content: "lists sonnet-4", CreatedAt: now.Add(-30 * day)})
	add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "Reports tab crashes on Windows",
		Content: "addFetchedLog gets undefined", CreatedAt: now.Add(-2 * day)})
	add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "PR #240 still conflicts",
		Content: "the fork can't be pushed to", CreatedAt: now.Add(-40 * day)})
	if err := s.MentionAnchors(ctx, "pawly", now.Add(-time.Hour),
		memory.Anchor{Kind: memory.AnchorPR, Value: "240"}); err != nil {
		t.Fatal(err)
	}

	built, err := s.BuildContext(ctx, memory.ContextRequest{Project: "pawly", For: memory.ForLead})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(built.Text, "--model help") {
		t.Errorf("a month-old issue nobody mentioned is still in the recap:\n%s", built.Text)
	}
	for _, want := range []string{"Reports tab crashes", "PR #240 still conflicts"} {
		if !strings.Contains(built.Text, want) {
			t.Errorf("the recap lost %q:\n%s", want, built.Text)
		}
	}
	found, err := s.Search(ctx, "pawly", "model help text", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Memories) == 0 {
		t.Error("a quiet issue should still be searchable")
	}
}

func TestTidyResolvesOldOpenItemsAndMergesTheRest(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Now()
	m := seed(t, s, now, staleStore)

	// A dry run says what it would do, and does nothing.
	plan, err := s.Tidy(ctx, "pawly", memory.TidyOptions{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	var old []string
	for _, r := range plan.Resolved {
		old = append(old, r.ID)
	}
	wantOld := []string{"awaiting-1", "awaiting-2", "containers", "flaky", "model-help-1",
		"names-restart", "names-reused", "names-unfixed"}
	if got := keysOf(m, old); !slices.Equal(got, wantOld) {
		t.Errorf("a dry run would resolve %v, want %v", got, wantOld)
	}
	if len(plan.Merged) != 0 {
		t.Errorf("nothing recent is a duplicate, and a dry run would merge %+v", plan.Merged)
	}
	if plan.Applied {
		t.Error("a dry run says it applied")
	}
	issues, _ := s.Memories(ctx, "pawly", []string{memory.KindIssue})
	if len(issues) != 12 {
		t.Fatalf("a dry run changed the store: %d issues live, want 12", len(issues))
	}

	// With a shorter cutoff, more goes; facts and decisions never do.
	plan, err = s.Tidy(ctx, "pawly", memory.TidyOptions{Now: now, OlderThan: 4 * day, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Resolved) != 9 {
		t.Errorf("resolved %d older than four days, want 9", len(plan.Resolved))
	}
	got, err := s.Memory(ctx, "pawly", m["flaky"].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ResolvedBy != memory.ResolvedByTidy {
		t.Errorf("resolved_by = %q, want %q", got.ResolvedBy, memory.ResolvedByTidy)
	}
	for _, k := range []string{"fact-agents", "fact-names", "fact-merge", "rail", "git-repo", "windows", "awaiting-3"} {
		got, err := s.Memory(ctx, "pawly", m[k].ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Live() {
			t.Errorf("%s was tidied away: %+v", k, got)
		}
	}
	if plan.Kept != 4 {
		t.Errorf("kept %d open items, want 4", plan.Kept)
	}
}

// Tidy merges what is left after the age cut.
func TestTidyMergesRecentDuplicates(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Now()
	m := seed(t, s, now, staleStore)
	plan, err := s.Tidy(ctx, "pawly", memory.TidyOptions{Now: now, OlderThan: 60 * day, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Resolved) != 0 || len(plan.Merged) != 5 {
		t.Errorf("resolved %d and merged %d, want 0 and 5", len(plan.Resolved), len(plan.Merged))
	}
	for _, mg := range plan.Merged {
		if mg.Memory.ID == m["rail"].ID || mg.Memory.ID == m["containers"].ID {
			t.Errorf("%q was merged into %q", mg.Memory.Title, mg.Into.Title)
		}
	}
}
