package api

import (
	"context"
	"net/http"
	"net/url"
)

// The lead client: what a project's chat calls, on the socket that decides
// which project it is. No path here names a project, because the chat can't
// choose one — that is the boundary.

func (c *Client) leadPath(suffix string) string { return "/v1/project" + suffix }

// ProjectFleet lists the agents of the project behind the lead socket.
func (c *Client) ProjectFleet(ctx context.Context) (Fleet, error) {
	var out Fleet
	return out, c.do(ctx, http.MethodGet, c.leadPath("/agents"), nil, &out)
}

// ProjectAccounts lists this machine's Claude Code accounts, from the project
// behind the lead socket's own point of view: which is its default, how many
// of its running agents use each, and each one's latest usage reading.
func (c *Client) ProjectAccounts(ctx context.Context) ([]LeadAccount, error) {
	var out []LeadAccount
	return out, c.do(ctx, http.MethodGet, c.leadPath("/accounts"), nil, &out)
}

func (c *Client) ProjectSelf(ctx context.Context) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodGet, c.leadPath(""), nil, &out)
}

// ProjectSettings reports what new agents start on, and the model and effort
// menus Claude Code last advertised for this account.
func (c *Client) ProjectSettings(ctx context.Context) (Settings, error) {
	var out Settings
	return out, c.do(ctx, http.MethodGet, c.leadPath("/settings"), nil, &out)
}

// CreateProjectAgent makes an agent for the project behind the lead socket.
func (c *Client) CreateProjectAgent(ctx context.Context, req CreateAgentRequest) (Job, error) {
	var out Job
	return out, c.do(ctx, http.MethodPost, c.leadPath("/agents"), req, &out)
}

func (c *Client) TellAgent(ctx context.Context, agent, message string) (ChatItem, error) {
	var out ChatItem
	path := c.leadPath("/agents/" + url.PathEscape(agent) + "/chat/messages")
	return out, c.do(ctx, http.MethodPost, path, ChatMessageRequest{Text: message}, &out)
}

func (c *Client) AgentChat(ctx context.Context, agent string) (ChatThread, error) {
	var out ChatThread
	return out, c.do(ctx, http.MethodGet, c.leadPath("/agents/"+url.PathEscape(agent)+"/chat"), nil, &out)
}

func (c *Client) AgentDiff(ctx context.Context, agent string, stat bool, paths ...string) (string, error) {
	var out struct {
		Diff string `json:"diff"`
	}
	path := c.leadPath("/agents/" + url.PathEscape(agent) + "/diff")
	q := url.Values{}
	if stat {
		q.Set("stat", "true")
	}
	for _, p := range paths {
		q.Add("path", p)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out.Diff, c.do(ctx, http.MethodGet, path, nil, &out)
}

// RetireProject frees agents of the project behind the socket.
func (c *Client) RetireProject(ctx context.Context, req RetireRequest) (RetireResult, error) {
	var out RetireResult
	return out, c.do(ctx, http.MethodPost, c.leadPath("/retire"), req, &out)
}

// ProjectNotes reads the notes of the project behind the lead socket.
func (c *Client) ProjectNotes(ctx context.Context) (Notes, error) {
	var out Notes
	return out, c.do(ctx, http.MethodGet, c.leadPath("/notes"), nil, &out)
}

// AppendProjectNote adds one entry to those notes, under the lead's heading.
func (c *Client) AppendProjectNote(ctx context.Context, text string) (Notes, error) {
	var out Notes
	return out, c.do(ctx, http.MethodPost, c.leadPath("/notes"), NotesRequest{Text: text}, &out)
}

// EditProjectNote changes what one entry of them says, named by quoting it.
func (c *Client) EditProjectNote(ctx context.Context, match, text string) (NoteChange, error) {
	var out NoteChange
	return out, c.do(ctx, http.MethodPost, c.leadPath("/notes/edit"), EditNoteRequest{Match: match, Text: text}, &out)
}

// RemoveProjectNote takes one entry out of them, the same way.
func (c *Client) RemoveProjectNote(ctx context.Context, match string) (NoteChange, error) {
	var out NoteChange
	return out, c.do(ctx, http.MethodPost, c.leadPath("/notes/remove"), EditNoteRequest{Match: match}, &out)
}

func (c *Client) ProjectQuestions(ctx context.Context) ([]Question, error) {
	var out []Question
	return out, c.do(ctx, http.MethodGet, c.leadPath("/questions"), nil, &out)
}

func (c *Client) LeadAnswerQuestion(ctx context.Context, id, answer string) (Question, error) {
	var out Question
	path := c.leadPath("/questions/" + url.PathEscape(id) + "/answer")
	return out, c.do(ctx, http.MethodPost, path, AnswerQuestionRequest{Answer: answer}, &out)
}

func (c *Client) LeadEscalateQuestion(ctx context.Context, id, why string) (Question, error) {
	var out Question
	path := c.leadPath("/questions/" + url.PathEscape(id) + "/escalate")
	return out, c.do(ctx, http.MethodPost, path, EscalateQuestionRequest{Why: why}, &out)
}

// LeadRunRequest is a command the chat runs in one of its agents' machines
// (D89). Timeout is in seconds; zero means the default.
type LeadRunRequest struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// LeadRunResult is how it went. Output is only the tail; Bytes is how much
// there was in all.
type LeadRunResult struct {
	ExitCode int    `json:"exitCode"`
	Output   string `json:"output"`
	Bytes    int64  `json:"bytes"`
	TimedOut bool   `json:"timedOut,omitempty"`
}

// LeadCopyRequest copies Path in agent From's machine into directory Into in
// agent To's.
type LeadCopyRequest struct {
	From    string `json:"from"`
	Path    string `json:"path"`
	To      string `json:"to"`
	Into    string `json:"into,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

type LeadCopyResult struct {
	Bytes int64 `json:"bytes"`
}

// RunInAgent runs a shell command in an agent's machine, in its worktree.
func (c *Client) RunInAgent(ctx context.Context, agent string, req LeadRunRequest) (LeadRunResult, error) {
	var out LeadRunResult
	return out, c.do(ctx, http.MethodPost, c.leadPath("/agents/"+url.PathEscape(agent)+"/run"), req, &out)
}

// CopyBetweenAgents copies a file or directory from one agent's machine to another's.
func (c *Client) CopyBetweenAgents(ctx context.Context, req LeadCopyRequest) (LeadCopyResult, error) {
	var out LeadCopyResult
	return out, c.do(ctx, http.MethodPost, c.leadPath("/copy"), req, &out)
}
