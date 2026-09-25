package api

import "time"

// Chat: a conversation with an agent's AI tool, which runs inside the agent and
// is driven over the Agent Client Protocol.

// ChatThread is an agent's conversation, as of event Seq.
type ChatThread struct {
	Agent   string      `json:"agent"`
	Seq     int64       `json:"seq"` // the last event it includes
	Session ChatSession `json:"session"`
	Items   []ChatItem  `json:"items"`
}

// ChatSession is the state of the AI tool's session behind a conversation.
type ChatSession struct {
	State   string `json:"state"`            // off, starting, ready, running, waiting (for your answer) or error
	Tool    string `json:"tool"`             // claude, codex or opencode
	Detail  string `json:"detail,omitempty"` // what it's doing while it starts
	Error   string `json:"error,omitempty"`
	Adapter string `json:"adapter,omitempty"` // the ACP adapter and its version
	// TurnStartedAt is when the running turn started, while one runs.
	TurnStartedAt *time.Time    `json:"turnStartedAt,omitempty"`
	Options       []ChatOption  `json:"options"`  // settings the AI tool offers, like its model
	Commands      []ChatCommand `json:"commands"` // slash commands
	ContextUsed   int64         `json:"contextUsed,omitempty"`
	ContextSize   int64         `json:"contextSize,omitempty"` // tokens the context window holds
	// Limited says the last turn was cut short by the AI tool's usage limit:
	// the one failure that waiting puts right. Cleared when a turn starts.
	Limited bool `json:"limited,omitempty"`
	// LimitedUntil is when the tool said that limit resets, when it said at
	// all. Absent means it didn't, not that the limit is over.
	LimitedUntil *time.Time `json:"limitedUntil,omitempty"`
	// ResumeAt is when AgentBox carries the limited turn on by itself.
	// Absent means it won't: the setting is off, or it has waited enough
	// times already.
	ResumeAt *time.Time `json:"resumeAt,omitempty"`
	// NoImages says the AI tool's adapter doesn't take images in a prompt
	// (ACP's promptCapabilities.image), so the composer doesn't offer to
	// attach any. Until an adapter of this tool has started once there is
	// nothing to go on, and images are allowed.
	NoImages bool `json:"noImages,omitempty"`
}

const (
	ChatOff      = "off"
	ChatStarting = "starting"
	ChatReady    = "ready"
	ChatRunning  = "running"
	ChatWaiting  = "waiting"
	ChatError    = "error"
)

// ChatOption is a setting of the session, like the model or the permission mode.
type ChatOption struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Category    string             `json:"category,omitempty"` // mode, model, thought_level, or the tool's own
	Type        string             `json:"type"`               // select or boolean
	Value       string             `json:"value"`              // "true" or "false" for a boolean
	Choices     []ChatOptionChoice `json:"choices"`
}

type ChatOptionChoice struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Group       string `json:"group,omitempty"`
	Kind        string `json:"kind,omitempty"` // for modes: standard, plan, auto_review or full_access
}

type ChatCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Hint        string `json:"hint,omitempty"` // what to type after it
}

// ChatItem is one entry of a conversation.
type ChatItem struct {
	ID string `json:"id"`
	// Turn is the ID of the user message whose turn this item belongs to.
	Turn string `json:"turn"`
	Kind string `json:"kind"` // user, aside, assistant, thought, tool, plan, permission, subagent, notice or error
	Text string `json:"text,omitempty"`
	// Images, on a user message or an aside, are the pictures sent with it.
	Images []ChatImage `json:"images,omitempty"`
	// Delivery, on an aside, is how that message reached the AI tool.
	Delivery string `json:"delivery,omitempty"`
	// Hidden marks an item the AI tool has to read but nobody wants to: the
	// prose AgentBox writes a lead when one of its agents finishes or asks,
	// and the turn that prose starts. The app leaves these out of the
	// timeline and shows the AgentEvent recorded beside them instead; the
	// lead's own answer, an ordinary assistant message, is what stays.
	Hidden     bool            `json:"hidden,omitempty"`
	Streaming  bool            `json:"streaming,omitempty"` // more text is coming
	Tool       *ChatTool       `json:"tool,omitempty"`
	Plan       []ChatPlanEntry `json:"plan,omitempty"`
	Permission *ChatPermission `json:"permission,omitempty"`
	Result     *ChatTurnResult `json:"result,omitempty"` // on a user message, once its turn has ended
	// Subagent is set on a subagent's card (D86): a subagent the AI tool
	// started, running in a session of its own.
	Subagent *ChatSubagent `json:"subagent,omitempty"`
	// Parent, on a message, thought or tool call a subagent made, is that
	// subagent's card. The app nests the item under the card instead of
	// showing it in the conversation itself.
	Parent    string    `json:"parent,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// An aside is a message sent while a turn was already running. It belongs to
// that turn rather than heading one of its own, and Delivery says what became
// of it.
const (
	ChatAsideWaiting  = "waiting"  // taken, not yet handed to the AI tool
	ChatAsideSent     = "sent"     // the AI tool took it into the running turn
	ChatAsideDeferred = "deferred" // it couldn't join that turn: it goes in as its own, after
	ChatAsideLost     = "lost"     // the session ended before it could be delivered
)

// ChatSubagent is a subagent the AI tool started (D86). What it does — its
// messages and tool calls — are items whose Parent is its card.
type ChatSubagent struct {
	Name string `json:"name"`
	Task string `json:"task"` // what it was asked to do
	// State is running, completed, failed, cancelled, disconnected, or stopped
	// when its session ended before it said.
	State string `json:"state"`
}

// ChatTool is a tool call: a command, a file read or edit, a search.
type ChatTool struct {
	CallID  string     `json:"callId"`
	Name    string     `json:"name,omitempty"` // the tool's own name, like Bash or Edit
	Title   string     `json:"title"`
	Kind    string     `json:"kind"`   // read, edit, delete, move, search, execute, think, fetch or other
	Status  string     `json:"status"` // pending, in_progress, completed, failed, or stopped when the turn ended first
	Command string     `json:"command,omitempty"`
	Paths   []string   `json:"paths,omitempty"`
	Output  string     `json:"output,omitempty"` // the end of its output
	Diffs   []ChatDiff `json:"diffs,omitempty"`
}

type ChatDiff struct {
	Path      string `json:"path"`
	OldText   string `json:"oldText"`
	NewText   string `json:"newText"`
	Created   bool   `json:"created,omitempty"`   // the file didn't exist before
	Truncated bool   `json:"truncated,omitempty"` // the texts were too long to keep whole
}

type ChatPlanEntry struct {
	Content string `json:"content"`
	Status  string `json:"status"` // pending, in_progress or completed
}

// ChatPermission is the AI tool asking before it uses a tool.
type ChatPermission struct {
	CallID  string                 `json:"callId"` // the tool call it asks about
	Title   string                 `json:"title"`
	Options []ChatPermissionOption `json:"options"`
	Outcome string                 `json:"outcome,omitempty"` // the ID of the option chosen, or "cancelled"; empty while it waits
}

type ChatPermissionOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // allow_once, allow_always, reject_once or reject_always
}

// ChatTurnResult is how a turn ended.
type ChatTurnResult struct {
	State      string    `json:"state"`                // completed, cancelled or failed
	StopReason string    `json:"stopReason,omitempty"` // the AI tool's reason, like end_turn or max_tokens
	EndedAt    time.Time `json:"endedAt"`
}

type ChatMessageRequest struct {
	Text   string            `json:"text"`
	Images []ChatImageUpload `json:"images,omitempty"`
}

// ChatImage is a picture sent with a message. Its bytes are kept in a file
// beside the conversation rather than in it, and served from
// .../chat/images/{id}.
type ChatImage struct {
	ID       string `json:"id"`
	MimeType string `json:"mimeType"` // image/png, image/jpeg, image/gif or image/webp
	Name     string `json:"name,omitempty"`
	Size     int64  `json:"size"` // bytes
}

// ChatImageUpload is a picture attached to a message being sent.
type ChatImageUpload struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
	Name     string `json:"name,omitempty"`
}

// The limits on a message's pictures. MaxChatImageBytes is what Anthropic's
// API takes in one image, 5 MiB once it is base64, as the file it decodes to.
const (
	MaxChatImages     = 8
	MaxChatImageBytes = 5 << 20 / 4 * 3
)

// ChatAnswerRequest answers a permission request; an empty option cancels it.
type ChatAnswerRequest struct {
	OptionID string `json:"optionId"`
}

type ChatOptionRequest struct {
	Value string `json:"value"`
}

// ProjectChat is a project's own chat, driven by its lead: the one agent that
// runs on the host, with no machine of its own. A project that has never been
// chatted with has nothing on disk, and Started is false.
type ProjectChat struct {
	Project string `json:"project"`
	Ref     string `json:"ref"`     // the lead's agent ref, for the chat routes
	Started bool   `json:"started"` // it has a worktree and a private HOME
	// Worktree is where the lead reads the project, standing on BaseRef with a
	// detached HEAD. Both are empty until it has started.
	Worktree string `json:"worktree,omitempty"`
	BaseRef  string `json:"baseRef,omitempty"`
	Chat     string `json:"chat"` // its session's state, as in ChatSession.State
}

const EventChat = "chat"

// ChatEvent is one change to an agent's conversation. An agent's events are
// numbered one after another, so a client that misses one fetches the thread again.
type ChatEvent struct {
	Agent   string       `json:"agent"`
	Seq     int64        `json:"seq"`
	Item    *ChatItem    `json:"item,omitempty"`    // added, or replaced whole
	Append  *ChatAppend  `json:"append,omitempty"`  // text added to an item
	Session *ChatSession `json:"session,omitempty"` // the session's new state
	Cleared bool         `json:"cleared,omitempty"` // every item was removed
}

// ChatAppend is text added to the end of an item's text.
type ChatAppend struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
