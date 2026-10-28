package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to a daemon: over its unix socket, or through a hub.
type Client struct {
	socket string // empty through a hub
	base   string // the URL paths are relative to
	token  string // a hub session, sent as a bearer token
	http   *http.Client
}

func NewClient(socket string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	return &Client{socket: socket, base: "http://agentbox", http: &http.Client{Transport: transport}}
}

// NewRemoteClient talks to a daemon through a hub. base is the environment's
// API on the hub, https://<hub>/v1/environments/<id>/api, and token a session
// on that hub.
func NewRemoteClient(base, token string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{}}
}

// Socket is the daemon's socket, or its URL through a hub.
func (c *Client) Socket() string {
	if c.socket == "" {
		return c.base
	}
	return c.socket
}

// Remote reports whether the client goes through a hub.
func (c *Client) Remote() bool { return c.socket == "" }

// HTTPClient returns the underlying client, which dials the daemon's socket;
// use it for WebSocket connections such as agent terminals.
func (c *Client) HTTPClient() *http.Client { return c.http }

// StatusError is an error response from the daemon.
type StatusError struct {
	Code    int
	Message string
	Reason  string // Error.Code: which refusal it is, when the daemon says
}

func (e *StatusError) Error() string { return e.Message }

func IsNotFound(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == http.StatusNotFound
}

// HasReason reports whether err is the daemon's refusal with that Error.Code.
func HasReason(err error, code string) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Reason == code
}

func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		var e Error
		if json.Unmarshal(data, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(data))
		}
		return nil, &StatusError{Code: resp.StatusCode, Message: e.Error, Reason: e.Code}
	}
	return resp, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	resp, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch out := out.(type) {
	case nil:
		_, err = io.Copy(io.Discard, resp.Body)
	case *string:
		var b []byte
		b, err = io.ReadAll(resp.Body)
		*out = string(b)
	default:
		err = json.NewDecoder(resp.Body).Decode(out)
	}
	return err
}

// AgentPath is the API path of an agent, from its "<project>/<agent>" ref.
func AgentPath(ref string) (string, error) {
	// At the last "/": an agent's name never has one, and what a project is
	// called, which a ref may name it by, can.
	i := strings.LastIndex(ref, "/")
	project, name := ref[:max(i, 0)], ref[i+1:]
	if i < 0 || project == "" || name == "" {
		return "", fmt.Errorf("invalid agent %q: use <project>/<agent>, like pawly/agent-01", ref)
	}
	return "/v1/agents/" + url.PathEscape(project) + "/" + url.PathEscape(name), nil
}

func (c *Client) agentDo(ctx context.Context, method, ref, suffix string, body, out any) error {
	path, err := AgentPath(ref)
	if err != nil {
		return err
	}
	return c.do(ctx, method, path+suffix, body, out)
}

// Fleet is a project's agents, with what each has changed, shown and opened.
func (c *Client) Fleet(ctx context.Context, project string) (Fleet, error) {
	var out Fleet
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/fleet", nil, &out)
}

// StartHeavy asks, inside an agent, to start a heavy command, waiting while
// the VM's memory is under pressure (HeavyRequest).
func (c *Client) StartHeavy(ctx context.Context, req HeavyRequest) (HeavyStart, error) {
	var out HeavyStart
	return out, c.do(ctx, http.MethodPost, "/v1/self/heavy", req, &out)
}

// TestPolicy says, inside an agent, whether its whole test suites are
// refused, and what its branch started from.
func (c *Client) TestPolicy(ctx context.Context) (TestPolicy, error) {
	var out TestPolicy
	return out, c.do(ctx, http.MethodGet, "/v1/self/tests", nil, &out)
}

// JoinHeavy puts a process of the agent's in a started heavy command's
// cgroup, so pausing the command pauses it and what it starts.
func (c *Client) JoinHeavy(ctx context.Context, key string, pid int) error {
	return c.do(ctx, http.MethodPost, "/v1/self/heavy/"+url.PathEscape(key)+"/join", HeavyJoin{PID: pid}, nil)
}

// EndHeavy says a heavy command ended.
func (c *Client) EndHeavy(ctx context.Context, key string) error {
	return c.do(ctx, http.MethodDelete, "/v1/self/heavy/"+url.PathEscape(key), nil, nil)
}

// Pressure is the VM's memory pressure and the heavy commands it holds.
func (c *Client) Pressure(ctx context.Context) (PressureStatus, error) {
	var out PressureStatus
	return out, c.do(ctx, http.MethodGet, "/v1/pressure", nil, &out)
}

// Ask puts a question to the project's chat and waits for the answer. Only an
// agent may call it, on the in-agent socket.
func (c *Client) Ask(ctx context.Context, question, about string) (Question, error) {
	var out Question
	return out, c.do(ctx, http.MethodPost, "/v1/self/ask", AskRequest{Question: question, Context: about}, &out)
}

// AgentEvents are everything a project's agents have reported — created,
// finished, asked, answered — newest first.
func (c *Client) AgentEvents(ctx context.Context, project string) ([]AgentEvent, error) {
	var out []AgentEvent
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/agent-events", nil, &out)
}

// ReadyToReview is a project's agents that finished and haven't been
// reviewed or dismissed since, newest first; every project's for "".
func (c *Client) ReadyToReview(ctx context.Context, project string) ([]ReviewItem, error) {
	path := "/v1/review"
	if project != "" {
		path = "/v1/projects/" + url.PathEscape(project) + "/review"
	}
	var out []ReviewItem
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// Reviewed is a project's agents the user reviewed or dismissed and whose
// work hasn't changed since, the latest verdict first; every project's for "".
func (c *Client) Reviewed(ctx context.Context, project string) ([]ReviewItem, error) {
	path := "/v1/review?reviewed=1"
	if project != "" {
		path = "/v1/projects/" + url.PathEscape(project) + "/review?reviewed=1"
	}
	var out []ReviewItem
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// ReviewPage is one page of ReadyToReview, or with reviewed of Reviewed.
func (c *Client) ReviewPage(ctx context.Context, project string, reviewed bool, limit int, cursor string) (ReviewPage, error) {
	path := "/v1/review"
	if project != "" {
		path = "/v1/projects/" + url.PathEscape(project) + "/review"
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if reviewed {
		q.Set("reviewed", "1")
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var out ReviewPage
	return out, c.do(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &out)
}

// Review records the user's verdict on an agent's work, taking it off
// ReadyToReview until it changes.
func (c *Client) Review(ctx context.Context, ref string, req ReviewRequest) error {
	path, err := AgentPath(ref)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, path+"/review", req, nil)
}

// RequestChanges sends an agent what to change in the work the user
// reviewed, and records it as their verdict.
func (c *Client) RequestChanges(ctx context.Context, ref string, req RequestChangesRequest) (TellResult, error) {
	path, err := AgentPath(ref)
	if err != nil {
		return TellResult{}, err
	}
	var out TellResult
	return out, c.do(ctx, http.MethodPost, path+"/request-changes", req, &out)
}

// ReviewNotes lists the notes left on an agent's diff, drafts and sent.
func (c *Client) ReviewNotes(ctx context.Context, ref string) ([]ReviewNote, error) {
	path, err := AgentPath(ref)
	if err != nil {
		return nil, err
	}
	var out []ReviewNote
	return out, c.do(ctx, http.MethodGet, path+"/review-notes", nil, &out)
}

// AddReviewNote leaves a draft note on lines of an agent's diff.
func (c *Client) AddReviewNote(ctx context.Context, ref string, req AddReviewNoteRequest) (ReviewNote, error) {
	path, err := AgentPath(ref)
	if err != nil {
		return ReviewNote{}, err
	}
	var out ReviewNote
	return out, c.do(ctx, http.MethodPost, path+"/review-notes", req, &out)
}

// EditReviewNote rewrites a draft note, or marks a sent one resolved.
func (c *Client) EditReviewNote(ctx context.Context, ref string, id int64, req EditReviewNoteRequest) (ReviewNote, error) {
	path, err := AgentPath(ref)
	if err != nil {
		return ReviewNote{}, err
	}
	var out ReviewNote
	return out, c.do(ctx, http.MethodPatch, fmt.Sprintf("%s/review-notes/%d", path, id), req, &out)
}

// DeleteReviewNote removes a note.
func (c *Client) DeleteReviewNote(ctx context.Context, ref string, id int64) error {
	path, err := AgentPath(ref)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("%s/review-notes/%d", path, id), nil, nil)
}

// RequestCredential asks the user for a credential this agent lacks, from
// inside it, and waits for the answer.
func (c *Client) RequestCredential(ctx context.Context, req CredentialRequest) (Question, error) {
	var out Question
	return out, c.do(ctx, http.MethodPost, "/v1/self/credential", req, &out)
}

// AnswerCredential answers an agent's credential request.
func (c *Client) AnswerCredential(ctx context.Context, project, id string, req AnswerCredentialRequest) (Question, error) {
	var out Question
	path := "/v1/projects/" + url.PathEscape(project) + "/questions/" + url.PathEscape(id) + "/credential"
	return out, c.do(ctx, http.MethodPost, path, req, &out)
}

// Questions are a project's, waiting ones unless all.
func (c *Client) Questions(ctx context.Context, project string, all bool) ([]Question, error) {
	path := "/v1/projects/" + url.PathEscape(project) + "/questions"
	if all {
		path += "?all=1"
	}
	var out []Question
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// AnswerQuestion answers one the project's chat passed to you.
func (c *Client) AnswerQuestion(ctx context.Context, project, id, answer string) (Question, error) {
	var out Question
	path := "/v1/projects/" + url.PathEscape(project) + "/questions/" + url.PathEscape(id) + "/answer"
	return out, c.do(ctx, http.MethodPost, path, AnswerQuestionRequest{Answer: answer}, &out)
}

// Sections lists the sidebar's sections, in the order it draws them (D79).
func (c *Client) Sections(ctx context.Context) ([]Section, error) {
	var out []Section
	return out, c.do(ctx, http.MethodGet, "/v1/sections", nil, &out)
}

// AddSection makes an empty section at the end of the list.
func (c *Client) AddSection(ctx context.Context, name string) (Section, error) {
	var out Section
	return out, c.do(ctx, http.MethodPost, "/v1/sections", AddSectionRequest{Name: name}, &out)
}

// UpdateSection renames a section or folds it away.
func (c *Client) UpdateSection(ctx context.Context, id string, req UpdateSectionRequest) (Section, error) {
	var out Section
	return out, c.do(ctx, http.MethodPatch, "/v1/sections/"+url.PathEscape(id), req, &out)
}

// RemoveSection deletes a section and keeps its projects: they go back to
// being in no section.
func (c *Client) RemoveSection(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/sections/"+url.PathEscape(id), nil, nil)
}

// SidebarLayout is UI v2's sidebar's hidden projects.
func (c *Client) SidebarLayout(ctx context.Context) (SidebarLayout, error) {
	var out SidebarLayout
	return out, c.do(ctx, http.MethodGet, "/v1/sidebar", nil, &out)
}

// SetSidebarLayout replaces the sidebar's hidden projects, and
// answers with what was kept.
func (c *Client) SetSidebarLayout(ctx context.Context, layout SidebarLayout) (SidebarLayout, error) {
	var out SidebarLayout
	return out, c.do(ctx, http.MethodPut, "/v1/sidebar", layout, &out)
}

// SetProjectLayout applies a whole new order — the sections, and the projects
// in and outside them — in one transaction, and answers with the projects as
// they now read.
func (c *Client) SetProjectLayout(ctx context.Context, layout ProjectLayout) ([]Project, error) {
	var out []Project
	return out, c.do(ctx, http.MethodPut, "/v1/projects/layout", layout, &out)
}

// RenameProject changes what a project is called; its slug stays.
func (c *Client) RenameProject(ctx context.Context, project, name string) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(project), UpdateProjectRequest{DisplayName: &name}, &out)
}

// SetAutonomy sets how much a project's chat does on its own.
func (c *Client) SetAutonomy(ctx context.Context, project, autonomy string) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(project), UpdateProjectRequest{Autonomy: &autonomy}, &out)
}

// SetFinishNotices sets what happens when one of a project's agents finishes:
// "chat" tells the project's chat and lets it decide, "off" only records it.
func (c *Client) SetFinishNotices(ctx context.Context, project, notices string) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(project), UpdateProjectRequest{FinishNotices: &notices}, &out)
}

// SetRolloverThreshold sets how full a project chat's context gets, as a
// percentage of it, before the conversation is consolidated into the project's
// memory and carried on in a fresh session. 0 switches that off.
func (c *Client) SetRolloverThreshold(ctx context.Context, project string, percent int) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(project), UpdateProjectRequest{RolloverThreshold: &percent}, &out)
}

// SetNesting turns a project's agents' nesting on or off: a real Incus daemon
// of their own, inside their own container. It needs the base image built
// with Incus.
func (c *Client) SetNesting(ctx context.Context, project string, on bool) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(project), UpdateProjectRequest{Nesting: &on}, &out)
}

// SetAgentPRs sets whether a project's agents push their own branch and open
// their own pull request when they finish.
func (c *Client) SetAgentPRs(ctx context.Context, project string, on bool) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(project), UpdateProjectRequest{AgentPRs: &on}, &out)
}

// SetContextBudget sets how many estimated tokens one context built from a
// project's memory may cost (D75).
func (c *Client) SetContextBudget(ctx context.Context, project string, tokens int) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(project), UpdateProjectRequest{ContextBudget: &tokens}, &out)
}

// SetConsolidation sets how many new events a project gathers before its chat
// is asked to distil them into memories. 0 switches consolidation off, both
// the distillation and the mechanical pass.
func (c *Client) SetConsolidation(ctx context.Context, project string, every int) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(project), UpdateProjectRequest{Consolidation: &every}, &out)
}

// SetConsolidationModel sets which model distils a project's events into
// memories: "cheap" for the cheap model of the AI tool it runs, a model id to
// name one, or "" for whatever its chat is running on.
func (c *Client) SetConsolidationModel(ctx context.Context, project, model string) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(project), UpdateProjectRequest{ConsolidationModel: &model}, &out)
}

// RolloverChat compacts a project's chat now, whatever its threshold says: the
// conversation is consolidated into the project's memory and carried on in a
// fresh session, with the chat itself left as it is.
func (c *Client) RolloverChat(ctx context.Context, project string) error {
	return c.do(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/chat/rollover", nil, nil)
}

// SetAgentModel sets the model a project's new agents are created on: "" to
// follow the model new agents start on, a model name, or "auto" to let the
// project's chat choose one per task.
func (c *Client) SetAgentModel(ctx context.Context, project, model string) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(project), UpdateProjectRequest{AgentModel: &model}, &out)
}

// SetBranchPrefix sets what a project's new agents' branches are named with,
// before the agent's name: "" for none.
func (c *Client) SetBranchPrefix(ctx context.Context, project, prefix string) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(project), UpdateProjectRequest{BranchPrefix: &prefix}, &out)
}

// SetMediaRetention sets how long a removed agent's media is kept: one of
// the MediaRetention values.
func (c *Client) SetMediaRetention(ctx context.Context, retention string) (Settings, error) {
	var out Settings
	return out, c.do(ctx, http.MethodPatch, "/v1/settings", UpdateSettingsRequest{MediaRetention: &retention}, &out)
}

// Retire frees what a project's finished agents are holding. It never deletes
// a branch: an agent's work outlives it.
func (c *Client) Retire(ctx context.Context, project string, req RetireRequest) (RetireResult, error) {
	var out RetireResult
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/retire", req, &out)
}

// ProjectMedia is every agent's media for a project, newest first. An empty
// agent or kind means all of them.
// AllMedia is every project's media, newest first, of the given kinds (all
// of them when none are given).
func (c *Client) AllMedia(ctx context.Context, kinds ...string) ([]MediaItem, error) {
	path := "/v1/media"
	if len(kinds) > 0 {
		path += "?" + url.Values{"kind": {strings.Join(kinds, ",")}}.Encode()
	}
	var out []MediaItem
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// MediaPage is one page of a Media list: path is /v1/media, a project's
// media or an agent's (MediaPath), and q its filters (MediaPage's doc), with
// limit and cursor.
func (c *Client) MediaPage(ctx context.Context, path string, q url.Values) (MediaPage, error) {
	var out MediaPage
	return out, c.do(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &out)
}

// MediaCounts is the counts on a Media list's filters, for the same path and
// filters as MediaPage.
func (c *Client) MediaCounts(ctx context.Context, path string, q url.Values) (MediaCounts, error) {
	var out MediaCounts
	return out, c.do(ctx, http.MethodGet, path+"/counts?"+q.Encode(), nil, &out)
}

// Notifications is what needs the user, newest first.
func (c *Client) Notifications(ctx context.Context) ([]Notification, error) {
	var out []Notification
	return out, c.do(ctx, http.MethodGet, "/v1/notifications", nil, &out)
}

// NotificationPage is one page of Notifications; query is its ?limit=,
// ?cursor= and ?unseen=.
func (c *Client) NotificationPage(ctx context.Context, query url.Values) (NotificationPage, error) {
	var out NotificationPage
	return out, c.do(ctx, http.MethodGet, "/v1/notifications?"+query.Encode(), nil, &out)
}

// SeeNotifications marks notifications seen.
func (c *Client) SeeNotifications(ctx context.Context, req SeeNotificationsRequest) (SeeNotificationsResult, error) {
	var out SeeNotificationsResult
	return out, c.do(ctx, http.MethodPost, "/v1/notifications/seen", req, &out)
}

func (c *Client) ProjectMedia(ctx context.Context, project, agent, kind string) ([]MediaItem, error) {
	q := url.Values{}
	if agent != "" {
		q.Set("agent", agent)
	}
	if kind != "" {
		q.Set("kind", kind)
	}
	path := "/v1/projects/" + url.PathEscape(project) + "/media"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out []MediaItem
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// ProjectPullRequests lists a project repository's pull requests.
func (c *Client) ProjectPullRequests(ctx context.Context, project string) (ProjectPullRequests, error) {
	var out ProjectPullRequests
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/pulls", nil, &out)
}

// ProjectIssues lists a project repository's open issues.
func (c *Client) ProjectIssues(ctx context.Context, project string) (ProjectIssues, error) {
	var out ProjectIssues
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/issues", nil, &out)
}

// MergePullRequest merges one of a project repository's pull requests with
// the given method: "merge", "squash" or "rebase".
func (c *Client) MergePullRequest(ctx context.Context, project string, number int, method string) (PullRequest, error) {
	var out PullRequest
	path := "/v1/projects/" + url.PathEscape(project) + "/pulls/" + strconv.Itoa(number) + "/merge"
	return out, c.do(ctx, http.MethodPost, path, MergePullRequestRequest{Method: method}, &out)
}

// ProjectLabels lists the labels of a project's repository.
func (c *Client) ProjectLabels(ctx context.Context, project string) (ProjectLabels, error) {
	var out ProjectLabels
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/labels", nil, &out)
}

// EditPullRequestLabels puts labels on a pull request and takes others off,
// and returns the labels it has now.
func (c *Client) EditPullRequestLabels(ctx context.Context, project string, number int, req EditLabelsRequest) ([]Label, error) {
	var out []Label
	path := "/v1/projects/" + url.PathEscape(project) + "/pulls/" + strconv.Itoa(number) + "/labels"
	return out, c.do(ctx, http.MethodPost, path, req, &out)
}

// SaveGitHubToken stores a GitHub token under an account, and returns who it
// belongs to.
func (c *Client) SaveGitHubToken(ctx context.Context, account, token string) (string, error) {
	var out struct {
		User string `json:"user"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/auth/github", GitHubTokenRequest{Token: token, Account: account}, &out)
	return out.User, err
}

// SaveCursorKey signs agents in to Cursor with an API key, once Cursor says
// whose it is, and returns the account's email when Cursor gave one.
func (c *Client) SaveCursorKey(ctx context.Context, account, key string) (string, error) {
	var out CursorKeyResponse
	err := c.do(ctx, http.MethodPost, "/v1/auth/cursor", CursorKeyRequest{APIKey: key, Account: account}, &out)
	return out.Email, err
}

// StartCursorLogin starts Cursor's browser sign-in for an account, or returns
// the one already under way.
func (c *Client) StartCursorLogin(ctx context.Context, account string) (CursorLogin, error) {
	var out CursorLogin
	return out, c.do(ctx, http.MethodPost, "/v1/auth/cursor/login", CursorLoginRequest{Account: account}, &out)
}

// SaveToolAccount stores a Codex or OpenCode login from an API key.
func (c *Client) SaveToolAccount(ctx context.Context, tool string, req ToolAccountRequest) error {
	return c.do(ctx, http.MethodPost, "/v1/auth/accounts/"+url.PathEscape(tool), req, nil)
}

// toolAccountPath is a Codex, OpenCode or Cursor account's path, with
// OpenCode's provider.
func toolAccountPath(tool, provider, account string) string {
	p := "/v1/auth/accounts/" + url.PathEscape(tool) + "/" + url.PathEscape(account)
	if provider != "" {
		return p + "?provider=" + url.QueryEscape(provider)
	}
	return p
}

// RemoveToolAccount forgets a Codex, OpenCode (provider's) or Cursor account.
func (c *Client) RemoveToolAccount(ctx context.Context, tool, provider, account string) error {
	return c.do(ctx, http.MethodDelete, toolAccountPath(tool, provider, account), nil, nil)
}

// SetDefaultToolAccount makes an account its tool's (or provider's) default.
func (c *Client) SetDefaultToolAccount(ctx context.Context, tool, provider, account string) error {
	p := toolAccountPath(tool, provider, account)
	path, query, _ := strings.Cut(p, "?")
	if query != "" {
		query = "?" + query
	}
	return c.do(ctx, http.MethodPost, path+"/default"+query, nil, nil)
}

// RenameToolAccount gives a stored Codex, OpenCode or Cursor account another name.
func (c *Client) RenameToolAccount(ctx context.Context, tool, provider, account, name string) (RenamedToolAccount, error) {
	var out RenamedToolAccount
	path, query, _ := strings.Cut(toolAccountPath(tool, provider, account), "?")
	if query != "" {
		query = "?" + query
	}
	return out, c.do(ctx, http.MethodPost, path+"/rename"+query, RenameToolAccountRequest{Name: name}, &out)
}

// CursorLoginStatus is how the browser sign-in is getting on.
func (c *Client) CursorLoginStatus(ctx context.Context) (CursorLogin, error) {
	var out CursorLogin
	return out, c.do(ctx, http.MethodGet, "/v1/auth/cursor/login", nil, &out)
}

// RemoveGitHubAccount forgets a stored GitHub account.
func (c *Client) RemoveGitHubAccount(ctx context.Context, account string) error {
	return c.do(ctx, http.MethodDelete, "/v1/auth/github/"+url.PathEscape(account), nil, nil)
}

// SetDefaultGitHubAccount picks the GitHub account agents use when neither
// they nor their project names one.
func (c *Client) SetDefaultGitHubAccount(ctx context.Context, account string) error {
	return c.do(ctx, http.MethodPost, "/v1/auth/github/"+url.PathEscape(account)+"/default", nil, nil)
}

// RenameGitHubAccount gives a stored GitHub account another name, and carries
// every project and agent on it over to that name.
func (c *Client) RenameGitHubAccount(ctx context.Context, account, name string) (RenamedGitHubAccount, error) {
	var out RenamedGitHubAccount
	return out, c.do(ctx, http.MethodPost, "/v1/auth/github/"+url.PathEscape(account)+"/rename", RenameGitHubAccountRequest{Name: name}, &out)
}

// Settings are the installation's own settings, with the model menus the AI
// tools last advertised. The lead reads its own copy over the lead socket
// (ProjectSettings); this is the whole-machine one.
func (c *Client) Settings(ctx context.Context) (Settings, error) {
	var settings Settings
	return settings, c.do(ctx, http.MethodGet, "/v1/settings", nil, &settings)
}

// UpdateSettings changes the installation's own settings; a nil field in req
// stays as it is.
func (c *Client) UpdateSettings(ctx context.Context, req UpdateSettingsRequest) (Settings, error) {
	var settings Settings
	return settings, c.do(ctx, http.MethodPatch, "/v1/settings", req, &settings)
}

func (c *Client) Setup(ctx context.Context) (SetupStatus, error) {
	var status SetupStatus
	return status, c.do(ctx, http.MethodGet, "/v1/setup", nil, &status)
}

func (c *Client) SaveClaudeToken(ctx context.Context, req ClaudeTokenRequest) error {
	return c.do(ctx, http.MethodPost, "/v1/auth/claude", req, nil)
}

// StartClaudeLogin runs `claude setup-token` on the daemon's machine, as a job:
// the page to approve comes back from ClaudeLogin, and the token it mints is
// stored under the account when it finishes (D59).
func (c *Client) StartClaudeLogin(ctx context.Context, req ClaudeLoginRequest) (Job, error) {
	var job Job
	return job, c.do(ctx, http.MethodPost, "/v1/auth/claude/login", req, &job)
}

// ClaudeLogin is how far a login has got, and which page to open for it.
func (c *Client) ClaudeLogin(ctx context.Context, job string) (ClaudeLogin, error) {
	var login ClaudeLogin
	return login, c.do(ctx, http.MethodGet, "/v1/auth/claude/login/"+url.PathEscape(job), nil, &login)
}

// ClaudeLoginCode hands a running login the code from its fallback page.
func (c *Client) ClaudeLoginCode(ctx context.Context, job, code string) error {
	return c.do(ctx, http.MethodPost, "/v1/auth/claude/login/"+url.PathEscape(job)+"/code", ClaudeLoginCodeRequest{Code: code}, nil)
}

func (c *Client) RemoveClaudeAccount(ctx context.Context, account string) error {
	return c.do(ctx, http.MethodDelete, "/v1/auth/claude/"+url.PathEscape(account), nil, nil)
}

// RenameClaudeAccount gives a stored Claude Code account another name, and
// carries every project and agent on it over to that name.
func (c *Client) RenameClaudeAccount(ctx context.Context, account, name string) (RenamedClaudeAccount, error) {
	var out RenamedClaudeAccount
	return out, c.do(ctx, http.MethodPost, "/v1/auth/claude/"+url.PathEscape(account)+"/rename", RenameClaudeAccountRequest{Name: name}, &out)
}

func (c *Client) SetDefaultClaudeAccount(ctx context.Context, account string) error {
	return c.do(ctx, http.MethodPost, "/v1/auth/claude/"+url.PathEscape(account)+"/default", nil, nil)
}

// MediaPath is the API path of an agent's media. An empty ref is the caller's
// own media, on the in-agent API.
func MediaPath(ref string) (string, error) {
	if ref == "" {
		return "/v1/self/media", nil
	}
	path, err := AgentPath(ref)
	return path + "/media", err
}

func (c *Client) mediaDo(ctx context.Context, method, ref, suffix string, body, out any) error {
	path, err := MediaPath(ref)
	if err != nil {
		return err
	}
	return c.do(ctx, method, path+suffix, body, out)
}

func (c *Client) Media(ctx context.Context, ref string) ([]MediaItem, error) {
	var items []MediaItem
	return items, c.mediaDo(ctx, http.MethodGet, ref, "", nil, &items)
}

func (c *Client) MediaItem(ctx context.Context, id string) (MediaItem, error) {
	var item MediaItem
	return item, c.do(ctx, http.MethodGet, "/v1/media/"+url.PathEscape(id), nil, &item)
}

// MediaFile opens an item's file: any item on the host API (ref is only
// checked there by the caller), and only the caller's own on the in-agent API
// (ref ""). The caller closes it.
func (c *Client) MediaFile(ctx context.Context, ref, id string) (io.ReadCloser, error) {
	return c.mediaFile(ctx, ref, id, "")
}

// RawMediaFile opens an edited recording's original, unedited, as
// MediaFile opens its file.
func (c *Client) RawMediaFile(ctx context.Context, ref, id string) (io.ReadCloser, error) {
	return c.mediaFile(ctx, ref, id, "?raw=1")
}

func (c *Client) mediaFile(ctx context.Context, ref, id, query string) (io.ReadCloser, error) {
	path := "/v1/media/" + url.PathEscape(id) + "/file"
	if ref == "" {
		path = "/v1/self/media/" + url.PathEscape(id) + "/file"
	}
	resp, err := c.request(ctx, http.MethodGet, path+query, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// DeleteSelfMedia deletes one of the caller's own items, on the in-agent API.
func (c *Client) DeleteSelfMedia(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/self/media/"+url.PathEscape(id), nil, nil)
}

func (c *Client) DeleteMedia(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/media/"+url.PathEscape(id), nil, nil)
}

// SetMediaFavorite marks an item a favorite, which keeps it from ever being
// removed automatically, or puts it back under the media retention.
func (c *Client) SetMediaFavorite(ctx context.Context, id string, favorite bool) (MediaItem, error) {
	var out MediaItem
	return out, c.do(ctx, http.MethodPatch, "/v1/media/"+url.PathEscape(id), UpdateMediaRequest{Favorite: &favorite}, &out)
}

// DeleteProjectMedia deletes many of a project's items in one call: the ones
// named by ID, or everything the same agent and kind filters as the list match.
func (c *Client) DeleteProjectMedia(ctx context.Context, project string, req DeleteMediaRequest) (DeleteMediaResult, error) {
	var out DeleteMediaResult
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/media/delete", req, &out)
}

// DeleteAgentMedia is DeleteProjectMedia for one agent's own gallery. Like
// the bulk delete, it's on the host API only: an agent deletes one item at a time (DeleteSelfMedia).
func (c *Client) DeleteAgentMedia(ctx context.Context, ref string, req DeleteMediaRequest) (DeleteMediaResult, error) {
	var out DeleteMediaResult
	return out, c.mediaDo(ctx, http.MethodPost, ref, "/delete", req, &out)
}

func (c *Client) Screenshot(ctx context.Context, ref string, req ScreenshotRequest) (MediaItem, error) {
	var item MediaItem
	return item, c.mediaDo(ctx, http.MethodPost, ref, "/screenshot", req, &item)
}

func (c *Client) Recording(ctx context.Context, ref string) (RecordingStatus, error) {
	var status RecordingStatus
	return status, c.mediaDo(ctx, http.MethodGet, ref, "/record", nil, &status)
}

func (c *Client) StartRecording(ctx context.Context, ref string, req RecordRequest) (RecordingStatus, error) {
	var status RecordingStatus
	return status, c.mediaDo(ctx, http.MethodPost, ref, "/record/start", req, &status)
}

func (c *Client) StopRecording(ctx context.Context, ref string) (MediaItem, error) {
	var item MediaItem
	return item, c.mediaDo(ctx, http.MethodPost, ref, "/record/stop", nil, &item)
}

func (c *Client) AddMedia(ctx context.Context, ref string, req AddMediaRequest) (MediaItem, error) {
	var item MediaItem
	return item, c.mediaDo(ctx, http.MethodPost, ref, "/add", req, &item)
}

func (c *Client) AddNote(ctx context.Context, ref string, req NoteRequest) (MediaItem, error) {
	var item MediaItem
	return item, c.mediaDo(ctx, http.MethodPost, ref, "/note", req, &item)
}

func (c *Client) AddLogs(ctx context.Context, ref string, req LogsRequest) (MediaItem, error) {
	var item MediaItem
	return item, c.mediaDo(ctx, http.MethodPost, ref, "/logs", req, &item)
}

func (c *Client) ExportMedia(ctx context.Context, ref string, req ExportRequest) (ExportResult, error) {
	var result ExportResult
	return result, c.mediaDo(ctx, http.MethodPost, ref, "/export", req, &result)
}

func (c *Client) UpdateAgent(ctx context.Context, ref string, req UpdateAgentRequest) (Agent, error) {
	var ag Agent
	return ag, c.agentDo(ctx, http.MethodPatch, ref, "", req, &ag)
}

// BrowserPath is the API path of an agent's browser. An empty ref is the
// caller's own browser, on the in-agent API.
func BrowserPath(ref string) (string, error) {
	if ref == "" {
		return "/v1/self/browser", nil
	}
	path, err := AgentPath(ref)
	return path + "/browser", err
}

func (c *Client) Browser(ctx context.Context, ref string) (BrowserStatus, error) {
	var status BrowserStatus
	path, err := BrowserPath(ref)
	if err != nil {
		return status, err
	}
	return status, c.do(ctx, http.MethodGet, path, nil, &status)
}

// BrowserGPU asks what an agent's desktop renders on.
func (c *Client) BrowserGPU(ctx context.Context, ref string) (GPUReport, error) {
	var report GPUReport
	path, err := BrowserPath(ref)
	if err != nil {
		return report, err
	}
	return report, c.do(ctx, http.MethodGet, path+"/gpu", nil, &report)
}

// BrowserAction starts or stops an agent's browser.
func (c *Client) BrowserAction(ctx context.Context, ref, action string) (BrowserStatus, error) {
	var status BrowserStatus
	path, err := BrowserPath(ref)
	if err != nil {
		return status, err
	}
	return status, c.do(ctx, http.MethodPost, path+"/"+action, nil, &status)
}

func (c *Client) OpenInBrowser(ctx context.Context, ref, target string) (BrowserStatus, error) {
	var status BrowserStatus
	path, err := BrowserPath(ref)
	if err != nil {
		return status, err
	}
	return status, c.do(ctx, http.MethodPost, path+"/open", BrowserOpenRequest{URL: target}, &status)
}

// AndroidPath is the API path of an agent's Android emulator. An empty ref is
// the caller's own emulator, on the in-agent API.
func AndroidPath(ref string) (string, error) {
	if ref == "" {
		return "/v1/self/android", nil
	}
	path, err := AgentPath(ref)
	return path + "/android", err
}

func (c *Client) androidDo(ctx context.Context, method, ref, suffix string, body, out any) error {
	path, err := AndroidPath(ref)
	if err != nil {
		return err
	}
	return c.do(ctx, method, path+suffix, body, out)
}

func (c *Client) Android(ctx context.Context, ref string) (AndroidStatus, error) {
	var status AndroidStatus
	return status, c.androidDo(ctx, http.MethodGet, ref, "", nil, &status)
}

// StartAndroid starts the emulator and waits until Android has booted.
func (c *Client) StartAndroid(ctx context.Context, ref string, req AndroidStartRequest) (AndroidStatus, error) {
	var status AndroidStatus
	return status, c.androidDo(ctx, http.MethodPost, ref, "/start", req, &status)
}

func (c *Client) StopAndroid(ctx context.Context, ref string) (AndroidStatus, error) {
	var status AndroidStatus
	return status, c.androidDo(ctx, http.MethodPost, ref, "/stop", nil, &status)
}

func (c *Client) InstallAPK(ctx context.Context, ref string, req AndroidInstallRequest) (AndroidInstallResult, error) {
	var result AndroidInstallResult
	return result, c.androidDo(ctx, http.MethodPost, ref, "/install", req, &result)
}

// IOSPath is where an agent's iOS Simulator endpoints are: /v1/self/ios for
// an agent's own (ref ""), and under the agent otherwise.
func IOSPath(ref string) (string, error) {
	if ref == "" {
		return "/v1/self/ios", nil
	}
	path, err := AgentPath(ref)
	return path + "/ios", err
}

func (c *Client) iosDo(ctx context.Context, method, ref, suffix string, body, out any) error {
	path, err := IOSPath(ref)
	if err != nil {
		return err
	}
	return c.do(ctx, method, path+suffix, body, out)
}

func (c *Client) IOS(ctx context.Context, ref string) (IOSStatus, error) {
	var status IOSStatus
	return status, c.iosDo(ctx, http.MethodGet, ref, "", nil, &status)
}

// OpenIOS boots a simulator, waits until it has, and makes it the agent's.
func (c *Client) OpenIOS(ctx context.Context, ref string, req IOSDeviceRequest) (IOSStatus, error) {
	var status IOSStatus
	return status, c.iosDo(ctx, http.MethodPost, ref, "/open", req, &status)
}

// CloseIOS shuts a simulator down.
func (c *Client) CloseIOS(ctx context.Context, ref string, req IOSDeviceRequest) (IOSStatus, error) {
	var status IOSStatus
	return status, c.iosDo(ctx, http.MethodPost, ref, "/close", req, &status)
}

func (c *Client) IOSInput(ctx context.Context, ref string, in IOSInput) error {
	return c.iosDo(ctx, http.MethodPost, ref, "/input", in, nil)
}

func (c *Client) InstallIOSApp(ctx context.Context, ref string, req IOSInstallRequest) (IOSResult, error) {
	var result IOSResult
	return result, c.iosDo(ctx, http.MethodPost, ref, "/install", req, &result)
}

func (c *Client) LaunchIOSApp(ctx context.Context, ref string, req IOSLaunchRequest) (IOSResult, error) {
	var result IOSResult
	return result, c.iosDo(ctx, http.MethodPost, ref, "/launch", req, &result)
}

func (c *Client) OpenIOSURL(ctx context.Context, ref string, req IOSOpenURLRequest) error {
	return c.iosDo(ctx, http.MethodPost, ref, "/openurl", req, nil)
}

func (c *Client) Preview(ctx context.Context) (PreviewInfo, error) {
	var info PreviewInfo
	return info, c.do(ctx, http.MethodGet, "/v1/preview", nil, &info)
}

func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/v1/version", nil, nil)
}

func (c *Client) Version(ctx context.Context) (VersionInfo, error) {
	var info VersionInfo
	return info, c.do(ctx, http.MethodGet, "/v1/version", nil, &info)
}

// Update is whether the daily update check runs, and what it last found.
func (c *Client) Update(ctx context.Context) (UpdateStatus, error) {
	var status UpdateStatus
	return status, c.do(ctx, http.MethodGet, "/v1/update", nil, &status)
}

// UsageStatsPending is what the next update check would send of the
// anonymous usage stats, exactly.
func (c *Client) UsageStatsPending(ctx context.Context) (UsageStatsPending, error) {
	var out UsageStatsPending
	return out, c.do(ctx, http.MethodGet, "/v1/usage-stats/pending", nil, &out)
}

// ReportSetupFunnel hands the daemon the steps of Setup taken before it was up.
func (c *Client) ReportSetupFunnel(ctx context.Context, r SetupFunnelReport) error {
	return c.do(ctx, http.MethodPost, "/v1/usage-events/setup", r, nil)
}

// LatestRelease is the update channel's latest release, as the release list has it now.
func (c *Client) LatestRelease(ctx context.Context) (UpdateRelease, error) {
	var out UpdateRelease
	return out, c.do(ctx, http.MethodGet, "/v1/update/release", nil, &out)
}

func (c *Client) Shutdown(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/shutdown", nil, nil)
}

func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var out []Project
	return out, c.do(ctx, http.MethodGet, "/v1/projects", nil, &out)
}

func (c *Client) AddProject(ctx context.Context, req AddProjectRequest) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPost, "/v1/projects", req, &out)
}

func (c *Client) Project(ctx context.Context, name string) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(name), nil, &out)
}

func (c *Client) UpdateProject(ctx context.Context, name string, req UpdateProjectRequest) (Project, error) {
	var out Project
	return out, c.do(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(name), req, &out)
}

func (c *Client) RemoveProject(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/projects/"+url.PathEscape(name), nil, nil)
}

// ProjectActivity returns when each project that has a chat item last did
// anything, and when the user last wrote to it.
func (c *Client) ProjectActivity(ctx context.Context) ([]ProjectActivity, error) {
	var out []ProjectActivity
	return out, c.do(ctx, http.MethodGet, "/v1/project-activity", nil, &out)
}

// ProjectIcon returns what a project shows as its icon, looking for one in
// its repository the first time.
func (c *Client) ProjectIcon(ctx context.Context, project string) (ProjectIcon, error) {
	var out ProjectIcon
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/icon", nil, &out)
}

// SetProjectIcon chooses a project's icon: an emoji or an image.
func (c *Client) SetProjectIcon(ctx context.Context, project string, req SetProjectIconRequest) (ProjectIcon, error) {
	var out ProjectIcon
	return out, c.do(ctx, http.MethodPut, "/v1/projects/"+url.PathEscape(project)+"/icon", req, &out)
}

// ResetProjectIcon forgets the chosen icon and looks for one in the
// repository again.
func (c *Client) ResetProjectIcon(ctx context.Context, project string) (ProjectIcon, error) {
	var out ProjectIcon
	return out, c.do(ctx, http.MethodDelete, "/v1/projects/"+url.PathEscape(project)+"/icon", nil, &out)
}

func (c *Client) Brief(ctx context.Context, project, agent string) (string, error) {
	var out string
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/brief?agent="+url.QueryEscape(agent), nil, &out)
}

// Notes returns a project's notes for its agents.
func (c *Client) Notes(ctx context.Context, project string) (Notes, error) {
	var out Notes
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/notes", nil, &out)
}

// SetNotes replaces a project's notes and rewrites the brief of every agent
// that already has one.
func (c *Client) SetNotes(ctx context.Context, project, text string) (Notes, error) {
	var out Notes
	return out, c.do(ctx, http.MethodPut, "/v1/projects/"+url.PathEscape(project)+"/notes", NotesRequest{Text: text}, &out)
}

// Base returns the project's saved base; ok is false when it has none.
func (c *Client) Base(ctx context.Context, project string) (base Base, ok bool, err error) {
	err = c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/base", nil, &base)
	if IsNotFound(err) && strings.Contains(err.Error(), "no saved base") {
		return Base{}, false, nil
	}
	return base, err == nil, err
}

func (c *Client) SaveBase(ctx context.Context, project, agent string) (Job, error) {
	var out Job
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/base", SaveBaseRequest{Agent: agent}, &out)
}

func (c *Client) RemoveBase(ctx context.Context, project string) error {
	return c.do(ctx, http.MethodDelete, "/v1/projects/"+url.PathEscape(project)+"/base", nil, nil)
}

// RevertBase puts the base the last save replaced back, and returns it.
func (c *Client) RevertBase(ctx context.Context, project string) (Base, error) {
	var out Base
	return out, c.do(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/base/revert", nil, &out)
}

// RemovePreviousBase drops what the last save kept, giving its disk back.
func (c *Client) RemovePreviousBase(ctx context.Context, project string) error {
	return c.do(ctx, http.MethodDelete, "/v1/projects/"+url.PathEscape(project)+"/base?previous=1", nil, nil)
}

func (c *Client) Agents(ctx context.Context, project string) ([]Agent, error) {
	var out []Agent
	return out, c.do(ctx, http.MethodGet, "/v1/agents?project="+url.QueryEscape(project), nil, &out)
}

func (c *Client) Agent(ctx context.Context, ref string) (Agent, error) {
	var out Agent
	return out, c.agentDo(ctx, http.MethodGet, ref, "", nil, &out)
}

func (c *Client) CreateAgent(ctx context.Context, req CreateAgentRequest) (Job, error) {
	var out Job
	return out, c.do(ctx, http.MethodPost, "/v1/agents", req, &out)
}

func (c *Client) DestroyAgent(ctx context.Context, ref string, force, deleteBranch, deleteMedia bool) error {
	q := url.Values{
		"force":        {strconv.FormatBool(force)},
		"deleteBranch": {strconv.FormatBool(deleteBranch)},
		"deleteMedia":  {strconv.FormatBool(deleteMedia)},
	}
	return c.agentDo(ctx, http.MethodDelete, ref, "?"+q.Encode(), nil, nil)
}

// AgentAction runs start, stop, pause, resume or session (make sure the tmux
// session exists) and returns the agent afterwards.
// StopAgents starts the job that stops every running or paused agent, or
// only those req.Refs names; its result is a StopAgentsResult.
func (c *Client) StopAgents(ctx context.Context, req StopAgentsRequest) (Job, error) {
	var out Job
	return out, c.do(ctx, http.MethodPost, "/v1/agents/stop", req, &out)
}

func (c *Client) AgentAction(ctx context.Context, ref, action string) (Agent, error) {
	var out Agent
	return out, c.agentDo(ctx, http.MethodPost, ref, "/"+action, nil, &out)
}

func (c *Client) Diff(ctx context.Context, ref string, stat bool) (string, error) {
	var out string
	return out, c.agentDo(ctx, http.MethodGet, ref, "/diff?stat="+strconv.FormatBool(stat), nil, &out)
}

func (c *Client) Snapshots(ctx context.Context, ref string) ([]Snapshot, error) {
	var out []Snapshot
	return out, c.agentDo(ctx, http.MethodGet, ref, "/snapshots", nil, &out)
}

func (c *Client) TakeSnapshot(ctx context.Context, ref string, req SnapshotRequest) (Snapshot, error) {
	var out Snapshot
	return out, c.agentDo(ctx, http.MethodPost, ref, "/snapshots", req, &out)
}

func (c *Client) DeleteSnapshot(ctx context.Context, ref, name string) error {
	return c.agentDo(ctx, http.MethodDelete, ref, "/snapshots/"+url.PathEscape(name), nil, nil)
}

func (c *Client) Restore(ctx context.Context, ref, snapshot string) (Job, error) {
	var out Job
	return out, c.agentDo(ctx, http.MethodPost, ref, "/restore", RestoreRequest{Snapshot: snapshot}, &out)
}

// Recreate starts a job that gives an agent whose machine is gone a new one.
func (c *Client) Recreate(ctx context.Context, ref string, req RecreateRequest) (Job, error) {
	var out Job
	return out, c.agentDo(ctx, http.MethodPost, ref, "/recreate", req, &out)
}

// CheckMigration compares the daemon's state with the state.db it was made from.
func (c *Client) CheckMigration(ctx context.Context, req MigrationCheckRequest) (MigrationCheck, error) {
	var out MigrationCheck
	return out, c.do(ctx, http.MethodPost, "/v1/migration/check", req, &out)
}

// Checkpoints lists an agent's turn checkpoints and saved states, oldest first.
func (c *Client) Checkpoints(ctx context.Context, ref string) ([]Checkpoint, error) {
	var out []Checkpoint
	return out, c.agentDo(ctx, http.MethodGet, ref, "/checkpoints", nil, &out)
}

// Rollback puts an agent's worktree and conversation back to a checkpoint.
func (c *Client) Rollback(ctx context.Context, ref, checkpoint string) (RollbackResult, error) {
	var out RollbackResult
	return out, c.agentDo(ctx, http.MethodPost, ref, "/rollback", RollbackRequest{Checkpoint: checkpoint}, &out)
}

func (c *Client) Fork(ctx context.Context, ref string, req ForkRequest) (Job, error) {
	var out Job
	return out, c.agentDo(ctx, http.MethodPost, ref, "/fork", req, &out)
}

func (c *Client) Usage(ctx context.Context, interval time.Duration) (Usage, error) {
	var out Usage
	return out, c.do(ctx, http.MethodGet, "/v1/usage?interval="+url.QueryEscape(interval.String()), nil, &out)
}

// DiskGuard is what the daemon's disk guard last found.
func (c *Client) DiskGuard(ctx context.Context) (DiskGuard, error) {
	var out DiskGuard
	return out, c.do(ctx, http.MethodGet, "/v1/disk", nil, &out)
}

func (c *Client) DiskUsage(ctx context.Context) (DiskUsage, error) {
	var out DiskUsage
	return out, c.do(ctx, http.MethodGet, "/v1/usage/disk", nil, &out)
}

// AgentDisk is one agent's machine and worktree sizes, as its info card shows them.
func (c *Client) AgentDisk(ctx context.Context, project, name string) (AgentDisk, error) {
	var out AgentDisk
	return out, c.do(ctx, http.MethodGet, "/v1/agents/"+url.PathEscape(project)+"/"+url.PathEscape(name)+"/disk", nil, &out)
}

// StoppedMachines lists the agents holding a stopped machine, in project or
// in every project when it is "". check adds what "remove stopped agents'
// machines" would do with each, which reads git and GitHub.
func (c *Client) StoppedMachines(ctx context.Context, project string, check bool) (StoppedMachines, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	if check {
		q.Set("check", "1")
	}
	var out StoppedMachines
	return out, c.do(ctx, http.MethodGet, "/v1/stopped-machines?"+q.Encode(), nil, &out)
}

// RemoveStopped removes the stopped machines of the agents in req that are
// still safe to remove.
func (c *Client) RemoveStopped(ctx context.Context, req RemoveStoppedRequest) (RemoveStoppedResult, error) {
	var out RemoveStoppedResult
	return out, c.do(ctx, http.MethodPost, "/v1/stopped-machines/remove", req, &out)
}

func (c *Client) MemoryUsage(ctx context.Context) (MemoryUsage, error) {
	var out MemoryUsage
	return out, c.do(ctx, http.MethodGet, "/v1/usage/memory", nil, &out)
}

// HostMemory is the daemon's machine's memory and CPUs, from /proc/meminfo.
func (c *Client) HostMemory(ctx context.Context) (HostMemory, error) {
	var out HostMemory
	return out, c.do(ctx, http.MethodGet, "/v1/usage/host", nil, &out)
}

func (c *Client) CPUUsage(ctx context.Context, interval time.Duration) (CPUUsage, error) {
	var out CPUUsage
	return out, c.do(ctx, http.MethodGet, "/v1/usage/cpu?interval="+url.QueryEscape(interval.String()), nil, &out)
}

func (c *Client) ImageReady(ctx context.Context) (bool, error) {
	var out struct {
		Ready bool `json:"ready"`
	}
	return out.Ready, c.do(ctx, http.MethodGet, "/v1/image", nil, &out)
}

func (c *Client) BuildImage(ctx context.Context, req BuildImageRequest) (Job, error) {
	var out Job
	return out, c.do(ctx, http.MethodPost, "/v1/image/build", req, &out)
}

func (c *Client) Auth(ctx context.Context) (AuthStatus, error) {
	var out AuthStatus
	return out, c.do(ctx, http.MethodGet, "/v1/auth", nil, &out)
}

func (c *Client) Jobs(ctx context.Context) ([]Job, error) {
	var out []Job
	return out, c.do(ctx, http.MethodGet, "/v1/jobs", nil, &out)
}

func (c *Client) Job(ctx context.Context, id string) (Job, error) {
	var out Job
	return out, c.do(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(id), nil, &out)
}

func (c *Client) CancelJob(ctx context.Context, id string) (Job, error) {
	var out Job
	return out, c.do(ctx, http.MethodPost, "/v1/jobs/"+url.PathEscape(id)+"/cancel", nil, &out)
}

// JobLog returns a job's log so far.
func (c *Client) JobLog(ctx context.Context, id string) (string, error) {
	var out string
	return out, c.do(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(id)+"/log", nil, &out)
}

// FollowJobLog copies a job's log to w as it is written, until the job ends.
func (c *Client) FollowJobLog(ctx context.Context, id string, w io.Writer) error {
	resp, err := c.request(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(id)+"/log?follow=true", nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, err = io.Copy(w, resp.Body)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// Events calls fn for every event on the daemon's stream until ctx ends or fn fails.
func (c *Client) Events(ctx context.Context, fn func(Event) error) error {
	resp, err := c.request(ctx, http.MethodGet, "/v1/events", nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		if err := fn(ev); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return scanner.Err()
}

// Remote is this machine's connection to a hub, as an environment.
func (c *Client) RemoteStatus(ctx context.Context) (RemoteStatus, error) {
	var out RemoteStatus
	return out, c.do(ctx, http.MethodGet, "/v1/remote", nil, &out)
}

// ConnectRemote saves a hub for this machine to connect to, and connects.
func (c *Client) ConnectRemote(ctx context.Context, req RemoteConnectRequest) (RemoteStatus, error) {
	var out RemoteStatus
	return out, c.do(ctx, http.MethodPut, "/v1/remote", req, &out)
}

// PairRemote asks a hub to approve this machine as an environment, and
// answers at once: the pairing is in RemoteStatus.Pairing, and the machine
// connects by itself when it's approved.
func (c *Client) PairRemote(ctx context.Context, req RemotePairRequest) (RemoteStatus, error) {
	var out RemoteStatus
	return out, c.do(ctx, http.MethodPost, "/v1/remote/pair", req, &out)
}

func (c *Client) DisconnectRemote(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/v1/remote", nil, nil)
}

// Self asks the in-agent API which agent this is.
func (c *Client) Self(ctx context.Context) (Self, error) {
	var out Self
	return out, c.do(ctx, http.MethodGet, "/v1/self", nil, &out)
}

// ReportDraft is the report the user would send, redacted, with the client's
// own sections (none from the command line) after the daemon's.
func (c *Client) ReportDraft(ctx context.Context, req ReportDraftRequest) (ReportDraft, error) {
	var draft ReportDraft
	return draft, c.do(ctx, http.MethodPost, "/v1/reports/draft", req, &draft)
}

// SendReport sends a report to AgentBox's developers.
func (c *Client) SendReport(ctx context.Context, req ReportRequest) (ReportSent, error) {
	var sent ReportSent
	return sent, c.do(ctx, http.MethodPost, "/v1/reports", req, &sent)
}

// PullRequestDetail reads one of a project repository's pull requests with its
// description, labels and check runs.
func (c *Client) PullRequestDetail(ctx context.Context, project string, number int) (PullRequestDetail, error) {
	var out PullRequestDetail
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/pulls/"+strconv.Itoa(number), nil, &out)
}

// PullRequestFiles lists the files one of a project repository's pull
// requests changes.
func (c *Client) PullRequestFiles(ctx context.Context, project string, number int) (PullRequestFiles, error) {
	var out PullRequestFiles
	return out, c.do(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/pulls/"+strconv.Itoa(number)+"/files", nil, &out)
}

// PullFileDiff reads the diff of one file a pull request changes.
func (c *Client) PullFileDiff(ctx context.Context, project string, number int, path string) (PullFileDiff, error) {
	var out PullFileDiff
	p := "/v1/projects/" + url.PathEscape(project) + "/pulls/" + strconv.Itoa(number) + "/diff?path=" + url.QueryEscape(path)
	return out, c.do(ctx, http.MethodGet, p, nil, &out)
}

// TerminalTails reads the last lines of the named agents' terminals in
// project: the daemon's default (4) when lines is 0.
func (c *Client) TerminalTails(ctx context.Context, project string, agents []string, lines int) (TerminalTails, error) {
	var out TerminalTails
	path := "/v1/projects/" + url.PathEscape(project) + "/terminals?agents=" + url.QueryEscape(strings.Join(agents, ","))
	if lines > 0 {
		path += "&lines=" + strconv.Itoa(lines)
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// Profile copies one of the daemon's Go profiles (net/http/pprof, served on
// its socket only) to w: "heap", "goroutine", "allocs"; query is pprof's own,
// "gc=1" or "debug=1".
func (c *Client) Profile(ctx context.Context, name, query string, w io.Writer) error {
	path := "/debug/pprof/" + url.PathEscape(name)
	if query != "" {
		path += "?" + query
	}
	resp, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, err = io.Copy(w, resp.Body)
	return err
}
