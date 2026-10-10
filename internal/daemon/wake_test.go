package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// wakeIncus is statefulIncus that logs every command it is given, in order,
// so a test can see when the machine started against what came after.
const wakeIncus = "exec 9>\"$INCUS_INSTANCES_FILE.lock\"; flock 9\necho \"incus $*\" >> \"$INCUS_LOG\"\n" + statefulIncus

// promptTool is an AI tool that starts a session and ends every turn at
// once, keeping the prompts it was given.
type promptTool struct {
	mu      sync.Mutex
	prompts []string
}

func (p *promptTool) launch(context.Context, state.Agent, func(string)) (*chat.Process, error) {
	toTool, fromChat := io.Pipe()
	toChat, fromTool := io.Pipe()
	exited := make(chan struct{})
	var once sync.Once
	acp.NewConn(toTool, fromTool, p)
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

func (p *promptTool) Notify(string, json.RawMessage) {}

func (p *promptTool) Request(method string, params json.RawMessage, reply func(any, error)) {
	switch method {
	case acp.MethodInitialize:
		reply(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}, nil)
	case acp.MethodSessionNew:
		reply(map[string]any{"sessionId": "s1"}, nil)
	case acp.MethodSessionPrompt:
		p.mu.Lock()
		p.prompts = append(p.prompts, string(params))
		p.mu.Unlock()
		reply(map[string]any{"stopReason": "end_turn"}, nil)
	default:
		reply(nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: method})
	}
}

func (p *promptTool) got(text string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, prompt := range p.prompts {
		if strings.Contains(prompt, text) {
			return true
		}
	}
	return false
}

// wakeTest is a daemon with one project and agent-01, whose machine is
// stopped, and agent-02, whose machine runs. Its chats run promptTool, and
// preparing one's model is logged beside Incus's commands, and fails if the
// machine isn't running.
type wakeTest struct {
	testDaemon
	tool  *promptTool
	agent state.Agent
	log   string
}

func newWakeTest(t *testing.T) *wakeTest {
	t.Helper()
	w := &wakeTest{tool: &promptTool{}}
	instance := func(name, status string) string {
		// The address is there whatever the status, which is all WaitReady
		// waits for once the machine has started.
		return fmt.Sprintf(`{"name":%q,"status":%q,"config":{},"expanded_config":{},`+
			`"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.8.8.2"}]}}}}`, name, status)
	}
	instances := "[" + instance("ab-hello-stack-agent-01", "Stopped") + "," + instance("ab-hello-stack-agent-02", "Running") + "]"
	w.testDaemon = startTestDaemon(t, t.TempDir(), wakeIncus, testConfig{instances: instances})
	w.log = w.root + "/incus.log"
	ctx := context.Background()
	repo := w.fixtureRepo(t, "hello-stack")
	if _, err := w.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	w.agent = addAgent(t, w.testDaemon, repo, "hello-stack", "agent-01", "Reminders page")
	addAgent(t, w.testDaemon, repo, "hello-stack", "agent-02", "Settings page")
	w.srv.chat.Launch = w.tool.launch
	w.srv.chat.Prepare = func(ctx context.Context, a state.Agent, _ string, _ int64) error {
		f, err := os.OpenFile(w.log, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(f, "prepare %s\n", a.Ref())
		_ = f.Close()
		inst, err := w.srv.cfg.Incus.Instance(ctx, a.Instance)
		if err != nil {
			return err
		}
		if inst.Status != "Running" {
			return fmt.Errorf("%s is %s", a.Ref(), inst.Status)
		}
		return nil
	}
	return w
}

// order says where in the log each line starting with one of these first
// appears, -1 where none does.
func (w *wakeTest) order(t *testing.T, prefixes ...string) []int {
	t.Helper()
	data, _ := os.ReadFile(w.log)
	lines := strings.Split(string(data), "\n")
	out := make([]int, len(prefixes))
	for i, prefix := range prefixes {
		out[i] = -1
		for n, line := range lines {
			if strings.HasPrefix(line, prefix) {
				out[i] = n
				break
			}
		}
	}
	return out
}

// The lead tells a stopped agent something: its machine starts, the way
// `agentbox start` starts it, before its chat prepares the model and starts
// the AI tool, which then gets the message.
func TestTellingAStoppedAgentStartsItsMachineFirst(t *testing.T) {
	t.Parallel()
	w := newWakeTest(t)
	ctx := context.Background()
	lead := api.NewClient(w.srv.leadSocketPath("hello-stack"))
	res, err := lead.TellAgent(ctx, "agent-01", "Add a test for the second page.")
	if err != nil {
		t.Fatal(err)
	}
	if res.Woke != "started" || res.Kind != "user" {
		t.Errorf("tell_agent = woke %q, a %s item; want its machine started and the message a turn", res.Woke, res.Kind)
	}
	waitFor(t, "the message to reach the AI tool", func() bool { return w.tool.got("Add a test for the second page.") })
	at := w.order(t, "incus start ab-hello-stack-agent-01", "prepare hello-stack/agent-01")
	if at[0] < 0 || at[1] < 0 || at[0] > at[1] {
		t.Errorf("start at line %d, the chat's preparation at %d: the machine has to start first", at[0], at[1])
	}
	if items := w.notices(t); len(items) != 0 {
		t.Errorf("the chat said %q", items)
	}

	// A running one is told without being started again.
	res, err = lead.TellAgent(ctx, "agent-02", "Look at the settings page.")
	if err != nil || res.Woke != "" {
		t.Fatalf("telling a running agent = woke %q, %v", res.Woke, err)
	}
	waitFor(t, "the message to reach agent-02", func() bool { return w.tool.got("Look at the settings page.") })
	if at := w.order(t, "incus start ab-hello-stack-agent-02"); at[0] >= 0 {
		t.Error("a running agent's machine was started")
	}
}

// A message typed into a stopped agent's chat starts its machine too, at
// once, however many agents already run.
func TestAChatMessageToAStoppedAgentStartsItsMachine(t *testing.T) {
	t.Parallel()
	w := newWakeTest(t)
	ctx := context.Background()
	if _, err := w.client.SendChat(ctx, w.agent.Ref(), "Carry on with the reminders page."); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the message to reach the AI tool", func() bool { return w.tool.got("Carry on with the reminders page.") })
	at := w.order(t, "incus start ab-hello-stack-agent-01", "prepare hello-stack/agent-01")
	if at[0] < 0 || at[0] > at[1] {
		t.Errorf("start at line %d, the chat's preparation at %d: the machine has to start first", at[0], at[1])
	}
}

// notices are what agent-01's chat said on its own: a failure to prepare its
// model, for one.
func (w *wakeTest) notices(t *testing.T) []string {
	t.Helper()
	th, err := w.srv.chat.Thread(w.agent)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range th.Items {
		if it.Kind == "notice" || it.Kind == "error" {
			out = append(out, it.Text)
		}
	}
	return out
}

// An answer to a question the agent stopped waiting on — its machine was
// stopped while it asked — is told to it as a message, starting it first.
func TestAnAnswerNobodyWaitsOnIsToldToTheAgent(t *testing.T) {
	t.Parallel()
	w := newWakeTest(t)
	ctx := context.Background()
	q := state.Question{ID: newID(), Project: "hello-stack", Agent: "agent-01", Text: "Paginate the reminders?",
		Status: state.QuestionPending, CreatedAt: time.Now()}
	if err := w.srv.store.AddQuestion(ctx, q); err != nil {
		t.Fatal(err)
	}
	if _, err := w.srv.answerQuestion(ctx, q.ID, "Yes, twenty a page.", "lead"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the answer to reach the AI tool", func() bool { return w.tool.got("Yes, twenty a page.") })
	at := w.order(t, "incus start ab-hello-stack-agent-01", "prepare hello-stack/agent-01")
	if at[0] < 0 || at[0] > at[1] {
		t.Errorf("start at line %d, the chat's preparation at %d: the machine has to start first", at[0], at[1])
	}
}
