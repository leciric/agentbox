package acp

import (
	"encoding/json"
	"strconv"
)

// The subset of the protocol AgentBox uses, from ACP's schema (version 1).

const ProtocolVersion = 1

// Methods the client calls.
const (
	MethodInitialize      = "initialize"
	MethodSessionNew      = "session/new"
	MethodSessionLoad     = "session/load"
	MethodSessionResume   = "session/resume"
	MethodSessionPrompt   = "session/prompt"
	MethodSessionCancel   = "session/cancel"
	MethodSetConfigOption = "session/set_config_option"
	// MethodSessionSteer puts a message into the turn that is already running,
	// instead of queuing it behind that turn the way session/prompt would. It's
	// an extension, not part of ACP's schema: adapters that have it say so in
	// InitializeResponse.Meta.
	MethodSessionSteer = "_session/steering"
)

// Methods the agent calls.
const (
	MethodSessionUpdate     = "session/update"
	MethodRequestPermission = "session/request_permission"
)

type InitializeRequest struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
}

// ClientCapabilities says what the client offers the agent. AgentBox offers
// neither files nor terminals: the adapters use their own tools, inside the agent.
type ClientCapabilities struct {
	FS       FileSystemCapability `json:"fs"`
	Terminal bool                 `json:"terminal"`
	// Subagents, when present, says the client can show subagents as sessions
	// of their own (D86): claude-agent-acp then announces each one with
	// subagent_spawned, sends what it does under its own session id, and says
	// how it ended with subagent_state_update.
	//
	// The ACP SDK claude-agent-acp 0.76.0 is built on validates this object
	// against a schema that doesn't have the field yet, and drops it before
	// the adapter looks — so the same thing is said in Meta too, in the form
	// the adapter also reads (SubagentSessionsMeta), which the SDK passes
	// through untouched.
	Subagents *struct{}      `json:"subagents,omitempty"`
	Meta      map[string]any `json:"_meta,omitempty"`
}

// SubagentSessionsMeta is ClientCapabilities.Meta saying the client shows
// subagents as sessions of their own: claude-agent-acp's "nativeSubagentSessions"
// capability, in the extension namespace it reads it from
// (clientSupportsAirCapability in its air-extension.js).
func SubagentSessionsMeta() map[string]any {
	return map[string]any{"jetbrains": map[string]any{"air": map[string]any{
		"version":      1,
		"capabilities": []string{"nativeSubagentSessions"},
	}}}
}

type FileSystemCapability struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type InitializeResponse struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AgentInfo         *Implementation   `json:"agentInfo,omitempty"`
	Meta              *ResponseMeta     `json:"_meta,omitempty"`
}

// ResponseMeta is initialize's top-level _meta, a sibling of the capabilities
// rather than part of them: it's where adapters advertise the extensions ACP's
// own schema doesn't cover.
type ResponseMeta struct {
	Steering *SteeringCapability `json:"steering,omitempty"`
}

// SteeringCapability is present when the adapter takes MethodSessionSteer.
type SteeringCapability struct {
	Supported bool `json:"supported"`
}

type AgentCapabilities struct {
	LoadSession         bool                `json:"loadSession"`
	PromptCapabilities  PromptCapabilities  `json:"promptCapabilities"`
	SessionCapabilities SessionCapabilities `json:"sessionCapabilities"`
}

type PromptCapabilities struct {
	Image           bool `json:"image"`
	EmbeddedContext bool `json:"embeddedContext"`
}

type SessionCapabilities struct {
	// Resume is present when the agent can resume a session without replaying it.
	Resume *struct{} `json:"resume,omitempty"`
}

// McpServer is an MCP server started over stdio.
type McpServer struct {
	Name    string        `json:"name"`
	Command string        `json:"command"`
	Args    []string      `json:"args"`
	Env     []EnvVariable `json:"env"`
}

type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type NewSessionRequest struct {
	Cwd        string      `json:"cwd"`
	McpServers []McpServer `json:"mcpServers"`
}

// ResumeSessionRequest is for session/resume and session/load, which take the same fields.
type ResumeSessionRequest struct {
	SessionID  string      `json:"sessionId"`
	Cwd        string      `json:"cwd"`
	McpServers []McpServer `json:"mcpServers"`
}

// SessionResponse answers session/new (with a session ID), session/resume and session/load.
type SessionResponse struct {
	SessionID     string         `json:"sessionId,omitempty"`
	ConfigOptions []ConfigOption `json:"configOptions"`
}

// ConfigOption is a setting of the session the client can show and change,
// like the model, the permission mode or the reasoning effort.
type ConfigOption struct {
	ID          string
	Name        string
	Description string
	Category    string // mode, model, thought_level, or the agent's own
	Type        string // select or boolean
	Value       string // the current value; "true" or "false" for a boolean
	Choices     []ConfigChoice
}

type ConfigChoice struct {
	Value       string
	Name        string
	Description string
	Group       string // the group's name, when the agent groups its choices
	Kind        string // _meta.kind, like standard, plan, auto_review or full_access
}

type wireChoice struct {
	Value       string       `json:"value"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Group       string       `json:"group"`
	Options     []wireChoice `json:"options"` // in a group
	Meta        struct {
		Kind string `json:"kind"`
	} `json:"_meta"`
}

func (o *ConfigOption) UnmarshalJSON(data []byte) error {
	var w struct {
		ID           string          `json:"id"`
		Name         string          `json:"name"`
		Description  string          `json:"description"`
		Category     string          `json:"category"`
		Type         string          `json:"type"`
		CurrentValue json.RawMessage `json:"currentValue"`
		Options      []wireChoice    `json:"options"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*o = ConfigOption{ID: w.ID, Name: w.Name, Description: w.Description, Category: w.Category, Type: w.Type}
	var s string
	var b bool
	switch {
	case json.Unmarshal(w.CurrentValue, &s) == nil:
		o.Value = s
	case json.Unmarshal(w.CurrentValue, &b) == nil:
		o.Value = strconv.FormatBool(b)
	}
	for _, c := range w.Options {
		if c.Group != "" || c.Options != nil {
			for _, g := range c.Options {
				o.Choices = append(o.Choices, ConfigChoice{Value: g.Value, Name: g.Name, Description: g.Description, Group: c.Name, Kind: g.Meta.Kind})
			}
			continue
		}
		o.Choices = append(o.Choices, ConfigChoice{Value: c.Value, Name: c.Name, Description: c.Description, Kind: c.Meta.Kind})
	}
	return nil
}

type SetConfigOptionRequest struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     any    `json:"value"`
	Type      string `json:"type,omitempty"` // "boolean" for a boolean value
}

type SetConfigOptionResponse struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
}

type PromptRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

type PromptResponse struct {
	StopReason string      `json:"stopReason"` // end_turn, max_tokens, max_turn_requests, refusal or cancelled
	Usage      *Usage      `json:"usage,omitempty"`
	Meta       *PromptMeta `json:"_meta,omitempty"`
}

type CancelNotification struct {
	SessionID string `json:"sessionId"`
}

// SteerRequest carries a message into a running turn. It's shaped like the part
// of PromptRequest that matters, so the adapter converts it the same way.
type SteerRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
	Meta      *SteerMeta     `json:"_meta,omitempty"`
}

type SteerMeta struct {
	Steering SteerOptions `json:"steering"`
}

type SteerOptions struct {
	// IdleBehavior of "promptRequired" makes the adapter hand a message back
	// rather than start a turn for it when no turn is running after all. An
	// adapter that starts one runs a turn its client never asked for and isn't
	// following; adapters that don't know the option ignore it.
	IdleBehavior string `json:"idleBehavior,omitempty"`
}

type SteerResponse struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

// What a SteerResponse says became of the message.
const (
	SteerInjected       = "injected"       // it joined the running turn
	SteerStartedNewTurn = "startedNewTurn" // no turn was running, so the adapter started one
	// SteerPromptRequired is both what an adapter answers when no turn was
	// running and the SteerOptions.IdleBehavior that asks it to answer that way.
	SteerPromptRequired = "promptRequired"
)

// ContentBlock is text, an image, or a reference to a resource. AgentBox reads
// the text of blocks and describes the rest.
type ContentBlock struct {
	Type     string `json:"type"` // text, image, audio, resource_link or resource
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	URI      string `json:"uri,omitempty"`
	Name     string `json:"name,omitempty"`
}

type SessionNotification struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// SessionUpdate is every kind of session/update, told apart by SessionUpdate.
// Only the fields of that kind are set.
type SessionUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`

	// agent_message_chunk, agent_thought_chunk, user_message_chunk
	Content   json.RawMessage `json:"content,omitempty"` // a ContentBlock; for tool calls, []ToolCallContent
	MessageID string          `json:"messageId,omitempty"`

	// tool_call, tool_call_update
	ToolCallID string             `json:"toolCallId,omitempty"`
	Title      *string            `json:"title,omitempty"`
	Name       string             `json:"name,omitempty"`
	Kind       string             `json:"kind,omitempty"`
	Status     string             `json:"status,omitempty"`
	Locations  []ToolCallLocation `json:"locations,omitempty"`
	RawInput   json.RawMessage    `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage    `json:"rawOutput,omitempty"`

	// plan
	Entries []PlanEntry `json:"entries,omitempty"`

	// available_commands_update
	AvailableCommands []AvailableCommand `json:"availableCommands,omitempty"`

	// config_option_update
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`

	// current_mode_update
	CurrentModeID string `json:"currentModeId,omitempty"`

	// subagent_spawned (with Name) and subagent_state_update, on the session
	// that started the subagent
	SubagentSessionID string `json:"subagentSessionId,omitempty"`
	Task              string `json:"task,omitempty"`
	State             string `json:"state,omitempty"` // completed, failed, disconnected or cancelled

	// usage_update: the context window, and — on the update that follows a
	// model result — what the session has cost so far (see Cost)
	Used int64 `json:"used,omitempty"`
	Size int64 `json:"size,omitempty"`
	Cost *Cost `json:"cost,omitempty"`

	Meta struct {
		ClaudeCode struct {
			ToolName string `json:"toolName"`
		} `json:"claudeCode"`
		// RateLimit rides on a usage_update when the provider's response
		// carried the account's limits (claude-agent-acp only).
		RateLimit *RateLimit `json:"_claude/rateLimit,omitempty"`
	} `json:"_meta"`
}

// Text returns the text of a message chunk.
func (u SessionUpdate) Text() string {
	var block ContentBlock
	if json.Unmarshal(u.Content, &block) != nil {
		return ""
	}
	return BlockText(block)
}

// ToolContent returns a tool call's content, or nil when the update doesn't change it.
func (u SessionUpdate) ToolContent() ([]ToolCallContent, bool) {
	if len(u.Content) == 0 || string(u.Content) == "null" {
		return nil, false
	}
	var content []ToolCallContent
	if json.Unmarshal(u.Content, &content) != nil {
		return nil, false
	}
	return content, true
}

// BlockText is a content block as text: its text, or a short description of what it holds.
func BlockText(b ContentBlock) string {
	switch b.Type {
	case "text", "":
		return b.Text
	case "resource_link":
		return "[" + b.Name + "](" + b.URI + ")"
	case "image":
		return "[image]"
	default:
		return "[" + b.Type + "]"
	}
}

type ToolCallLocation struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
}

// ToolCallContent is what a tool call produced: content, a diff, or a terminal.
type ToolCallContent struct {
	Type    string       `json:"type"` // content, diff or terminal
	Content ContentBlock `json:"content"`
	Path    string       `json:"path,omitempty"`
	OldText *string      `json:"oldText,omitempty"`
	NewText string       `json:"newText,omitempty"`
}

type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority"` // high, medium or low
	Status   string `json:"status"`   // pending, in_progress or completed
}

type AvailableCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Input       *struct {
		Hint string `json:"hint"`
	} `json:"input,omitempty"`
}

type RequestPermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  SessionUpdate      `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // allow_once, allow_always, reject_once or reject_always
}

type RequestPermissionResponse struct {
	Outcome PermissionOutcome `json:"outcome"`
}

type PermissionOutcome struct {
	Outcome  string `json:"outcome"` // selected or cancelled
	OptionID string `json:"optionId,omitempty"`
}
