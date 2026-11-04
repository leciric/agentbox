package chat

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// What the user writes to an agent from a project's chat (the composer's To:
// chip, api.ChatMessageRequest.To) goes to that agent's chat; the project's
// chat keeps a notice of it, so the lead knows what its agents were told
// without a turn of its own to pay for. The notice starts nothing: the lead
// reads it as one line at the start of its next turn (toldNote), whatever
// starts that turn.

// maxToldLine is how much of a message the lead's line about it quotes.
const maxToldLine = 300

// Told adds to a's chat, a project's, the notice that the user wrote text to
// agent to; attached counts the pictures and files that went with it. It
// starts no turn, and doesn't wait for a running one.
func (m *Manager) Told(a state.Agent, to, text string, attached int) (api.ChatItem, error) {
	text = strings.TrimSpace(text)
	if to == "" || (text == "" && attached == 0) {
		return api.ChatItem{}, errors.New("nothing was told")
	}
	c, err := m.conversation(a)
	if err != nil {
		return api.ChatItem{}, err
	}
	defer c.mu.Unlock()
	it := c.add("notice", c.lastTurn())
	it.Text, it.To = text, to
	if text == "" {
		it.Text = fmt.Sprintf("(%d attached)", attached)
	}
	c.flush(true)
	return clone(*it), nil
}

// toldNote is what the user told agents from this chat since the turn before
// t, a line each, for t's prompt; "" when they told none. The caller holds
// c.mu.
func (c *conversation) toldNote(t *turn) string {
	end := len(c.items)
	for i, it := range c.items {
		if it.ID == t.id {
			end = i
			break
		}
	}
	var lines []string
	for i := end - 1; i >= 0; i-- {
		it := c.items[i]
		if it.Kind == "user" {
			break
		}
		if it.Kind == "notice" && it.To != "" {
			lines = append(lines, fmt.Sprintf("User told %s: %s", it.To, toldLine(it.Text)))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return "[AgentBox: the user wrote to agents directly from this chat since your last turn. Nothing to do about it unless it changes your plan.\n" +
		strings.Join(lines, "\n") + "]"
}

// toldLine is a message as one line, cut at maxToldLine.
func toldLine(text string) string {
	line := strings.Join(strings.Fields(text), " ")
	if len(line) <= maxToldLine {
		return line
	}
	cut := maxToldLine
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut] + "…"
}
