package cli

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/mcp"
)

// create_agent's "ai" decides which AI tool an agent runs. Claude Code is the
// default and what it has always made; OpenCode is offered only where an agent
// could really run it, so asking for it otherwise is refused here, with the
// two things that have to be set up rather than a login error from the daemon.
func TestLeadAI(t *testing.T) {
	for _, c := range []struct {
		ai        string
		ready     bool
		want      string
		wantErrIs string
	}{
		{ai: "", want: "claude"},
		{ai: "claude", want: "claude"},
		{ai: "Claude ", want: "claude"},
		{ai: "opencode", ready: true, want: "opencode"},
		{ai: "OpenCode", ready: true, want: "opencode"},
		{ai: "opencode", ready: false, wantErrIs: "agentbox image build --opencode"},
		{ai: "codex", ready: true, wantErrIs: `ai is "codex"`},
		{ai: "gpt", wantErrIs: `ai is "gpt"`},
	} {
		got, err := leadAI(c.ai, c.ready)
		switch {
		case c.wantErrIs != "":
			if err == nil || !strings.Contains(err.Error(), c.wantErrIs) {
				t.Errorf("leadAI(%q, %v) error = %v, want it to mention %q", c.ai, c.ready, err, c.wantErrIs)
			}
		case err != nil || got != c.want:
			t.Errorf("leadAI(%q, %v) = %q, %v, want %q", c.ai, c.ready, got, err, c.want)
		}
	}
	// The refusal says both halves: an image without OpenCode and a missing
	// login are fixed differently, and the lead can do neither itself.
	_, err := leadAI("opencode", false)
	if err == nil || !strings.Contains(err.Error(), "agentbox auth opencode") {
		t.Errorf("the refusal doesn't mention the login: %v", err)
	}
}

// describeChoices confirms what was chosen back to the lead. The AI tool is
// named only when it isn't the one agents are made on by default.
func TestDescribeChoicesNamesTheTool(t *testing.T) {
	model := "anthropic/claude-sonnet-5"
	if got := describeChoices("opencode", &model, nil, true, "", ""); !strings.Contains(got, "on opencode") || !strings.Contains(got, model) {
		t.Errorf("describeChoices for an OpenCode agent = %q", got)
	}
	if got := describeChoices("claude", nil, nil, true, "", ""); got != "" {
		t.Errorf("describeChoices with nothing chosen = %q, want nothing said", got)
	}
	if got := describeChoices("claude", nil, nil, true, "off", ""); !strings.Contains(got, "notify off") {
		t.Errorf("describeChoices with notify off = %q, want it mentioned", got)
	}
	if got := describeChoices("claude", nil, nil, true, "", "work"); !strings.Contains(got, "work account") {
		t.Errorf("describeChoices with an account chosen = %q, want it mentioned", got)
	}
}

// The project chat's memory tools. These four are what makes memory the
// chat's to curate: it searches, writes down, keeps the current note, and
// reads where the project stands.
func TestProjectMemoryTools(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "lead.sock")
	calls := make(chan string, 16)
	serveFakeLeadAPI(t, socket, calls)
	tools := projectTools(context.Background(), api.NewClient(socket))

	byName := map[string]mcp.Tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	for _, want := range []string{"search_memory", "remember", "update_working_memory", "project_state"} {
		tool, ok := byName[want]
		if !ok {
			t.Fatalf("the project chat has no %s tool", want)
		}
		if tool.Description == "" {
			t.Errorf("%s has no description: it is all a model reads", want)
		}
	}
	// remember is not append_note, and says so: the two are easy to confuse,
	// and the wrong one puts a paragraph in every agent's brief.
	if !strings.Contains(byName["remember"].Description, "append_note") {
		t.Error("remember doesn't tell the model how it differs from append_note")
	}

	if _, err := byName["remember"].Run(json.RawMessage(`{"title":"The API listens on port 7777"}`)); err != nil {
		t.Fatal(err)
	}
	if got := <-calls; got != "POST /v1/project/memory/memories" {
		t.Errorf("remember called %s", got)
	}
	// A memory with no title is refused before the round trip.
	if _, err := byName["remember"].Run(json.RawMessage(`{"title":"  "}`)); err == nil {
		t.Error("a memory with no title was accepted")
	}

	out, err := byName["update_working_memory"].Run(json.RawMessage(`{"current_task":"Move the API off 7777"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Move the API off 7777") {
		t.Errorf("update_working_memory answered %q, want the note it wrote back", out)
	}
	if got := <-calls; got != "PATCH /v1/project/memory/working" {
		t.Errorf("update_working_memory called %s", got)
	}
}

// describeSearch is what a model reads when it searches. A search that found
// nothing has to say so in a way that suggests what to do about it.
func TestDescribeSearch(t *testing.T) {
	empty := describeSearch("port 7777", api.MemorySearchResults{})
	if !strings.Contains(empty, "Nothing remembered") || !strings.Contains(empty, "remember it") {
		t.Errorf("an empty search = %q", empty)
	}
	got := describeSearch("port", api.MemorySearchResults{
		Memories: []api.Memory{{ID: "mem_1", Title: "The API listens on port 7777", Kind: "project", Importance: 5,
			Content: "internal/daemon/server.go\nbinds :7777."}},
		Reports: []api.AgentReport{{Agent: "agent-02", Status: "partial", Summary: "Nothing moved yet."}},
	})
	// The id is there, because following something up needs it, and the
	// content is one line, because a list of results shouldn't become a wall.
	if !strings.Contains(got, "mem_1") || !strings.Contains(got, "internal/daemon/server.go binds :7777.") {
		t.Errorf("describeSearch = %q", got)
	}
	if !strings.Contains(got, "agent-02 (partial)") {
		t.Errorf("describeSearch left out the report: %q", got)
	}
}

// The project chat's notes tools. Editing and removing are it carrying out an
// instruction from the conversation (D82), so what they change is named in the
// call and in the answer, and nothing they do to the user's own text is quiet.
func TestProjectNotesTools(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "lead.sock")
	calls := make(chan string, 16)
	serveFakeLeadAPI(t, socket, calls)

	byName := map[string]mcp.Tool{}
	for _, tool := range projectTools(context.Background(), api.NewClient(socket)) {
		byName[tool.Name] = tool
	}
	for _, want := range []string{"read_notes", "append_note", "edit_note", "remove_note"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("the project chat has no %s tool", want)
		}
	}
	// The description is all a model reads: both say they are for carrying out
	// an instruction, and both say what a quote that can't name one entry does.
	for _, name := range []string{"edit_note", "remove_note"} {
		for _, want := range []string{"when the user asks you to", "quot"} {
			if !strings.Contains(byName[name].Description, want) {
				t.Errorf("%s doesn't say %q:\n%s", name, want, byName[name].Description)
			}
		}
	}
	// append_note names the other store, and the cost that decides between
	// them: a note is in every brief, a memory is only searched.
	for _, want := range []string{"remember", "search_memory", "every agent's"} {
		if !strings.Contains(byName["append_note"].Description, want) {
			t.Errorf("append_note doesn't tell the model %q belongs elsewhere:\n%s", want, byName["append_note"].Description)
		}
	}

	out, err := byName["edit_note"].Run(json.RawMessage(`{"match":"Java 21","text":"the build needs Java 25"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := <-calls; got != "POST /v1/project/notes/edit" {
		t.Errorf("edit_note called %s", got)
	}
	for _, want := range []string{"was: - 2026-09-18: Java 21", "now: the build needs Java 25", "the user's own text"} {
		if !strings.Contains(out, want) {
			t.Errorf("edit_note answered %q, want it to say %q", out, want)
		}
	}

	if out, err = byName["remove_note"].Run(json.RawMessage(`{"match":"Java 21"}`)); err != nil {
		t.Fatal(err)
	}
	if got := <-calls; got != "POST /v1/project/notes/remove" {
		t.Errorf("remove_note called %s", got)
	}
	if !strings.Contains(out, "was: - 2026-09-18: Java 21") || strings.Contains(out, "now:") {
		t.Errorf("remove_note answered %q, want what went and nothing in its place", out)
	}

	// A change nothing names is refused before the round trip: there is no
	// quote to match, so there is nothing to act on.
	for _, args := range []string{`{"match":"  ","text":"x"}`, `{"match":"x","text":"  "}`} {
		if _, err := byName["edit_note"].Run(json.RawMessage(args)); err == nil {
			t.Errorf("edit_note took %s", args)
		}
	}
	if _, err := byName["remove_note"].Run(json.RawMessage(`{"match":"  "}`)); err == nil {
		t.Error("remove_note took a quote of nothing")
	}
}

// serveFakeLeadAPI answers a lead socket's routes, so the tools can be
// exercised without a daemon.
func serveFakeLeadAPI(t *testing.T, socket string, calls chan<- string) {
	t.Helper()
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Only the memory and notes routes are recorded: projectTools reads
		// the project's settings as it starts, and that isn't under test.
		if strings.HasPrefix(r.URL.Path, "/v1/project/memory") || strings.HasPrefix(r.URL.Path, "/v1/project/notes") {
			calls <- r.Method + " " + r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/project/memory/memories":
			_ = json.NewEncoder(w).Encode(api.Memory{ID: "mem_test"})
		case "/v1/project/notes/edit", "/v1/project/notes/remove":
			var req api.EditNoteRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			// The entry is the user's own, so the tools have something to
			// warn about: a change to their text is theirs to be told about.
			_ = json.NewEncoder(w).Encode(api.NoteChange{
				Notes:   api.Notes{Text: "the notes as they now are\n"},
				Was:     "- 2026-09-18: " + req.Match,
				Now:     req.Text,
				Section: "## House rules",
			})
		case "/v1/project/memory/working":
			var patch api.WorkingMemoryPatch
			_ = json.NewDecoder(r.Body).Decode(&patch)
			out := api.WorkingMemory{}
			if patch.CurrentTask != nil {
				out.CurrentTask = *patch.CurrentTask
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			_, _ = w.Write([]byte("{}"))
		}
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// TestAgentDiffIsClippedForTheChat (D87): a diff too long to read whole comes
// back as what it touches and its start, cut at a line, with how to read the
// rest — never the whole thing, which would stay in the chat's context.
func TestAgentDiffIsClippedForTheChat(t *testing.T) {
	big := strings.Repeat("+a line of an enormous change\n", 5000)
	out := clipDiff("agent-21", big, " server.mjs | 5000 ++++\n 1 file changed")
	if len(out) > maxChatDiff {
		t.Errorf("the clipped diff is %d bytes, over %d", len(out), maxChatDiff)
	}
	for _, want := range []string{"agent-21's change is 146 KiB", "server.mjs | 5000", `agent_diff("agent-21", path)`} {
		if !strings.Contains(out, want) {
			t.Errorf("the clipped diff is missing %q", want)
		}
	}
	if !strings.Contains(out, "+a line of an enormous change\n\n[cut here") {
		t.Error("the diff wasn't cut at a line")
	}
}

// TestReadAgentLeavesSubagentsOut (D86, D87): a subagent is one line and its
// words aren't the agent's; a long entry is clipped.
func TestReadAgentLeavesSubagentsOut(t *testing.T) {
	thread := api.ChatThread{Items: []api.ChatItem{
		{ID: "u", Kind: "user", Text: strings.Repeat("do the thing ", 500)},
		{ID: "s", Kind: "subagent", Subagent: &api.ChatSubagent{Name: "Explore", Task: "find the parser", State: "completed"}},
		{ID: "st", Kind: "tool", Parent: "s", Tool: &api.ChatTool{Title: "grep parser", Status: "completed"}},
		{ID: "sa", Kind: "assistant", Parent: "s", Text: "the subagent's report"},
		{ID: "a", Kind: "assistant", Text: "Done: it is in parse.go."},
	}}
	out := describeThread("agent-01", thread, 20)
	if strings.Contains(out, "grep parser") || strings.Contains(out, "the subagent's report") {
		t.Errorf("a subagent's work reached the chat as the agent's:\n%s", out)
	}
	if !strings.Contains(out, "[it started a subagent] Explore: find the parser (completed)") || !strings.Contains(out, "[it said] Done: it is in parse.go.") {
		t.Errorf("read_agent = %s", out)
	}
	if !strings.Contains(out, " […]") || len(out) > 3000 {
		t.Errorf("a %d-character task wasn't clipped: %d characters out", 6500, len(out))
	}
}

// TestDescribeAccounts (D88): list_accounts never says a token, is silent
// about spreading agents when there is only one to spread across, and is
// honest about a window that reset since its reading.
func TestDescribeAccounts(t *testing.T) {
	if out := describeAccounts(nil); !strings.Contains(out, "only one Claude Code account") || !strings.Contains(out, "claude_account can be left out") {
		t.Errorf("describeAccounts(nil) = %q, want the single-account message", out)
	}
	if out := describeAccounts([]api.LeadAccount{{Name: "default", Default: true}}); !strings.Contains(out, "only one Claude Code account") {
		t.Errorf("describeAccounts(one) = %q, want the single-account message", out)
	}

	now := time.Now()
	accounts := []api.LeadAccount{
		{Name: "default", Default: true, Project: true, Agents: 2},
		{Name: "work", Agents: 1, Limit: &api.ClaudeLimit{
			Account: "work", Status: "allowed_warning", At: now,
			Windows: []api.ClaudeLimitWindow{
				{Name: "five_hour", Label: "5-hour", Utilization: 0.87, ResetsAt: now.Add(2 * time.Hour)},
				{Name: "seven_day", Label: "Weekly", Utilization: 0.3, ResetsAt: now.Add(-time.Hour)},
			},
		}},
		{Name: "spare"},
	}
	out := describeAccounts(accounts)
	for _, want := range []string{
		"default", "machine default", "this project's account", "2 running agent(s)",
		"work", "1 running agent(s)", "87% used", "allowed warning",
		"Weekly reset since the reading",
		"spare", "no usage reading yet",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("describeAccounts(...) is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sk-ant") || strings.Contains(out, "token") {
		t.Errorf("describeAccounts(...) said something about a token:\n%s", out)
	}
}

// run_in_agent tells the lead how a command ended and shows what it printed,
// saying so when that is only the end of it (D89).
func TestDescribeRun(t *testing.T) {
	for _, tc := range []struct {
		res  api.LeadRunResult
		want []string
	}{
		{api.LeadRunResult{Output: "ok\n", Bytes: 3}, []string{"Exit code 0.", "\n\nok"}},
		{api.LeadRunResult{ExitCode: 2}, []string{"Exit code 2.", "It printed nothing."}},
		{api.LeadRunResult{TimedOut: true, ExitCode: 124, Output: "tail\n", Bytes: 3 << 20}, []string{"ran out of time", "It printed 3.0 MiB; this is the end of it:", "tail"}},
	} {
		got := describeRun(tc.res)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("describeRun(%+v) = %q, want it to contain %q", tc.res, got, want)
			}
		}
	}
}

// TestCreateAgentNamesTheAgentDefaults: create_agent says what an agent left
// without a model or a window gets — the Agents section of Settings, never the
// Lead's, which is the model the lead reading it runs on and easy to assume.
func TestCreateAgentNamesTheAgentDefaults(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings api.Settings
		project  api.Project
		want     []string
	}{
		{"nothing chosen", api.Settings{DefaultLeadModel: "haiku"}, api.Project{},
			[]string{"Leave this out for opus", "Leave this out for 200k"}},
		{"chosen in Settings", api.Settings{DefaultClaudeModel: "sonnet", DefaultAgentContextWindow: "1000000", DefaultLeadModel: "haiku"}, api.Project{},
			[]string{"Leave this out for sonnet", "Leave this out for 1m"}},
		{"chosen for the project", api.Settings{DefaultClaudeModel: "sonnet"}, api.Project{AgentModel: "claude-fable-5-1"},
			[]string{"Leave this out for claude-fable-5-1, the model this project's settings name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "lead.sock")
			ln, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			mux.HandleFunc("/v1/project/settings", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(tc.settings) })
			mux.HandleFunc("/v1/project", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(tc.project) })
			srv := &http.Server{Handler: mux}
			go func() { _ = srv.Serve(ln) }()
			t.Cleanup(func() { _ = srv.Close() })

			var params string
			for _, tool := range projectTools(context.Background(), api.NewClient(socket)) {
				if tool.Name == "create_agent" {
					raw, _ := json.Marshal(tool.Schema)
					params = string(raw)
				}
			}
			for _, want := range tc.want {
				if !strings.Contains(params, want) {
					t.Errorf("create_agent doesn't say %q:\n%s", want, params)
				}
			}
			if strings.Contains(params, "haiku") {
				t.Error("create_agent names the lead's default model as the agents'")
			}
		})
	}
}
