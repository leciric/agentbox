// Package homehost runs the Home chat's AI tool on the user's own computer
// while the daemon stays in AgentBox's VM.
//
// Every other chat's tool runs where the daemon is: an agent's in its machine,
// a project's lead beside the daemon in a private HOME. The Home chat is the
// one the user talks to about their computer as a whole, so its tool runs on
// that computer, as the user, with their programs, files, desktop and their
// own Claude Code login:
//
//	app ──spawns──▶ agentbox home-host ══ WebSocket to /v1/home-host, yamux ══▶ daemon (Hub)
//	                     │ spawns, as the user                                     │ ACP over a stream
//	                     ▼                                                         ▼
//	               claude-agent-acp ◀──────────── stdin/stdout ─────────────── package chat
//	                     │ MCP, stdio
//	                     ▼
//	               agentbox home-host mcp ══ WebSocket to /v1/home-host/mcp ══▶ agentbox mcp (in the VM)
//
// The host can always reach the daemon (its socket is forwarded to the host,
// or relayed on Windows) and the daemon can never reach the host, so the
// helper dials in and the daemon drives it from there: it opens a stream for
// each thing it wants (probe, spawn, wait, kill) on the helper's connection.
// Each stream starts with one JSON request line and its answer lines; a spawn's
// stream then carries the adapter's stdin and stdout, so package chat speaks
// ACP to it exactly as to a local process.
//
// The helper is a child of the desktop app and ends with it (its stdin
// closes). Without it — the app closed, a phone or the CLI driving the chat,
// the tool not installed — the Home chat runs in the VM as before, and says so.
package homehost

import (
	"bufio"
	"encoding/json"
	"io"
)

// Path and MCPPath are the daemon's endpoints: the helper's connection, and an
// MCP server's stdio, carried to `agentbox mcp` in the VM.
const (
	Path    = "/v1/home-host"
	MCPPath = "/v1/home-host/mcp"
)

// SocketEnv is where the helper reaches the daemon: the forwarded socket's
// path, or on Windows the app's relay pipe. Unset, it is the usual socket.
const SocketEnv = "AGENTBOX_HOST_SOCKET"

// SessionEnv names the Home chat session an MCP server serves.
const SessionEnv = "AGENTBOX_HOME_SESSION"

const (
	opProbe = "probe"
	opSpawn = "spawn"
	opWait  = "wait"
	opKill  = "kill"
)

// Spec is what the daemon asks the host to run.
type Spec struct {
	Tool string `json:"tool"` // claude, codex or opencode
	// Session is the Home chat session it runs: each but the first has a
	// folder of its own, as its brief differs, and its own MCP server.
	Session string `json:"session,omitempty"`
	// Package is the adapter's npm package at the version AgentBox pins
	// (npm:@agentclientprotocol/claude-agent-acp@1.2.3), installed on the host
	// when it has none; empty for a tool that speaks ACP itself.
	Package string `json:"package,omitempty"`
	// Files are written into the working directory before it starts, by
	// relative path: the brief, and Claude Code's project settings.
	Files map[string]string `json:"files,omitempty"`
	// LoginEnv is AgentBox's own login for the tool, used only when the host
	// has none of its own.
	LoginEnv []string `json:"loginEnv,omitempty"`
	// Env is set whatever the host has: the permission policy, for a tool
	// that takes it from the environment.
	Env []string `json:"env,omitempty"`
}

// Info is what the host says about itself.
type Info struct {
	Version string `json:"version"`
	OS      string `json:"os"` // runtime.GOOS
	Host    string `json:"host"`
	Home    string `json:"home"` // the user's home folder
	Dir     string `json:"dir"`  // the chat's working directory
	// Tools are the AI tools found on the host, by AgentBox's name.
	Tools map[string]Tool `json:"tools"`
}

// Tool is one AI tool on the host.
type Tool struct {
	Path  string `json:"path"`  // its command line; "" when it isn't installed
	Login bool   `json:"login"` // whether it has a login of its own there
}

// MCPServer is an MCP server the session is started with, in ACP's terms.
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []string `json:"env"` // NAME=value
}

type request struct {
	Op   string `json:"op"`
	ID   string `json:"id,omitempty"`
	Spec *Spec  `json:"spec,omitempty"`
}

// reply is one answer line. A spawn sends any number with only Status before
// its last.
type reply struct {
	Status string      `json:"status,omitempty"`
	Error  string      `json:"error,omitempty"`
	Info   *Info       `json:"info,omitempty"`
	ID     string      `json:"id,omitempty"`
	Cwd    string      `json:"cwd,omitempty"`
	MCP    []MCPServer `json:"mcp,omitempty"`
	Exit   *int        `json:"exit,omitempty"`
	Stderr string      `json:"stderr,omitempty"`
}

func writeLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func readLine(r *bufio.Reader, v any) error {
	line, err := r.ReadBytes('\n')
	if err != nil {
		if err == io.EOF && len(line) == 0 {
			return io.ErrUnexpectedEOF
		}
		if err != io.EOF {
			return err
		}
	}
	return json.Unmarshal(line, v)
}
