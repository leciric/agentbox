package daemon

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// leadToolDaemon is a daemon with the hello-stack project, two Claude Code
// accounts it may use, and the lead's composer on choice.
func leadToolDaemon(t *testing.T, choice state.Chat) testDaemon {
	t.Helper()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	creds := credentials.Store{Dir: d.paths.Credentials()}
	for _, name := range []string{"default", "work"} {
		if err := creds.SaveClaudeToken(name, "sk-ant-oat01-"+name); err != nil {
			t.Fatal(err)
		}
	}
	addProjectAllowingEvery(t, d, d.fixtureRepo(t, "hello-stack"))
	if choice.Tool != "" || choice.NextTool != "" || choice.Account != "" {
		if err := d.srv.store.SaveChat(context.Background(), "hello-stack", state.LeadName, choice); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func signInCursor(t *testing.T, d testDaemon, names ...string) {
	t.Helper()
	creds := credentials.Store{Dir: d.paths.Credentials()}
	for _, name := range names {
		if err := creds.SaveToolAccount("cursor", "", name, []byte(`{"version":1,"backendUrl":"https://api2.cursor.sh","apiKey":"key_`+name+`","createdAtMs":1}`)); err != nil {
			t.Fatal(err)
		}
	}
}

// checked is what checkCreate makes of a request from the lead.
func checked(t *testing.T, d testDaemon, req api.CreateAgentRequest) api.CreateAgentRequest {
	t.Helper()
	req.Project = "hello-stack"
	got, err := d.srv.checkCreate(context.Background(), req, true)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestNewAgentRunsTheLeadsTool: an agent that names no tool runs the one the
// project's lead runs, on the lead's account for it, for each tool a lead
// can run; a project whose chat never chose one gets Claude Code.
func TestNewAgentRunsTheLeadsTool(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name    string
		choice  state.Chat
		setup   func(t *testing.T, d testDaemon)
		ai      string
		account func(api.CreateAgentRequest) string
		want    string
	}{
		{name: "no chat", ai: "claude", account: func(r api.CreateAgentRequest) string { return r.ClaudeAccount }},
		{name: "claude, its own account", choice: state.Chat{Tool: "claude", Account: "work"}, ai: "claude",
			account: func(r api.CreateAgentRequest) string { return r.ClaudeAccount }, want: "work"},
		{name: "cursor", choice: state.Chat{Tool: "cursor", ToolAccounts: state.AccountMap{"cursor": "team"}},
			setup: func(t *testing.T, d testDaemon) { signInCursor(t, d, "", "team") }, ai: "cursor",
			account: func(r api.CreateAgentRequest) string { return r.CursorAccount }, want: "team"},
		{name: "codex", choice: state.Chat{Tool: "codex"},
			setup: func(t *testing.T, d testDaemon) {
				if err := d.srv.store.SetFlag(ctx, state.SettingImageCodex, true); err != nil {
					t.Fatal(err)
				}
				if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveToolAccount("codex", "", "", []byte(`{"tokens":{}}`)); err != nil {
					t.Fatal(err)
				}
			}, ai: "codex", account: func(r api.CreateAgentRequest) string { return r.CodexAccount }},
		{name: "opencode", choice: state.Chat{Tool: "opencode"},
			setup: func(t *testing.T, d testDaemon) {
				if err := d.srv.store.SetFlag(ctx, state.SettingImageOpenCode, true); err != nil {
					t.Fatal(err)
				}
				if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveToolAccount("opencode", "anthropic", "", []byte(`{"type":"api","key":"sk"}`)); err != nil {
					t.Fatal(err)
				}
			}, ai: "opencode", account: func(r api.CreateAgentRequest) string { return describeOpenCode(r.OpenCodeAccounts) }},
		// A switch the user chose that waits for the chat's next message
		// is already their word on what the agents run.
		{name: "pending switch to cursor", choice: state.Chat{Tool: "claude", NextTool: "cursor"},
			setup: func(t *testing.T, d testDaemon) { signInCursor(t, d, "") }, ai: "cursor",
			account: func(r api.CreateAgentRequest) string { return r.CursorAccount }},
		// The lead's own account for its tool was removed: the project's.
		{name: "cursor, its account gone", choice: state.Chat{Tool: "cursor", ToolAccounts: state.AccountMap{"cursor": "gone"}},
			setup: func(t *testing.T, d testDaemon) { signInCursor(t, d, "") }, ai: "cursor",
			account: func(r api.CreateAgentRequest) string { return r.CursorAccount }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := leadToolDaemon(t, c.choice)
			if c.setup != nil {
				c.setup(t, d)
			}
			got := checked(t, d, api.CreateAgentRequest{Title: "Reminders page", Task: "add it"})
			if got.AI != c.ai || c.account(got) != c.want || got.ToolNotice != "" {
				t.Errorf("agent runs %q on %q (notice %q), want %q on %q", got.AI, c.account(got), got.ToolNotice, c.ai, c.want)
			}
		})
	}
}

func describeOpenCode(m map[string]string) string {
	var parts []string
	for k, v := range m {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

// TestNewAgentFallsBackToClaude: a lead's tool that agents can't run here
// gives Claude Code, with the reason for the reply, rather than a refusal.
func TestNewAgentFallsBackToClaude(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		choice state.Chat
		setup  func(t *testing.T, d testDaemon)
		reason string
	}{
		{name: "cursor, not signed in", choice: state.Chat{Tool: "cursor"}, reason: "Cursor"},
		{name: "opencode, not in the image", choice: state.Chat{Tool: "opencode"},
			setup: func(t *testing.T, d testDaemon) {
				if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveToolAccount("opencode", "anthropic", "", []byte(`{"type":"api","key":"sk"}`)); err != nil {
					t.Fatal(err)
				}
			}, reason: "base image has no OpenCode"},
		{name: "codex, no login", choice: state.Chat{Tool: "codex"},
			setup: func(t *testing.T, d testDaemon) {
				if err := d.srv.store.SetFlag(context.Background(), state.SettingImageCodex, true); err != nil {
					t.Fatal(err)
				}
			}, reason: "Codex"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := leadToolDaemon(t, c.choice)
			if c.setup != nil {
				c.setup(t, d)
			}
			got := checked(t, d, api.CreateAgentRequest{Title: "Reminders page", Task: "add it"})
			if got.AI != "claude" || !strings.Contains(got.ToolNotice, "runs Claude Code") || !strings.Contains(got.ToolNotice, c.reason) {
				t.Errorf("agent runs %q, notice %q; want Claude Code, saying %q", got.AI, got.ToolNotice, c.reason)
			}
		})
	}

	// The reply to create_agent says so, and the agent is made on Claude Code.
	t.Run("reply", func(t *testing.T) {
		t.Parallel()
		d := leadToolDaemon(t, state.Chat{Tool: "cursor"})
		lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
		job, err := lead.CreateProjectAgent(context.Background(), api.CreateAgentRequest{Title: "Reminders page", Task: "add it"})
		if err != nil {
			t.Fatal(err)
		}
		if job.AI != "claude" || !strings.Contains(job.Notice, "not Cursor like you") {
			t.Errorf("reply = %+v, want Claude Code and why", job)
		}
	})
}

// TestNewAgentToolOverrides: a tool, an account or a Claude Code model named
// for the agent wins over the lead's tool, and a model of the lead's tool
// keeps it.
func TestNewAgentToolOverrides(t *testing.T) {
	t.Parallel()
	d := leadToolDaemon(t, state.Chat{Tool: "cursor", Account: "work", ToolAccounts: state.AccountMap{"cursor": "team"}})
	signInCursor(t, d, "", "team")
	model := func(s string) *string { return &s }
	for _, c := range []struct {
		name          string
		req           api.CreateAgentRequest
		ai, claude    string
		cursorAccount string
	}{
		{name: "ai named", req: api.CreateAgentRequest{AI: "claude"}, ai: "claude"},
		{name: "claude account named", req: api.CreateAgentRequest{ClaudeAccount: "default"}, ai: "claude", claude: "default"},
		{name: "cursor account named", req: api.CreateAgentRequest{CursorAccount: "default"}, ai: "cursor", cursorAccount: "default"},
		{name: "claude model named", req: api.CreateAgentRequest{Model: model("sonnet")}, ai: "claude"},
		{name: "cursor model named", req: api.CreateAgentRequest{Model: model("composer-2")}, ai: "cursor", cursorAccount: "team"},
		{name: "cursor's auto", req: api.CreateAgentRequest{Model: model("default")}, ai: "cursor", cursorAccount: "team"},
	} {
		req := c.req
		req.Title, req.Task = "Reminders page", "add it"
		got := checked(t, d, req)
		if got.AI != c.ai || got.ClaudeAccount != c.claude || got.CursorAccount != c.cursorAccount || got.ToolNotice != "" {
			t.Errorf("%s: agent runs %q, Claude Code account %q, Cursor account %q, notice %q; want %q, %q, %q",
				c.name, got.AI, got.ClaudeAccount, got.CursorAccount, got.ToolNotice, c.ai, c.claude, c.cursorAccount)
		}
	}
}

// TestFanOutVariantsFollowTheLead: a group's variant that names no tool runs
// the lead's, and the reply says which ones fell back to Claude Code.
func TestFanOutVariantsFollowTheLead(t *testing.T) {
	t.Parallel()
	d := leadToolDaemon(t, state.Chat{Tool: "cursor"})
	out, err := d.client.FanOut(context.Background(), api.FanOutRequest{
		Project: "hello-stack", Title: "Reminders page", Task: "add it",
		Variants: []api.FanOutVariant{{}, {AI: "claude"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Group.Members[0].AI != "claude" || len(out.Notices) != 1 || !strings.HasPrefix(out.Notices[0], "Agent A: ") {
		t.Errorf("members %+v, notices %q; want A on Claude Code with one notice for it", out.Group.Members, out.Notices)
	}

	signInCursor(t, d, "")
	out, err = d.client.FanOut(context.Background(), api.FanOutRequest{
		Project: "hello-stack", Title: "Login page", Task: "add it",
		Variants: []api.FanOutVariant{{}, {AI: "claude"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Group.Members[0].AI != "cursor" || out.Group.Members[1].AI != "claude" || len(out.Notices) != 0 {
		t.Errorf("members %+v, notices %q; want A on Cursor, B on Claude Code", out.Group.Members, out.Notices)
	}
}

// TestNewAgentRunsTheProjectsTool: a project that names a tool for its agents
// gives it to one that names none before its chat's, and setting it through
// the API clears the defaults that belonged to the tool before.
func TestNewAgentRunsTheProjectsTool(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := leadToolDaemon(t, state.Chat{Tool: "cursor", ToolAccounts: state.AccountMap{"cursor": "team"}})
	signInCursor(t, d, "")
	str := func(v string) *string { return &v }
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{AgentEffort: str("low"), AgentMode: str("auto_review")}); err != nil {
		t.Fatal(err)
	}
	p, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{AgentTool: str("claude"), AgentModel: str("sonnet")})
	if err != nil {
		t.Fatal(err)
	}
	if p.AgentTool != "claude" || p.AgentModel != "sonnet" || p.AgentEffort != "" || p.AgentMode != "" {
		t.Errorf("after choosing the tool: %+v, want claude on sonnet and nothing else", p)
	}
	if got := checked(t, d, api.CreateAgentRequest{}); got.AI != "claude" || got.CursorAccount != "" {
		t.Errorf("an agent that names no tool runs %q on %q, want the project's claude", got.AI, got.CursorAccount)
	}
	// What the agent names still comes first.
	if got := checked(t, d, api.CreateAgentRequest{AI: "cursor"}); got.AI != "cursor" {
		t.Errorf("an agent that names cursor runs %q", got.AI)
	}
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{AgentTool: str("opencode"), AgentEffort: str("high")}); err == nil || !strings.Contains(err.Error(), "has none") {
		t.Errorf("an effort for OpenCode: %v", err)
	}
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{AgentTool: str("vim")}); err == nil {
		t.Error("a tool that doesn't exist was accepted")
	}
	// And none is the chat's tool again.
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{AgentTool: str("")}); err != nil {
		t.Fatal(err)
	}
	if got := checked(t, d, api.CreateAgentRequest{}); got.AI != "cursor" {
		t.Errorf("with no project tool an agent runs %q, want the chat's cursor", got.AI)
	}
	// A project that pins OpenCode names the model too: the lead runs another
	// tool, so there is no model of its to inherit, and OpenCode has no
	// default the adapter may pick.
	if err := d.srv.store.SetProjectAgentDefaults(ctx, "hello-stack", state.ProjectAgentDefaults{Tool: str("opencode")}); err != nil {
		t.Fatal(err)
	}
	if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveToolAccount("opencode", "anthropic", "", []byte(`{"type":"api","key":"sk"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.srv.checkCreate(ctx, api.CreateAgentRequest{Project: "hello-stack", Title: "Reminders page", Task: "add it"}, true); err == nil || !strings.Contains(err.Error(), "needs a model") {
		t.Errorf("pinning OpenCode without a model: %v, want a refusal saying it needs one", err)
	}
	if err := d.srv.store.SetProjectAgentModel(ctx, "hello-stack", "opencode/deepseek-v4.1-flash"); err != nil {
		t.Fatal(err)
	}
	got := checked(t, d, api.CreateAgentRequest{Title: "Reminders page", Task: "add it"})
	if got.AI != "opencode" || got.Model == nil || *got.Model != "opencode/deepseek-v4.1-flash" || got.ToolNotice != "" {
		t.Errorf("agent runs %q on model %v (notice %q), want the project's opencode on its pinned model", got.AI, got.Model, got.ToolNotice)
	}
}

// TestNewAgentInheritsTheLeadsModel: an agent that follows an OpenCode lead
// starts on the exact model the lead's session runs, not the adapter default
// the account may not have enabled.
func TestNewAgentInheritsTheLeadsModel(t *testing.T) {
	t.Parallel()
	d := leadToolDaemon(t, state.Chat{Tool: "opencode", Options: map[string]string{"model": "opencode/deepseek-v4.1-flash"}})
	if err := d.srv.store.SetFlag(context.Background(), state.SettingImageOpenCode, true); err != nil {
		t.Fatal(err)
	}
	if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveToolAccount("opencode", "anthropic", "", []byte(`{"type":"api","key":"sk"}`)); err != nil {
		t.Fatal(err)
	}
	got := checked(t, d, api.CreateAgentRequest{Title: "Reminders page", Task: "add it"})
	if got.AI != "opencode" || got.Model == nil || *got.Model != "opencode/deepseek-v4.1-flash" {
		t.Errorf("agent runs %q on model %v, want opencode on opencode/deepseek-v4.1-flash", got.AI, got.Model)
	}
	// An explicit model for this agent still wins over the lead's.
	chosen := "anthropic/claude-sonnet-5"
	got = checked(t, d, api.CreateAgentRequest{Title: "Reminders page", Task: "add it", Model: &chosen})
	if got.Model == nil || *got.Model != chosen {
		t.Errorf("agent model = %v, want the one in the request %q", got.Model, chosen)
	}
}
