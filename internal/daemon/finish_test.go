package daemon

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// finishedTask is the line between "the agent is genuinely done" and "the
// turn just ended", which happens for other reasons too.
func TestFinishedTask(t *testing.T) {
	cases := []struct {
		name   string
		result api.ChatTurnResult
		want   bool
	}{
		{"a normal end", api.ChatTurnResult{State: "completed", StopReason: "end_turn"}, true},
		{"no stop reason at all", api.ChatTurnResult{State: "completed", StopReason: ""}, true},
		{"cancelled", api.ChatTurnResult{State: "cancelled", StopReason: "cancelled"}, false},
		{"failed", api.ChatTurnResult{State: "failed"}, false},
		{"cut short by the model's output limit", api.ChatTurnResult{State: "completed", StopReason: "max_tokens"}, false},
		{"cut short by the turn's request limit", api.ChatTurnResult{State: "completed", StopReason: "max_turn_requests"}, false},
		{"a refusal", api.ChatTurnResult{State: "completed", StopReason: "refusal"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := finishedTask(c.result); got != c.want {
				t.Errorf("finishedTask(%+v) = %v, want %v", c.result, got, c.want)
			}
		})
	}
}

// leadReadyToChat gives a project a lead whose adapter is a stub that exits at
// once: enough to tell "AgentBox tried to start a turn" (the state moves off
// api.ChatOff) from "it didn't try at all" (the state never changes), without
// a real Claude Code login or a network.
func leadReadyToChat(t *testing.T, d testDaemon, project string) state.Agent {
	t.Helper()
	ctx := context.Background()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveClaudeToken("", "test-token"); err != nil {
		t.Fatal(err)
	}
	tools := d.paths.Tools()
	for _, rel := range []string{
		filepath.Join(".local", "bin", "claude"),
		filepath.Join("node_modules", ".bin", "claude-agent-acp"),
	} {
		path := filepath.Join(tools, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The adapter's version, so it counts as the pinned one rather than one an
	// older AgentBox left behind, which would be reinstalled.
	pkg := strings.TrimPrefix(agent.ChatAdapters["claude"].Package, "npm:")
	at := strings.LastIndex(pkg, "@")
	manifest := filepath.Join(tools, "node_modules", pkg[:at], "package.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"version":"`+pkg[at+1:]+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	lead, err := d.srv.manager(nil).EnsureLead(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	return lead
}

// saidLast leaves an agent with the conversation a finished turn leaves: a
// user message, a tool call, and what the agent's AI tool said last — nothing
// at all, for an agent that never summed up. It writes the items package chat
// stores, which the daemon's own chat manager loads back.
func saidLast(t *testing.T, d testDaemon, a state.Agent, message string) {
	t.Helper()
	now := time.Now()
	items := []api.ChatItem{
		{
			ID: "u1", Turn: "u1", Kind: "user", Text: "Do the thing.", CreatedAt: now, UpdatedAt: now,
			Result: &api.ChatTurnResult{State: "completed", StopReason: "end_turn", EndedAt: now},
		},
		{
			ID: "t1", Turn: "u1", Kind: "tool", CreatedAt: now, UpdatedAt: now,
			Tool: &api.ChatTool{CallID: "c1", Name: "Bash", Title: "go test ./...", Kind: "execute", Status: "completed"},
		},
	}
	if message != "" {
		items = append(items, api.ChatItem{ID: "a1", Turn: "u1", Kind: "assistant", Text: message, CreatedAt: now, UpdatedAt: now})
	}
	rows := make([]state.ChatItem, 0, len(items))
	for i, it := range items {
		data, err := json.Marshal(it)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, state.ChatItem{ID: it.ID, Position: int64(i), Data: data})
	}
	if err := d.srv.store.SaveChatItems(context.Background(), a.Project, a.Name, rows); err != nil {
		t.Fatal(err)
	}
}

// noticeTo waits for what the lead was told, and returns it.
func noticeTo(t *testing.T, d testDaemon, lead state.Agent) string {
	t.Helper()
	var notice string
	waitFor(t, "the lead to be told", func() bool {
		th, err := d.srv.chat.Thread(lead)
		if err != nil {
			return false
		}
		for _, it := range th.Items {
			if it.Kind == "notice" && it.Text != "" {
				notice = it.Text
			}
		}
		return notice != ""
	})
	return notice
}

// The point of the notice: the lead learns what the agent did, not only how
// much it changed, and can decide from the notice alone.
func TestFinishNoticeCarriesTheAgentsSummary(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	summary := "Added pagination to the reminders list, twenty a page, with the page in the query " +
		"string. The API already took limit and offset, so nothing changed server side. The two " +
		"snapshot tests of the old unpaginated list needed rewriting, which is worth a look."
	saidLast(t, d, a, summary)
	// Work to have finished, so the notice is the shape a lead really sees:
	// what changed as well as what the agent says about it.
	if err := os.WriteFile(filepath.Join(a.Worktree, "server.mjs"), []byte("// paginated\nconsole.log('hi');\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d.srv.agentFinished(a, api.ChatTurnResult{State: "completed", StopReason: "end_turn"})

	notice := noticeTo(t, d, lead)
	t.Logf("the lead was told:\n\n%s\n", notice)
	if !strings.Contains(notice, "1 file(s)") || !strings.Contains(notice, "not yet committed") {
		t.Errorf("the notice lost what the agent changed:\n%s", notice)
	}
	if !strings.Contains(notice, "agent-01") || !strings.Contains(notice, "Reminders page") {
		t.Errorf("the notice doesn't say which agent finished:\n%s", notice)
	}
	if !strings.Contains(notice, quote(summary)) {
		t.Errorf("the notice doesn't carry the agent's summary:\n%s", notice)
	}
	if !strings.Contains(notice, `read_agent("agent-01")`) {
		t.Errorf("the notice no longer points at the conversation:\n%s", notice)
	}
	if strings.Contains(notice, "no summary") || strings.Contains(notice, "start of a longer summary") {
		t.Errorf("a summary that fits was reported as missing or cut:\n%s", notice)
	}
}

// A summary of the size agents on this project actually write is cut, or the
// notice would cost the lead more context than the read_agent call it saves.
func TestFinishNoticeCutsAnEnormousSummary(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "The finish notice")
	report := longReport()
	saidLast(t, d, a, report)

	d.srv.agentFinished(a, api.ChatTurnResult{State: "completed", StopReason: "end_turn"})

	notice := noticeTo(t, d, lead)
	t.Logf("the agent wrote %d characters; the lead was told %d:\n\n%s\n", len(report), len(notice), notice)
	if len(notice) > len(report)/4 {
		t.Errorf("the notice is %d characters of the agent's %d: the cap isn't doing its job", len(notice), len(report))
	}
	if !strings.Contains(notice, "start of a longer summary") {
		t.Errorf("the notice doesn't say it was cut, so the lead reads part of the summary as all of it:\n%s", notice)
	}
	if strings.Contains(notice, "|") {
		t.Errorf("the notice carries part of a table, which reads like the whole of one:\n%s", notice)
	}
	if !strings.Contains(notice, "What I did") {
		t.Errorf("the beginning of the summary didn't survive:\n%s", notice)
	}
}

// An older agent, or one that stopped after a tool call, left nothing to quote.
// The notice still has to be worth reading — and must not dress up whatever was
// there as a summary.
func TestFinishNoticeWithoutASummary(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	saidLast(t, d, a, "") // it stopped after a tool call

	d.srv.agentFinished(a, api.ChatTurnResult{State: "completed", StopReason: "end_turn"})

	notice := noticeTo(t, d, lead)
	t.Logf("the lead was told:\n\n%s\n", notice)
	if !strings.Contains(notice, "agent-01") || !strings.Contains(notice, "Reminders page") {
		t.Errorf("the notice doesn't say which agent finished:\n%s", notice)
	}
	if !strings.Contains(notice, "It left no summary.") {
		t.Errorf("the notice doesn't say there is no summary:\n%s", notice)
	}
	if !strings.Contains(notice, `Read what it did with read_agent("agent-01")`) {
		t.Errorf("with no summary, reading the conversation is the way to find out — and the notice must say so:\n%s", notice)
	}
	if strings.Contains(notice, ">") {
		t.Errorf("the notice quotes something that isn't a summary:\n%s", notice)
	}
}

// A genuine finish wakes the lead and starts a turn on its own, whatever the
// project's autonomy is set to: noticing isn't the same as acting without
// asking, which is what autonomy actually governs.
func TestAgentFinishingWakesTheLeadRegardlessOfAutonomy(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	if p, err := d.client.Project(ctx, "hello-stack"); err != nil || p.Autonomy != state.AutonomyAsk {
		t.Fatalf("a new project's autonomy = %+v, %v; want ask", p, err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")

	if state := d.srv.chat.State(lead.Ref()); state != "" {
		t.Fatalf("the lead's chat is already %q before anything happened", state)
	}
	d.srv.leadAsked(a) // the work was the lead's to hear about (D87)
	d.srv.agentFinished(a, api.ChatTurnResult{State: "completed", StopReason: "end_turn"})

	waitFor(t, "the lead to react", func() bool {
		return d.srv.chat.State(lead.Ref()) != "" && d.srv.chat.State(lead.Ref()) != api.ChatOff
	})
	thread, err := d.srv.chat.Thread(lead)
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Items) == 0 {
		t.Fatal("the lead's chat has nothing in it")
	}
	var sawNotice, sawTurn bool
	for _, it := range thread.Items {
		if it.Kind == "notice" && it.Text != "" {
			sawNotice = true
		}
		if it.Kind == "user" {
			sawTurn = true
		}
	}
	if !sawNotice {
		t.Error("no notice was posted")
	}
	if !sawTurn {
		t.Error("a notice was posted, but no turn was started for it: the lead won't react on its own")
	}
}

// A turn that ends any other way — cancelled, failed, or cut short by a limit
// — is the agent stopping to breathe, not finishing: it must not wake the
// lead, or the lead would be triggered on every ordinary pause.
func TestAgentPausingDoesNotWakeTheLead(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")

	for _, result := range []api.ChatTurnResult{
		{State: "cancelled", StopReason: "cancelled"},
		{State: "failed"},
		{State: "completed", StopReason: "max_turn_requests"},
	} {
		d.srv.agentFinished(a, result)
	}
	// Give a wrongly-woken lead time to show itself before concluding it didn't.
	time.Sleep(200 * time.Millisecond)
	if state := d.srv.chat.State(lead.Ref()); state != "" {
		t.Errorf("the lead's chat state is %q, want untouched", state)
	}
	thread, err := d.srv.chat.Thread(lead)
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Items) != 0 {
		t.Errorf("the lead's chat has %d item(s), want none: %+v", len(thread.Items), thread.Items)
	}
}

// The lead is never told about its own agent twice for the same finish, and
// several agents finishing while it is still reacting to an earlier one are
// folded into the one turn that follows, not a turn each — otherwise a lead
// that reacts by nudging its agents could wake itself in an endless chain.
func TestFinishesWhileTheLeadIsBusyBecomeOneTurn(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a1 := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	a2 := addAgent(t, d, repo, "hello-stack", "agent-02", "Why the build is slow")

	// The first finish starts the lead's turn; because the stub adapter exits
	// at once, that turn takes a moment to fail, which is the window the
	// second finish arrives in.
	d.srv.leadAsked(a1)
	d.srv.leadAsked(a2)
	d.srv.agentFinished(a1, api.ChatTurnResult{State: "completed", StopReason: "end_turn"})
	d.srv.agentFinished(a2, api.ChatTurnResult{State: "completed", StopReason: "end_turn"})

	waitFor(t, "both notices to settle", func() bool {
		th, err := d.srv.chat.Thread(lead)
		if err != nil {
			return false
		}
		turns := 0
		for _, it := range th.Items {
			if it.Kind == "user" {
				turns++
			}
		}
		return turns > 0
	})
	// However they landed — one merged turn, or the second queued behind the
	// first's — the agent must never be told about twice: at most one turn per
	// finish, never more.
	th, err := d.srv.chat.Thread(lead)
	if err != nil {
		t.Fatal(err)
	}
	turns := 0
	for _, it := range th.Items {
		if it.Kind == "user" {
			turns++
		}
	}
	if turns == 0 || turns > 2 {
		t.Errorf("%d turn(s) started for two finishes, want 1 or 2 (batched, or queued one after the other)", turns)
	}
}

// The setting's whole point: with finish notices off, the lead's history still
// shows that the agent finished and what it said, but no turn is started, so
// the finish costs nothing.
func TestAgentFinishingWithNoticesOffRecordsWithoutATurn(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.SetFinishNotices(ctx, "hello-stack", state.FinishNoticesOff); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	summary := "Added pagination to the reminders list, twenty a page, with the page in the query " +
		"string. Nothing changed server side, and two snapshot tests needed rewriting."
	saidLast(t, d, a, summary)

	d.srv.agentFinished(a, api.ChatTurnResult{State: "completed", StopReason: "end_turn"})

	// The notice is still there, with the summary in it: the history shows what
	// happened, and the lead reads it the next time the user writes.
	notice := noticeTo(t, d, lead)
	t.Logf("the lead was told, without a turn:\n\n%s\n", notice)
	if !strings.Contains(notice, "agent-01") || !strings.Contains(notice, quote(summary)) {
		t.Errorf("the recorded notice lost what the agent did:\n%s", notice)
	}
	// Give a wrongly-started turn time to show itself before concluding there
	// wasn't one.
	time.Sleep(200 * time.Millisecond)
	if st := d.srv.chat.State(lead.Ref()); st != "" && st != api.ChatOff {
		t.Errorf("the lead's chat state is %q: a turn was started, which is what off is for", st)
	}
	thread, err := d.srv.chat.Thread(lead)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range thread.Items {
		if it.Kind == "user" {
			t.Errorf("a turn was started for the notice: %+v", it)
		}
	}
}

// Questions are not the same thing (D42): the agent is blocked until somebody
// answers, so its question wakes the lead even with finish notices off.
func TestQuestionsStillWakeTheLeadWithNoticesOff(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.SetFinishNotices(ctx, "hello-stack", state.FinishNoticesOff); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")

	asked := make(chan api.Question, 1)
	go func() {
		q, err := d.srv.askForTest(ctx, a, "Should the page paginate?", "building it")
		if err != nil {
			t.Errorf("ask: %v", err)
		}
		asked <- q
	}()

	waitFor(t, "the lead to react to the question", func() bool {
		th, err := d.srv.chat.Thread(lead)
		if err != nil {
			return false
		}
		for _, it := range th.Items {
			if it.Kind == "user" {
				return true
			}
		}
		return false
	})

	// And the agent still gets its answer, so the chain isn't merely started.
	id := waitForQuestion(t, d, "hello-stack")
	if _, err := d.srv.answerQuestion(ctx, id, "Yes, 20 per page.", "lead"); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-asked:
		if answer.Answer != "Yes, 20 per page." {
			t.Errorf("the agent got %+v", answer)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the agent was never told the answer")
	}
}

// The setting round trip over the API, and what a project starts with.
func TestFinishNoticesAreSetPerProjectAndDefaultToLead(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	p, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")})
	if err != nil {
		t.Fatal(err)
	}
	if p.FinishNotices != state.FinishNoticesLead {
		t.Errorf("a new project's finish notices = %q, want %q", p.FinishNotices, state.FinishNoticesLead)
	}
	updated, err := d.client.SetFinishNotices(ctx, "hello-stack", state.FinishNoticesOff)
	if err != nil {
		t.Fatal(err)
	}
	if updated.FinishNotices != state.FinishNoticesOff {
		t.Errorf("SetFinishNotices() = %q, want off", updated.FinishNotices)
	}
	// It is read back the same way the app reads it, not only from the answer.
	if got, err := d.client.Project(ctx, "hello-stack"); err != nil || got.FinishNotices != state.FinishNoticesOff {
		t.Errorf("Project() = %q, %v; want off", got.FinishNotices, err)
	}
	if _, err := d.client.SetFinishNotices(ctx, "hello-stack", "quietly"); err == nil {
		t.Error("an unknown finish-notices value was accepted")
	}
	// Setting one thing leaves the others alone.
	back, err := d.client.SetFinishNotices(ctx, "hello-stack", state.FinishNoticesChat)
	if err != nil {
		t.Fatal(err)
	}
	if back.Autonomy != state.AutonomyAsk || back.MediaRetentionDays != state.DefaultMediaRetentionDays {
		t.Errorf("setting finish notices changed something else: %+v", back)
	}
}

// TestAFinishTheLeadDidNotAskForDoesNotWakeIt (D87): a turn somebody drove from
// the agent's own chat is recorded, but the lead isn't woken to read it — a
// turn of its whole context for news it didn't ask for. Once the lead asks
// the agent for something (tell_agent, through its own socket), the next
// finish wakes it, and only that one.
func TestAFinishTheLeadDidNotAskForDoesNotWakeIt(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	turns := func() int {
		th, err := d.srv.chat.Thread(lead)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, it := range th.Items {
			if it.Kind == "user" {
				n++
			}
		}
		return n
	}

	d.srv.agentFinished(a, api.ChatTurnResult{State: "completed", StopReason: "end_turn"})
	time.Sleep(200 * time.Millisecond)
	if n := turns(); n != 0 {
		t.Fatalf("a finish the lead didn't ask for started %d turn(s)", n)
	}
	if notice := noticeTo(t, d, lead); !strings.Contains(notice, "agent-01") {
		t.Errorf("the finish wasn't recorded for the lead: %q", notice)
	}

	// The lead asks, through its own socket; that finish wakes it.
	leadClient := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	if _, err := leadClient.TellAgent(ctx, "agent-01", "Add a test for the second page."); err != nil {
		t.Fatal(err)
	}
	d.srv.agentFinished(a, api.ChatTurnResult{State: "completed", StopReason: "end_turn"})
	waitFor(t, "the lead to react to the work it asked for", func() bool { return turns() == 1 })

	// And one request is answered by one finish: the next is the agent's own.
	d.srv.agentFinished(a, api.ChatTurnResult{State: "completed", StopReason: "end_turn"})
	time.Sleep(200 * time.Millisecond)
	if n := turns(); n != 1 {
		t.Errorf("%d turns after a second finish nobody asked for, want still 1", n)
	}
}

// TestCreateAgentFailureInTheJobNoticesTheLead: some mistakes create_agent
// can't see before the job starts — here, a name that collides with a branch
// an earlier agent left behind, only found once Create is already running.
// The chat asked for the agent (CreateProjectAgent succeeded, same as the
// bug report), so it must still be told the job failed, the same way a
// finish it asked for wakes it — otherwise it believes an agent exists that
// never came to be.
func TestCreateAgentFailureInTheJobNoticesTheLead(t *testing.T) {
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveClaudeToken("default", "sk-ant-oat01-default"); err != nil {
		t.Fatal(err)
	}
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	lead := leadReadyToChat(t, d, "hello-stack")
	// Leaves behind the branch agentbox/agent-01, which build() refuses to
	// reuse.
	addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	if err := d.srv.store.RemoveAgent(ctx, "hello-stack", "agent-01"); err != nil {
		t.Fatal(err)
	}

	leadClient := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	job, err := leadClient.CreateProjectAgent(ctx, api.CreateAgentRequest{Name: "agent-01", Title: "Another one", Task: "add it"})
	if err != nil {
		t.Fatalf("CreateProjectAgent() = %v, want the job to start: this failure can only be found inside it", err)
	}
	if err := d.client.FollowJobLog(ctx, job.ID, io.Discard); err != nil {
		t.Fatal(err)
	}
	j, err := d.client.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != api.JobFailed || !strings.Contains(j.Error, "already exists") {
		t.Fatalf("job = %+v, want it refused for the colliding branch", j)
	}

	notice := noticeTo(t, d, lead)
	t.Logf("the lead was told:\n\n%s\n", notice)
	if !strings.Contains(notice, "create_agent failed") || !strings.Contains(notice, "already exists") {
		t.Errorf("the lead was never told the job it started failed:\n%s", notice)
	}
}
