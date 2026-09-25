package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// A project you have never written to has a chat, and that chat has made
// nothing: no row, no worktree, no private HOME, and certainly no machine.
func TestProjectChatCostsNothingUntilUsed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}

	info, err := d.client.ProjectChat(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if info.Started {
		t.Errorf("ProjectChat() = %+v, want it not started", info)
	}
	if info.Ref != "hello-stack/lead" {
		t.Errorf("Ref = %q, want hello-stack/lead", info.Ref)
	}
	if info.Chat != api.ChatOff {
		t.Errorf("Chat = %q, want %q", info.Chat, api.ChatOff)
	}

	// Reading the empty conversation still makes nothing.
	thread, err := d.client.Chat(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Items) != 0 {
		t.Errorf("Chat() = %d items, want none", len(thread.Items))
	}
	if agents, err := d.client.Agents(ctx, ""); err != nil || len(agents) != 0 {
		t.Errorf("Agents() = %+v, %v; want none", agents, err)
	}
	if _, err := os.Stat(d.paths.Worktree("hello-stack", state.LeadName)); !os.IsNotExist(err) {
		t.Errorf("a worktree was made just by looking (stat: %v)", err)
	}
	if _, err := os.Stat(d.paths.LeadHome("hello-stack")); !os.IsNotExist(err) {
		t.Errorf("a private HOME was made just by looking (stat: %v)", err)
	}
	// Nothing was asked of incus: the lead has no machine to make.
	if log, err := os.ReadFile(filepath.Join(root, "incus.log")); err == nil && len(log) > 0 {
		t.Errorf("incus was called: %s", log)
	}
}

// The lead is the project's chat, not one of its agents: it never appears in
// the agent list, and none of the routes that assume a machine accept it.
func TestLeadIsNotReachableAsAnAgent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	// A login, so the lead can be created, then create it as a message would.
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveClaudeToken("", "test-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.srv.manager(nil).EnsureLead(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}

	if agents, err := d.client.Agents(ctx, ""); err != nil || len(agents) != 0 {
		t.Errorf("Agents() = %+v, %v; the lead is not an agent of the project", agents, err)
	}
	info, err := d.client.ProjectChat(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Started || info.BaseRef != "main" {
		t.Errorf("ProjectChat() = %+v, want it started on main", info)
	}

	// Every per-agent route goes through one guard, so checking a few is enough.
	for _, call := range []struct {
		what string
		err  error
	}{
		{"get", func() error { _, err := d.client.Agent(ctx, "hello-stack/lead"); return err }()},
		{"start", func() error { _, err := d.client.AgentAction(ctx, "hello-stack/lead", "start"); return err }()},
		{"diff", func() error { _, err := d.client.Diff(ctx, "hello-stack/lead", false); return err }()},
		{"snapshots", func() error { _, err := d.client.Snapshots(ctx, "hello-stack/lead"); return err }()},
	} {
		if call.err == nil || !strings.Contains(call.err.Error(), "no machine") {
			t.Errorf("%s on the lead = %v, want it refused for having no machine", call.what, call.err)
		}
	}

	// Nor can the chat run a command in itself, or copy from or to itself, as
	// if it had a machine (D89).
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	if _, err := lead.RunInAgent(ctx, state.LeadName, api.LeadRunRequest{Command: "id"}); err == nil || !strings.Contains(err.Error(), "the project's chat") {
		t.Errorf("RunInAgent(lead) = %v, want it refused", err)
	}
	if _, err := lead.CopyBetweenAgents(ctx, api.LeadCopyRequest{From: state.LeadName, Path: "README.md", To: "agent-01"}); err == nil || !strings.Contains(err.Error(), "the project's chat") {
		t.Errorf("CopyBetweenAgents(from the lead) = %v, want it refused", err)
	}
	if _, err := lead.RunInAgent(ctx, "agent-404", api.LeadRunRequest{Command: "id"}); err == nil {
		t.Error("RunInAgent() ran in an agent that doesn't exist")
	}

	// Removing the project takes its chat with it, rather than reporting a
	// leftover agent.
	if err := d.client.RemoveProject(ctx, "hello-stack"); err != nil {
		t.Fatalf("RemoveProject() = %v", err)
	}
	if _, err := os.Stat(d.paths.Worktree("hello-stack", state.LeadName)); !os.IsNotExist(err) {
		t.Errorf("the lead's worktree outlived its project (stat: %v)", err)
	}
}

// Sending to a project whose AgentBox has no Claude Code login fails before
// anything is created, the same way creating an agent does.
func TestProjectChatNeedsALoginBeforeItMakesAnything(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	_, err := d.client.SendChat(ctx, "hello-stack", "hello")
	if err == nil {
		t.Fatal("SendChat() succeeded without a Claude Code login")
	}
	var se *api.StatusError
	if !strings.Contains(err.Error(), "login") {
		t.Errorf("SendChat() = %v, want it to name the missing login", err)
	}
	_ = se
	if _, err := os.Stat(d.paths.Worktree("hello-stack", state.LeadName)); !os.IsNotExist(err) {
		t.Errorf("a worktree was made despite the failure (stat: %v)", err)
	}
}

// Reading a project's chat before its first message must not leave the
// conversation holding the placeholder lead, which has no worktree: the first
// message creates the real one, and that is the record the AI tool is started
// with. The real run caught this as "cwd must be an absolute path".
func TestReadingTheChatFirstDoesNotStaleTheWorktree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	d := startTestDaemon(t, root, fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveClaudeToken("", "test-token"); err != nil {
		t.Fatal(err)
	}
	// Read it first, which caches a conversation for a lead that doesn't exist.
	if _, err := d.client.Chat(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	// Then create it, as the first message does.
	a, err := d.srv.manager(nil).EnsureLead(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.srv.chat.Thread(a); err != nil {
		t.Fatal(err)
	}
	if got := d.srv.chat.AgentFor("hello-stack/lead").Worktree; got != a.Worktree {
		t.Errorf("the conversation holds worktree %q, want %q", got, a.Worktree)
	}
}

// Opening a project's chat starts it, the way the app does, so the menus of
// the lead's AI tool — model, effort, mode — are there before the first
// message. That makes the lead and starts its tool with no turn: the cost of a
// project's chat is paid when it's opened, never when the project is added.
func TestOpeningTheProjectChatStartsItsToolWithoutATurn(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveClaudeToken("", "test-token"); err != nil {
		t.Fatal(err)
	}
	launched := make(chan state.Agent, 1)
	d.srv.chat.Launch = func(_ context.Context, a state.Agent, _ func(string)) (*chat.Process, error) {
		launched <- a
		return nil, errors.New("this test starts no AI tool")
	}

	if _, err := d.client.StartChat(ctx, "hello-stack"); err != nil {
		t.Fatalf("StartChat() = %v", err)
	}
	select {
	case a := <-launched:
		if !a.IsLead() || a.Worktree != d.paths.Worktree("hello-stack", state.LeadName) {
			t.Errorf("launched %+v, want the project's lead in its own worktree", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening the chat started no AI tool")
	}
	info, err := d.client.ProjectChat(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Started {
		t.Errorf("ProjectChat() = %+v, want the lead made", info)
	}
	thread, err := d.client.Chat(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range thread.Items {
		if it.Kind == "user" {
			t.Errorf("opening the chat sent a message: %+v", it)
		}
	}
}
