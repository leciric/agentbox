package api

import "time"

// Run slots (internal/runslots): an agent's run phase — the app, docker
// compose, Playwright, its desktop, browser or emulator, recordings — takes a
// run slot, and waits its turn for one in a first-come, first-served queue.
// Its code phase never waits.

// EventRunSlots says the run slots changed: who holds one, who waits, how
// many are free. Its data is RunSlots.
const EventRunSlots = "run-slots"

// RunSlots is GET /v1/run-slots: how many runs fit in the VM now, who holds
// a slot and who waits for one.
type RunSlots struct {
	// Total is InUse plus Free: the slots there are now. Free is how many
	// more runs fit, counted in SlotSize, the run peak of the project at the
	// front of the queue, or with nobody queued the largest among the
	// projects with agents running. With nothing held and nobody queued,
	// one is always free.
	Total    int   `json:"total"`
	Free     int   `json:"free"`
	InUse    int   `json:"inUse"`
	Queued   int   `json:"queued"`
	SlotSize int64 `json:"slotSize"`
	// Pool is what every agent's machine may use together (the VM less what
	// the daemon, Incus and the lead chats keep), CodeUse what the machines
	// of agents in their code phase use now, and FreeMemory what is left of
	// the pool after them and every slot held, in bytes.
	Pool       int64 `json:"pool"`
	CodeUse    int64 `json:"codeUse"`
	FreeMemory int64 `json:"freeMemory"`
	// Holders hold a slot, longest first; Queue waits for one, front first.
	Holders []RunSlotHolder `json:"holders"`
	Queue   []RunSlotWaiter `json:"queue"`
	// Projects is each project with agents running, or a learned run
	// peak, by name.
	Projects []ProjectRunSlots `json:"projects"`
}

// RunSlotHolder is an agent holding a run slot.
type RunSlotHolder struct {
	Agent   string    `json:"agent"` // project/agent
	Project string    `json:"project"`
	Since   time.Time `json:"since"`
	// Expires is when it goes unless the agent uses its run phase meanwhile.
	Expires time.Time `json:"expires"`
	// Reserved is its project's run peak when it was granted; Use what its
	// machine uses now, and Peak the most it used since, in bytes.
	Reserved int64 `json:"reserved"`
	Use      int64 `json:"use"`
	Peak     int64 `json:"peak"`
}

// RunSlotWaiter is an agent waiting for a run slot.
type RunSlotWaiter struct {
	Agent   string    `json:"agent"`
	Project string    `json:"project"`
	Ahead   int       `json:"ahead"`
	Since   time.Time `json:"since"`
}

// ProjectRunSlots is one project's run peak and slots.
type ProjectRunSlots struct {
	Project string `json:"project"`
	// Peak is what one of its runs is counted as, in bytes: learned from
	// Runs runs, or the default while Learned is false.
	Peak    int64 `json:"peak"`
	Learned bool  `json:"learned"`
	Runs    int   `json:"runs"`
	// Free is how many of its runs fit now; InUse and Queued its agents
	// holding a slot and waiting for one.
	Free   int `json:"free"`
	InUse  int `json:"inUse"`
	Queued int `json:"queued"`
}

// RunSlot is where an agent stands for a run slot (Agent.RunSlot): holding
// one, or waiting with Ahead before it. Absent for neither.
type RunSlot struct {
	State   string     `json:"state"` // RunSlotHolding or RunSlotWaiting
	Ahead   int        `json:"ahead,omitempty"`
	Since   time.Time  `json:"since"`
	Expires *time.Time `json:"expires,omitempty"`
}

const (
	RunSlotHolding = "holding"
	RunSlotWaiting = "waiting"
)

// RunUpRequest is POST /v1/self/run/up, from inside an agent: a run slot,
// for its run phase. A slot it holds is renewed; one free is granted;
// otherwise it waits at the back of the queue, or keeps its place there.
type RunUpRequest struct {
	// WaitSeconds is how long the answer may wait for the slot before
	// saying where the agent stands (at most 30). Asking again keeps its
	// place; a place nobody asks for in a minute is dropped.
	WaitSeconds int `json:"waitSeconds,omitempty"`
	// Try takes a slot only if one is free now, and never queues: for what
	// can't wait (a desktop tool call).
	Try bool `json:"try,omitempty"`
	// Command is what it is for, shown to the agent's user.
	Command string `json:"command,omitempty"`
	// Browser says the slot is for the agent's desktop and browser (a
	// desktop tool call): ending the run phase stops the browser.
	Browser bool `json:"browser,omitempty"`
}

// RunPhase is where the calling agent's run phase stands: POST
// /v1/self/run/up, GET /v1/self/run and DELETE /v1/self/run.
type RunPhase struct {
	// State is RunSlotHolding, RunSlotWaiting or "" for neither.
	State string `json:"state"`
	// Ahead is, waiting, how many are before it; not queued (Try), how
	// many wait.
	Ahead int `json:"ahead"`
	// Granted says this request granted the slot.
	Granted bool `json:"granted,omitempty"`
	// Key names the run phase's cgroup, which what it runs joins
	// (RunJoin).
	Key string `json:"key,omitempty"`
	// Expires is when the slot goes unless renewed.
	Expires *time.Time `json:"expires,omitempty"`
	// Free and Total are the VM's run slots (RunSlots).
	Free  int `json:"free"`
	Total int `json:"total"`
	// Stopped is, answering a DELETE, what ending the run phase stopped.
	Stopped []string `json:"stopped,omitempty"`
	// Peak is, answering a DELETE, the most the agent's machine used while
	// it held the slot, in bytes, which its project's run peak learns from.
	Peak int64 `json:"peak,omitempty"`
}

// RunJoin is POST /v1/self/run/join: a process of the agent's, and what it
// starts after, into its run phase's cgroup, which ending the run phase
// stops.
type RunJoin struct {
	PID int `json:"pid"`
}

// ErrorNoRunSlot is the Code of an in-agent start of what needs a run slot
// (its browser, emulator or a recording) refused because the agent holds none
// and none is free: `agentbox run up` waits for one.
const ErrorNoRunSlot = "no-run-slot"
