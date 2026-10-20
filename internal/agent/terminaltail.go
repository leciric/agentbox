package agent

import (
	"context"
	"strings"

	"agentbox/internal/state"
)

// TerminalTail is the end of an agent's terminal, as the agent list's rows
// show it in a thumbnail: the pane its AI tool runs in (an agent used from
// the terminal) or else its shell, what that pane is running, and its last
// few lines of text.
type TerminalTail struct {
	Window  string
	Command string   // the pane's foreground command: "bash" when idle
	Busy    bool     // Command isn't a shell waiting at its prompt
	Lines   []string // oldest first, at most the lines asked for
}

// shells are the foreground commands of a pane that is waiting at a prompt.
var shells = map[string]bool{"": true, "bash": true, "sh": true, "zsh": true, "fish": true, "dash": true, "login": true}

// TerminalTail reads the last lines of the agent's terminal with one exec:
// tmux's foreground command for the pane, then the pane's visible text (no
// scrollback: a thumbnail shows what is on screen now).
func (m *Manager) TerminalTail(ctx context.Context, a state.Agent, lines int) (TerminalTail, error) {
	window := "shell"
	if a.AI != "none" && a.Interface != state.InterfaceChat && Tools[a.AI].Command != "" {
		window = a.AI
	}
	target := shellQuote(session + ":" + window)
	script := "tmux display-message -p -t " + target + " '#{pane_current_command}' 2>/dev/null || exit 0\n" +
		"tmux capture-pane -p -J -t " + target + " 2>/dev/null\n"
	out, err := m.agentShell(ctx, a, script)
	if err != nil {
		return TerminalTail{}, err
	}
	command, pane, _ := strings.Cut(out, "\n")
	command = strings.TrimSpace(command)
	return TerminalTail{Window: window, Command: command, Busy: !shells[command], Lines: tailLines(pane, lines)}, nil
}

// tailLines is the last n lines of a pane worth showing: no blank lines, and
// none that are only a TUI's frame (box drawing, rules), the frame's side
// bars trimmed off the rest, each cut to a thumbnail's width at most.
func tailLines(pane string, n int) []string {
	var keep []string
	for _, line := range strings.Split(pane, "\n") {
		line = strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│┃║|"))
		if decoration(line) {
			continue
		}
		if r := []rune(line); len(r) > 120 {
			line = string(r[:120])
		}
		keep = append(keep, line)
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return keep
}

// decoration reports whether a line has fewer than two characters that
// aren't box drawing, block elements, rules or spaces.
func decoration(line string) bool {
	text := 0
	for _, r := range line {
		if r == ' ' || r == '─' || r == '-' || r == '=' || r == '_' || (r >= 0x2500 && r <= 0x259f) {
			continue
		}
		if text++; text >= 2 {
			return false
		}
	}
	return true
}
