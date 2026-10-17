package chat

import (
	"regexp"
	"time"

	"agentbox/internal/api"
)

// An agent uses its desktop when its AI tool drives the display: the desktop
// MCP server's mouse and keyboard, the Playwright one's pages, or the
// browser and recording commands, by itself or through a subagent. The app
// shows a live view of the desktops in use beside the lead's chat
// (components/pip), and only those: a desktop left idle is not news.

// desktopTool is a call to the desktop or Playwright MCP server, by the name
// Claude Code gives it (mcp__desktop__click) or the title other tools do
// (desktop/click, playwright.browser_navigate).
var desktopTool = regexp.MustCompile(`(?i)^(mcp__(desktop|playwright)__|(desktop|playwright)[/.:]\w)`)

// desktopCommand is a shell command that shows or records the desktop.
var desktopCommand = regexp.MustCompile(`\bagentbox\s+(browser|media\s+(record|screenshot))\b`)

func usesDesktop(t *api.ChatTool) bool {
	return desktopTool.MatchString(t.Name) || desktopTool.MatchString(t.Title) || desktopCommand.MatchString(t.Command)
}

// sawTool notes a tool call that uses the desktop. The conversation is locked.
func (c *conversation) sawTool(t *api.ChatTool) {
	if usesDesktop(t) {
		c.desktopAt = c.m.now()
	}
}

// DesktopAt is when an agent's AI tool last used its desktop, as far as this
// daemon has seen; zero when it hasn't.
func (m *Manager) DesktopAt(ref string) time.Time {
	c := m.existing(ref)
	if c == nil {
		return time.Time{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.desktopAt
}
