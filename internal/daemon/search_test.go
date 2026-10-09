package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

func TestSearchFindsEveryKind(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	store, mem := d.srv.store, d.srv.memory()
	now := time.Now()

	if err := store.SetAgentTitle(ctx, a.Project, a.Name, "Hatch connector preset"); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.AddTask(ctx, memory.Task{Project: a.Project, Agent: a.Name, Goal: "Add a Hatch preset to the connectors grid"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.AddMemory(ctx, memory.Memory{Project: a.Project, Kind: memory.KindIssue, Title: "Hatch has no remote MCP endpoint", Content: "Blocked until it ships."}); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.AppendEvent(ctx, memory.Event{Project: a.Project, Agent: a.Name, Type: "note", Payload: json.RawMessage(`{"text":"asked about Hatch's URL"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.AddReport(ctx, memory.Report{Project: a.Project, Agent: a.Name, Task: "Hatch preset", Status: "blocked", Summary: "Waiting on the URL."}); err != nil {
		t.Fatal(err)
	}
	// Memory of the Home chat, which has no Memory page to open, is left out.
	if _, err := mem.AddMemory(ctx, memory.Memory{Project: state.HomeProject, Title: "Hatch at home"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddMedia(ctx, state.Media{ID: "m1", Project: a.Project, Agent: a.Name, Kind: "note", Name: "Preset grid", Text: "The Hatch tile sits last.", Source: "agent", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSkill(ctx, state.Skill{Name: "hatch-deploy", Description: "Deploys to Hatch", Enabled: true, UpdatedAt: now}, []state.SkillFile{{Path: "SKILL.md", Content: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetConnector(ctx, state.Connector{Name: "hatch", URL: "https://hatch.example/mcp", Auth: "none", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetConnector(ctx, state.Connector{Project: a.Project, Name: "linear", URL: "https://mcp.linear.app/sse", Auth: "none", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	notesPath := d.srv.cfg.Paths.ProjectNotes(a.Project)
	if err := os.MkdirAll(filepath.Dir(notesPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notesPath, []byte("# Notes\n\n- Use pnpm.\n- Hatch's MCP needs a token.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.AddAgent(ctx, state.Agent{Project: a.Project, Name: state.LeadName, Instance: "ab-hello-stack-lead", Role: state.RoleLead, Status: state.AgentReady, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	chatItem := func(id, kind, text string) []state.ChatItem {
		return []state.ChatItem{{ID: id, Data: []byte(`{"id":"` + id + `","kind":"` + kind + `","text":"` + text + `"}`)}}
	}
	if err := store.SaveChatItems(ctx, a.Project, a.Name, chatItem("c-agent", "assistant", "The Hatch preset is in.")); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveChatItems(ctx, a.Project, state.LeadName, chatItem("c-lead", "user", "Ask Hatch for their URL")); err != nil {
		t.Fatal(err)
	}
	d.srv.pulls.readFor("acme/hello", a.Project)
	d.srv.pulls.mu.Lock()
	d.srv.pulls.byRepo["acme/hello"] = pullsEntry{prs: []api.PullRequest{
		{Number: 271, Title: "feat: a Hatch connector preset", State: "open", URL: "https://github.com/acme/hello/pull/271"},
		{Number: 12, Title: "fix: something else", State: "merged"},
	}}
	d.srv.pulls.mu.Unlock()

	got, err := d.client.Search(ctx, "hatc", 0)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]api.SearchGroup{}
	var order []string
	for _, g := range got.Groups {
		kinds[g.Kind] = g
		order = append(order, g.Kind)
	}
	want := []string{api.SearchAgents, api.SearchChats, api.SearchMemories, api.SearchEvents, api.SearchReports, api.SearchMedia, api.SearchSkills, api.SearchConnectors, api.SearchNotes, api.SearchPulls}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("groups = %v, want %v", order, want)
	}
	if h := kinds[api.SearchAgents].Hits[0]; h.ID != a.Ref() || h.Title != "Hatch connector preset" || h.Tag != a.Branch || !strings.Contains(h.Detail, "connectors grid") {
		t.Errorf("agent hit = %+v", h)
	}
	// An agent's message is its chat's; the lead's is its project's chat.
	chats := map[string]api.SearchHit{}
	for _, h := range kinds[api.SearchChats].Hits {
		chats[h.ID] = h
	}
	if h := chats["c-agent"]; h.Agent != a.Name || h.Project != a.Project || h.Tag != "assistant" || h.Title != "The Hatch preset is in." {
		t.Errorf("agent's chat hit = %+v", h)
	}
	if h := chats["c-lead"]; h.Agent != "" || h.Project != a.Project {
		t.Errorf("lead's chat hit = %+v", h)
	}
	if g := kinds[api.SearchMemories]; len(g.Hits) != 1 || g.Hits[0].Memory == nil || g.Hits[0].Project != a.Project {
		t.Errorf("memories = %+v, want the project's and not the Home chat's", g.Hits)
	}
	if h := kinds[api.SearchEvents].Hits[0]; h.Detail != "asked about Hatch's URL" || h.Event == nil || h.Agent != a.Name {
		t.Errorf("event hit = %+v", h)
	}
	if h := kinds[api.SearchMedia].Hits[0]; h.ID != "m1" || h.Media == nil || h.Media.AgentName != a.Name || h.Media.AgentGone {
		t.Errorf("media hit = %+v", h)
	}
	if h := kinds[api.SearchConnectors].Hits; len(h) != 1 || h[0].ID != "hatch" || h[0].Project != "" {
		t.Errorf("connector hits = %+v, want the AgentBox-wide one", h)
	}
	if h := kinds[api.SearchNotes].Hits; len(h) != 1 || h[0].ID != "4" || h[0].Title != "Hatch's MCP needs a token." {
		t.Errorf("note hits = %+v", h)
	}
	if h := kinds[api.SearchPulls].Hits; len(h) != 1 || h[0].ID != "271" || h[0].Project != a.Project || h[0].Tag != "open" {
		t.Errorf("pull hits = %+v", h)
	}

	// A project by its name, a pull request by its number, and a word in
	// only one place.
	if got, err := d.client.Search(ctx, "hello", 0); err != nil || len(got.Groups) == 0 || got.Groups[0].Kind != api.SearchProjects || got.Groups[0].Hits[0].ID != a.Project {
		t.Errorf("search hello = %+v, %v", got, err)
	}
	if got, err := d.client.Search(ctx, "#27", 0); err != nil || len(got.Groups) != 1 || got.Groups[0].Hits[0].ID != "271" {
		t.Errorf("search #27 = %+v, %v", got, err)
	}
	if got, err := d.client.Search(ctx, "linear", 0); err != nil || len(got.Groups) != 1 || got.Groups[0].Hits[0].Project != a.Project {
		t.Errorf("search linear = %+v, %v", got, err)
	}
	// Too short for the chat index: no chats, the rest still.
	if got, err := d.client.Search(ctx, "ha", 0); err != nil || slices.ContainsFunc(got.Groups, func(g api.SearchGroup) bool { return g.Kind == api.SearchChats }) {
		t.Errorf("search ha = %+v, %v; want no chats", got, err)
	}
	// Every word is required.
	if got, err := d.client.Search(ctx, "hatch zebra", 0); err != nil || len(got.Groups) != 0 {
		t.Errorf("search hatch zebra = %+v, %v; want nothing", got, err)
	}
	if got, err := d.client.Search(ctx, "   ", 0); err != nil || len(got.Groups) != 0 {
		t.Errorf("an empty search = %+v, %v", got, err)
	}
}

func TestSearchLimitsEachGroup(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	for _, name := range []string{"deploy-a", "deploy-b", "deploy-c"} {
		if err := d.srv.store.SetSkill(ctx, state.Skill{Name: name, UpdatedAt: time.Now()}, []state.SkillFile{{Path: "SKILL.md", Content: []byte("x")}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.client.Search(ctx, "deploy", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 1 || len(got.Groups[0].Hits) != 2 || !got.Groups[0].More {
		t.Fatalf("got %+v, want two skills and more", got)
	}
	if _, err := d.client.Search(ctx, "deploy", -1); err != nil {
		t.Errorf("no limit: %v", err)
	}
}

func TestSearchWordsScore(t *testing.T) {
	ws := searchWordsOf("Conn pre")
	if ws.score("Hatch", "something") >= 0 {
		t.Error("a field set without the words matched")
	}
	inName, elsewhere := ws.score("Connector presets"), ws.score("Hatch", "connector presets")
	if inName <= elsewhere || elsewhere < 0 {
		t.Errorf("in the name %d, elsewhere %d: the name should count for more", inName, elsewhere)
	}
	if exact := searchWordsOf("hatch").score("Hatch"); exact <= searchWordsOf("hatch").score("Hatch preset") {
		t.Error("an exact name should outrank a longer one")
	}
}

func TestSearchSnippet(t *testing.T) {
	ws := searchWordsOf("needle")
	if got := ws.snippet("short\n text"); got != "short text" {
		t.Errorf("short = %q", got)
	}
	long := strings.Repeat("hay ", 80) + "the needle is here " + strings.Repeat("straw ", 80)
	got := ws.snippet(long)
	if !strings.Contains(got, "needle") || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") || len([]rune(got)) > snippetRunes+2 {
		t.Errorf("long = %q", got)
	}
	if got := ws.snippet(strings.Repeat("İstanbul ", 40) + "needle"); !strings.Contains(got, "needle") {
		t.Errorf("after runes that change length when lowered = %q", got)
	}
}
