package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A hidden prompt in a session of its own, on a model of its own (D78).
//
// Ask (hidden.go) borrows the chat's session because it is warm and already
// knows the project. That is the right trade for a question about the
// conversation — compaction's is one — and the wrong one for a distillation,
// which is summarising rather than engineering and has no business being
// billed to the model the user chats on. It also spends the chat's own
// context: a 60 KiB window of events and the answer to it land in the
// session's history, so consolidating a project brings its next compaction
// closer.
//
// AskAside is the other half of that choice: the same AI tool, the same login
// and the same worktree, in a session nothing else will ever use. It starts an
// adapter of its own, asks once, and throws the session away — it never
// touches a conversation, never takes its lock, and never stores a session id,
// so a turn the user is taking meanwhile neither waits for it nor loses it.
//
// Everything about it is allowed to fail. The caller falls back to Ask, which
// is what consolidation did before there was a choice.

// asideStartTimeout bounds the adapter's handshake. The prompt itself is
// bounded by ctx, which a caller sets from how long it is willing to wait
// altogether; this is only the part before anything has been asked, where a
// tool that is being installed, or one that will never answer, looks the same.
const asideStartTimeout = startTimeout

// AskAside runs one hidden prompt in a session of its own and answers with
// what it said, and with the model it really ran on.
//
// model is the model to start on: "" leaves the tool on whatever it would
// choose for itself. A model the tool refuses is an error and not a silent
// downgrade — the whole point of asking is to spend less, and a pass that
// quietly ran on the expensive model would be invisible in the pass log.
//
// The session takes no turn and answers no permission request. It is not
// sandboxed beyond that: launching through the chat's own Launcher means it
// starts in the same HOME, so it reads the same brief and is held by the same
// permission policy — for a lead, the deny rules that keep it off the host's
// shell (leadSettings). What that leaves it able to do without asking is read
// files in the worktree, which wastes a little of a cheap model and nothing
// else; everything that would need permission is refused, because there is
// nobody watching this session to say yes.
func (m *Manager) AskAside(ctx context.Context, a state.Agent, model, ask string) (answer, ranOn string, err error) {
	if m.Launch == nil {
		return "", "", errNoSession
	}
	proc, err := m.Launch(ctx, a, func(string) {})
	if err != nil {
		return "", "", err
	}
	// Whatever happens, the adapter goes: this session exists for one prompt,
	// and a process left behind would hold a login open for nothing.
	defer func() {
		proc.Stop()
		proc.Stdout.Close()
	}()

	h := &asideHandler{}
	conn := acp.NewConn(proc.Stdout, proc.Stdin, h)
	explain := func(err error) error {
		if !errors.Is(err, acp.ErrClosed) {
			return err
		}
		if line := lastLine(proc.Stderr()); line != "" {
			return fmt.Errorf("%w: %s", err, line)
		}
		return err
	}

	start, cancel := context.WithTimeout(ctx, asideStartTimeout)
	defer cancel()
	var init acp.InitializeResponse
	if err := conn.Call(start, acp.MethodInitialize, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion,
		ClientInfo:      &acp.Implementation{Name: "agentbox", Title: "AgentBox", Version: m.Version},
	}, &init); err != nil {
		return "", "", explain(err)
	}
	// A new session every time, never a resume: the one thing this must not
	// do is read, or add to, the conversation the user is having.
	var session acp.SessionResponse
	if err := conn.Call(start, acp.MethodSessionNew, acp.NewSessionRequest{
		Cwd: a.Worktree, McpServers: []acp.McpServer{},
	}, &session); err != nil {
		return "", "", explain(err)
	}
	if session.SessionID == "" {
		return "", "", errors.New("the adapter started no session")
	}
	h.session(session.SessionID)

	options := toOptions(session.ConfigOptions)
	ranOn = optionValueOf(options, "model")
	if model != "" {
		set, err := setAsideModel(start, conn, session.SessionID, options, model)
		if err != nil {
			return "", "", fmt.Errorf("%s wouldn't distil on %q: %w", ToolNames[a.AI], model, err)
		}
		ranOn = set
	}

	var res acp.PromptResponse
	err = conn.Call(ctx, acp.MethodSessionPrompt, acp.PromptRequest{
		SessionID: session.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: ask}},
	}, &res)
	// A session nothing else will use still spent its tokens on this project:
	// a distillation is billed to the account like any turn, so it goes in
	// the ledger under the chat that asked for it.
	if rows := tokenRows(state.TokenRow{
		Project: a.Project, Agent: a.Name, AI: a.AI, Session: session.SessionID,
		Turn: newID(), Kind: state.TokensConsolidation, At: m.now(),
	}, res.ByModel(ranOn), ranOn, h.cost()); len(rows) > 0 {
		go m.recordTokens(rows)
	}
	if err != nil {
		return h.answer(), ranOn, explain(err)
	}
	answer = h.answer()
	if strings.TrimSpace(answer) == "" {
		return "", ranOn, errors.New("the session said nothing")
	}
	return answer, ranOn, nil
}

// setAsideModel points the session at a model and answers with the one it
// reports afterwards, which is not always the one that was asked for: a tool
// resolves aliases ("haiku") to whatever the account really runs.
//
// A model the session's own menu doesn't list is refused here rather than
// sent, because set_config_option is validated against that menu (D46) and
// its refusal arrives as an internal error that says nothing useful. Unlike
// the chat, an aside has nowhere to store a preference for next time: this
// session is the only one there will be, so a model it can't take is an
// answer now, and the caller falls back.
func setAsideModel(ctx context.Context, conn *acp.Conn, sessionID string, options []api.ChatOption, model string) (string, error) {
	i := slices.IndexFunc(options, func(o api.ChatOption) bool { return o.ID == "model" })
	if i < 0 {
		return "", errors.New("it offers no model setting")
	}
	option := options[i]
	if !validValue(option, model) {
		return "", fmt.Errorf("this account's menu doesn't list it (it offers %s)", strings.Join(choiceValues(option), ", "))
	}
	var out acp.SetConfigOptionResponse
	if err := conn.Call(ctx, acp.MethodSetConfigOption, setRequest(sessionID, option, model), &out); err != nil {
		return "", err
	}
	if value := optionValueOf(toOptions(out.ConfigOptions), "model"); value != "" {
		return value, nil
	}
	return model, nil
}

// choiceValues are the values a setting offers, for an error that says what
// could have been asked for instead.
func choiceValues(o api.ChatOption) []string {
	out := make([]string, 0, len(o.Choices))
	for _, c := range o.Choices {
		out = append(out, c.Value)
	}
	return out
}

// Model is the model an agent's chat is running on, as its tool last said, or
// the preference its next session will start on when none is running. It is
// "" when nothing is known, which is a chat whose session has never started
// and whose model was never chosen.
//
// It reads a conversation this daemon already has rather than making one:
// asking what a chat runs on must not start a chat.
func (m *Manager) Model(a state.Agent) string {
	c := m.existing(a.Ref())
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if value := optionValueOf(c.session.Options, "model"); value != "" {
		return value
	}
	return c.stored.Options["model"]
}

// asideHandler is the whole client side of an aside session: it collects the
// text and refuses everything else.
type asideHandler struct {
	mu        sync.Mutex
	sessionID string
	text      strings.Builder
	spend     spend
}

// cost is what the session has cost so far, for the token ledger.
func (h *asideHandler) cost() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.spend.take()
}

func (h *asideHandler) session(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessionID = id
}

func (h *asideHandler) answer() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.text.String()
}

// Notify keeps the message chunks and the cost, and drops the rest. There is
// no conversation to update — the session is about to be thrown away — and no
// plan or tool call worth showing anybody.
func (h *asideHandler) Notify(method string, params json.RawMessage) {
	if method != acp.MethodSessionUpdate {
		return
	}
	var n acp.SessionNotification
	if json.Unmarshal(params, &n) != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessionID != "" && n.SessionID != h.sessionID {
		return
	}
	switch n.Update.SessionUpdate {
	case "agent_message_chunk":
		h.text.WriteString(n.Update.Text())
	case "usage_update":
		h.spend.observe(n.Update.Cost)
	}
}

// Request says no, to everything. A permission request is the interesting
// one: answering "cancelled" is what the chat's own handler does outside a
// turn, and it is the honest answer here — nobody is watching this session,
// so nothing can say yes on the user's behalf.
func (h *asideHandler) Request(method string, params json.RawMessage, reply func(any, error)) {
	if method == acp.MethodRequestPermission {
		reply(acp.RequestPermissionResponse{Outcome: acp.PermissionOutcome{Outcome: "cancelled"}}, nil)
		return
	}
	reply(nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: method + " isn't supported"})
}
