package chat

import (
	"strings"
	"unicode"

	"agentbox/internal/state"
)

// A message that starts with a slash command (/compact, /rename, a skill) is
// for the AI tool, not its model: the tool runs it only when it is a prompt's
// own first word. Sent mid-turn, it can't join the running turn, which would
// hand it to the model behind midTurn's note as text to read. So it waits for
// the turn to end, marked queued, and goes in as a prompt of its own
// (drainOutbox, sendOutbox). Plain messages still join the running turn.

// slashCommand says whether text is a slash command: a slash and a name as
// its first word, the name running to whitespace or the end. A path
// (/home/me/notes.txt) isn't one, since a name holds no further slash.
func slashCommand(text string) bool {
	if len(text) < 2 || text[0] != '/' {
		return false
	}
	return leadingName(text[1:])
}

// leadingName says whether text starts with a command's or a skill's name
// that runs to whitespace or the end.
func leadingName(text string) bool {
	n := 0
	for n < len(text) && isNameByte(text[n]) {
		n++
	}
	return n > 0 && (n == len(text) || unicode.IsSpace(rune(text[n])))
}

// isCommand says whether a message is one for the AI tool to run rather than
// its model to read: a slash command, or a skill named first as the composer's
// "/" list puts one ($name), which skillPrompt turns into what the tool runs.
// skills is skillNames: a $word that names none stays a message.
func isCommand(text string, skills map[string]bool) bool {
	text = strings.TrimSpace(text)
	if slashCommand(text) {
		return true
	}
	if len(text) < 2 || text[0] != '$' || !leadingName(text[1:]) {
		return false
	}
	name := text[1:]
	if i := strings.IndexFunc(name, unicode.IsSpace); i >= 0 {
		name = name[:i]
	}
	return skills[name]
}

// commandFor is isCommand for a message sent to a's chat. It reads the
// installed skills only for a message that could name one, and before the
// conversation is locked, as prompt does.
func (m *Manager) commandFor(a state.Agent, text string) bool {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "$") {
		return isCommand(text, m.skillNames(a))
	}
	return slashCommand(text)
}
