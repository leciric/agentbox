package agent

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// The subagents AgentBox defines for Claude Code, in every agent and every
// lead: the one that drives the desktop (D83), and a cheaper Explore (D84).

// The desktop tools belong to a subagent in Claude Code (D83).
//
// The desktop MCP server's screenshot is how a model sees the display, and a
// model driving a UI takes one before nearly every click. Every image stays in
// the conversation and is sent again with every model call after it: one agent
// verifying a UI by hand took 109 of them, held them for the rest of its
// session, and spent more of its context on pixels than on anything else.
//
// A subagent's context is its own and is thrown away when it answers. So in
// Claude Code the server is declared in a subagent's definition rather than in
// ~/.claude.json: the agent itself never has a screenshot tool, and every
// screenshot lands in a context that lasts one job. The agent gets back what
// was learned, in words. Codex and OpenCode have no subagent definitions to
// scope a server to, so they keep the tools directly.

// desktopAgentFile is the definition, relative to the agent's HOME.
const desktopAgentFile = ".claude/agents/desktop.md"

//go:embed desktop_agent.md
var desktopAgentPrompt string

// desktopAgentDescription is what Claude Code shows the agent when it decides
// whether to hand something over, so it says what the subagent is for, how to
// ask, and what it costs.
const desktopAgentDescription = "Drives this machine's virtual display — mouse, keyboard and screenshots of the whole " +
	"screen — and reports back in words. Use it for anything that needs eyes on the display: native windows and dialogs, " +
	"the file manager, the terminal, a walkthrough recorded with the real cursor, or checking what a page really looks " +
	"like. Give it an exact goal and say what to report. Its screenshots stay in its own context and go when it answers. " +
	`It runs on Haiku; pass model "sonnet" when what it has to judge is subtle.`

// desktopAgent renders the subagent's definition around the server that
// drives the display. The frontmatter's values are written as JSON, which is
// YAML too, so a path or a description with a colon or a quote in it stays
// one value.
func desktopAgent(server mcpServer) (string, error) {
	spec, err := json.Marshal(map[string]any{"type": "stdio", "command": server.command, "args": server.args})
	if err != nil {
		return "", err
	}
	description, err := json.Marshal(desktopAgentDescription)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("---\nname: desktop\ndescription: %s\nmodel: haiku\nmcpServers:\n  - %s: %s\n---\n\n%s",
		description, server.name, spec, desktopAgentPrompt), nil
}

// Explore runs on Haiku (D84).
//
// Claude Code's built-in Explore — the subagent it sends searches to — runs on
// whatever the agent that started it runs on, so an agent on Opus searches on
// Opus. A search is reading and matching, and a definition of the same name
// in ~/.claude/agents takes the built-in's place (Claude Code ranks a user's
// definitions above its own), so this one keeps the name every agent already
// reaches for and changes what it costs. It may read and search and nothing
// more, can't start subagents of its own, and stops after exploreMaxTurns: a
// search that needs more is a task, and belongs to the agent.

// exploreAgentFile is the definition, relative to HOME.
const exploreAgentFile = ".claude/agents/Explore.md"

// exploreMaxTurns bounds one search.
const exploreMaxTurns = 40

//go:embed explore_agent.md
var exploreAgentPrompt string

const exploreAgentDescription = "Read-only search on Haiku, for when answering means sweeping many files, directories " +
	"or naming conventions and only the conclusion is needed, not the file dumps. It locates code; it doesn't review " +
	"or change it. Say how broad to go (\"quick\", \"medium\" or \"very thorough\") and what to report."

// exploreAgent renders Explore's definition.
func exploreAgent() (string, error) {
	description, err := json.Marshal(exploreAgentDescription)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("---\nname: Explore\ndescription: %s\nmodel: haiku\ndisallowedTools: Agent, Edit, Write, NotebookEdit\nmaxTurns: %d\n---\n\n%s",
		description, exploreMaxTurns, exploreAgentPrompt), nil
}
