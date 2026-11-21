package daemon

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// longTask is a task like the ones create_agent gave agent-861 and agent-864
// before AgentBox lost them: many lines, a list and blank lines.
const longTask = "Agents' worktrees live under the host's home, shared into the VM by virtiofs, so every git op goes through it.\n\n" +
	"Move them onto the VM's own disk:\n- keep Open in editor working\n- leave the hub alone\n\nDone is a PR with before/after numbers."

// taskLine is a line of longTask no prompt has but one that carries it.
const taskLine = "- keep Open in editor working"

// promptTexts is the text of every prompt a promptTool was sent, decoded.
func promptTexts(t *testing.T, p *promptTool) []string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, raw := range p.prompts {
		var req acp.PromptRequest
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		for _, b := range req.Prompt {
			text.WriteString(b.Text)
		}
		out = append(out, text.String())
	}
	return out
}

func carrying(t *testing.T, p *promptTool, line string) int {
	t.Helper()
	n := 0
	for _, text := range promptTexts(t, p) {
		if strings.Contains(text, line) {
			n++
		}
	}
	return n
}

func taskKept(t *testing.T, d testDaemon, a state.Agent) (state.AgentTask, bool) {
	t.Helper()
	tk, ok, err := d.srv.store.AgentTaskOf(context.Background(), a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	return tk, ok
}

// startBlocksIncus is readyOneAgentIncus whose `incus start` waits for
// $INCUS_GO, having said it is starting in $INCUS_STARTING: the machine is
// being made for as long as a test likes.
const startBlocksIncus = `if [ "$1" = start ]; then
  touch "$INCUS_STARTING"
  while [ ! -e "$INCUS_GO" ]; do sleep 0.05; done
fi
` + readyOneAgentIncus

// The task create_agent gives an agent is in the store from the moment the
// agent's row is added, before its machine is made, which is the time
// AgentBox stopped in on 20 and 21 Nov 2026; it is sent once the machine
// is, as itself, and forgotten as its prompt goes to the AI tool.
func TestATaskIsKeptWhileTheMachineIsMade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	starting, release := filepath.Join(root, "starting"), filepath.Join(root, "go")
	tool := &promptTool{}
	d := startTestDaemon(t, root, startBlocksIncus, testConfig{
		env: map[string]string{"INCUS_STARTING": starting, "INCUS_GO": release},
		setup: func(s *Server) {
			s.chat.Launch = tool.launch
			s.chat.Prepare = func(context.Context, state.Agent, string, int64) error { return nil }
		},
	})
	// After the daemon's cleanup, so before it in turn: a test that fails
	// while the machine is held still lets it go, or the daemon's stop
	// would wait on the create job for good.
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o644) })
	if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveClaudeToken("", "sk-ant-oat01-x"); err != nil {
		t.Fatal(err)
	}
	addProjectAllowingEvery(t, d, d.fixtureRepo(t, "hello-stack"))
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	job, err := lead.CreateProjectAgent(ctx, api.CreateAgentRequest{Name: "agent-01", Title: "Worktrees on VM disk", Task: longTask})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent's machine to be starting", func() bool { _, err := os.Stat(starting); return err == nil })
	a, err := d.srv.store.Agent(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != state.AgentCreating {
		t.Fatalf("agent-01 is %s while its machine starts", a.Status)
	}
	if tk, ok := taskKept(t, d, a); !ok || tk.Text != longTask || !tk.ByLead {
		t.Fatalf("while its machine is made, agent-01's task is kept as %+v (%v); want create_agent's task", tk, ok)
	}

	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "agent-01 to be made", func() bool {
		j, err := d.client.Job(ctx, job.ID)
		return err == nil && j.Done()
	})
	if j, _ := d.client.Job(ctx, job.ID); j.Status != api.JobSucceeded {
		t.Fatalf("creating agent-01 = %s: %s", j.Status, j.Error)
	}
	waitFor(t, "the task to reach the AI tool", func() bool { return carrying(t, tool, taskLine) > 0 })
	if got := promptTexts(t, tool); got[0] != longTask {
		t.Errorf("the first prompt is %q, want the task as create_agent gave it", got[0])
	}
	waitFor(t, "the task to be forgotten", func() bool { _, ok := taskKept(t, d, a); return !ok })
}

// keptTaskOnly is agent-01 as AgentBox stopping between making it and
// sending its task left it, which is how agent-861 and agent-864 were left:
// made (ready), its task kept, nothing in its chat.
func keptTaskOnly(t *testing.T, d testDaemon) state.Agent {
	t.Helper()
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Worktrees on VM disk")
	if err := d.srv.store.SaveAgentTask(ctx, state.AgentTask{
		Project: a.Project, Agent: a.Name, Text: longTask, ByLead: true, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	return a
}

// The next daemon sends a task the last one never did: as the agent's first
// message, carried on as a turn the stop cut short, its machine started for
// it. The AI tool gets the task itself, once, and the agent is recorded as
// made, as the create job would have.
func TestALeftTaskIsSentByTheNextDaemon(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	d1 := restartDaemon(t, root, "Running", (&promptTool{}).launch)
	a := keptTaskOnly(t, d1)
	d1.stop()

	tool := &promptTool{}
	d2 := restartDaemon(t, root, "Stopped", tool.launch)
	waitFor(t, "the task to reach the AI tool", func() bool { return carrying(t, tool, taskLine) > 0 })
	waitFor(t, "its turn to end", func() bool {
		turns, err := d2.srv.store.RunningTurns(ctx)
		return err == nil && len(turns) == 0 && d2.srv.chat.State(a.Ref()) == api.ChatReady
	})
	if n := carrying(t, tool, taskLine); n != 1 {
		t.Errorf("the task went in %d prompts, want 1", n)
	}
	if got := promptTexts(t, tool)[0]; !strings.HasSuffix(got, longTask) || strings.Contains(got, resumeText) {
		t.Errorf("the prompt is %q, want the task, not a restart's carry on", got)
	}
	if users := chatTexts(t, d2, a, "user"); len(users) == 0 || users[0] != longTask {
		t.Errorf("the chat's messages are %q, want the task first, shown", users)
	}
	if at := (&wakeTest{testDaemon: d2, log: root + "/incus.log"}).order(t, "incus start ab-hello-stack-agent-01"); at[0] < 0 {
		t.Error("the agent's machine wasn't started to send the task")
	}
	if _, ok := taskKept(t, d2, a); ok {
		t.Error("the task is still kept after it was sent")
	}
	created := eventsOfType(t, d2, "hello-stack", "agent_created")
	if p := payloadOf(t, created[0]); created[0].Agent != a.Name || p["task"] != longTask {
		t.Errorf("agent_created = %+v, want agent-01 with its task", p)
	}
	d2.stop()

	again := &promptTool{}
	d3 := restartDaemon(t, root, "Running", again.launch)
	d3.srv.continueTurns(ctx)
	if n := len(again.prompts); n != 0 {
		t.Errorf("a third daemon sent %d prompts; the task had been sent", n)
	}
}

// With "Continue agents after restarts" off nothing starts by itself, so the
// task waits in the chat; the message that wakes the agent then carries it,
// where on 20 Nov 2026 "Start your task now" was all agent-861 ever read.
func TestAKeptTaskGoesWithTheNextMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tool := &promptTool{}
	d := restartDaemon(t, t.TempDir(), "Running", tool.launch)
	a := keptTaskOnly(t, d)
	off := false
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{ContinueAfterRestart: &off}); err != nil {
		t.Fatal(err)
	}
	d.srv.continueTurns(ctx)
	if n := len(promptTexts(t, tool)); n != 0 {
		t.Fatalf("%d prompts sent with the setting off", n)
	}
	if users := chatTexts(t, d, a, "user"); len(users) != 1 || users[0] != longTask {
		t.Errorf("the chat's messages are %q, want the task waiting in it", users)
	}

	nudge := "You haven't started: Claude Code never ran your first turn. Start your task now."
	if _, err := d.client.SendChat(ctx, a.Ref(), nudge); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the nudge to reach the AI tool", func() bool { return tool.got("Start your task now") })
	got := promptTexts(t, tool)[0]
	if !strings.Contains(got, longTask) || !strings.HasSuffix(got, nudge) {
		t.Errorf("the nudge's prompt is %q, want the task ahead of it", got)
	}
	if _, ok := taskKept(t, d, a); ok {
		t.Error("the task is still kept after the nudge carried it")
	}
}

// stallTool is an AI tool that never opens a session: AgentBox stops after
// the agent's task is in its chat and before any session was told it.
type stallTool struct {
	mu    sync.Mutex
	asked bool
}

func (s *stallTool) launch(context.Context, state.Agent, func(string)) (*chat.Process, error) {
	toTool, fromChat := io.Pipe()
	toChat, fromTool := io.Pipe()
	exited := make(chan struct{})
	var once sync.Once
	acp.NewConn(toTool, fromTool, s)
	return &chat.Process{
		Stdin: fromChat, Stdout: toChat,
		Stop: func() {
			once.Do(func() {
				_ = fromTool.Close()
				_ = toTool.Close()
				close(exited)
			})
		},
		Wait:   func() error { <-exited; return nil },
		Stderr: func() string { return "" },
	}, nil
}

func (s *stallTool) Notify(string, json.RawMessage) {}

func (s *stallTool) Request(method string, _ json.RawMessage, reply func(any, error)) {
	switch method {
	case acp.MethodInitialize:
		reply(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}, nil)
	case acp.MethodSessionNew:
		s.mu.Lock()
		s.asked = true
		s.mu.Unlock()
		// Never answered.
	default:
		reply(nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: method})
	}
}

func (s *stallTool) opening() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asked
}

// Right after create: the task is in the agent's chat and its turn has
// begun, but AgentBox stops before the AI tool has a session to be told it
// in. Carried on, the turn's prompt is the task — not "continue where you
// left off" to a session that never heard of one.
func TestATaskSurvivesAStopBeforeItsPrompt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stall := &stallTool{}
	d1 := restartDaemon(t, root, "Running", stall.launch)
	a := keptTaskOnly(t, d1)
	if _, err := d1.srv.chat.Send(a, longTask); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the AI tool to be asked for a session", stall.opening)
	d1.stop()

	tool := &promptTool{}
	d2 := restartDaemon(t, root, "Running", tool.launch)
	waitFor(t, "the task to reach the AI tool", func() bool { return carrying(t, tool, taskLine) > 0 })
	if got := promptTexts(t, tool)[0]; !strings.HasSuffix(got, longTask) || strings.Contains(got, resumeText) {
		t.Errorf("the carried-on prompt is %q, want the task", got)
	}
	if users := chatTexts(t, d2, a, "user"); len(users) == 0 || users[0] != longTask || strings.Count(strings.Join(users, "\n"), taskLine) != 1 {
		t.Errorf("the chat's messages are %q, want the task once, first", users)
	}
	waitFor(t, "the task to be forgotten", func() bool { _, ok := taskKept(t, d2, a); return !ok })
}

// incompleteAgent is agent-01 as AgentBox stopping while its machine was
// being made left it, which is how agent-862 was left: marked creating, its
// worktree made, its task kept, and its machine half made.
func incompleteAgent(t *testing.T, tool *promptTool) (testDaemon, state.Agent) {
	t.Helper()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), readyOneAgentIncus, testConfig{setup: func(s *Server) {
		s.chat.Launch = tool.launch
		s.chat.Prepare = func(context.Context, state.Agent, string, int64) error { return nil }
	}})
	if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveClaudeToken("", "sk-ant-oat01-x"); err != nil {
		t.Fatal(err)
	}
	a := keptTaskOnly(t, d)
	if err := d.srv.store.SetAgentStatus(ctx, a.Project, a.Name, state.AgentCreating); err != nil {
		t.Fatal(err)
	}
	a.Status = state.AgentCreating
	return d, a
}

// Starting an agent AgentBox left incomplete finishes making it — the
// half-made machine goes and a new one is made — and sends it its task,
// where on 21 Nov 2026 starting agent-862 left it as it was.
func TestStartingAnIncompleteAgentFinishesIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tool := &promptTool{}
	d, a := incompleteAgent(t, tool)
	if _, err := d.client.AgentAction(ctx, a.Ref(), "start"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the task to reach the AI tool", func() bool { return carrying(t, tool, taskLine) > 0 })
	if got := promptTexts(t, tool)[0]; got != longTask {
		t.Errorf("the first prompt is %q, want the task", got)
	}
	made, err := d.srv.store.Agent(ctx, a.Project, a.Name)
	if err != nil || made.Status != state.AgentReady {
		t.Fatalf("agent-01 is %+v, %v; want it made", made.Status, err)
	}
	log, _ := os.ReadFile(filepath.Join(d.root, "incus.log"))
	if !strings.Contains(string(log), "delete --force ab-hello-stack-agent-01") {
		t.Errorf("the half-made machine wasn't removed first; incus log:\n%s", log)
	}
	waitFor(t, "the task to be forgotten", func() bool { _, ok := taskKept(t, d, a); return !ok })
}

// So does a message to it, which goes after the task: the lead's "carry on"
// to an agent left incomplete gets it made and working on what it was
// asked.
func TestAMessageToAnIncompleteAgentFinishesIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tool := &promptTool{}
	d, a := incompleteAgent(t, tool)
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	if _, err := lead.TellAgent(ctx, a.Name, "carry on"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both to reach the AI tool", func() bool { return len(promptTexts(t, tool)) >= 2 })
	got := promptTexts(t, tool)
	if got[0] != longTask || !strings.Contains(got[1], "carry on") || strings.Contains(got[1], taskLine) {
		t.Errorf("the prompts are %q, want the task, then the message", got)
	}
}
