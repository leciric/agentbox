package main

import (
	"bufio"
	"encoding/json"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// event is one message of Claude Code's: a line of `claude -p
// --output-format stream-json`, stamped when the benchmark read it, or an
// entry of a session's transcript (~/.claude/projects/…/<session>.jsonl),
// which carries its own timestamp. Both hold the same messages, so one
// analysis serves the three modes.
type event struct {
	At  time.Time
	Msg claudeMsg
}

type claudeMsg struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	Timestamp   string `json:"timestamp"` // a transcript's
	IsSidechain bool   `json:"isSidechain"`
	// ParentToolUseID is set on a subagent's messages in a stream; a
	// transcript keeps those in files of their own.
	ParentToolUseID *string `json:"parent_tool_use_id"`
	Message         *struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *usage          `json:"usage"`
	} `json:"message"`

	// system/init
	Model             string `json:"model"`
	ClaudeCodeVersion string `json:"claude_code_version"`
	APIKeySource      string `json:"apiKeySource"`
	MCPServers        []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`

	// system/hook_started and hook_response, with --include-hook-events
	HookID   string `json:"hook_id"`
	HookName string `json:"hook_name"`

	// system/task_started, task_updated and task_notification: a Bash
	// command running as a task, which Claude Code moves to the background
	// when it outlasts the Bash tool's timeout (two minutes by default)
	TaskID       string `json:"task_id"`
	ToolUseID    string `json:"tool_use_id"`
	Status       string `json:"status"`
	Backgrounded bool   `json:"is_backgrounded"`
	// queue-operation, in a transcript: what was queued for the model, a
	// <task-notification> among them when a background command ends
	Operation string          `json:"operation"`
	Content   json.RawMessage `json:"content"`
	Patch     *struct {
		Status       string `json:"status"`
		Backgrounded *bool  `json:"is_backgrounded"`
	} `json:"patch"`

	// result; its usage is the session's total, where a stream's assistant
	// messages carry a running count of their output
	Usage         *usage  `json:"usage"`
	DurationMS    float64 `json:"duration_ms"`
	DurationAPIMS float64 `json:"duration_api_ms"`
	NumTurns      int     `json:"num_turns"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
	IsError       bool    `json:"is_error"`
	Result        string  `json:"result"`
}

type usage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"` // a tool_result's
}

// movedToBackground matches what the Bash tool answers for a command that
// goes on in the background: one that outlasted its timeout, or was asked to.
var movedToBackground = regexp.MustCompile(`moved to the background|running in background`)

// taskNotification is what a transcript queues when a background command
// ends: its tool call and how it ended.
var taskNotification = regexp.MustCompile(`(?s)<task-notification>.*?<tool-use-id>([^<]+)</tool-use-id>.*?<status>([^<]+)</status>`)

func (m claudeMsg) blocks() []block {
	if m.Message == nil || len(m.Message.Content) == 0 || m.Message.Content[0] != '[' {
		return nil
	}
	var bs []block
	_ = json.Unmarshal(m.Message.Content, &bs)
	return bs
}

// stamped is how the benchmark stores a stream-json line: when it was read,
// and the line.
type stamped struct {
	At    time.Time       `json:"at"`
	Event json.RawMessage `json:"event"`
}

// readStamped reads a file of stamped lines.
func readStamped(r io.Reader) ([]event, error) {
	var out []event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var s stamped
		if json.Unmarshal(sc.Bytes(), &s) != nil {
			continue
		}
		var m claudeMsg
		if json.Unmarshal(s.Event, &m) != nil {
			continue
		}
		out = append(out, event{At: s.At, Msg: m})
	}
	return out, sc.Err()
}

// readTranscript reads a session transcript: the main conversation's
// messages, at the times they were written, in order.
func readTranscript(r io.Reader) ([]event, error) {
	var out []event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var m claudeMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.Timestamp == "" || m.IsSidechain {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, m.Timestamp)
		if err != nil {
			continue
		}
		out = append(out, event{At: at, Msg: m})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, sc.Err()
}

// buildCommand matches the shell commands that build or test: the fixture's
// check.sh, and the go, cargo and make commands an agent may run instead.
var buildCommand = regexp.MustCompile(`check\.sh|\bgo (test|build|vet|run)\b|\bcargo (build|test|check|run)\b|\bmake\b`)

// toolStat is how often a tool was called and how long its calls took, added
// up (calls that overlapped count twice).
type toolStat struct {
	Calls int     `json:"calls"`
	MS    float64 `json:"ms"`
}

type buildCall struct {
	Command string  `json:"command"`
	MS      float64 `json:"ms"`
}

type tokens struct {
	Input         int64 `json:"input"`
	CacheRead     int64 `json:"cacheRead"`
	CacheCreation int64 `json:"cacheCreation"`
	Output        int64 `json:"output"`
}

// Prompt is everything the model read: what was new, from the cache, and
// written to it.
func (t tokens) Prompt() int64 { return t.Input + t.CacheRead + t.CacheCreation }

// breakdown is where a run's time went, from Claude Code's own messages.
//
// The active span runs from when Claude Code had the prompt (its init
// message, or the prompt in a transcript) to its last message. Within it, tool
// time is the union of every tool call's span, from the message that asked for
// it to the one that brought its result, so calls that ran together count
// once; what is left is model time: waiting for and reading the model's
// answers, and Claude Code's own work between them.
type breakdown struct {
	ActiveMS    float64 `json:"activeMs"`
	ModelMS     float64 `json:"modelMs"`
	ToolMS      float64 `json:"toolMs"`
	BuildToolMS float64 `json:"buildToolMs"` // the part of ToolMS spent in build and test commands
	HookMS      float64 `json:"hookMs"`      // hooks' own time, inside the tool time
	// APIMS is Claude Code's own count of time spent waiting for the API
	// (duration_api_ms), only in a stream's result message.
	APIMS     float64             `json:"apiMs,omitempty"`
	Turns     int                 `json:"turns"` // model calls: distinct assistant messages
	ToolCalls int                 `json:"toolCalls"`
	Tools     map[string]toolStat `json:"tools,omitempty"`
	Builds    []buildCall         `json:"builds,omitempty"`
	Hooks     int                 `json:"hooks"`
	// Backgrounded is how many commands went on in the background, past the
	// Bash tool's timeout or asked to: the model then spends calls waiting
	// for them. Their time counts as tool time until they end.
	Backgrounded int      `json:"backgrounded,omitempty"`
	Tokens       tokens   `json:"tokens"`
	CostUSD      float64  `json:"costUsd,omitempty"`
	Model        string   `json:"model,omitempty"`
	Version      string   `json:"claudeVersion,omitempty"`
	KeySource    string   `json:"apiKeySource,omitempty"`
	MCP          []string `json:"mcpServers,omitempty"`
	IsError      bool     `json:"isError,omitempty"`
	Result       string   `json:"result,omitempty"`

	// Start and End are the active span, for the caller to set what came
	// before and after it against its own clock.
	Start time.Time `json:"-"`
	End   time.Time `json:"-"`
}

type span struct{ from, to time.Time }

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// union is how long the spans cover together, clipped to [from, to].
func union(spans []span, from, to time.Time) time.Duration {
	var clipped []span
	for _, s := range spans {
		if s.from.Before(from) {
			s.from = from
		}
		if s.to.After(to) {
			s.to = to
		}
		if s.to.After(s.from) {
			clipped = append(clipped, s)
		}
	}
	slices.SortFunc(clipped, func(a, b span) int { return a.from.Compare(b.from) })
	var total time.Duration
	var cur span
	for i, s := range clipped {
		switch {
		case i == 0:
			cur = s
		case !s.from.After(cur.to):
			if s.to.After(cur.to) {
				cur.to = s.to
			}
		default:
			total += cur.to.Sub(cur.from)
			cur = s
		}
	}
	if len(clipped) > 0 {
		total += cur.to.Sub(cur.from)
	}
	return total
}

// analyze works out a breakdown from a run's messages, in the order they
// came.
func analyze(events []event) breakdown {
	b := breakdown{Tools: map[string]toolStat{}}
	type call struct {
		name, command string
		from          time.Time
	}
	calls := map[string]*call{}
	var order []string
	ended := map[string]time.Time{}
	hookStart := map[string]time.Time{}
	taskCall := map[string]string{}   // task id → the tool call that started it
	taskEnd := map[string]time.Time{} // tool call → when its task ended
	backgrounded := map[string]bool{} // tool calls
	var hookSpans []span
	usageByID := map[string]usage{}
	var ids []string
	var total *usage
	for _, e := range events {
		m := e.Msg
		if m.ParentToolUseID != nil && *m.ParentToolUseID != "" {
			continue
		}
		switch m.Type {
		case "system":
			switch m.Subtype {
			case "init":
				if b.Start.IsZero() {
					b.Start = e.At
				}
				b.Model, b.Version, b.KeySource = m.Model, m.ClaudeCodeVersion, m.APIKeySource
				for _, s := range m.MCPServers {
					b.MCP = append(b.MCP, s.Name+":"+s.Status)
				}
			case "hook_started":
				hookStart[m.HookID] = e.At
			case "hook_response":
				if from, ok := hookStart[m.HookID]; ok {
					hookSpans = append(hookSpans, span{from, e.At})
					b.Hooks++
					delete(hookStart, m.HookID)
				}
			case "task_started", "task_updated", "task_notification":
				if m.ToolUseID != "" {
					taskCall[m.TaskID] = m.ToolUseID
				}
				call, status := taskCall[m.TaskID], m.Status
				if m.Backgrounded && call != "" {
					backgrounded[call] = true
				}
				if m.Patch != nil {
					status = m.Patch.Status
					if m.Patch.Backgrounded != nil && *m.Patch.Backgrounded && call != "" {
						backgrounded[call] = true
					}
				}
				if call != "" && status != "" && status != "running" {
					if _, ended := taskEnd[call]; !ended {
						taskEnd[call] = e.At
					}
				}
			}
		case "queue-operation":
			var text string
			if m.Operation == "enqueue" && json.Unmarshal(m.Content, &text) == nil {
				for _, n := range taskNotification.FindAllStringSubmatch(text, -1) {
					if _, ended := taskEnd[n[1]]; !ended && n[2] != "running" {
						taskEnd[n[1]] = e.At
					}
				}
			}
		case "user":
			if m.Message == nil {
				continue
			}
			bs := m.blocks()
			if b.Start.IsZero() && (len(bs) == 0 || bs[0].Type == "text") {
				b.Start = e.At // a transcript's prompt
			}
			for _, bl := range bs {
				if bl.Type == "tool_result" {
					if _, done := ended[bl.ToolUseID]; !done {
						ended[bl.ToolUseID] = e.At
					}
					if movedToBackground.Match(bl.Content) {
						backgrounded[bl.ToolUseID] = true
					}
				}
			}
			if !b.Start.IsZero() {
				b.End = e.At
			}
		case "assistant":
			if m.Message == nil || m.Message.Model == "<synthetic>" {
				continue
			}
			if b.Start.IsZero() {
				b.Start = e.At
			}
			b.End = e.At
			if id := m.Message.ID; id != "" {
				if _, seen := usageByID[id]; !seen {
					ids = append(ids, id)
				}
				if m.Message.Usage != nil {
					usageByID[id] = *m.Message.Usage
				} else if _, seen := usageByID[id]; !seen {
					usageByID[id] = usage{}
				}
			}
			if b.Model == "" {
				b.Model = m.Message.Model
			}
			for _, bl := range m.blocks() {
				if bl.Type != "tool_use" || calls[bl.ID] != nil {
					continue
				}
				c := &call{name: bl.Name, from: e.At}
				if bl.Name == "Bash" {
					var in struct {
						Command string `json:"command"`
					}
					_ = json.Unmarshal(bl.Input, &in)
					c.command = in.Command
				}
				calls[bl.ID] = c
				order = append(order, bl.ID)
			}
		case "result":
			b.End = e.At
			b.APIMS, b.CostUSD, b.IsError = m.DurationAPIMS, m.TotalCostUSD, m.IsError
			b.Result = truncate(m.Result, 300)
			total = m.Usage
		}
	}
	if b.Start.IsZero() {
		return b
	}
	var all, builds []span
	for _, id := range order {
		c := calls[id]
		to, ok := ended[id]
		if !ok {
			to = b.End // never answered: it ran until the end
		}
		if end, ok := taskEnd[id]; ok && end.After(to) {
			to = end // it went on in the background
		}
		s := span{c.from, to}
		all = append(all, s)
		d := ms(to.Sub(c.from))
		st := b.Tools[c.name]
		st.Calls++
		st.MS += d
		b.Tools[c.name] = st
		if c.command != "" && buildCommand.MatchString(c.command) {
			builds = append(builds, s)
			b.Builds = append(b.Builds, buildCall{Command: truncate(c.command, 200), MS: d})
		}
	}
	b.ToolCalls = len(order)
	b.Backgrounded = len(backgrounded)
	b.Turns = len(ids)
	for _, id := range ids {
		u := usageByID[id]
		b.Tokens.Input += u.InputTokens
		b.Tokens.CacheRead += u.CacheReadTokens
		b.Tokens.CacheCreation += u.CacheCreationTokens
		b.Tokens.Output += u.OutputTokens
	}
	if total != nil {
		b.Tokens = tokens{Input: total.InputTokens, CacheRead: total.CacheReadTokens, CacheCreation: total.CacheCreationTokens, Output: total.OutputTokens}
	}
	active := b.End.Sub(b.Start)
	tool := union(all, b.Start, b.End)
	b.ActiveMS = ms(active)
	b.ToolMS = ms(tool)
	b.ModelMS = ms(active - tool)
	b.BuildToolMS = ms(union(builds, b.Start, b.End))
	b.HookMS = ms(union(hookSpans, b.Start, b.End))
	return b
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
