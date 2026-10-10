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

// ev is one event of a test window, a minute after the one before it.
func ev(at time.Time, i int, typ, agent string, payload map[string]any) memory.Event {
	raw, _ := json.Marshal(payload)
	return memory.Event{
		ID: fmt.Sprintf("evt_%04d", i), Project: "hello-stack", Agent: agent, Type: typ,
		At: at.Add(time.Duration(i) * time.Minute), Payload: raw,
	}
}

// history is the event part of a prompt, the part the filter shapes.
func history(ask string) string {
	_, after, _ := strings.Cut(ask, "## What has happened since the last pass\n\n")
	before, _, _ := strings.Cut(after, "\n"+distillShape)
	return before
}

func TestDistillFilterDropsEmptyPayloadFields(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	ask, _ := distillAsk([]memory.Event{
		ev(at, 0, "agent_finished", "agent-04", map[string]any{
			"title": "Index the count query", "branch": "agentbox/index-count", "files": 3, "dirty": false,
			"summary": "", "reportId": "rep_123", "pr": map[string]any{"number": 81, "url": "https://github.com/o/r/pull/81", "state": "open"},
		}),
		ev(at, 1, "artifact_created", "agent-04", map[string]any{"artifactId": "art_9", "kind": "screenshot", "name": "count"}),
	}, nil)
	got := history(ask)
	for _, gone := range []string{"dirty", "summary", "reportId", "rep_123", "url", "art_9"} {
		if strings.Contains(got, gone) {
			t.Errorf("the prompt still has %q:\n%s", gone, got)
		}
	}
	for _, kept := range []string{`"number":81`, `"files":3`, "agentbox/index-count", `"name":"count"`} {
		if !strings.Contains(got, kept) {
			t.Errorf("the prompt lost %q:\n%s", kept, got)
		}
	}
}

func TestDistillFilterDropsLeadTurnsThatSayNothing(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	turn := func(i int, msg, reply string) memory.Event {
		return ev(at, i, "lead_turn", "", map[string]any{"userMessage": msg, "assistantReply": reply})
	}
	ask, shown := distillAsk([]memory.Event{
		turn(0, "thanks", "You're welcome!"),
		turn(1, "(no message: background work it had left running ended: go test ./... exited 0)", "Still waiting."),
		turn(2, "ok ok ok ok ok ok ok ok ok ok ok ok", "ok ok ok ok ok ok ok ok ok ok ok ok ok"),
		turn(3, "go with B", "Going with B: agent-290 rewrites the pass log so a dropped event is visible."),
		turn(4, "merge #249", "Done."),
		turn(5, "status?", "Waiting on #252."),
	}, nil)
	got := history(ask)
	if shown != 6 {
		t.Errorf("the prompt covers %d events, want all 6: dropped ones count as read", shown)
	}
	for _, gone := range []string{"welcome", "Still waiting", "ok ok"} {
		if strings.Contains(got, gone) {
			t.Errorf("an empty turn is still in the prompt (%q):\n%s", gone, got)
		}
	}
	for _, kept := range []string{"Going with B", "merge #249", "Waiting on #252"} {
		if !strings.Contains(got, kept) {
			t.Errorf("a turn that says something was dropped (%q):\n%s", kept, got)
		}
	}
}

func TestDistillFilterFoldsRepeats(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	var events []memory.Event
	for i := range 4 {
		events = append(events, ev(at, i, "agent_stalled", "agent-03", map[string]any{
			"quiet_minutes": 10 + 5*i, "turn_minutes": 30 + 5*i, "tool_running": "Bash",
		}))
	}
	events = append(events,
		ev(at, 4, "pr_broken", "agent-03", map[string]any{"number": 78, "problems": []string{"ci"}, "head": "abc"}),
		ev(at, 5, "pr_broken", "agent-03", map[string]any{"number": 78, "problems": []string{"ci"}, "head": "def"}),
		ev(at, 6, "pr_broken", "agent-03", map[string]any{"number": 81, "problems": []string{"ci"}, "head": "123"}),
		ev(at, 7, "artifact_created", "agent-03", map[string]any{"kind": "screenshot", "name": "shot-1"}),
		ev(at, 8, "artifact_created", "agent-03", map[string]any{"kind": "screenshot", "name": "shot-2"}),
		ev(at, 9, "artifact_created", "agent-05", map[string]any{"kind": "screenshot", "name": "shot-3"}),
	)
	got := history(must(distillAsk(events, nil)))
	if n := strings.Count(got, "agent_stalled"); n != 1 || !strings.Contains(got, "agent_stalled (agent-03) ×4, last 09:03") {
		t.Errorf("four stalls are %d lines, want one that counts them:\n%s", n, got)
	}
	if !strings.Contains(got, "×2, last 09:05") || !strings.Contains(got, `"number":81`) {
		t.Errorf("one pull request broken twice should fold, and another stay apart:\n%s", got)
	}
	if strings.Count(got, "artifact_created (agent-03)") != 1 || !strings.Contains(got, "artifact_created (agent-05)") {
		t.Errorf("numbered screenshots fold per agent, never across agents:\n%s", got)
	}
}

func must(ask string, _ int) string { return ask }

func TestDistillFilterShowsLongTextOnce(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	q := "Should the reminders page paginate, or load everything at once?"
	got := history(must(distillAsk([]memory.Event{
		ev(at, 0, "question_asked", "agent-03", map[string]any{"questionId": "q_1a2b3c4d", "question": q}),
		ev(at, 1, "question_answered", "agent-03", map[string]any{"questionId": "q_1a2b3c4d", "question": q, "answer": "Paginate, 50 a page."}),
	}, nil)))
	if strings.Count(got, q) != 1 || !strings.Contains(got, `"question":"(as above)"`) {
		t.Errorf("the question should be shown once:\n%s", got)
	}
	if strings.Count(got, "q_1a2b3c4d") != 2 {
		t.Errorf("a question id is an anchor and is never shortened:\n%s", got)
	}
}

// A window bigger than the prompt is read in pieces, and the watermark says
// so: what didn't fit is left for the next pass instead of being skipped.
func TestDistillPromptLeavesWhatDidntFitForNextTime(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	var events []memory.Event
	for i := range memory.MaxDistillEvents {
		var words []string
		for w := range 100 {
			words = append(words, fmt.Sprintf("w%d.%d", i, w))
		}
		events = append(events, ev(at, i, "lead_turn", "", map[string]any{
			"userMessage":    fmt.Sprintf("Turn %d: %s", i, strings.Join(words[:40], " ")),
			"assistantReply": fmt.Sprintf("Answer %d: %s", i, strings.Join(words[40:], " ")),
		}))
	}
	ask, shown := distillAsk(events, nil)
	if shown <= 0 || shown >= len(events) {
		t.Fatalf("the prompt covers %d of %d events, want some but not all", shown, len(events))
	}
	if len(ask) > maxDistillPrompt+len(distillShape)+200 {
		t.Errorf("the prompt is %d bytes, over its bound", len(ask))
	}
	want := fmt.Sprintf("(%d more events didn't fit", len(events)-shown)
	if !strings.Contains(ask, want) {
		t.Errorf("the prompt doesn't say %q", want)
	}
	if !strings.Contains(ask, fmt.Sprintf("Turn %d:", shown-1)) || strings.Contains(ask, fmt.Sprintf("Turn %d:", shown)) {
		t.Errorf("the prompt should end at event %d", shown-1)
	}
}

// A window that is all noise costs no model call: the pass is recorded and
// the watermark moves past it.
func TestDistillationOfNothingButNoiseAsksNoModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, lead := consolidationProject(t)
	if _, err := d.client.SetConsolidation(ctx, "hello-stack", 20); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).Truncate(time.Minute)
	var last memory.Event
	for i := range 25 {
		e, err := d.srv.memory().AppendEvent(ctx, ev(at, i, "lead_turn", "", map[string]any{"userMessage": "ok", "assistantReply": "Still waiting."}))
		if err != nil {
			t.Fatal(err)
		}
		last = e
	}
	asked := func(context.Context, state.Agent, string) (string, error) {
		t.Error("a window of nothing but noise was sent to a model")
		return emptyDistillation, nil
	}
	d.srv.askLead = asked
	d.srv.askAside = func(ctx context.Context, a state.Agent, _, ask string) (string, string, error) {
		answer, err := asked(ctx, a, ask)
		return answer, "", err
	}
	if !d.srv.distillIfDue(ctx, lead) {
		t.Fatal("the pass didn't run")
	}
	pass := lastPass(t, d)
	if pass.Error != "" || pass.ThroughEventID != last.ID || pass.EventsRead != 25 {
		t.Errorf("pass = %+v, want a successful one through %s that read 25 events", pass, last.ID)
	}
}

// What the filter is for, measured: a window of 300 events shaped like a real
// day on this project, as the prompt showed it before the filter and after.
// `go test ./internal/daemon -run TestDistillFilterOnARealisticWindow -v`
// prints the numbers.
func TestDistillFilterOnARealisticWindow(t *testing.T) {
	t.Parallel()
	events := realisticWindow()
	if len(events) != memory.MaxDistillEvents {
		t.Fatalf("the fixture has %d events, want a full window of %d", len(events), memory.MaxDistillEvents)
	}
	before := unfilteredHistory(events)
	after := distillLines(events)
	var b strings.Builder
	for _, l := range after {
		b.WriteString(l.render(events, len(events)) + "\n")
	}
	filtered := b.String()

	ask, shown := distillAsk(events, nil)
	t.Logf("window: %d events; lines in the prompt: %d before, %d after", len(events), len(events), len(after))
	t.Logf("history, uncapped: %d bytes before, %d after (%.0f%% smaller); ≈%d → ≈%d tokens at 4 bytes a token",
		len(before), len(filtered), 100*(1-float64(len(filtered))/float64(len(before))), len(before)/4, len(filtered)/4)
	t.Logf("under the %d KiB cap: %d of %d events fit before, %d after", maxDistillPrompt>>10, fitBefore(events), len(events), shown)
	t.Logf("whole prompt sent: %d bytes", len(ask))

	if len(filtered) > len(before)*3/4 {
		t.Errorf("the filter took the history from %d to %d bytes, want at least a quarter off", len(before), len(filtered))
	}
	// Everything the window establishes is still in it.
	for _, kept := range []string{
		`"number":249`, `"number":250`, `"number":251`, `"number":252`, "q_7c1e9a20",
		"agentbox/feat-memory-pressure", "Paginate", "killed", "GH_CONFIG_DIR",
	} {
		if !strings.Contains(filtered, kept) {
			t.Errorf("the filtered history lost %q", kept)
		}
	}
}

// unfilteredHistory is the history as distillAsk wrote it before the filter:
// one line per event, its payload as stored.
func unfilteredHistory(events []memory.Event) string {
	var b strings.Builder
	for _, e := range events {
		line := fmt.Sprintf("- %s %s", e.At.Format("2006-01-02 15:04"), e.Type)
		if e.Agent != "" {
			line += " (" + e.Agent + ")"
		}
		if payload := collapseLines(string(e.Payload)); payload != "" && payload != "{}" {
			line += " " + truncate(payload, 600)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// fitBefore is how many events the unfiltered prompt held under the cap.
func fitBefore(events []memory.Event) int {
	size := len(distillPreamble) + 200
	for i, line := range strings.Split(unfilteredHistory(events), "\n") {
		if size+len(line) > maxDistillPrompt {
			return i
		}
		size += len(line) + 1
	}
	return len(events)
}

// realisticWindow is 300 events with the payloads the daemon's capture sites
// really write (memoryevents.go and its callers), in the proportions a busy
// day on this project has: six agents, each created with a task of a few
// hundred words, working through stalls, memory pauses, red CI and
// screenshots to a pull request; the lead's chat around them, much of it
// short; a question, a credential, the notes.
func realisticWindow() []memory.Event {
	at := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	var events []memory.Event
	add := func(typ, agent string, payload map[string]any) {
		events = append(events, ev(at, len(events), typ, agent, payload))
	}
	turn := func(msg, reply string) {
		add("lead_turn", "", map[string]any{"userMessage": msg, "assistantReply": reply})
	}
	woken := func(what, reply string) {
		turn("(no message: background work it had left running ended: "+what+")", reply)
	}
	type job struct {
		agent, title, branch, task, summary, cmd string
		pr                                       int
		stalls, pauses, broken, shots            int
	}
	jobs := []job{
		{"agent-282", "Agents get a share of the VM's cores", "agentbox/feat-cpu-share",
			"Give each agent limits.cpu equal to its share of the VM's cores, minimum 2, rebalanced live as agents start and stop. Export GOMAXPROCS, GOFLAGS=-p, CARGO_BUILD_JOBS, MAKEFLAGS and VITEST_MAX_WORKERS through BASH_ENV so test runners start fewer workers. Measure go test's peak memory at share 2, 4 and 8 and put the numbers in the PR. Done: PR open, tests green.",
			"limits.cpu follows the share live; worker env vars through BASH_ENV; go test peak memory about halved at share 4, runs 1.5-2x slower.", "go test ./...", 249, 3, 4, 3, 6},
		{"agent-283", "Memory by pressure, not a queue", "agentbox/feat-memory-pressure",
			"Replace admission (slots, light/normal/heavy sizes, learned baselines, burst leases) with measured pressure: PSI. Every agent starts at once; each gets a hard memory.max (the VM cap minus an eighth); OOM kills are reported to the agent's chat; heavy commands wait before they start while pressure is high; running ones are frozen newest-first, never the oldest, and frozen memory goes to zram set up at start. Remove the queue from the daemon, the CLI and the app.",
			"PSI gate and freezer in internal/daemon/pressure.go; queue, slots and sizes removed everywhere; zram ~1/4 of the VM, zstd. Untested on the real VM; thresholds are guesses.", "npm --prefix desktop test", 252, 4, 5, 4, 8},
		{"agent-284", "Memory hygiene: anchors and tidy", "agentbox/feat-memory-anchors",
			"Issue memories go stale: 93 of 119 are still open, with duplicates. Give a memory anchors (a PR, a branch, a question id) that close it on their own when merged, closed or answered; age issues out of the recap after 21 days; dedupe by content; and add `agentbox memory tidy` to resolve old items by a cutoff (default 7 days) with no model calls.",
			"Anchors with auto-close, 21-day aging, content dedup, and agentbox memory tidy --older-than 7d.", "go test ./internal/memory", 251, 2, 2, 2, 3},
		{"agent-285", "Remove Tasks", "agentbox/refactor-remove-tasks",
			"Remove the Tasks concept entirely: the Tasks tab, Settings → Memory → Tasks, Ctrl+K task results, tasks.go and its tables by a migration, my_task, the task list in project_state and close-on-merge. Keep the agent's task text.",
			"Tasks gone from daemon, CLI and app; migration drops the tables; the agent's task text stays.", "npm --prefix desktop run typecheck", 250, 2, 3, 3, 5},
		{"agent-286", "Hatch pages in the chat", "agentbox/feat-hatch-artifacts-chat",
			"Rework the Hatch pages in the chat on the existing branch: a fanned stack of up to three plus +N at the composer, only for new unseen pages; call them pages, not artifacts; previews in-app once leciric/hatch#3 is deployed.",
			"Fanned stack at the composer for unseen pages; previews wait on hatch#3.", "npm --prefix desktop start", 248, 3, 2, 2, 9},
		{"agent-287", "Lead brief: drop stale still-open items", "agentbox/fix-lead-brief-stale",
			"The lead brief keeps repeating old 'Still open' items (the D1 migration, agent-name collisions, old PR lists). Drop items whose anchors are closed and anything older than three weeks from the brief.",
			"The brief's still-open list is anchored and aged out; golden tests updated.", "go test ./internal/brief", 253, 1, 1, 1, 2},
	}
	short := []struct{ msg, reply string }{
		{"thanks", "You're welcome!"}, {"ok", "Waiting on CI."}, {"?", "Still running."},
		{"go on", "On it."}, {"ok", "Still waiting."}, {"cool", "Thanks!"},
	}
	// What the chat says between the agents' work: every one of them says
	// something, and none of them twice (the window's tail makes up more).
	talk := []struct{ msg, reply string }{
		{"What's left before the release?", "Only the hatch#3 deploy for in-app previews; #249–#253 are merged. Run `agentbox memory tidy` once the daemon updates."},
		{"Is zram on in the real VM?", "Not yet: the kernel 6.12.111+deb13-cloud-amd64 has CONFIG_ZRAM=m and no swap on, so #252 loads the module at start. Nobody has watched it on the real VM."},
		{"Why are the PSI thresholds what they are?", "They're guesses from agent-283's runs on a 16 GiB VM: some avg10 over 20 gates heavy commands, full avg10 over 5 freezes the newest run. They need a week of real use."},
		{"Did #250 drop the tasks tables?", "Yes, by an appended migration; old rows are gone and the agent's task text stays on the agent itself."},
		{"Can the Notes tab replace the plan?", "We tried it as a mock-up (Hatch v1–v3) and you dropped it: the memory items behind it weren't trustworthy enough to plan from."},
		{"Which items in memory are stale?", "93 of 119 issue memories were still open, with duplicates; #251's anchors close them on merge and tidy resolves anything older than 7 days."},
		{"How do I preview a page in the chat?", "Pages show as a fanned stack at the composer when there are new ones; clicking opens hatch.linting.dev until hatch#3 is deployed, then in-app."},
		{"Should agents keep their caches between runs?", "Go and npm caches are shared read-only from the base image and per-agent writable on /t; nothing is kept between agents, which is what keeps them isolated."},
		{"Why did agent-286 take two tries?", "agent-280 built the first version as artifacts; you chose the mix on the options page, so agent-286 reworked it on the same branch as pages."},
		{"Where does the lead's context come from?", "The ACP adapter owns the session; the daemon only shapes it at start through the brief files and by rolling the session."},
		{"Does memory tidy call a model?", "No: it resolves by cutoff (default 7 days) and anchors, with no model calls, so it's safe to run on every project."},
		{"Can we get rid of the Tasks tab in the CLI too?", "#250 removed it from the daemon, the CLI (agentbox tasks) and the app together, so nothing is left to call it."},
	}
	statuses := []string{"Still waiting on CI.", "Waiting on CI.", "Still running.", "CI is still running."}
	n := 0
	for i, j := range jobs {
		turn(fmt.Sprintf("Start an agent on this: %s", j.task),
			fmt.Sprintf("Started %s on %q (branch %s). It will open a PR against main when the tests are green; I'll tell you when it finishes or asks something.", j.agent, j.title, j.branch))
		add("agent_created", j.agent, map[string]any{"title": j.title, "task": j.task, "model": "claude-opus-5-5", "branch": j.branch})
		add("task_created", j.agent, map[string]any{"taskId": fmt.Sprintf("tsk_%02d", i), "goal": j.task, "status": "open", "parentTaskId": "", "dependsOn": nil})
		add("task_status_changed", j.agent, map[string]any{"taskId": fmt.Sprintf("tsk_%02d", i), "goal": j.task, "from": "open", "to": "in_progress"})
		for k := range j.stalls {
			add("agent_stalled", j.agent, map[string]any{"quiet_minutes": 12 + 6*k, "turn_minutes": 40 + 6*k, "tool_running": "Bash"})
			s := short[n%len(short)]
			n++
			turn(s.msg, s.reply)
		}
		for k := range j.pauses {
			add("memory_paused", j.agent, map[string]any{"command": j.cmd})
			woken(fmt.Sprintf("%s finished (exit 0) after %d minutes", j.cmd, 3+k), statuses[k%len(statuses)])
		}
		if i == 1 {
			add("oom_kill", j.agent, map[string]any{"kills": 1, "limit": 7516192768, "own_limit": true, "process": "node", "pid": 48211, "rss": 7290000000})
			turn("Why was agent-283 killed?", "Its vitest run went past its memory.max (7 GiB) with 8 workers: the OOM kill was reported to its chat and it reran with --maxWorkers=2, which passed.")
		}
		if i == 2 {
			add("question_asked", j.agent, map[string]any{"questionId": "q_7c1e9a20", "question": "Should memory tidy resolve old issues by default, or only print what it would resolve?", "context": "building agentbox memory tidy"})
			turn("What is agent-284 asking?", "Whether `agentbox memory tidy` should resolve by default or only print what it would resolve. I'd say resolve by default with --dry-run to print.")
			add("question_answered", j.agent, map[string]any{"questionId": "q_7c1e9a20", "question": "Should memory tidy resolve old issues by default, or only print what it would resolve?", "answer": "Resolve by default; Paginate the output; --dry-run only prints.", "answeredBy": "user"})
		}
		if i == 3 {
			add("credential_requested", j.agent, map[string]any{"kind": "github", "name": "", "reason": "gh pr create answers 403 on leciric/agentbox"})
			turn("gh says 403 for agent-285", "gh needs GH_CONFIG_DIR=/home/lint/.config/gh in the lead's shell; the agent's token was missing the repo scope, which the credential request fixes.")
		}
		for k := range j.shots {
			add("artifact_created", j.agent, map[string]any{"artifactId": fmt.Sprintf("art_%d%02d", i, k), "kind": "screenshot", "name": fmt.Sprintf("%s-%d", j.branch[9:], k+1)})
		}
		add("artifact_created", j.agent, map[string]any{"artifactId": fmt.Sprintf("art_%d99", i), "kind": "recording", "name": "walkthrough"})
		for k := range j.broken {
			add("pr_broken", j.agent, map[string]any{"number": j.pr, "url": fmt.Sprintf("https://github.com/leciric/agentbox/pull/%d", j.pr), "problems": []string{"ci"}, "head": fmt.Sprintf("%07x", 0xa1b2c3+i*16+k)})
			woken(fmt.Sprintf("gh pr checks %d --watch exited 1", j.pr), statuses[(k+1)%len(statuses)])
		}
		add("task_status_changed", j.agent, map[string]any{"taskId": fmt.Sprintf("tsk_%02d", i), "goal": j.task, "from": "in_progress", "to": "done"})
		add("agent_finished", j.agent, map[string]any{
			"title": j.title, "branch": j.branch, "files": 9 + i*3, "insertions": 400 + i*90, "deletions": 120 + i*40, "dirty": false,
			"summary": j.summary, "reportId": fmt.Sprintf("rep_%02d", i),
			"pr": map[string]any{"number": j.pr, "url": fmt.Sprintf("https://github.com/leciric/agentbox/pull/%d", j.pr), "state": "OPEN"},
		})
		turn("", fmt.Sprintf("%s finished: #%d is open and green. %s", j.agent, j.pr, j.summary))
		add("pr_merged", "", map[string]any{"number": j.pr, "url": fmt.Sprintf("https://github.com/leciric/agentbox/pull/%d", j.pr), "branch": j.branch, "method": "squash"})
		add("agent_retired", j.agent, map[string]any{"how": "destroy", "branch": j.branch})
	}
	add("notes_changed", "", map[string]any{"how": "edit", "length": 412, "preview": "2026-10-09: gh needs GH_CONFIG_DIR=/home/lint/.config/gh; the agentbox CLI needs HOME=/home/lint.linux; there is no jq, use python3."})
	// The rest of the day: the chat going on around the merges, a third of
	// it saying something new each time, the rest short turns, background
	// jobs ending and the same stall noticed again.
	files := []string{"internal/daemon/pressure.go", "internal/memory/anchors.go", "internal/brief/lead.md.tmpl", "desktop/src/renderer/components/Composer.tsx", "internal/state/state.go", "internal/agent/limits.go"}
	for k := 0; len(events) < memory.MaxDistillEvents; k++ {
		switch k % 6 {
		case 0, 3:
			i := k / 3
			if i < len(talk) {
				turn(talk[i].msg, talk[i].reply)
				break
			}
			f, pr := files[i%len(files)], 240+i
			turn(fmt.Sprintf("What did #%d change in %s?", pr, f),
				fmt.Sprintf("#%d touched %s: %d lines, mostly moving the %s check behind a helper so the tests can call it without a VM. Nothing else depends on it yet.", pr, f, 20+7*i, []string{"pressure", "anchor", "aging", "stack", "migration", "share"}[i%6]))
		case 1:
			sh := short[k%len(short)]
			turn(sh.msg, sh.reply)
		case 2:
			add("agent_stalled", "agent-287", map[string]any{"quiet_minutes": 10 + k, "turn_minutes": 30 + k, "tool_running": "Bash"})
		case 4:
			woken(fmt.Sprintf("gh run watch %d exited 0", 18000+k), statuses[k%len(statuses)])
		case 5:
			add("artifact_created", "agent-287", map[string]any{"artifactId": fmt.Sprintf("art_t%d", k), "kind": "screenshot", "name": fmt.Sprintf("brief-%d", k)})
		}
	}
	return events
}
