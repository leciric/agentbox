package cli_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/cli"
	"agentbox/internal/paths"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// TestSecretsCommands walks the command line: a value read from stdin, a list
// that shows names and never values, and a removal.
func TestSecretsCommands(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "sk-secret-value\n", "secrets", "set", "hello-stack", "OPENAI_API_KEY", "--value-stdin")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Stored OPENAI_API_KEY for every agent of hello-stack", "$OPENAI_API_KEY", "can't be read back")
	if strings.Contains(out, "sk-secret-value") {
		t.Errorf("the command printed the value back:\n%s", out)
	}
	// No agent yet, and the output says what that means rather than "0 agents".
	mustContain(t, out, "No agent has it yet")

	out, err = run(t, "", "secrets", "list", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "$OPENAI_API_KEY", "project", "no agent yet")
	if strings.Contains(out, "sk-secret-value") {
		t.Errorf("the list printed the value:\n%s", out)
	}

	// Setting it again replaces it, and says so in the same words.
	if _, err := run(t, "sk-second-value", "secrets", "set", "hello-stack", "OPENAI_API_KEY", "--value-stdin"); err != nil {
		t.Fatal(err)
	}
	if secrets, err := run(t, "", "secrets", "list", "hello-stack"); err != nil || strings.Count(secrets, "OPENAI_API_KEY") != 1 {
		t.Errorf("setting a secret twice left %q, %v", secrets, err)
	}

	out, err = run(t, "", "secrets", "rm", "hello-stack", "OPENAI_API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Removed OPENAI_API_KEY from hello-stack")
	out, err = run(t, "", "secrets", "list", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No secrets for hello-stack yet")
}

// TestSecretValueNeverInArgv is the rule the command line exists to keep: a
// value passed as an argument would stay in the shell's history and be
// readable in `ps` by every process on the machine while the command runs. So
// there is no argument and no flag that takes one, and trying is refused with
// the reason.
func TestSecretValueNeverInArgv(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}

	// A third argument, the way anyone would first try it.
	out, err := run(t, "", "secrets", "set", "hello-stack", "OPENAI_API_KEY", "sk-would-be-in-history")
	if err == nil {
		t.Fatal("a value passed as an argument was accepted")
	}
	for _, want := range []string{"never an argument", "shell history", "ps"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should say why (%q): %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "sk-would-be-in-history") || strings.Contains(out, "sk-would-be-in-history") {
		t.Errorf("the refusal echoed the value: %v\n%s", err, out)
	}
	// Nothing was stored, so the value isn't also sitting in the database.
	if list, _ := run(t, "", "secrets", "list", "hello-stack"); !strings.Contains(list, "No secrets") {
		t.Errorf("the refused secret was stored: %s", list)
	}

	// No flag on any secrets command takes a value either, so there is no
	// other way to put one in argv. The one flag there is (--value-stdin) says
	// where to *read* the value from. pflag prints a flag's type after its
	// name, and only for flags that take an argument, so a line with a type on
	// it is exactly what must not exist here.
	for _, path := range [][]string{{"secrets", "set"}, {"secrets", "list"}, {"secrets", "rm"}} {
		cmd, _, err := cli.NewRootCmd().Find(path)
		if err != nil {
			t.Fatalf("no %s command: %v", strings.Join(path, " "), err)
		}
		for _, line := range strings.Split(strings.TrimSpace(cmd.LocalFlags().FlagUsages()), "\n") {
			flag, usage, _ := strings.Cut(strings.TrimSpace(line), "  ")
			if len(strings.Fields(flag)) > 1 {
				t.Errorf("agentbox %s has a flag that takes an argument (%s): a secret's value must not be able to reach argv (%s)",
					strings.Join(path, " "), flag, usage)
			}
		}
	}
}

// TestSecretsSetNeedsAValue checks the other half: without a terminal and
// without piped input, it says how to pass the value instead of storing an
// empty one. (The name is short on purpose: a test's temporary directory is
// part of the daemon's socket path, which unix sockets cap at 107 bytes.)
func TestSecretsSetNeedsAValue(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "", "secrets", "set", "hello-stack", "OPENAI_API_KEY", "--value-stdin"); err == nil {
		t.Error("an empty stdin stored an empty secret")
	} else if !strings.Contains(err.Error(), "no value for OPENAI_API_KEY on stdin") {
		t.Errorf("unhelpful error for empty stdin: %v", err)
	}
	// A name that is not an environment variable name is refused by the
	// daemon, and the command line passes that through as it is.
	if _, err := run(t, "value", "secrets", "set", "hello-stack", "openai-key", "--value-stdin"); err == nil {
		t.Error("a name that no shell could export was accepted")
	}
}

// TestSecretsListForAnAgent shows both scopes, with the project's marked, which
// is what tells you where to remove a secret from.
func TestSecretsListForAnAgent(t *testing.T) {
	isolate(t)
	startDaemon(t)
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}
	// An agent row is enough: this daemon's incus knows no machines, so there
	// is nothing to write a file into, and the list is what's under test.
	addAgentRow(t, "hello-stack", "agent-01")
	if _, err := run(t, "shared", "secrets", "set", "hello-stack", "SHARED_KEY", "--value-stdin"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "own", "secrets", "set", "hello-stack/agent-01", "OWN_KEY", "--value-stdin"); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "secrets", "list", "hello-stack/agent-01")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "$SHARED_KEY", "$OWN_KEY", "project", "agent", "hello-stack/agent-01")
	if strings.Contains(out, "shared") || strings.Contains(out, "own\n") {
		t.Errorf("the list leaked a value:\n%s", out)
	}
	// The project's own list doesn't show the agent's.
	out, err = run(t, "", "secrets", "list", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "OWN_KEY") {
		t.Errorf("a project's list shows an agent's own secret:\n%s", out)
	}
}

// addAgentRow adds an agent to the isolated state directly, for the cases that
// need one to exist without a machine behind it.
func addAgentRow(t *testing.T, project, name string) {
	t.Helper()
	p, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(p.StateDB())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := state.Agent{Project: project, Name: name, Instance: "ab-" + project + "-" + name, AI: "none",
		Branch: "agentbox/" + name, Worktree: filepath.Join(t.TempDir(), name), Status: state.AgentReady, CreatedAt: time.Now()}
	if err := st.AddAgent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}
