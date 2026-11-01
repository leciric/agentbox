package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/github"
)

// issuesStub is a GitHub with two open issues, #7 unlabelled and #8 labelled
// agentbox, and no pull requests.
func issuesStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/hello-stack/issues":
			eight := `{"number":8,"title":"Dark mode","state":"open","html_url":"https://github.com/acme/hello-stack/issues/8","labels":[{"name":"agentbox","color":"0e8a16"}],"updated_at":"2026-10-01T10:00:00Z"}`
			if r.URL.Query().Get("labels") == "agentbox" {
				_, _ = fmt.Fprintf(w, "[%s]", eight)
				return
			}
			_, _ = fmt.Fprintf(w, `[{"number":7,"title":"Reminders crash","state":"open","html_url":"https://github.com/acme/hello-stack/issues/7","comments":1,"user":{"login":"ana"},"updated_at":"2026-10-02T10:00:00Z"},
				{"number":5,"title":"A pull request","pull_request":{"url":"x"}}, %s]`, eight)
		case r.URL.Path == "/repos/acme/hello-stack/issues/7":
			_, _ = w.Write([]byte(`{"number":7,"title":"Reminders crash","body":"Saving a reminder crashes the app.","state":"open","html_url":"https://github.com/acme/hello-stack/issues/7","comments":1,"user":{"login":"ana"}}`))
		case r.URL.Path == "/repos/acme/hello-stack/issues/7/comments":
			_, _ = w.Write([]byte(`[{"body":"Only on Android.","user":{"login":"bo"}}]`))
		case r.URL.Path == "/repos/acme/hello-stack/issues/8":
			_, _ = w.Write([]byte(`{"number":8,"title":"Dark mode","body":"Add it.","state":"open","html_url":"https://github.com/acme/hello-stack/issues/8"}`))
		case r.URL.Path == "/repos/acme/hello-stack/pulls":
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/repos/acme/hello-stack":
			_, _ = w.Write([]byte(`{"default_branch":"main","permissions":{"push":true}}`))
		case strings.Contains(r.URL.Path, "/commits/"):
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected GitHub call: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Handing an issue to an agent makes one whose task is the issue, comments
// included, and whose pull request is to close it; the Issues view then shows
// the agent on the issue, and the issue can't be handed twice.
func TestAnIssueHandedToAnAgentIsItsTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), recordingIncus, testConfig{instances: runningAgent01})
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)
	d.setGitHub(t, issuesStub(t).URL)

	before, err := d.client.ProjectIssues(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if before.GitHub != "acme/hello-stack" || len(before.Issues) != 2 || before.Issues[0].Number != 7 || before.Issues[0].Handed != nil {
		t.Fatalf("issues before = %+v, want #7 and #8, no pull request, nobody on them", before)
	}

	job, err := d.client.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello-stack", Name: "agent-01", AI: "none", Issue: 7, Task: "Leave the iOS code alone."})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "agent-01 to be created", func() bool {
		j, err := d.client.Job(ctx, job.ID)
		return err == nil && j.Done()
	})
	if j, _ := d.client.Job(ctx, job.ID); j.Status != api.JobSucceeded {
		t.Fatalf("creating agent-01 = %s: %s", j.Status, j.Error)
	}
	events := eventsOfType(t, d, "hello-stack", "agent_created")
	if len(events) != 1 {
		t.Fatalf("agent_created events = %d, want 1", len(events))
	}
	p := payloadOf(t, events[0])
	task, _ := p["task"].(string)
	for _, want := range []string{"#7", "Saving a reminder crashes the app.", "Only on Android.", "@bo", `"Closes #7"`, "Leave the iOS code alone."} {
		if !strings.Contains(task, want) {
			t.Errorf("task lacks %q:\n%s", want, task)
		}
	}
	if p["title"] != "#7 Reminders crash" {
		t.Errorf("title = %v", p["title"])
	}
	if branch, _ := p["branch"].(string); !strings.Contains(branch, "issue-7-reminders-crash") {
		t.Errorf("branch = %q", branch)
	}

	waitFor(t, "the issue to name its agent", func() bool {
		out, err := d.client.ProjectIssues(ctx, "hello-stack")
		return err == nil && out.Issues[0].Handed != nil && out.Issues[0].Handed.Agent == "agent-01"
	})
	if _, err := d.client.CreateAgent(ctx, api.CreateAgentRequest{Project: "hello-stack", AI: "none", Issue: 7}); err == nil || !strings.Contains(err.Error(), "agent-01") {
		t.Errorf("handing #7 again = %v, want it refused, naming agent-01", err)
	}
}

// A project that picks issues up hands each one labelled agentbox to an agent
// once, and leaves the others alone.
func TestPickingUpHandsLabelledIssuesOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), recordingIncus, testConfig{instances: runningAgent01})
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	githubRepoStub(t, d, repo)
	d.setGitHub(t, issuesStub(t).URL)
	// A pickup's agent is on the default tool, Claude Code, which needs a login.
	if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveClaudeToken("default", "sk-ant-oat01-default"); err != nil {
		t.Fatal(err)
	}

	on := true
	if _, err := d.client.UpdateProject(ctx, "hello-stack", api.UpdateProjectRequest{PickUpIssues: &on}); err != nil {
		t.Fatal(err)
	}
	p, err := d.srv.store.Project(ctx, "hello-stack")
	if err != nil || !p.PickUpIssues {
		t.Fatalf("PickUpIssues = %v, %v", p.PickUpIssues, err)
	}
	var created atomic.Int32
	count := func() int {
		rows, _ := d.srv.store.IssueAgents(ctx, "hello-stack", "acme/hello-stack")
		if _, ok := rows[7]; ok {
			t.Error("#7 isn't labelled, and was picked up")
		}
		if r, ok := rows[8]; ok && r.Auto {
			created.Store(1)
		}
		return len(rows)
	}
	d.srv.pickUpProjectIssues(ctx, p)
	if count(); created.Load() != 1 {
		t.Fatal("#8 is labelled agentbox and wasn't picked up")
	}
	listed := func() int {
		all, err := d.srv.jobs.list(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		return len(all)
	}
	jobs := listed()
	d.srv.pickUpProjectIssues(ctx, p)
	if now := listed(); now != jobs {
		t.Errorf("a second pass made %d more jobs, want none", now-jobs)
	}
	count()
}

func TestIssueTaskIsCutToFit(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("word ", issueBodyLimit)
	comments := make([]github.IssueComment, 40)
	for i := range comments {
		comments[i] = github.IssueComment{Author: "bo", Body: strings.Repeat("é", issueCommentLimit), CreatedAt: time.Now()}
	}
	task := issueTask(github.Repo{Owner: "acme", Name: "x"}, github.Issue{Number: 3, Title: "Big", Body: long}, comments, "")
	if len(task) > issueTaskLimit+2_000 {
		t.Errorf("task is %d bytes, want about %d at most", len(task), issueTaskLimit)
	}
	if !strings.Contains(task, "gh issue view 3 --comments") || !strings.Contains(task, `"Closes #3"`) || !strings.Contains(task, "more comments not included") {
		t.Errorf("a cut task doesn't say how to read the rest, or lost its closing line:\n%s", task[len(task)-600:])
	}
	if !strings.HasSuffix(strings.TrimSpace(task), "guessing.") {
		t.Error("with nothing extra, the task should end on its own instructions")
	}
}
