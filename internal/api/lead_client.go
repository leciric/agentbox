package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
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

// ProjectArchivedAgents lists the past agents of the project behind the lead
// socket; opt.Project is ignored.
func (c *Client) ProjectArchivedAgents(ctx context.Context, opt ArchivedOptions) ([]ArchivedAgent, error) {
	var out []ArchivedAgent
	return out, c.do(ctx, http.MethodGet, c.leadPath("/archived-agents")+"?"+opt.values().Encode(), nil, &out)
}

// ProjectAccounts lists this machine's Claude Code accounts, from the project
// behind the lead socket's own point of view: which is its default, how many
// of its running agents use each, and each one's latest usage reading.
func (c *Client) ProjectAccounts(ctx context.Context) ([]LeadAccount, error) {
	var out []LeadAccount
	return out, c.do(ctx, http.MethodGet, c.leadPath("/accounts"), nil, &out)
}

// ProjectToolAccounts are the machine's Codex, OpenCode and Cursor accounts.
func (c *Client) ProjectToolAccounts(ctx context.Context) ([]ToolAccount, error) {
	var out []ToolAccount
	return out, c.do(ctx, http.MethodGet, c.leadPath("/tool-accounts"), nil, &out)
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

func (c *Client) TellAgent(ctx context.Context, agent, message string) (TellResult, error) {
	var out TellResult
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

// LeadHostCopyRequest asks to copy a file or directory from a project's
// repository on the host into an agent's machine. Path is absolute and must
// be inside one of the user's projects; Into is relative to the agent's
// worktree (its root when empty).
type LeadHostCopyRequest struct {
	Path    string `json:"path"`
	To      string `json:"to"`
	Into    string `json:"into,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
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

// CopyToAgent copies a file or directory from a project's repository on the host into an agent's machine.
func (c *Client) CopyToAgent(ctx context.Context, req LeadHostCopyRequest) (LeadCopyResult, error) {
	var out LeadCopyResult
	return out, c.do(ctx, http.MethodPost, c.leadPath("/copy-from-host"), req, &out)
}

// AgentCheckpoints lists one of the project's agents' checkpoints, oldest first.
func (c *Client) AgentCheckpoints(ctx context.Context, agent string) ([]Checkpoint, error) {
	var out []Checkpoint
	return out, c.do(ctx, http.MethodGet, c.leadPath("/agents/"+url.PathEscape(agent)+"/checkpoints"), nil, &out)
}

// RollbackAgent puts one of the project's agents back to a checkpoint.
func (c *Client) RollbackAgent(ctx context.Context, agent, checkpoint string) (RollbackResult, error) {
	var out RollbackResult
	return out, c.do(ctx, http.MethodPost, c.leadPath("/agents/"+url.PathEscape(agent)+"/rollback"), RollbackRequest{Checkpoint: checkpoint}, &out)
}

// SwitchAgentAccount moves one of the project's running agents to another
// account of its AI tool, resuming its chat on it.
func (c *Client) SwitchAgentAccount(ctx context.Context, agent string, req AgentAccountRequest) (AgentAccountSwitch, error) {
	var out AgentAccountSwitch
	return out, c.do(ctx, http.MethodPost, c.leadPath("/agents/"+url.PathEscape(agent)+"/account"), req, &out)
}

// ForkAgent starts a job making a new agent from one of the project's.
func (c *Client) ForkAgent(ctx context.Context, agent string, req ForkRequest) (Job, error) {
	var out Job
	return out, c.do(ctx, http.MethodPost, c.leadPath("/agents/"+url.PathEscape(agent)+"/fork"), req, &out)
}

// ProjectSkills lists every skill, each saying whether the lead's project's
// agents get it.
func (c *Client) ProjectSkills(ctx context.Context) ([]Skill, error) {
	var out []Skill
	return out, c.do(ctx, http.MethodGet, c.leadPath("/skills"), nil, &out)
}

// ProjectSkill is one skill with its files.
func (c *Client) ProjectSkill(ctx context.Context, name string) (SkillDetail, error) {
	var out SkillDetail
	return out, c.do(ctx, http.MethodGet, c.leadPath("/skills/"+url.PathEscape(name)), nil, &out)
}

// CreateProjectSkill stores a new skill the lead wrote.
func (c *Client) CreateProjectSkill(ctx context.Context, req LeadNewSkillRequest) (Skill, error) {
	var out Skill
	return out, c.do(ctx, http.MethodPost, c.leadPath("/skills"), req, &out)
}

// EditProjectSkill replaces a skill's SKILL.md once the user approves it.
func (c *Client) EditProjectSkill(ctx context.Context, name, content string) (Skill, error) {
	var out Skill
	return out, c.do(ctx, http.MethodPut, c.leadPath("/skills/"+url.PathEscape(name)), SaveSkillRequest{Content: content}, &out)
}

// SwitchProjectSkill turns a skill on or off, in the lead's project or
// AgentBox-wide; turning one off waits for the user's approval.
func (c *Client) SwitchProjectSkill(ctx context.Context, name string, req LeadSkillSwitchRequest) (Skill, error) {
	var out Skill
	return out, c.do(ctx, http.MethodPut, c.leadPath("/skills/"+url.PathEscape(name)+"/switch"), req, &out)
}

// RemoveProjectSkill deletes a skill once the user approves it.
func (c *Client) RemoveProjectSkill(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, c.leadPath("/skills/"+url.PathEscape(name)), nil, nil)
}

// Talking to other projects' leads. Each request names the other project or
// the thread, never its own: the socket still says which project is talking.

// ProjectMessageRequest starts a thread with another project's lead: a
// message, or, with Topic, an alignment on it.
type ProjectMessageRequest struct {
	Project      string `json:"project"`
	Message      string `json:"message"`
	ExpectsReply bool   `json:"expectsReply,omitempty"`
	Topic        string `json:"topic,omitempty"`
}

// ProjectReplyRequest answers the other lead in a thread.
type ProjectReplyRequest struct {
	Message      string `json:"message"`
	ExpectsReply bool   `json:"expectsReply,omitempty"`
}

// ProjectSettleRequest settles an alignment with what the two leads agreed.
type ProjectSettleRequest struct {
	Summary string `json:"summary"`
}

// ProjectThread is a conversation between two projects' leads, from one of
// them's side: Project is the other.
type ProjectThread struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Kind      string    `json:"kind"` // message or align
	Topic     string    `json:"topic,omitempty"`
	Status    string    `json:"status"` // open or settled
	Summary   string    `json:"summary,omitempty"`
	StartedBy string    `json:"startedBy"`
	Messages  int       `json:"messages"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Last is the thread's newest message.
	Last *ProjectThreadMessage `json:"last,omitempty"`
}

// ProjectThreadMessage is one message in a thread.
type ProjectThreadMessage struct {
	From         string    `json:"from"`
	Text         string    `json:"text"`
	Hop          int       `json:"hop"`
	ExpectsReply bool      `json:"expectsReply,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

// MessageProject sends another project's lead a message, starting a thread.
func (c *Client) MessageProject(ctx context.Context, req ProjectMessageRequest) (ProjectThread, error) {
	var out ProjectThread
	return out, c.do(ctx, http.MethodPost, c.leadPath("/threads"), req, &out)
}

// ReplyProject answers the other project's lead in a thread.
func (c *Client) ReplyProject(ctx context.Context, thread string, req ProjectReplyRequest) (ProjectThread, error) {
	var out ProjectThread
	return out, c.do(ctx, http.MethodPost, c.leadPath("/threads/"+url.PathEscape(thread)+"/replies"), req, &out)
}

// SettleAlignment settles an alignment thread.
func (c *Client) SettleAlignment(ctx context.Context, thread, summary string) (ProjectThread, error) {
	var out ProjectThread
	return out, c.do(ctx, http.MethodPost, c.leadPath("/threads/"+url.PathEscape(thread)+"/settle"), ProjectSettleRequest{Summary: summary}, &out)
}

// ProjectThreads lists the project's threads with other projects' leads.
func (c *Client) ProjectThreads(ctx context.Context) ([]ProjectThread, error) {
	var out []ProjectThread
	return out, c.do(ctx, http.MethodGet, c.leadPath("/threads"), nil, &out)
}

// ProjectFanOut runs one task on several agents of the project behind the
// lead socket, as a group to compare.
func (c *Client) ProjectFanOut(ctx context.Context, req FanOutRequest) (FanOutResult, error) {
	var out FanOutResult
	return out, c.do(ctx, http.MethodPost, c.leadPath("/groups"), req, &out)
}

// ProjectGroups lists the project's groups: every open one and the recently
// decided.
func (c *Client) ProjectGroups(ctx context.Context) ([]Group, error) {
	var out []Group
	return out, c.do(ctx, http.MethodGet, c.leadPath("/groups"), nil, &out)
}

// ProjectGroup is one group, its agents compared.
func (c *Client) ProjectGroup(ctx context.Context, id string) (Group, error) {
	var out Group
	return out, c.do(ctx, http.MethodGet, c.leadPath("/groups/"+url.PathEscape(id)), nil, &out)
}

// RecommendInGroup records the lead's pick of a group's agents.
func (c *Client) RecommendInGroup(ctx context.Context, id string, req RecommendRequest) (Group, error) {
	var out Group
	return out, c.do(ctx, http.MethodPost, c.leadPath("/groups/"+url.PathEscape(id)+"/recommend"), req, &out)
}
