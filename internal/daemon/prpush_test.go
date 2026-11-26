package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/github"
	"agentbox/internal/state"
)

// someoneElses is a commit id no agent has: where a reviewer's, or the
// user's, push left a pull request's branch.
const someoneElses = "feedfacefeedfacefeedfacefeedfacefeedface"

func TestPushedHeadIsAgents(t *testing.T) {
	t.Parallel()
	heads := []agentHead{
		{agent: "agent-01", branch: "agentbox/agent-01", tip: "c3", own: []string{"c3", "c2", "c1"}},
		{agent: "agent-01", branch: "agentbox/follow-up", tip: "d1", own: []string{"d1"}},
		{agent: "agent-02", branch: "agentbox/agent-02", tip: "e2", own: []string{"e2", "e1"}},
		{agent: "agent-03", branch: "agentbox/agent-03", failed: true},
	}
	for _, c := range []struct {
		name, agent, sha string
		want             bool
	}{
		{"its tip", "agent-01", "c3", true},
		{"an older commit of its own, pushed before it committed again", "agent-01", "c1", true},
		{"the tip of a follow-up branch", "agent-01", "d1", true},
		{"somebody else's commit", "agent-01", someoneElses, false},
		{"another agent's commit", "agent-01", "e2", false},
		{"no head", "agent-01", "", false},
		{"an agent git couldn't read says no", "agent-03", "c1", false},
		{"an agent with no heads", "agent-09", "c1", false},
	} {
		if got := pushedHeadIsAgents(heads, c.agent, c.sha); got != c.want {
			t.Errorf("%s: pushedHeadIsAgents(%s, %q) = %v, want %v", c.name, c.agent, c.sha, got, c.want)
		}
	}
}

// The push a broken pull request's agent is told to make depends on whose
// commit the branch is at: forced, but held to that commit, when it's the
// agent's own; never forced when somebody else pushed.
func TestPRFixMessageForcesOnlyOverTheAgentsOwnHead(t *testing.T) {
	t.Parallel()
	pr := github.WatchedPR{
		Number: 9, URL: "https://github.com/acme/hello-stack/pull/9", HeadBranch: "feat/reminders", BaseBranch: "main",
		HeadSHA: "0123456789abcdef0123456789abcdef01234567",
	}
	problems := []prProblem{prConflict, prChecks}

	mine := prFixMessage(pr, problems, true)
	pinned := "`git push --force-with-lease=feat/reminders:" + pr.HeadSHA + " origin HEAD:feat/reminders`"
	if !strings.Contains(mine, pinned) {
		t.Errorf("an agent whose own commit the branch is at is told %q, want the lease held to the head: %s", mine, pinned)
	}
	if strings.Contains(mine, "isn't a commit you made") || !strings.Contains(mine, "bring `origin/main` into your branch") {
		t.Errorf("an agent whose own commit the branch is at is told %q, want its usual conflict advice", mine)
	}
	// A bare lease is the hole: nothing in the message may offer it.
	if strings.Contains(strings.ReplaceAll(mine, "--force-with-lease=feat/reminders:"+pr.HeadSHA, ""), "--force") {
		t.Errorf("an agent whose own commit the branch is at is offered a force without the head pinned: %q", mine)
	}

	theirs := prFixMessage(pr, problems, false)
	if strings.Contains(theirs, "--force") {
		t.Errorf("an agent told somebody else pushed is offered a forced push: %q", theirs)
	}
	for _, want := range []string{
		"isn't a commit you made",
		"git pull --rebase origin feat/reminders",
		"`git push origin HEAD:feat/reminders`",
		"a merge, not a rebase",
	} {
		if !strings.Contains(theirs, want) {
			t.Errorf("an agent told somebody else pushed got %q, want %q", theirs, want)
		}
	}
}

// The reported bug end to end: the watch told the agent to push with a bare
// --force-with-lease after a fetch, and what a reviewer pushed was dropped.
// The message follows the head GitHub reports.
func TestPRWatchNoForceOverAnotherPush(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	rec := recordPRWatch(d)
	_, agents := pullsProject(t, d, "agent-01")
	first := commitOn(t, agents["agent-01"], "reminders.txt")

	// The watch follows the pull request whose head is the agent's tip.
	gh.set(9, first, "OPEN", "MERGEABLE", "PENDING", "")
	pollNow(d)
	rec.take()
	// The agent committed again since it pushed: the head is its own commit,
	// though no longer its tip.
	commitOn(t, agents["agent-01"], "more.txt")
	gh.set(9, first, "OPEN", "MERGEABLE", "FAILURE", "", "test")
	pollNow(d)
	said, _ := rec.take()
	if len(said) != 1 || !strings.Contains(said[0], "--force-with-lease=feat/reminders:"+first+" ") || strings.Contains(said[0], "isn't a commit you made") {
		t.Fatalf("the agent's own head: told %q, want a push held to %s", said, first)
	}

	// A reviewer pushes to the branch; the checks fail on their commit.
	gh.set(9, someoneElses, "OPEN", "MERGEABLE", "FAILURE", "", "test")
	pollNow(d)
	said, _ = rec.take()
	if len(said) != 1 || strings.Contains(said[0], "--force") || !strings.Contains(said[0], "git pull --rebase origin feat/reminders") {
		t.Fatalf("somebody else's head: told %q, want no force and a pull --rebase", said)
	}

	// And it conflicts: brought in by merging, since a rebase would rewrite
	// what the reviewer pushed.
	gh.set(9, someoneElses, "OPEN", "CONFLICTING", "FAILURE", "", "test")
	pollNow(d)
	said, _ = rec.take()
	if len(said) != 1 || strings.Contains(said[0], "--force") || !strings.Contains(said[0], "merge `origin/main` into it") {
		t.Fatalf("a conflict on somebody else's head: told %q, want a merge and no force", said)
	}

	// The agent takes it in and pushes: the head is its own again (here, its
	// next commit), and it is told it may force, held to that.
	next := commitOn(t, agents["agent-01"], "again.txt")
	gh.set(9, next, "OPEN", "MERGEABLE", "FAILURE", "", "test")
	pollNow(d)
	said, _ = rec.take()
	if len(said) != 1 || !strings.Contains(said[0], "--force-with-lease=feat/reminders:"+next+" ") {
		t.Fatalf("the agent's own head again: told %q, want a push held to %s", said, next)
	}
}

// A restack rebases the child's branch and pushes it forced. Held to its
// pull request's head when that is the child's own; with somebody else's, the
// child takes theirs in first, so the rebase carries them along.
func TestRestackForcesOnlyOverTheChildsOwnHead(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		mine  bool
		wants []string
		not   []string
	}{
		{
			name:  "own",
			mine:  true,
			wants: []string{"`git push --force-with-lease=agentbox/agent-02:%s origin HEAD:agentbox/agent-02`", "should list only your own commits"},
			not:   []string{"isn't a commit you made", "git pull --rebase"},
		},
		{
			name:  "theirs",
			mine:  false,
			wants: []string{"`git push --force-with-lease=agentbox/agent-02:%s origin HEAD:agentbox/agent-02`", "isn't a commit you made", "git pull --rebase origin agentbox/agent-02", "your commits and theirs, nothing else"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := startTestDaemon(t, t.TempDir(), fakeIncus)
			rec := recordPRWatch(d)
			_, agents := pullsProject(t, d, "agent-01", "agent-02")
			ctx := context.Background()
			parentTip := commitOn(t, agents["agent-01"], "parent.txt")
			if err := d.srv.store.SetAgentBase(ctx, "hello-stack", "agent-02", "agentbox/agent-01", parentTip); err != nil {
				t.Fatal(err)
			}
			head := commitOn(t, agents["agent-02"], "child.txt")
			if !c.mine {
				head = someoneElses
			}
			var mu sync.Mutex
			retargeted := false
			stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/hello-stack/pulls" && r.URL.Query().Get("base") == "agentbox/agent-01":
					if retargeted {
						_, _ = w.Write([]byte(`[]`))
						return
					}
					fmt.Fprintf(w, `[{"number":15,"state":"open","html_url":"https://github.com/acme/hello-stack/pull/15","base":{"ref":"agentbox/agent-01"},"head":{"ref":"agentbox/agent-02","sha":%q}}]`, head)
				case r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/hello-stack/pulls/15":
					retargeted = true
					_, _ = w.Write([]byte(`{}`))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(stub.Close)
			d.setGitHub(t, stub.URL)
			p, err := d.srv.store.Project(ctx, "hello-stack")
			if err != nil {
				t.Fatal(err)
			}
			client, repo, err := d.srv.githubFor(p)
			if err != nil {
				t.Fatal(err)
			}
			d.srv.restack(ctx, p, client, repo, stackBase{Number: 12, URL: "https://github.com/acme/hello-stack/pull/12", State: "merged",
				Head: "agentbox/agent-01", HeadSHA: parentTip, Base: "main"})

			said, _ := rec.take()
			if len(said) != 1 || !strings.HasPrefix(said[0], "agent-02: ") {
				t.Fatalf("told %q, want agent-02 once", said)
			}
			msg := said[0]
			for _, want := range c.wants {
				if strings.Contains(want, "%s") {
					want = fmt.Sprintf(want, head)
				}
				if !strings.Contains(msg, want) {
					t.Errorf("told %q, want %q", msg, want)
				}
			}
			for _, not := range c.not {
				if strings.Contains(msg, not) {
					t.Errorf("told %q, want no %q", msg, not)
				}
			}
			if strings.Contains(strings.ReplaceAll(msg, "--force-with-lease=agentbox/agent-02:"+head, ""), "--force") {
				t.Errorf("told %q: a forced push that isn't held to the head", msg)
			}
		})
	}
}

// A restack with no pull request to read a head from is as it was.
func TestRestackMessageWithoutAPullRequest(t *testing.T) {
	t.Parallel()
	parent := stackBase{Number: 12, URL: "u", Head: "agentbox/agent-01", HeadSHA: "abcdef1234567", Base: "main"}
	msg := restackMessage(parent, state.Agent{Branch: "agentbox/agent-02"}, nil, true)
	if !strings.Contains(msg, "open your pull request against it") || !strings.Contains(msg, "push it again with `--force-with-lease`") {
		t.Errorf("restackMessage without a pull request = %q", msg)
	}
}
