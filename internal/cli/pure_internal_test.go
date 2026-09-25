package cli

// Tests for the small formatting and parsing functions behind the commands:
// the exact text a command prints, and the exact errors bad arguments get.
// These don't need a daemon, so they run straight against the functions that
// build a command's output from an API answer.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// outCmd is a bare *cobra.Command whose stdout is the returned buffer, for
// functions that only write to cmd.OutOrStdout().
func outCmd() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	return cmd, &buf
}

func TestRenderTopShowsTheHostAndEveryAgent(t *testing.T) {
	var buf bytes.Buffer
	usage := api.Usage{
		Host: api.HostUsage{CPU: 42.3, Cores: 8, MemUsed: 4 << 30, MemTotal: 16 << 30, PoolUsed: 10 << 30, PoolTotal: 100 << 30},
		Agents: []api.AgentUsage{
			{Ref: "pawly/agent-01", State: "running", CPU: 150, Memory: 512 << 20, Processes: 12, Cores: 4, Limits: api.Limits{CPU: "4", Memory: "8GiB"}},
			{Ref: "pawly/agent-02", State: "paused", CPU: 0, Memory: 100 << 20, Processes: 1},
		},
	}
	if err := renderTop(&buf, usage); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"HOST   CPU 42%", "MEMORY 4.0 GiB / 16.0 GiB", "DISK POOL 10.0 GiB used, 90.0 GiB free",
		"pawly/agent-01", "38% of 4 cores", // 150/4 = 37.5 -> rounds to 38
		"pawly/agent-02", "512.0 MiB / 8GiB",
		"100.0 MiB / no limit", // agent-02 has no memory limit
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renderTop missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	if err := renderTop(&buf, api.Usage{Host: api.HostUsage{Cores: 4}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No agents.") {
		t.Errorf("renderTop with nobody running = %q", buf.String())
	}
}

func TestRenderTokensAndTurns(t *testing.T) {
	var buf bytes.Buffer
	since := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	report := api.TokenReport{
		Since:       &since,
		TokenCounts: api.TokenCounts{Total: 1_500_000},
		Agents: []api.AgentTokens{
			{Ref: "pawly/agent-01", Exists: true, TokenCounts: api.TokenCounts{Total: 1_500_000, CostUSD: 3.4567}, Turns: 5, MaxContext: 120_000, LastAt: since,
				Models: []api.ModelTokens{{Model: "sonnet-5", TokenCounts: api.TokenCounts{Total: 1_500_000, CostUSD: 3.4567}}}},
		},
	}
	if err := renderTokens(&buf, report, true); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"1.5M tokens", "pawly/agent-01", "$3.46", "sonnet-5"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderTokens missing %q:\n%s", want, out)
		}
	}

	// An agent that's gone still shows, marked so.
	buf.Reset()
	report.Agents[0].Exists = false
	renderTokens(&buf, report, false)
	if !strings.Contains(buf.String(), "pawly/agent-01 (gone)") {
		t.Errorf("a retired agent isn't marked gone:\n%s", buf.String())
	}

	buf.Reset()
	if err := renderTokens(&buf, api.TokenReport{}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Nothing spent since the ledger began.") {
		t.Errorf("an empty report = %q", buf.String())
	}

	buf.Reset()
	lines := []api.TokenTurn{{Project: "pawly", Agent: "agent-01", At: since, Kind: "chat", Model: "sonnet-5", TokenCounts: api.TokenCounts{Total: 900, CostUSD: 0.001}}}
	if err := renderTokenTurns(&buf, lines, true); err != nil {
		t.Fatal(err)
	}
	out = buf.String()
	if !strings.Contains(out, "AGENT") || !strings.Contains(out, "WHEN") || !strings.Contains(out, "pawly/agent-01") || !strings.Contains(out, "<$0.01") {
		t.Errorf("renderTokenTurns = %q", out)
	}

	buf.Reset()
	renderTokenTurns(&buf, nil, true)
	if !strings.Contains(buf.String(), "Nothing in the ledger yet.") {
		t.Errorf("renderTokenTurns with nothing = %q", buf.String())
	}
}

func TestHumanTokensAndUSD(t *testing.T) {
	for _, c := range []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{950, "950"},
		{12_400, "12.4K"},
		{1_300_000, "1.3M"},
		{2_500_000_000, "2.50B"},
	} {
		if got := humanTokens(c.n); got != c.want {
			t.Errorf("humanTokens(%d) = %q, want %q", c.n, got, c.want)
		}
	}
	for _, c := range []struct {
		v    float64
		want string
	}{
		{0, "-"},
		{0.001, "<$0.01"},
		{3.456, "$3.46"},
	} {
		if got := usd(c.v); got != c.want {
			t.Errorf("usd(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestParseStretch(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want time.Duration
	}{
		{"7d", 7 * 24 * time.Hour},
		{"5h", 5 * time.Hour},
		{"90m", 90 * time.Minute},
	} {
		d, err := parseStretch(c.raw)
		if err != nil || d != c.want {
			t.Errorf("parseStretch(%q) = %v, %v; want %v", c.raw, d, err, c.want)
		}
	}
	for _, bad := range []string{"", "0d", "-5h", "soon", "0"} {
		if _, err := parseStretch(bad); err == nil {
			t.Errorf("parseStretch(%q) accepted, want an error naming the choices", bad)
		} else if !strings.Contains(err.Error(), "isn't a stretch of time") {
			t.Errorf("parseStretch(%q) error = %v", bad, err)
		}
	}
}

func TestFormatEvent(t *testing.T) {
	ts := time.Date(2026, 9, 20, 10, 30, 0, 0, time.Local)
	job := api.Job{ID: "j1", Kind: "create", Target: "pawly/agent-01", Status: api.JobFailed, Error: "boom"}
	line := formatEvent(api.Event{Type: api.EventJob, Time: ts, Data: mustJSON(t, job)})
	if !strings.Contains(line, "job    create pawly/agent-01 failed [j1]: boom") {
		t.Errorf("formatEvent(job) = %q", line)
	}

	ch := api.AgentChange{Ref: "pawly/agent-01", Removed: true}
	line = formatEvent(api.Event{Type: api.EventAgent, Time: ts, Data: mustJSON(t, ch)})
	if !strings.Contains(line, "agent  pawly/agent-01 removed") {
		t.Errorf("formatEvent(agent removed) = %q", line)
	}

	u := api.Usage{Host: api.HostUsage{CPU: 10, MemUsed: 1 << 30}}
	line = formatEvent(api.Event{Type: api.EventUsage, Time: ts, Data: mustJSON(t, u)})
	if !strings.Contains(line, "usage  host cpu 10% mem 1.0 GiB") {
		t.Errorf("formatEvent(usage) = %q", line)
	}

	line = formatEvent(api.Event{Type: "mystery", Time: ts, Data: []byte(`"x"`)})
	if !strings.Contains(line, "mystery") {
		t.Errorf("formatEvent(unknown) = %q", line)
	}
}

func TestTook(t *testing.T) {
	created := time.Now().Add(-90 * time.Second)
	finished := created.Add(90 * time.Second)
	j := api.Job{CreatedAt: created, FinishedAt: &finished}
	if got := took(j); got != "1m30s" {
		t.Errorf("took(finished) = %q, want 1m30s", got)
	}
	// A job still running is timed against now, so it's at least as long as
	// the time since it started.
	running := api.Job{CreatedAt: time.Now().Add(-time.Second)}
	if got := took(running); got == "0s" {
		t.Errorf("took(running) = %q, want more than 0s", got)
	}
}

func TestPrintAllowedAndAllowedAccounts(t *testing.T) {
	cmd, buf := outCmd()
	printAllowed(buf, api.Project{Name: "pawly"})
	if !strings.Contains(buf.String(), "pawly may use every Claude Code account") {
		t.Errorf("printAllowed with none set = %q", buf.String())
	}
	buf.Reset()
	printAllowed(buf, api.Project{Name: "pawly", ClaudeAccounts: []string{"work", "personal"}})
	if !strings.Contains(buf.String(), "pawly may use the Claude Code accounts work, personal") {
		t.Errorf("printAllowed with a list = %q", buf.String())
	}
	_ = cmd

	// --allow-all always clears the list, without touching the daemon.
	list, err := allowedAccounts(nil, nil, "pawly", true, false, nil, nil, nil)
	if err != nil || list == nil || len(list) != 0 {
		t.Errorf("allowedAccounts(all) = %v, %v; want an empty, non-nil list", list, err)
	}

	// Replacing the list outright, from --allow.
	list, err = allowedAccounts(nil, nil, "pawly", false, true, []string{"work"}, []string{"personal"}, []string{"work"})
	if err != nil || len(list) != 1 || list[0] != "personal" {
		t.Errorf("allowedAccounts(replace) = %v, %v", list, err)
	}

	// Removing everything from an explicit list is refused: it would leave no account.
	if _, err := allowedAccounts(nil, nil, "pawly", false, true, []string{"work"}, nil, []string{"work"}); err == nil ||
		!strings.Contains(err.Error(), "no Claude Code account at all") {
		t.Errorf("allowedAccounts emptied by --allow-remove: got %v, want an error naming --allow-all", err)
	}
}

func TestWaitingForAndAutonomyWords(t *testing.T) {
	for _, c := range []struct {
		status, want string
	}{
		{"pending", "waiting for the project's chat"},
		{"escalated", "waiting for you"},
		{"answered", "answered"},
	} {
		if got := waitingFor(api.Question{Status: c.status}); got != c.want {
			t.Errorf("waitingFor(%q) = %q, want %q", c.status, got, c.want)
		}
	}
	if got := autonomyWords("on"); !strings.Contains(got, "its chat acts on what it decides") {
		t.Errorf("autonomyWords(on) = %q", got)
	}
	if got := autonomyWords("ask"); !strings.Contains(got, "does the routine itself") {
		t.Errorf("autonomyWords(ask) = %q", got)
	}
}

func TestLimitWords(t *testing.T) {
	if got := limitWords(api.Limits{}); !strings.Contains(got, "every core") || !strings.Contains(got, "no memory limit") {
		t.Errorf("limitWords(no limits) = %q", got)
	}
	if got := limitWords(api.Limits{CPU: "4", Memory: "8GiB"}); !strings.Contains(got, "4") || !strings.Contains(got, "8GiB") {
		t.Errorf("limitWords(capped) = %q", got)
	}
}

func TestPrintBrowser(t *testing.T) {
	cmd, buf := outCmd()
	printBrowser(cmd, api.BrowserStatus{})
	if !strings.Contains(buf.String(), "The browser isn't running") {
		t.Errorf("printBrowser(nothing) = %q", buf.String())
	}
	buf.Reset()
	printBrowser(cmd, api.BrowserStatus{Display: true})
	if !strings.Contains(buf.String(), "the desktop is") {
		t.Errorf("printBrowser(display only) = %q", buf.String())
	}
	buf.Reset()
	printBrowser(cmd, api.BrowserStatus{Running: true, Version: "128.0", Pages: []api.BrowserPage{{URL: "http://localhost:3000", Title: "App"}}})
	out := buf.String()
	if !strings.Contains(out, "running (128.0)") || !strings.Contains(out, "http://localhost:3000") || !strings.Contains(out, "App") {
		t.Errorf("printBrowser(running) = %q", out)
	}
}

func TestOptionalRef(t *testing.T) {
	if got := optionalRef(nil); got != "" {
		t.Errorf("optionalRef(nil) = %q", got)
	}
	if got := optionalRef([]string{"pawly/agent-01"}); got != "pawly/agent-01" {
		t.Errorf("optionalRef = %q", got)
	}
}

func TestTopCommand(t *testing.T) {
	root := &cobra.Command{Use: "agentbox"}
	auth := &cobra.Command{Use: "auth"}
	claude := &cobra.Command{Use: "claude"}
	root.AddCommand(auth)
	auth.AddCommand(claude)
	if got := topCommand(claude); got != "auth" {
		t.Errorf("topCommand(auth claude) = %q, want auth", got)
	}
	if got := topCommand(root); got != "agentbox" {
		t.Errorf("topCommand(root) = %q, want agentbox", got)
	}
}

func TestGitHubErrorLine(t *testing.T) {
	for _, c := range []struct {
		name string
		err  api.GitHubError
		want string
	}{
		{"no access", api.GitHubError{Kind: api.GitHubNoAccess, Account: "work", Login: "octocat", Repo: "leciric/agentbox"},
			`the account work (octocat) can't see leciric/agentbox. Pick another one with agentbox github-account`},
		{"bad token", api.GitHubError{Kind: api.GitHubBadToken, Account: "work"},
			`GitHub refused the account work. Save its token again with agentbox auth github --account work`},
		{"no account", api.GitHubError{Kind: api.GitHubNoAccount, Message: "no GitHub account is stored"},
			`no GitHub account is stored. Add one with agentbox auth github --account <name>`},
		{"other", api.GitHubError{Kind: "otherErr", Message: "GitHub is down"}, "GitHub is down"},
	} {
		if got := githubErrorLine(&c.err); !strings.Contains(got, c.want) {
			t.Errorf("%s: githubErrorLine = %q, want it to contain %q", c.name, got, c.want)
		}
	}
}

func TestFleetFormattingHelpers(t *testing.T) {
	if got := dash(""); got != "-" {
		t.Errorf("dash(\"\") = %q", got)
	}
	if got := dash("x"); got != "x" {
		t.Errorf("dash(x) = %q", got)
	}
	if got := titleSuffix(""); got != "" {
		t.Errorf("titleSuffix(\"\") = %q", got)
	}
	if got := titleSuffix("Fix login"); got != " (Fix login)" {
		t.Errorf("titleSuffix = %q", got)
	}
	if got := mediaCount(0); got != "-" {
		t.Errorf("mediaCount(0) = %q", got)
	}
	if got := mediaCount(3); got != "3" {
		t.Errorf("mediaCount(3) = %q", got)
	}
	if got := changes(api.AgentChanges{}); got != "-" {
		t.Errorf("changes(none) = %q", got)
	}
	if got := changes(api.AgentChanges{Files: 1, Insertions: 5, Deletions: 2}); got != "1 file +5/-2" {
		t.Errorf("changes(one file) = %q", got)
	}
	if got := changes(api.AgentChanges{Files: 3, Dirty: true}); got != "3 files*" {
		t.Errorf("changes(dirty) = %q", got)
	}
	if got := pullRequest(nil); got != "-" {
		t.Errorf("pullRequest(nil) = %q", got)
	}
	pr := &api.PullRequest{Number: 42, State: "open", Draft: true, Checks: "failing", Comments: 2}
	if got := pullRequest(pr); got != "#42 open (draft), checks failing, 2 comment(s)" {
		t.Errorf("pullRequest = %q", got)
	}
	if got := chatDoing(api.ChatRunning); got != "working" {
		t.Errorf("chatDoing(running) = %q", got)
	}
	if got := chatDoing(api.ChatWaiting); got != "waiting for you" {
		t.Errorf("chatDoing(waiting) = %q", got)
	}
	if got := chatDoing("mystery"); got != "" {
		t.Errorf("chatDoing(mystery) = %q", got)
	}
	if got := doingOrIdle(api.FleetAgent{Idle: true}); got != "idle, holding a machine" {
		t.Errorf("doingOrIdle(idle) = %q", got)
	}
}

func TestPrintFleet(t *testing.T) {
	cmd, buf := outCmd()
	printFleet(cmd, api.Fleet{Project: "pawly"})
	if !strings.Contains(buf.String(), "pawly has no agents yet. Create one with: agentbox create pawly") {
		t.Errorf("printFleet(empty) = %q", buf.String())
	}

	buf.Reset()
	printFleet(cmd, api.Fleet{
		Project: "pawly",
		Agents: []api.FleetAgent{{
			Agent:   api.Agent{Ref: "pawly/agent-01", Name: "agent-01", Title: "Fix login"},
			Changes: api.AgentChanges{Files: 2, Insertions: 10, Deletions: 1},
			PR:      &api.PullRequest{Number: 7, State: "open"},
		}},
		Idle:          2,
		GitHub:        "leciric/agentbox",
		GitHubAccount: "work",
	})
	out := buf.String()
	for _, want := range []string{
		"agent-01", "Fix login", "2 files +10/-1", "#7 open",
		"2 agent(s) finished and are holding a machine. Free them with: agentbox retire pawly",
		"GitHub: leciric/agentbox as work",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("printFleet missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	printFleet(cmd, api.Fleet{
		Project:     "pawly",
		Agents:      []api.FleetAgent{{Agent: api.Agent{Ref: "pawly/agent-01", Name: "agent-01"}}},
		GitHubError: &api.GitHubError{Kind: api.GitHubBadToken, Account: "work"},
	})
	if !strings.Contains(buf.String(), "GitHub refused the account work") {
		t.Errorf("printFleet with a GitHub error = %q", buf.String())
	}
}

func TestPrintRetired(t *testing.T) {
	cmd, buf := outCmd()
	printRetired(cmd, api.RetireResult{How: api.RetireStop})
	if !strings.Contains(buf.String(), "Nothing to retire: no agent has finished and is holding a machine.") {
		t.Errorf("printRetired(nothing) = %q", buf.String())
	}

	buf.Reset()
	printRetired(cmd, api.RetireResult{
		How: api.RetireDestroy,
		Retired: []api.RetiredAgent{
			{Name: "agent-01", Title: "Fix login", Branch: "agentbox/agent-01"},
		},
		Skipped: []api.RetiredAgent{{Name: "agent-02", Reason: "still working"}},
	})
	out := buf.String()
	for _, want := range []string{
		"Destroyed agent-01 (Fix login) — its work stays on agentbox/agent-01",
		"Left agent-02: still working",
		"Their branches are still there. Start the next task with a new agent.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("printRetired missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	printRetired(cmd, api.RetireResult{How: api.RetireStop, DryRun: true, Retired: []api.RetiredAgent{{Name: "agent-03"}}})
	if !strings.Contains(buf.String(), "Would stop agent-03") {
		t.Errorf("printRetired(dry run) = %q", buf.String())
	}
}

func TestPrintAgent(t *testing.T) {
	cmd, buf := outCmd()
	printAgent(cmd, api.Agent{
		Ref: "pawly/agent-01", Title: "Fix login", AI: "claude", Autonomous: true, Interface: "chat",
		ClaudeAccount: "work", GitHubAccount: "personal", Source: "the base image",
		Branch: "agentbox/fix-login", BaseRef: "main", Worktree: "/home/x/worktrees/pawly/agent-01", IP: "10.0.0.5",
	}, 3*time.Second)
	out := buf.String()
	for _, want := range []string{
		"Agent pawly/agent-01 ready in 3s", "title", "Fix login", "claude (autonomous)", "interface",
		"work (Claude Code)", "personal (GitHub)", "copy of the base image",
		"agentbox/fix-login (from main)", "/home/x/worktrees/pawly/agent-01", "10.0.0.5",
		"Chat with it in the app, or: agentbox chat pawly/agent-01",
		"Attach with: agentbox shell pawly/agent-01",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("printAgent missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	printAgent(cmd, api.Agent{Ref: "pawly/agent-02", AI: "none", Source: "the base image", Branch: "agentbox/agent-02", BaseRef: "main"}, time.Second)
	out = buf.String()
	if strings.Contains(out, "interface") {
		t.Errorf("an AI-less agent shouldn't show an interface line:\n%s", out)
	}
	if strings.Contains(out, "Chat with it") {
		t.Errorf("an AI-less agent shouldn't be offered chat:\n%s", out)
	}
}

func TestNotRunning(t *testing.T) {
	if err := notRunning(api.Agent{Ref: "pawly/agent-01", State: "paused"}); !strings.Contains(err.Error(), "is paused: run agentbox resume pawly/agent-01") {
		t.Errorf("notRunning(paused) = %v", err)
	}
	if err := notRunning(api.Agent{Ref: "pawly/agent-01", State: "stopped"}); !strings.Contains(err.Error(), "is stopped: run agentbox start pawly/agent-01") {
		t.Errorf("notRunning(stopped) = %v", err)
	}
}

func TestDescribeAI(t *testing.T) {
	if got := describeAI("claude", true); got != "claude (autonomous)" {
		t.Errorf("describeAI(autonomous) = %q", got)
	}
	if got := describeAI("claude", false); got != "claude" {
		t.Errorf("describeAI(not autonomous) = %q", got)
	}
}

func TestPrintSavedAndMediaAgent(t *testing.T) {
	cmd, buf := outCmd()
	printSaved(cmd, api.MediaItem{Name: "empty-state", ID: "med_1", Kind: "screenshot", Size: 2048})
	if !strings.Contains(buf.String(), `Saved "empty-state" (screenshot, 2.0 KiB) as med_1`) {
		t.Errorf("printSaved = %q", buf.String())
	}
	buf.Reset()
	printSaved(cmd, api.MediaItem{Name: "a note", ID: "med_2", Kind: "note"})
	if !strings.Contains(buf.String(), `Saved "a note" (note) as med_2`) {
		t.Errorf("printSaved(no size) = %q", buf.String())
	}

	if got := mediaAgent(api.MediaItem{Agent: "pawly/agent-01"}); got != "agent-01" {
		t.Errorf("mediaAgent(bare) = %q", got)
	}
	if got := mediaAgent(api.MediaItem{AgentName: "agent-01", AgentTitle: "Fix login"}); got != "agent-01 · Fix login" {
		t.Errorf("mediaAgent(titled) = %q", got)
	}
	if got := mediaAgent(api.MediaItem{AgentName: "agent-01", AgentGone: true}); got != "agent-01 (removed)" {
		t.Errorf("mediaAgent(gone) = %q", got)
	}

	if got := recordInput("desktop"); !strings.Contains(got, "keys and mouse") {
		t.Errorf("recordInput(desktop) = %q", got)
	}
	if got := recordInput("playwright"); got != "" {
		t.Errorf("recordInput(playwright) = %q, want empty", got)
	}
}

func TestShort(t *testing.T) {
	if got := short("abc123def456"); got != "abc123d" {
		t.Errorf("short(long) = %q, want the first 7 characters", got)
	}
	if got := short("abc"); got != "abc" {
		t.Errorf("short(short) = %q, want it unchanged", got)
	}
}

func TestVersionAndExitCodeError(t *testing.T) {
	old := version
	version = "1.2.3"
	t.Cleanup(func() { version = old })
	if got := Version(); got != "1.2.3" {
		t.Errorf("Version() = %q, want 1.2.3", got)
	}
	if got := exitCodeError(42).Error(); got != "exit status 42" {
		t.Errorf("exitCodeError(42).Error() = %q", got)
	}
}

func TestWindowChoice(t *testing.T) {
	if got := windowChoice(nil); got != "" {
		t.Errorf("windowChoice(nil) = %q, want empty", got)
	}
	window := "1m"
	if got := windowChoice(&window); got != ", with a 1m context window" {
		t.Errorf("windowChoice(1m) = %q", got)
	}
}

func TestPrintAndroid(t *testing.T) {
	cmd, buf := outCmd()
	printAndroid(cmd, api.AndroidStatus{Available: true})
	if !strings.Contains(buf.String(), "isn't running") {
		t.Errorf("printAndroid(off) = %q", buf.String())
	}
	buf.Reset()
	printAndroid(cmd, api.AndroidStatus{Available: true, Running: true})
	if !strings.Contains(buf.String(), "is starting") {
		t.Errorf("printAndroid(starting) = %q", buf.String())
	}
	buf.Reset()
	printAndroid(cmd, api.AndroidStatus{Available: true, Running: true, Booted: true, Device: "emulator-5554"})
	if !strings.Contains(buf.String(), "is running: emulator-5554") {
		t.Errorf("printAndroid(booted) = %q", buf.String())
	}
	buf.Reset()
	printAndroid(cmd, api.AndroidStatus{Available: false, Problem: "no KVM"})
	if !strings.Contains(buf.String(), "can't run emulators yet: no KVM") {
		t.Errorf("printAndroid(unavailable) = %q", buf.String())
	}
}

func TestChatPrinterItems(t *testing.T) {
	var buf bytes.Buffer
	p := newChatPrinter(&buf)

	p.item(api.ChatItem{ID: "1", Kind: "user", Text: "Hello"})
	if !strings.Contains(buf.String(), "› Hello\n\n") {
		t.Errorf("user item = %q", buf.String())
	}
	// Shown once: printing the same item again adds nothing.
	before := buf.Len()
	p.item(api.ChatItem{ID: "1", Kind: "user", Text: "Hello"})
	if buf.Len() != before {
		t.Errorf("a user item was shown twice: %q", buf.String())
	}

	buf.Reset()
	p.item(api.ChatItem{ID: "2", Kind: "aside", Text: "Also this", Delivery: api.ChatAsideDeferred})
	if !strings.Contains(buf.String(), "› Also this  (waiting for this turn to end)\n\n") {
		t.Errorf("aside item = %q", buf.String())
	}

	buf.Reset()
	p.item(api.ChatItem{ID: "3", Kind: "assistant", Text: "Hi there", Streaming: true})
	p.item(api.ChatItem{ID: "3", Kind: "assistant", Text: "Hi there, more", Streaming: false})
	if buf.String() != "Hi there, more\n\n" {
		t.Errorf("streamed assistant item = %q", buf.String())
	}

	buf.Reset()
	p.item(api.ChatItem{ID: "4", Kind: "tool", Tool: &api.ChatTool{Title: "Reading main.go", Status: "completed"}})
	p.item(api.ChatItem{ID: "5", Kind: "tool", Tool: &api.ChatTool{Kind: "execute", Command: "go test ./...", Status: "failed"}})
	p.item(api.ChatItem{ID: "6", Kind: "tool", Tool: &api.ChatTool{Title: "Editing x.go", Status: "stopped"}})
	out := buf.String()
	for _, want := range []string{"✓ Reading main.go", "✗ go test ./...", "◼ Editing x.go (stopped)"} {
		if !strings.Contains(out, want) {
			t.Errorf("tool items missing %q: %q", want, out)
		}
	}

	buf.Reset()
	p.item(api.ChatItem{ID: "7", Parent: "3", Kind: "tool", Tool: &api.ChatTool{Title: "Subagent's tool", Status: "completed"}})
	if !strings.Contains(buf.String(), "      ✓ Subagent's tool") {
		t.Errorf("a subagent's tool call isn't indented under it: %q", buf.String())
	}
	buf.Reset()
	p.item(api.ChatItem{ID: "8", Parent: "3", Kind: "assistant", Text: "hidden"})
	if buf.Len() != 0 {
		t.Errorf("a subagent's own words leaked into the transcript: %q", buf.String())
	}

	buf.Reset()
	p.item(api.ChatItem{ID: "9", Kind: "subagent", Subagent: &api.ChatSubagent{Name: "Explore", Task: "find the config", State: "running"}})
	if !strings.Contains(buf.String(), "⧉ subagent Explore: find the config") {
		t.Errorf("subagent card = %q", buf.String())
	}
	buf.Reset()
	p.item(api.ChatItem{ID: "10", Kind: "subagent", Subagent: &api.ChatSubagent{Name: "Explore", Task: "x", State: "completed"}})
	if !strings.Contains(buf.String(), "⧉ Explore completed") {
		t.Errorf("finished subagent card = %q", buf.String())
	}

	buf.Reset()
	p.item(api.ChatItem{ID: "11", Kind: "permission", Permission: &api.ChatPermission{
		Title:   "Run rm -rf?",
		Options: []api.ChatPermissionOption{{ID: "yes", Name: "Allow"}},
		Outcome: "yes",
	}})
	if !strings.Contains(buf.String(), "? Run rm -rf?: Allow") {
		t.Errorf("permission item = %q", buf.String())
	}
	// A permission still waiting prints nothing: there's no outcome to show yet.
	buf.Reset()
	p.item(api.ChatItem{ID: "12", Kind: "permission", Permission: &api.ChatPermission{Title: "Run rm -rf?"}})
	if buf.Len() != 0 {
		t.Errorf("a pending permission printed something: %q", buf.String())
	}

	buf.Reset()
	p.item(api.ChatItem{ID: "13", Kind: "notice", Text: "the session was compacted"})
	p.item(api.ChatItem{ID: "14", Kind: "error", Text: "the AI tool stopped"})
	out = buf.String()
	if !strings.Contains(out, "! the session was compacted") || !strings.Contains(out, "! the AI tool stopped") {
		t.Errorf("notice/error items = %q", out)
	}
}

func TestDeliveryNote(t *testing.T) {
	if got := deliveryNote(api.ChatAsideDeferred); got == "" {
		t.Error("deliveryNote(deferred) is empty")
	}
	if got := deliveryNote(api.ChatAsideLost); got == "" {
		t.Error("deliveryNote(lost) is empty")
	}
	if got := deliveryNote(""); got != "" {
		t.Errorf("deliveryNote(\"\") = %q, want empty", got)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
