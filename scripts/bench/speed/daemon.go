package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// daemon talks to AgentBox's daemon over its socket, as the CLI and the app
// do (internal/api). Only the few calls and fields the benchmark needs are
// here, so it stays its own module and doesn't move with every API change.
type daemon struct {
	socket string
	http   *http.Client
}

func defaultSocket() string {
	if s := os.Getenv("AGENTBOX_SOCKET"); s != "" {
		return s
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, _ := os.UserHomeDir()
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "agentbox", "run", "agentbox.sock")
}

func newDaemon(socket string) *daemon {
	return &daemon{socket: socket, http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}}
}

func (d *daemon) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://agentbox"+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return fmt.Errorf("the daemon at %s: %w", d.socket, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(data))
		}
		return fmt.Errorf("%s %s: %s (%d)", method, path, e.Error, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func agentPath(project, name string) string {
	return "/v1/agents/" + url.PathEscape(project) + "/" + url.PathEscape(name)
}

type apiProject struct {
	Name string `json:"name"`
	Root string `json:"root"` // its main checkout
}

type createRequest struct {
	Project      string    `json:"project"`
	Name         string    `json:"name"`
	Title        string    `json:"title,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	Task         string    `json:"task,omitempty"`
	AI           string    `json:"ai"`
	Model        *string   `json:"model,omitempty"`
	Effort       *string   `json:"effort,omitempty"`
	From         string    `json:"from,omitempty"`
	NoEnv        bool      `json:"noEnv,omitempty"`
	FinishNotice string    `json:"finishNotice,omitempty"`
	Connectors   *[]string `json:"connectors,omitempty"`
}

type apiJob struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

type apiAgent struct {
	Project  string `json:"project"`
	Name     string `json:"name"`
	Instance string `json:"instance"`
	Worktree string `json:"worktree"`
	State    string `json:"state"`
	Chat     string `json:"chat,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
}

type chatThread struct {
	Session struct {
		State      string   `json:"state"`
		Error      string   `json:"error,omitempty"`
		Adapter    string   `json:"adapter,omitempty"`
		Background []string `json:"background,omitempty"`
	} `json:"session"`
	Items []chatItem `json:"items"`
}

type chatItem struct {
	ID        string      `json:"id"`
	Turn      string      `json:"turn"`
	Kind      string      `json:"kind"`
	Text      string      `json:"text,omitempty"`
	Woken     bool        `json:"woken,omitempty"`
	Hidden    bool        `json:"hidden,omitempty"`
	Tool      *chatTool   `json:"tool,omitempty"`
	Result    *turnResult `json:"result,omitempty"` // on a user message, once its turn ended
	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
}

type chatTool struct {
	Name    string `json:"name,omitempty"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Command string `json:"command,omitempty"`
}

type turnResult struct {
	State      string    `json:"state"` // completed, cancelled or failed
	StopReason string    `json:"stopReason,omitempty"`
	EndedAt    time.Time `json:"endedAt"`
	Background []string  `json:"background,omitempty"`
}

func (d *daemon) projects(ctx context.Context) ([]apiProject, error) {
	var out []apiProject
	return out, d.do(ctx, http.MethodGet, "/v1/projects", nil, &out)
}

func (d *daemon) create(ctx context.Context, req createRequest) (apiJob, error) {
	var out apiJob
	return out, d.do(ctx, http.MethodPost, "/v1/agents", req, &out)
}

func (d *daemon) job(ctx context.Context, id string) (apiJob, error) {
	var out apiJob
	return out, d.do(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(id), nil, &out)
}

func (d *daemon) agent(ctx context.Context, project, name string) (apiAgent, error) {
	var out apiAgent
	return out, d.do(ctx, http.MethodGet, agentPath(project, name), nil, &out)
}

func (d *daemon) chat(ctx context.Context, project, name string) (chatThread, error) {
	var out chatThread
	return out, d.do(ctx, http.MethodGet, agentPath(project, name)+"/chat", nil, &out)
}

func (d *daemon) destroy(ctx context.Context, project, name string) error {
	q := url.Values{"force": {"true"}, "deleteBranch": {"true"}, "deleteMedia": {"true"}}
	return d.do(ctx, http.MethodDelete, agentPath(project, name)+"?"+q.Encode(), nil, nil)
}

// ensureRunning starts or resumes an agent that AgentBox stopped or paused
// while it sat idle (auto-stop, idle reclaim): the exec mode's machine can
// sit through a long stretch of agent-mode runs.
func (d *daemon) ensureRunning(ctx context.Context, project, name string) error {
	a, err := d.agent(ctx, project, name)
	if err != nil {
		return err
	}
	switch a.State {
	case "running":
		return nil
	case "paused":
		return d.do(ctx, http.MethodPost, agentPath(project, name)+"/resume", nil, nil)
	default:
		return d.do(ctx, http.MethodPost, agentPath(project, name)+"/start", nil, nil)
	}
}

// waitJob waits for a job to finish, and fails with its error.
func (d *daemon) waitJob(ctx context.Context, id string, poll time.Duration) error {
	for {
		j, err := d.job(ctx, id)
		if err != nil {
			return err
		}
		switch j.Status {
		case "running", "":
		case "succeeded":
			return nil
		default: // failed, cancelled
			if j.Error != "" {
				return fmt.Errorf("job %s %s: %s", id, j.Status, j.Error)
			}
			return fmt.Errorf("job %s %s", id, j.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// createAgent creates an agent and waits for its machine, returning when the
// creation job ended.
func (d *daemon) createAgent(ctx context.Context, req createRequest, poll time.Duration) error {
	j, err := d.create(ctx, req)
	if err != nil {
		return err
	}
	return d.waitJob(ctx, j.ID, poll)
}

// agentbox runs the agentbox command: for the project settings the API has
// no simpler way to reach, and `agentbox exec`, which is how a command gets
// into an agent's machine (incus exec, through the VM's front end).
type agentbox struct {
	bin string
}

func (a agentbox) cmd(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, a.bin, args...)
}

// run runs agentbox and returns its output, failing with its stderr.
func (a agentbox) run(ctx context.Context, args ...string) (string, error) {
	cmd := a.cmd(ctx, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("agentbox %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// exec runs a shell command in an agent's worktree, as its user, in a login
// shell, and returns its stdout.
func (a agentbox) exec(ctx context.Context, ref, command string) (string, error) {
	return a.run(ctx, "exec", ref, "--", command)
}
