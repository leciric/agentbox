package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// The prompt chat.Resume sends, and the notice it leaves.
const (
	resumeText    = "AgentBox restarted while you were working; continue where you left off."
	resumedText   = "AgentBox restarted while this turn was running, and resumed it."
	notResumedFor = "AgentBox restarted while this turn was running, and didn't resume it"
)

// hangTool is an AI tool whose turns never end: the daemon is stopped mid-turn.
type hangTool struct {
	mu      sync.Mutex
	prompts int
}

func (h *hangTool) launch(context.Context, state.Agent, func(string)) (*chat.Process, error) {
	toTool, fromChat := io.Pipe()
	toChat, fromTool := io.Pipe()
	exited := make(chan struct{})
	var once sync.Once
	acp.NewConn(toTool, fromTool, h)
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

func (h *hangTool) Notify(string, json.RawMessage) {}

func (h *hangTool) Request(method string, _ json.RawMessage, reply func(any, error)) {
	switch method {
	case acp.MethodInitialize:
		reply(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}, nil)
	case acp.MethodSessionNew:
		reply(map[string]any{"sessionId": "s1"}, nil)
	case acp.MethodSessionPrompt:
		h.mu.Lock()
		h.prompts++
		h.mu.Unlock()
		// Never answered: the turn runs until the daemon goes.
	default:
		reply(nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: method})
	}
}

func (h *hangTool) started() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.prompts > 0
}

func (p *promptTool) count(text string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, prompt := range p.prompts {
		if strings.Contains(prompt, text) {
			n++
		}
	}
	return n
}

// restartDaemon starts a daemon on root whose one agent, agent-01, has a
// machine in the given status and a chat running launch.
func restartDaemon(t *testing.T, root, status string, launch chat.Launcher) testDaemon {
	t.Helper()
	instances := fmt.Sprintf(`[{"name":"ab-hello-stack-agent-01","status":%q,"config":{},"expanded_config":{},`+
		`"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.8.8.2"}]}}}}]`, status)
	return startTestDaemon(t, root, wakeIncus, testConfig{instances: instances, queue: func(s *Server) {
		s.queueEvery = 0
		s.projectShape = func(context.Context, string) (agent.Shape, error) {
			return agent.Shape{Baseline: 4 * gib, Burst: 2 * gib}, nil
		}
		// Set before Run, which is when the turns are carried on.
		s.chat.Launch = launch
		s.chat.Prepare = func(context.Context, state.Agent, string, int64) error { return nil }
	}})
}

func chatTexts(t *testing.T, d testDaemon, a state.Agent, kinds ...string) []string {
	t.Helper()
	th, err := d.srv.chat.Thread(a)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range th.Items {
		for _, k := range kinds {
			if it.Kind == k {
				out = append(out, it.Text)
			}
		}
	}
	return out
}

// A turn AgentBox's stop cut short is carried on by the next daemon, once:
// its machine is started again, the chat is told to continue, and the
// conversation says so. A third daemon finds nothing left to resume.
func TestATurnCutShortByARestartIsResumedOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ctx := context.Background()

	hang := &hangTool{}
	d1 := restartDaemon(t, root, "Running", hang.launch)
	repo := d1.fixtureRepo(t, "hello-stack")
	if _, err := d1.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d1, repo, "hello-stack", "agent-01", "Reminders page")
	if _, err := d1.client.SendChat(ctx, a.Ref(), "Build the reminders page."); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the turn to reach the AI tool", hang.started)
	turns, err := d1.srv.store.RunningTurns(ctx)
	if err != nil || len(turns) != 1 || turns[0].Kind != state.TurnOfAgent || turns[0].Prompt != "Build the reminders page." {
		t.Fatalf("running turns = %+v, %v; want the one turn recorded", turns, err)
	}
	// AgentBox stops mid-turn, and the VM with it: the machine is stopped
	// when the next daemon starts.
	d1.stop()

	tool := &promptTool{}
	d2 := restartDaemon(t, root, "Stopped", tool.launch)
	waitFor(t, "the turn to be carried on", func() bool { return tool.got(resumeText) })
	waitFor(t, "the carried-on turn to end", func() bool {
		turns, err := d2.srv.store.RunningTurns(ctx)
		return err == nil && len(turns) == 0 && d2.srv.chat.State(a.Ref()) == api.ChatReady
	})
	if n := tool.count(resumeText); n != 1 {
		t.Errorf("the chat was told to carry on %d times, want once", n)
	}
	// promptTool can't load a session, so the fresh one is told the
	// conversation it carries on (the context transfer from rollbacks).
	if !tool.got("couldn't be resumed, so this is a new one") || !tool.got("Build the reminders page.") {
		t.Errorf("the fresh session wasn't told the conversation: %q", tool.prompts)
	}
	if at := (&wakeTest{testDaemon: d2, log: root + "/incus.log"}).order(t, "incus start ab-hello-stack-agent-01"); at[0] < 0 {
		t.Error("the agent's machine wasn't started to carry the turn on")
	}
	notices := strings.Join(chatTexts(t, d2, a, "notice"), "\n")
	if !strings.Contains(notices, resumedText) {
		t.Errorf("the chat's notices are %q; want it to say the turn was resumed", notices)
	}
	d2.stop()

	again := &promptTool{}
	d3 := restartDaemon(t, root, "Running", again.launch)
	d3.srv.continueTurns(ctx) // what Run did already, with nothing to do
	if n := len(again.prompts); n != 0 {
		t.Errorf("a third daemon sent %d prompts; the turn had already been carried on", n)
	}
}

// resumeSetup is a daemon whose agent-01 ended a turn, with that turn then
// recorded as one a restart cut short, resumed `resumes` times already.
func resumeSetup(t *testing.T, resumes int) (testDaemon, *promptTool, state.Agent, state.RunningTurn) {
	t.Helper()
	ctx := context.Background()
	tool := &promptTool{}
	d := restartDaemon(t, t.TempDir(), "Running", tool.launch)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	res, err := d.client.SendChat(ctx, a.Ref(), "Build the reminders page.")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the turn to end", func() bool { return d.srv.chat.State(a.Ref()) == api.ChatReady && tool.got("Build the reminders") })
	rt := state.RunningTurn{Project: a.Project, Agent: a.Name, Kind: state.TurnOfAgent, Turn: res.ID,
		Prompt: "Build the reminders page.", StartedAt: time.Now(), Resumes: resumes}
	if err := d.srv.store.SaveRunningTurn(ctx, rt); err != nil {
		t.Fatal(err)
	}
	return d, tool, a, rt
}

func assertNotResumed(t *testing.T, d testDaemon, tool *promptTool, a state.Agent, why string) {
	t.Helper()
	d.srv.continueTurns(context.Background())
	if tool.got(resumeText) {
		t.Error("the turn was carried on")
	}
	notices := strings.Join(chatTexts(t, d, a, "notice"), "\n")
	if !strings.Contains(notices, notResumedFor) || !strings.Contains(notices, why) {
		t.Errorf("the chat's notices are %q; want it to say the turn wasn't resumed, because %s", notices, why)
	}
	if turns, _ := d.srv.store.RunningTurns(context.Background()); len(turns) != 0 {
		t.Errorf("running turns = %+v; want the record dropped", turns)
	}
}

func TestATurnIsntResumedWithTheSettingOff(t *testing.T) {
	t.Parallel()
	d, tool, a, _ := resumeSetup(t, 0)
	off := false
	if _, err := d.client.UpdateSettings(context.Background(), api.UpdateSettingsRequest{ContinueAfterRestart: &off}); err != nil {
		t.Fatal(err)
	}
	assertNotResumed(t, d, tool, a, "is off in Settings")
}

func TestAPausedAgentsTurnIsntResumed(t *testing.T) {
	t.Parallel()
	d, tool, a, _ := resumeSetup(t, 0)
	if err := d.srv.store.SetPausedAt(context.Background(), a.Project, a.Name, time.Now()); err != nil {
		t.Fatal(err)
	}
	assertNotResumed(t, d, tool, a, "paused")
}

// A turn that brought AgentBox down every time it was carried on isn't
// carried on forever.
func TestATurnIsResumedAtMostThreeTimes(t *testing.T) {
	t.Parallel()
	d, tool, a, _ := resumeSetup(t, maxTurnResumes)
	assertNotResumed(t, d, tool, a, "already resumed 3 times")
}

// A message the user sent after the interrupted turn is newer work: the turn
// isn't carried on over it.
func TestATurnWithNewerWorkIsntResumed(t *testing.T) {
	t.Parallel()
	d, tool, a, rt := resumeSetup(t, 0)
	if _, err := d.client.SendChat(context.Background(), a.Ref(), "Never mind, do the settings page."); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the newer turn to end", func() bool { return d.srv.chat.State(a.Ref()) == api.ChatReady && tool.got("settings page") })
	if err := d.srv.store.SaveRunningTurn(context.Background(), rt); err != nil {
		t.Fatal(err)
	}
	d.srv.continueTurns(context.Background())
	if tool.got(resumeText) {
		t.Error("the turn was carried on over a newer message")
	}
	if turns, _ := d.srv.store.RunningTurns(context.Background()); len(turns) != 0 {
		t.Errorf("running turns = %+v; want the record dropped", turns)
	}
}
