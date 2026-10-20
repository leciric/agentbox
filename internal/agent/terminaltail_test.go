package agent_test

import (
	"context"
	"reflect"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// TestTerminalTailReadsThePane checks the one exec's output is split into the
// foreground command and the pane's last lines, with blank lines and a TUI's
// frame dropped, and that a shell at its prompt isn't busy.
func TestTerminalTailReadsThePane(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  exec) printf 'go\n$ go test ./...\n\n╭──────────╮\n│ ok  agentbox/internal/api │\n╰──────────╯\nok  agentbox/internal/agent\n=== RUN TestX\n\n\n' ;;
esac`))
	a := logsFixture(t, f, "none")
	got, err := f.m.TerminalTail(context.Background(), a, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := agent.TerminalTail{Window: "shell", Command: "go", Busy: true, Lines: []string{"ok  agentbox/internal/api", "ok  agentbox/internal/agent", "=== RUN TestX"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TerminalTail() = %+v, want %+v", got, want)
	}
}

// TestTerminalTailIdleShellAndToolWindow checks a pane at a bash prompt
// isn't busy, and that an agent used from the terminal is read from its AI
// tool's window rather than the shell.
func TestTerminalTailIdleShellAndToolWindow(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  exec) printf 'bash\nlint@agent:~$ \n' ;;
esac`))
	a := logsFixture(t, f, "claude")
	a.Interface = state.InterfaceCLI
	got, err := f.m.TerminalTail(context.Background(), a, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got.Busy || got.Window != "claude" || len(got.Lines) != 1 {
		t.Errorf("TerminalTail() = %+v, want an idle claude window with one line", got)
	}
	a.Interface = state.InterfaceChat
	if got, _ := f.m.TerminalTail(context.Background(), a, 4); got.Window != "shell" {
		t.Errorf("TerminalTail() of a chat agent read %q, want the shell", got.Window)
	}
}
