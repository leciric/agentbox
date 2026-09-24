package brief_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/brief"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestRender(t *testing.T) {
	cases := map[string]brief.Data{
		"agent": {
			Project:  "pawly",
			Agent:    "agent-02",
			Worktree: "/home/dev/.local/share/agentbox/worktrees/pawly/agent-02",
			Branch:   "agentbox/agent-02",
			BaseRef:  "main",
			IP:       "10.239.149.23",
			EnvFiles: []string{".env", "apps/api/.env.local"},
		},
		"android": {
			Project:  "pawly",
			Agent:    "agent-03",
			Title:    "Medication reminders",
			Worktree: "/home/dev/.local/share/agentbox/worktrees/pawly/agent-03",
			Branch:   "agentbox/agent-03",
			BaseRef:  "main",
			IP:       "10.239.149.24",
			Android:  true,
		},
		"secrets": {
			Project:  "pawly",
			Agent:    "agent-04",
			Worktree: "/home/dev/.local/share/agentbox/worktrees/pawly/agent-04",
			Branch:   "agentbox/agent-04",
			BaseRef:  "main",
			IP:       "10.239.149.25",
			Secrets:  []string{"OPENAI_API_KEY", "STRIPE_SECRET_KEY"},
		},
		"preview": {
			Project:  "pawly",
			Agent:    "agent-01",
			Worktree: "/home/dev/.local/share/agentbox/worktrees/pawly/agent-01",
			Branch:   "agentbox/agent-01",
			BaseRef:  "main",
		},
		// AgentBox in the VM it makes on a Mac: the Mac reaches the agent's
		// servers only through the preview proxy.
		"vm": {
			Project:  "pawly",
			Agent:    "agent-05",
			Worktree: "/Users/dev/.local/share/agentbox/worktrees/pawly/agent-05",
			Branch:   "agentbox/agent-05",
			BaseRef:  "main",
			IP:       "10.99.0.12",
			VM:       true,
			Host:     "a Mac",
		},
		// AgentBox in WSL on Windows (D94): Windows reaches the agent's
		// servers only through the preview proxy.
		"windows": {
			Project:  "pawly",
			Agent:    "agent-05",
			Worktree: "/home/dev/.local/share/agentbox/worktrees/pawly/agent-05",
			Branch:   "agentbox/agent-05",
			BaseRef:  "main",
			IP:       "10.99.0.12",
			VM:       true,
			Host:     "Windows",
		},
		// The project's notes, folded in verbatim: what the user and the lead
		// wrote is what every agent of the project reads.
		"notes": {
			Project:  "pawly",
			Agent:    "agent-04",
			Worktree: "/home/dev/.local/share/agentbox/worktrees/pawly/agent-04",
			Branch:   "agentbox/agent-04",
			BaseRef:  "main",
			IP:       "10.239.149.25",
			Notes: "The app is a pnpm monorepo: `pnpm install` at the root, `pnpm dev` in `apps/web`.\n" +
				"\n## From the lead\n\n- 2026-09-18: the e2e tests need a Postgres on 5432 (`docker compose up db`).\n",
		},
		// What the project remembers, built for this agent's own task (D75).
		// It is a section of its own, under the notes, and it says plainly
		// that it is a summary: an agent that thinks it has been given
		// everything is an agent that never searches.
		"knowledge": {
			Project:  "pawly",
			Agent:    "agent-06",
			Title:    "OAuth login",
			Worktree: "/home/dev/.local/share/agentbox/worktrees/pawly/agent-06",
			Branch:   "agentbox/agent-06",
			BaseRef:  "main",
			IP:       "10.239.149.26",
			Knowledge: "**What this project is doing**\n\n- Goal: Ship the reminders page\n" +
				"- Working on: Logging in with Google\n\n" +
				"**Still open**\n\n- **The count query is unindexed** — It scans the whole table.\n\n" +
				"**What the project knows about it**\n\n" +
				"- **The OAuth callback needs the exact port** — Google rejects a redirect URI whose port isn't registered.",
		},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := brief.Render(data)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", name+".golden")
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("Render() mismatch (run with -update to accept)\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

// The lead's whole brief, in each autonomy, with every optional section on.
// Run with -update after changing lead.md.tmpl, and read the diff: this is
// the text the project's chat reads before every turn (D90).
func TestRenderLeadGolden(t *testing.T) {
	for _, autonomy := range []string{"ask", "on"} {
		t.Run(autonomy, func(t *testing.T) {
			got, err := brief.RenderLead(brief.LeadData{
				Project: "pawly", Root: "/home/dev/www/pawly",
				Worktree: "/home/dev/.local/share/agentbox/worktrees/pawly/lead",
				BaseRef:  "main", Autonomy: autonomy, CanSpawn: true,
				AgentModel: "auto", ModelMenu: []string{"default", "opus", "sonnet", "haiku"},
				ClaudeAccounts: []string{"personal", "work"},
				Notes:          "## From the lead\n\n- 2026-09-18: the e2e tests need a Postgres on 5432.\n",
				Recap:          "**What this project is doing**\n\n- Adding reminders to the pet profile.",
			})
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", "lead-"+autonomy+".golden")
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("RenderLead() mismatch (run with -update to accept)\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

// TestRenderLeadAutonomy checks what the lead may do without asking (D90).
// Routine follow-through is done and reported in either setting — asking is
// for product decisions and costly surprises — "on" goes further, and the
// hard limits hold in both.
func TestRenderLeadAutonomy(t *testing.T) {
	lead := func(autonomy string) string {
		t.Helper()
		got, err := brief.RenderLead(brief.LeadData{
			Project: "pawly", Root: "/src/pawly",
			Worktree: "/home/dev/.local/share/agentbox/worktrees/pawly/lead",
			BaseRef:  "main", Autonomy: autonomy, CanSpawn: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	shared := []string{
		// Brevity, whatever the setting.
		"Talk like Claude Code in a bare terminal", "**No preamble, no pleasantries",
		"**Don't talk about the machinery**",
		// ...and to its agents.
		"**Write to agents as tersely as to the user.**",
		"**A few sentences by default.**", "**Don't restate**", "**After creating an agent, one line:**",
		"**At most one question, and only when the decision is genuinely the user's.**",
		// Routine is done, not proposed.
		"Routine is done, not proposed, and reported in one line",
		"retire an agent whose work is merged, or pushed with a pull request open",
		"answer an agent's question you can answer",
		"create the obvious next agent for work the user already asked for",
		// The limits.
		"Never throw away uncommitted or unpushed work without saying so first",
		"Never push to `main`, merge, release or tag unless the user asked",
		"Before spending a lot",
	}
	ask, on := lead("ask"), lead("on")
	for name, text := range map[string]string{"ask": ask, "on": on} {
		for _, want := range shared {
			if !strings.Contains(text, want) {
				t.Errorf("%s: the lead's brief doesn't say %q", name, want)
			}
		}
	}
	if !strings.Contains(ask, "only for **real product decisions**") || strings.Contains(ask, "**act on your own**") {
		t.Errorf("ask: the lead isn't told to propose only real product decisions:\n%s", ask)
	}
	if !strings.Contains(on, "**act on your own**") || strings.Contains(on, "propose rather than act") {
		t.Errorf("on: the lead isn't told to act on its own:\n%s", on)
	}
}

// TestRenderLeadAgentModel checks what a project's chat is told about the model
// its agents are created on, which is the only part of the lead brief that says
// anything about cost. The three modes have to read differently: nothing at all
// when the project follows the installation's setting, one line when the
// project names a model for every agent, and a section when the choice is the
// chat's per task.
func TestRenderLeadAgentModel(t *testing.T) {
	lead := func(model string, menu []string, canSpawn bool) string {
		t.Helper()
		got, err := brief.RenderLead(brief.LeadData{
			Project: "pawly", Root: "/src/pawly",
			Worktree: "/home/dev/.local/share/agentbox/worktrees/pawly/lead",
			BaseRef:  "main", Autonomy: "ask",
			AgentModel: model, ModelMenu: menu, CanSpawn: canSpawn,
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	has := func(t *testing.T, text string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(text, w) {
				t.Errorf("the lead's brief doesn't mention %q", w)
			}
		}
	}
	hasNot := func(t *testing.T, text string, unwanted ...string) {
		t.Helper()
		for _, u := range unwanted {
			if strings.Contains(text, u) {
				t.Errorf("the lead's brief mentions %q, and shouldn't", u)
			}
		}
	}

	t.Run("following the installation", func(t *testing.T) {
		text := lead("", []string{"opus", "sonnet"}, true)
		// Nothing is said about models at all: the chat doesn't choose here,
		// and a rule it can't act on is noise in front of everything else.
		hasNot(t, text, "You choose each agent's model", "Fable", "runs on `")
		// What it is told about creating agents is untouched.
		has(t, text, "**One agent, one task.**", "This project asks you to **propose rather than act**")
	})

	t.Run("a model for every agent", func(t *testing.T) {
		text := lead("haiku", []string{"opus", "sonnet", "haiku"}, true)
		has(t, text, "Every agent you create here runs on `haiku`", "only when one agent really needs something else")
		hasNot(t, text, "You choose each agent's model", "Fable")
	})

	t.Run("auto", func(t *testing.T) {
		text := lead("auto", []string{"default", "opus", "sonnet", "haiku"}, true)
		has(t, text,
			"### You choose each agent's model",
			"Pass `model` to `create_agent` every time",
			// The menu is this account's own, not a list AgentBox holds.
			"`default`, `opus`, `sonnet`, `haiku`",
			// The three bands of difficulty, and what each one gets.
			"mechanical or small", "`sonnet` or `haiku`",
			"ordinary feature work", "hard design work",
			"higher `effort`",
			// Saying which model, and the one model it may not pick itself.
			"**Say what you chose, in one line, as you create the agent.**",
			"**Never choose Fable** unless the user has asked for it",
		)
		hasNot(t, text, "Every agent you create here runs on")
	})

	t.Run("auto before any chat has advertised a menu", func(t *testing.T) {
		text := lead("auto", nil, true)
		// AgentBox composes no model list of its own, so with nothing
		// remembered the brief points at the tool rather than inventing one.
		has(t, text, "### You choose each agent's model", "whatever `create_agent`'s `model` parameter lists for this account")
		hasNot(t, text, "The menu is,")
	})

	t.Run("auto with no tools to create agents", func(t *testing.T) {
		// A lead that can't create agents has nothing to choose a model for.
		hasNot(t, lead("auto", []string{"opus"}, false), "You choose each agent's model", "Fable")
	})
}

// The compact window is a setting, not the fixed number the brief used to
// print: a configured window is named and comma-grouped, and 0 — a real
// setting, meaning the model's own window — says so rather than "0 tokens".
func TestRenderCompactWindow(t *testing.T) {
	configured, err := brief.Render(brief.Data{Project: "pawly", Agent: "agent-01", Branch: "agentbox/agent-01", BaseRef: "main", CompactWindow: 200_000})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(configured, "200,000 tokens") {
		t.Errorf("Render() doesn't name the configured compact window:\n%s", configured)
	}
	if strings.Contains(configured, "your model's whole context window") {
		t.Errorf("Render() says the window is unset when it isn't:\n%s", configured)
	}

	unset, err := brief.Render(brief.Data{Project: "pawly", Agent: "agent-01", Branch: "agentbox/agent-01", BaseRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(unset, "your model's whole context window") {
		t.Errorf("Render() doesn't say the window is unset when CompactWindow is 0:\n%s", unset)
	}
	if strings.Contains(unset, "0 tokens") {
		t.Errorf("Render() says \"0 tokens\", which is false:\n%s", unset)
	}
}

// A project with no notes gets no section at all, rather than an empty one.
func TestRenderWithoutNotes(t *testing.T) {
	text, err := brief.Render(brief.Data{Project: "pawly", Agent: "agent-01", Branch: "agentbox/agent-01", BaseRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "Project notes") {
		t.Errorf("Render() has a notes section without notes:\n%s", text)
	}
}

// The lead reads the same notes its agents do, and is told how to add to them.
func TestRenderLeadNotes(t *testing.T) {
	d := brief.LeadData{
		Project:  "pawly",
		Root:     "/home/dev/www/pawly",
		Worktree: "/home/dev/.local/share/agentbox/worktrees/pawly/lead",
		BaseRef:  "main",
		CanSpawn: true,
		Autonomy: "ask",
		Notes:    "## From the lead\n\n- 2026-09-18: the e2e tests need a Postgres on 5432.\n",
	}
	text, err := brief.RenderLead(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Project notes", "- 2026-09-18: the e2e tests need a Postgres on 5432.", "append_note", "read_notes",
		// It can change one entry, and only because it was asked to (D82).
		"`edit_note` and `remove_note`", "**when the user asks you to**",
		"matches nothing, or more than one, changes nothing",
		// Which store a fact belongs in, and that the user is told which.
		"### Notes or memory", "pasted into **every** agent's brief", "When in doubt, `remember`",
		"**Say which of the two you used, and why, in a sentence.**",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("RenderLead() is missing %q:\n%s", want, text)
		}
	}

	d.Notes = ""
	text, err = brief.RenderLead(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "2026-09-18") {
		t.Errorf("RenderLead() kept notes that are gone:\n%s", text)
	}
	// It is still told the notes exist and are its to add to.
	for _, want := range []string{"## Project notes", "Nothing is written down yet.", "append_note"} {
		if !strings.Contains(text, want) {
			t.Errorf("RenderLead() without notes is missing %q:\n%s", want, text)
		}
	}

	// A lead without tools is told about the notes, but not about a tool it
	// hasn't got.
	d.CanSpawn = false
	text, err = brief.RenderLead(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"append_note", "edit_note", "remove_note", "### Notes or memory"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("RenderLead() offers %s to a lead with no tools:\n%s", unwanted, text)
		}
	}
}

// A lead is told about OpenCode only when agents can really run it, and then
// it is told which models are OpenCode's rather than Claude Code's: the two
// sets of names are not interchangeable in either direction.
func TestRenderLeadOpenCode(t *testing.T) {
	lead := func(openCode []string, canSpawn bool) string {
		t.Helper()
		got, err := brief.RenderLead(brief.LeadData{
			Project: "pawly", Root: "/src/pawly",
			Worktree: "/home/dev/.local/share/agentbox/worktrees/pawly/lead",
			BaseRef:  "main", Autonomy: "ask", CanSpawn: canSpawn,
			ModelMenu: []string{"opus", "sonnet"}, OpenCodeMenu: openCode,
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	text := lead([]string{"anthropic/claude-sonnet-5", "openai/gpt-5.4"}, true)
	for _, want := range []string{
		"### Agents can run OpenCode too",
		"`create_agent`'s `ai`",
		"`anthropic/claude-sonnet-5`, `openai/gpt-5.4`",
		"Keep `claude` unless there is a reason",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the lead's brief doesn't mention %q", want)
		}
	}

	// Nothing at all when OpenCode isn't set up, or when the lead has no tools
	// to create agents with: a choice it can't make is noise.
	for _, text := range []string{lead(nil, true), lead([]string{"anthropic/claude-sonnet-5"}, false)} {
		if strings.Contains(text, "OpenCode") {
			t.Errorf("the lead's brief offers OpenCode where it can't be used:\n%s", text)
		}
	}
}

// TestRenderLeadClaudeAccounts checks that the brief only tells the lead to
// spread agents across accounts (D88) when there is more than one to spread
// across — one account is the ordinary case, and saying nothing about it
// there is the point, not a gap.
func TestRenderLeadClaudeAccounts(t *testing.T) {
	lead := func(accounts []string) string {
		t.Helper()
		got, err := brief.RenderLead(brief.LeadData{
			Project: "pawly", Root: "/src/pawly",
			Worktree: "/home/dev/.local/share/agentbox/worktrees/pawly/lead",
			BaseRef:  "main", Autonomy: "ask", CanSpawn: true,
			ClaudeAccounts: accounts,
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	for _, accounts := range [][]string{nil, {"personal"}} {
		text := lead(accounts)
		if strings.Contains(text, "Spread agents across accounts") {
			t.Errorf("the lead's brief spreads agents across accounts with only %v:\n%s", accounts, text)
		}
	}

	text := lead([]string{"personal", "work"})
	for _, want := range []string{
		"### Spread agents across accounts",
		"This project may use 2 Claude Code accounts: `personal`, `work`",
		"`list_accounts`",
		"`claude_account` to `create_agent`",
		"most headroom",
		"5-hour limit",
		"Say which account you picked",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the lead's brief doesn't mention %q:\n%s", want, text)
		}
	}
}

// The lead is told it has the user's machine, that it asks first, and that the
// output stays in its context; the agent tools only when it can create agents (D89).
func TestRenderLeadShell(t *testing.T) {
	d := brief.LeadData{
		Project:  "pawly",
		Root:     "/home/dev/www/pawly",
		Worktree: "/home/dev/.local/share/agentbox/worktrees/pawly/lead",
		BaseRef:  "main",
		CanSpawn: true,
		Autonomy: "ask",
	}
	text, err := brief.RenderLead(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"**a shell on the user's machine**", "**It asks first.**",
		"Ask before anything you can't take back",
		"isn't somewhere to keep work.", "`run_in_agent`", "`copy_between_agents`",
		// Output costs tokens, and real work is still an agent's.
		"stays in this conversation", "`| tail -n 20`", "when a job turns into a session, it is an agent's",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("RenderLead() is missing %q:\n%s", want, text)
		}
	}
	for _, gone := range []string{"no shell", "sandboxed", "are refused"} {
		if strings.Contains(text, gone) {
			t.Errorf("RenderLead() still says %q:\n%s", gone, text)
		}
	}

	d.CanSpawn = false
	if text, err = brief.RenderLead(d); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "`run_in_agent`") {
		t.Errorf("RenderLead() without spawning offers run_in_agent:\n%s", text)
	}
}

// TestRenderLeadOnAMac: on a Mac the lead's shell is the Lima VM's, which
// reaches the Mac's home through the mount but not macOS itself, and its
// brief says so rather than calling it the user's machine.
func TestRenderLeadOnAMac(t *testing.T) {
	d := brief.LeadData{Project: "pawly", Root: "/Users/dev/pawly", Worktree: "/w", BaseRef: "main", Autonomy: "ask", CanSpawn: true}
	linux, err := brief.RenderLead(d)
	if err != nil {
		t.Fatal(err)
	}
	d.VM, d.Host = true, "a Mac"
	mac, err := brief.RenderLead(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(linux, "a shell on the user's machine") || strings.Contains(linux, "Linux VM") {
		t.Errorf("on Linux, the brief should give the lead the user's machine:\n%s", linux)
	}
	for _, want := range []string{"a shell in the Linux VM AgentBox runs in on the user's Mac", "It isn't macOS itself", "home folder is mounted in the VM"} {
		if !strings.Contains(mac, want) {
			t.Errorf("on a Mac, the brief should say %q", want)
		}
	}
	if strings.Contains(mac, "a shell on the user's machine") {
		t.Error("on a Mac, the brief still calls the VM's shell the user's machine")
	}
	d.Host = "Windows"
	windows, err := brief.RenderLead(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(windows, "a shell in the Linux distro AgentBox runs in on Windows") || strings.Contains(windows, "Mac") {
		t.Errorf("on Windows, the brief should give the lead the WSL distro, and not mention a Mac:\n%s", windows)
	}
}
