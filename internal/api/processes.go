package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// What agents still have running: the app's Running view, `agentbox ps` and
// `agentbox kill`. Each agent's processes are read from its machine and
// grouped in trees, so a dev server with what it started is one thing to see
// and to kill.

// ProcessTreeHeavy is how much memory makes a tree heavy (ProcessTree.Heavy).
const ProcessTreeHeavy = 1 << 30

// ProcessTreeIdle is how long a tree has to have used no CPU to be idle
// (ProcessTree.Idle).
const ProcessTreeIdle = 30 * time.Minute

// Tree kinds (ProcessTree.Kind).
const (
	// ProcessTreeAgent is what the agent, or the user in its terminal,
	// started: a dev server, a watcher, a test run, the desktop's browser.
	ProcessTreeAgent = "agent"
	// ProcessTreeTool is the AI tool's own: its adapter, the tool and its
	// MCP servers. Protected.
	ProcessTreeTool = "tool"
	// ProcessTreeSystem is the machine's own: init, its services, tmux, the
	// desktop's display, and the shells the tool runs under. Protected.
	ProcessTreeSystem = "system"
)

// ProcessTree is one group of processes in an agent's machine: a process and
// every process it started, of the same kind.
type ProcessTree struct {
	// PID is the tree's root as the machine numbers it: what the agent's own
	// ps shows and what the kill endpoint takes.
	PID int `json:"pid"`
	// Label is a few words to show it by: the command its root runs, the way
	// it was typed (the command a Bash tool's shell runs, not the shell), or
	// the AI tool's name for the tool's, "System" for the machine's.
	Label string `json:"label"`
	// Command is its root's whole command line.
	Command string `json:"command"`
	Kind    string `json:"kind"`
	// Protected is a tree the machine or its AI tool needs, which the kill
	// endpoint refuses: the tool's and the machine's.
	Protected bool `json:"protected"`
	// Processes is how many there are in it.
	Processes int `json:"processes"`
	// Memory is what it holds: its processes' proportional share (PSS) when
	// the daemon could read it, their resident memory otherwise. RSS and PSS
	// are those apart; PSS is 0 when it couldn't be read.
	Memory int64 `json:"memory"`
	RSS    int64 `json:"rss"`
	PSS    int64 `json:"pss"`
	// CPU is the share of one core it used lately (100 is a whole core),
	// since the daemon last looked, a second ago at least; CPUSeconds is all
	// it has used since it started.
	CPU        float64 `json:"cpu"`
	CPUSeconds float64 `json:"cpuSeconds"`
	// Started is when its root started.
	Started time.Time `json:"started"`
	// IdleSeconds is how long it has used next to no CPU, as far as the
	// daemon has seen: since it last saw it busy, or since it started when
	// it has hardly used any.
	IdleSeconds int64 `json:"idleSeconds"`
	// Heavy is a tree of ProcessTreeHeavy or more, and Idle one that has been
	// idle ProcessTreeIdle or more: what the app flags. Neither for a
	// protected tree.
	Heavy bool `json:"heavy"`
	Idle  bool `json:"idle"`
	// Members are its processes, its root first.
	Members []ProcessMember `json:"members"`
}

// ProcessMember is one process of a ProcessTree.
type ProcessMember struct {
	PID     int    `json:"pid"`
	PPID    int    `json:"ppid"`
	Name    string `json:"name"`
	Command string `json:"command"`
	Memory  int64  `json:"memory"`
}

// AgentProcesses is what one agent's machine has running.
type AgentProcesses struct {
	Ref     string `json:"ref"`
	Project string `json:"project"`
	Name    string `json:"name"`
	Title   string `json:"title"`
	// Running is whether its machine runs: one that doesn't has no trees.
	Running bool `json:"running"`
	// Trees are its process trees, the biggest first.
	Trees []ProcessTree `json:"trees"`
	// Memory, CPU and Processes are its trees' added up, and Killable the
	// memory of the ones that aren't protected.
	Memory    int64   `json:"memory"`
	Killable  int64   `json:"killable"`
	CPU       float64 `json:"cpu"`
	Processes int     `json:"processes"`
	// Error is why its processes couldn't be read, when they couldn't.
	Error string `json:"error,omitempty"`
}

// ProcessList is what GET /v1/processes gives: every agent with a running
// machine, in one project or all of them, the one holding most first.
type ProcessList struct {
	Agents []AgentProcesses `json:"agents"`
	At     time.Time        `json:"at"`
}

// KillProcessResult is what POST .../processes/{pid}/kill did.
type KillProcessResult struct {
	Ref string `json:"ref"`
	// PID is the process asked for, as the machine numbers it, and Label the
	// tree it started.
	PID   int    `json:"pid"`
	Label string `json:"label"`
	// Processes is how many were killed with it, Memory what they held.
	Processes int   `json:"processes"`
	Memory    int64 `json:"memory"`
	// Forced is how many didn't end on SIGTERM and got SIGKILL, and Left
	// how many hadn't ended even then.
	Forced int `json:"forced"`
	Left   int `json:"left"`
}

// ErrorProtectedProcess is the Code of the kill endpoint refusing a process
// the agent's machine or its AI tool needs.
const ErrorProtectedProcess = "protected-process"

// Processes lists what every agent with a running machine has running, in
// project, or in every project when it's "".
func (c *Client) Processes(ctx context.Context, project string) (ProcessList, error) {
	path := "/v1/processes"
	if project != "" {
		path += "?" + url.Values{"project": {project}}.Encode()
	}
	var out ProcessList
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// AgentProcesses lists what one agent's machine has running, from its
// "<project>/<agent>" ref.
func (c *Client) AgentProcesses(ctx context.Context, ref string) (AgentProcesses, error) {
	var out AgentProcesses
	return out, c.agentDo(ctx, http.MethodGet, ref, "/processes", nil, &out)
}

// KillProcess kills a process in an agent's machine, by its number there,
// with every process it started: SIGTERM, then SIGKILL to what is left a few
// seconds later. The machine's own processes and its AI tool's are refused.
func (c *Client) KillProcess(ctx context.Context, ref string, pid int) (KillProcessResult, error) {
	var out KillProcessResult
	return out, c.agentDo(ctx, http.MethodPost, ref, "/processes/"+strconv.Itoa(pid)+"/kill", nil, &out)
}
