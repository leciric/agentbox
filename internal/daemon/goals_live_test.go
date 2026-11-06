//go:build integration

package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// liveIncus is the tests' fake Incus with `exec` run here, in a shell: the
// daemon's goal checks run their command in the agent's machine, which on a
// machine without Incus is this one, in the agent's worktree.
const liveIncus = `case "$1" in
  exec) for last; do :; done; exec sh -c "$last" ;;
esac
` + statefulIncus

// TestGoalsWithClaudeCode drives both kinds of goal through real Claude Code
// sessions (claude-agent-acp), with the daemon's own goal code in between:
//
//   - agent-01 has a goal AgentBox drives, checked by a command, whose task
//     allows one line of a file per turn: it takes the daemon sending it back
//     twice to meet it;
//   - agent-02 is given Claude Code's own /goal, which keeps it going inside
//     one prompt: AgentBox only shows it, set and then met.
//
// It runs the model: it needs AGENTBOX_CLAUDE_LIVE, and a Claude Code login
// (CLAUDE_CODE_OAUTH_TOKEN, or DEMO_CLAUDE_TOKEN in its place).
// AGENTBOX_GOAL_HOLD=<file> keeps the daemon up once it is done, writing its
// socket's path to the file, until the file is removed: the desktop app run
// with AGENTBOX_SOCKET pointing there shows the two agents' goals.
func TestGoalsWithClaudeCode(t *testing.T) {
	if os.Getenv("AGENTBOX_CLAUDE_LIVE") == "" {
		t.Skip("set AGENTBOX_CLAUDE_LIVE to run Claude Code for real")
	}
	adapter, err := exec.LookPath(cmpEnv("AGENTBOX_CLAUDE_ACP", "claude-agent-acp"))
	if err != nil {
		t.Skip("claude-agent-acp isn't installed on this machine")
	}
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	token := cmpEnv("CLAUDE_CODE_OAUTH_TOKEN", os.Getenv("DEMO_CLAUDE_TOKEN"))
	ctx := context.Background()
	launch := func(ctx context.Context, a state.Agent, _ func(string)) (*chat.Process, error) {
		cmd := exec.CommandContext(ctx, adapter)
		cmd.Dir = a.Worktree
		// The tests' environment points Anthropic at nothing (TestMain) and
		// git at an empty home: Claude Code gets the real ones.
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "ANTHROPIC_BASE_URL=") && !strings.HasPrefix(kv, "HOME=") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		cmd.Env = append(cmd.Env, "HOME="+me.HomeDir, "ANTHROPIC_MODEL=sonnet")
		if token != "" {
			cmd.Env = append(cmd.Env, "CLAUDE_CODE_OAUTH_TOKEN="+token)
		}
		return chat.StartCommand(cmd)
	}
	instances := `[` + liveInstance("agent-01") + `,` + liveInstance("agent-02") + `]`
	root := t.TempDir()
	d := startTestDaemon(t, root, liveIncus, testConfig{instances: instances, setup: func(s *Server) {
		s.chat.Launch = launch
		s.chat.Prepare = func(context.Context, state.Agent, string, int64) error { return nil }
	}})
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}

	// The goal AgentBox drives, through the code create_agent runs.
	native := addAgent(t, d, repo, "hello-stack", "agent-01", "Three lines, checked by AgentBox")
	spec, err := d.srv.checkGoal(&api.GoalSpec{
		Objective: "lines.txt holds exactly three lines: one, two, three",
		Checks:    []api.GoalCheck{{Kind: api.GoalCheckCommand, Command: `printf 'one\ntwo\nthree\n' | cmp -s - lines.txt`}},
		Budget:    4,
	})
	if err != nil {
		t.Fatal(err)
	}
	task := "Append the next missing line to lines.txt (creating it if needed): the lines are one, two, three, in that order. " +
		"Write exactly ONE line in this turn, check the file, then end your turn. Never write two lines in one turn."
	if _, err := d.srv.chat.Send(native, d.srv.startGoal(ctx, native, spec, task)); err != nil {
		t.Fatal(err)
	}

	// Claude Code's own /goal.
	tool := addAgent(t, d, repo, "hello-stack", "agent-02", "Claude Code's own /goal")
	if _, err := d.client.SendChat(ctx, tool.Ref(), "/goal The file pair.txt in the current directory has exactly 2 lines: alpha, beta. Write only ONE line per turn, then end your turn."); err != nil {
		t.Fatal(err)
	}

	goal := func(a state.Agent) *api.AgentGoal {
		info, err := d.client.Agent(ctx, a.Ref())
		if err != nil {
			t.Fatal(err)
		}
		return info.Goal
	}
	done := func(a state.Agent) bool { g := goal(a); return g != nil && g.Status != api.GoalActive }
	// Somebody at each chat says yes to whatever it asks: these agents
	// aren't autonomous.
	approve := func(a state.Agent) {
		th, err := d.srv.chat.Thread(a)
		if err != nil {
			return
		}
		for _, it := range th.Items {
			if it.Kind != "permission" || it.Permission == nil || it.Permission.Outcome != "" {
				continue
			}
			for _, o := range it.Permission.Options {
				if strings.HasPrefix(o.Kind, "allow") {
					if _, err := d.srv.chat.Answer(a, it.ID, o.ID); err != nil {
						t.Logf("answering %q: %v", it.Permission.Title, err)
					}
					break
				}
			}
		}
	}
	deadline := time.Now().Add(15 * time.Minute)
	for !(done(native) && done(tool)) {
		approve(native)
		approve(tool)
		if time.Now().After(deadline) {
			t.Fatalf("timed out: agent-01's goal %+v, agent-02's %+v", goal(native), goal(tool))
		}
		time.Sleep(2 * time.Second)
	}

	g := goal(native)
	t.Logf("agent-01's goal: %+v", *g)
	if g.Status != api.GoalMet || g.Continuations < 1 {
		t.Errorf("agent-01's goal = %+v, want met after being sent back", g)
	}
	if b, _ := os.ReadFile(filepath.Join(native.Worktree, "lines.txt")); string(b) != "one\ntwo\nthree\n" {
		t.Errorf("lines.txt = %q", b)
	}
	g = goal(tool)
	t.Logf("agent-02's goal: %+v", *g)
	if g.Status != api.GoalMet || g.Source != api.GoalSourceTool {
		t.Errorf("agent-02's goal = %+v, want Claude Code's, met", g)
	}
	for _, a := range []state.Agent{native, tool} {
		th, err := d.srv.chat.Thread(a)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, it := range th.Items {
			switch it.Kind {
			case "goal", "user", "notice":
				lines = append(lines, fmt.Sprintf("%s: %s", it.Kind, firstLineOf(it.Text)))
			case "assistant":
				if strings.HasPrefix(it.Text, "Goal set") {
					t.Errorf("%s: the tool's Goal set line is a message: %q", a.Name, it.Text)
				}
				lines = append(lines, "assistant: "+firstLineOf(it.Text))
			}
		}
		t.Logf("%s's chat:\n  %s", a.Name, strings.Join(lines, "\n  "))
	}

	if hold := os.Getenv("AGENTBOX_GOAL_HOLD"); hold != "" {
		if err := os.WriteFile(hold, []byte(d.paths.Socket()), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("holding the daemon at %s until %s is removed", d.paths.Socket(), hold)
		for {
			if _, err := os.Stat(hold); err != nil {
				break
			}
			time.Sleep(time.Second)
		}
	}
}

func liveInstance(name string) string {
	return fmt.Sprintf(`{"name":"ab-hello-stack-%s","status":"Running","config":{},"expanded_config":{},`+
		`"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.8.8.2"}]}}}}`, name)
}

func cmpEnv(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// TestGoalChecksAgainstARealPullRequest runs a goal's pr_open and ci_green
// checks the way goalAfterTurn does, against a real pull request on GitHub:
// an agent whose branch is at the pull request's head, in a clone of this
// repository. No model runs. It needs AGENTBOX_GOAL_PR (the pull request's
// number), AGENTBOX_GOAL_SHA (its head commit, which this repository must
// have) and GH_TOKEN; AGENTBOX_GOAL_REPO is the repository, owner/name
// (leciric/agentbox by default).
func TestGoalChecksAgainstARealPullRequest(t *testing.T) {
	number, sha, token := os.Getenv("AGENTBOX_GOAL_PR"), os.Getenv("AGENTBOX_GOAL_SHA"), cmpEnv("GH_TOKEN", os.Getenv("GITHUB_TOKEN"))
	if number == "" || sha == "" || token == "" {
		t.Skip("set AGENTBOX_GOAL_PR, AGENTBOX_GOAL_SHA and GH_TOKEN to check a real pull request")
	}
	ctx := context.Background()
	here, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	d := startTestDaemon(t, root, statefulIncus, testConfig{instances: `[` + liveInstanceOf("agentbox", "agent-01") + `]`})
	d.setGitHub(t, "https://api.github.com")
	if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveGitHubToken("", token); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repos", "agentbox")
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "clone", "--quiet", "--shared", "--branch", "main", strings.TrimSpace(string(here)), repo).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	git(repo, "remote", "set-url", "origin", "https://github.com/"+cmpEnv("AGENTBOX_GOAL_REPO", "leciric/agentbox")+".git")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	worktree := d.paths.Worktree("agentbox", "agent-01")
	git(repo, "worktree", "add", "--quiet", "-b", "agentbox/goal-check", worktree, sha)
	a := state.Agent{
		Project: "agentbox", Name: "agent-01", Title: "Goal checks", Instance: "ab-agentbox-agent-01",
		AI: "claude", Branch: "agentbox/goal-check", BaseRef: "main", BaseCommit: git(repo, "merge-base", "main", sha),
		Worktree: worktree, Status: state.AgentReady, CreatedAt: time.Now(), Interface: state.InterfaceChat,
	}
	if err := d.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	g := api.AgentGoal{Objective: "PR #" + number + " is open and green", Source: api.GoalSourceAgentBox, Status: api.GoalActive, Budget: 1,
		Checks: []api.GoalCheck{{Kind: api.GoalCheckPROpen}, {Kind: api.GoalCheckCIGreen}}}
	missing := d.srv.runGoalChecks(ctx, a, &g)
	for _, c := range g.Checks {
		t.Logf("%s: met=%v, %s", c.Kind, *c.Met, c.Detail)
	}
	t.Logf("missing: %q", missing)
	if !*g.Checks[0].Met || !strings.Contains(g.Checks[0].Detail, "#"+number) {
		t.Errorf("pr_open = %+v, want #%s open", g.Checks[0], number)
	}
	if !strings.Contains(g.Checks[1].Detail, "#"+number) {
		t.Errorf("ci_green = %+v, want a verdict on #%s", g.Checks[1], number)
	}
	if want := os.Getenv("AGENTBOX_GOAL_CI"); want != "" && fmt.Sprint(*g.Checks[1].Met) != want {
		t.Errorf("ci_green met = %v, want %s", *g.Checks[1].Met, want)
	}
}

func liveInstanceOf(project, name string) string {
	return fmt.Sprintf(`{"name":"ab-%s-%s","status":"Running","config":{},"expanded_config":{},`+
		`"state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.8.8.2"}]}}}}`, project, name)
}
