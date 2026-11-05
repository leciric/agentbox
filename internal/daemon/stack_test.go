package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/github"
)

// When the pull request a child agent is stacked on merges, the child's pull
// request is retargeted to where the parent merged, the child's base moves
// there with the parent's old tip kept, and the child is told to rebase from
// that tip — once, however many times the merge is seen.
func TestRestackMovesTheChildOntoWhereItsParentMerged(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	rec := recordPRWatch(d)
	var mu sync.Mutex
	var retargeted []string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/hello-stack/pulls" && r.URL.Query().Get("base") == "agentbox/agent-01":
			if len(retargeted) > 0 {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"number":15,"state":"open","html_url":"https://github.com/acme/hello-stack/pull/15","base":{"ref":"agentbox/agent-01"},"head":{"ref":"agentbox/agent-02","sha":"c2"}}]`))
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/hello-stack/pulls/15":
			var body struct{ Base string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			retargeted = append(retargeted, body.Base)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(stub.Close)
	d.setGitHub(t, stub.URL)
	_, agents := pullsProject(t, d, "agent-01", "agent-02", "agent-03")
	ctx := context.Background()
	parentTip := commitOn(t, agents["agent-01"], "parent.txt")
	// agent-02 stands on agent-01's branch and has a pull request onto it;
	// agent-03 stands on it too, with none yet.
	for _, name := range []string{"agent-02", "agent-03"} {
		if err := d.srv.store.SetAgentBase(ctx, "hello-stack", name, "agentbox/agent-01", parentTip); err != nil {
			t.Fatal(err)
		}
	}
	p, err := d.srv.store.Project(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	client, repo, err := d.srv.githubFor(p)
	if err != nil {
		t.Fatal(err)
	}
	parent := stackBase{Number: 12, URL: "https://github.com/acme/hello-stack/pull/12", State: "merged",
		Head: "agentbox/agent-01", HeadSHA: parentTip, Base: "main"}
	d.srv.restack(ctx, p, client, repo, parent)
	d.srv.restack(ctx, p, client, repo, parent) // the watch, after the app's merge

	if len(retargeted) != 1 || retargeted[0] != "main" {
		t.Fatalf("retargeted = %v, want #15 to main once", retargeted)
	}
	for _, name := range []string{"agent-02", "agent-03"} {
		a, err := d.srv.store.Agent(ctx, "hello-stack", name)
		if err != nil {
			t.Fatal(err)
		}
		if a.BaseRef != "main" || a.BaseCommit != parentTip {
			t.Errorf("%s's base = %s at %s, want main at the parent's old tip", name, a.BaseRef, a.BaseCommit)
		}
	}
	said, leads := rec.take()
	if len(said) != 2 {
		t.Fatalf("told %q, want both children once", said)
	}
	for _, msg := range said {
		if !strings.Contains(msg, "git rebase --onto origin/main "+parentTip) || !strings.Contains(msg, "--force-with-lease") {
			t.Errorf("a child was told %q, want a rebase from the parent's old tip", msg)
		}
	}
	if !strings.Contains(said[0], "#15 targets `main` now") && !strings.Contains(said[1], "#15 targets `main` now") {
		t.Errorf("agent-02 wasn't told its pull request moved: %q", said)
	}
	if !strings.Contains(strings.Join(said, "\n"), "agent-03: AgentBox: the pull request your branch is stacked on, #12") ||
		!strings.Contains(strings.Join(said, "\n"), "open your pull request against it") {
		t.Errorf("agent-03 wasn't told where to open its pull request: %q", said)
	}
	if len(leads) != 1 || leads[0].act || !strings.Contains(leads[0].text, "agent-02, agent-03") {
		t.Errorf("the lead was told %+v, want one notice naming both, no turn", leads)
	}
}

// A parent closed without merging moves nothing: the chat decides.
func TestRestackLeavesAClosedParentsChildrenToTheChat(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	rec := recordPRWatch(d)
	_, _ = pullsProject(t, d, "agent-01", "agent-02")
	ctx := context.Background()
	if err := d.srv.store.SetAgentBase(ctx, "hello-stack", "agent-02", "agentbox/agent-01", "abc"); err != nil {
		t.Fatal(err)
	}
	p, _ := d.srv.store.Project(ctx, "hello-stack")
	d.srv.restack(ctx, p, github.Client{BaseURL: "http://127.0.0.1:1"}, github.Repo{Owner: "acme", Name: "hello-stack"},
		stackBase{Number: 12, State: "closed", Head: "agentbox/agent-01", HeadSHA: "abc", Base: "main"})
	said, leads := rec.take()
	if len(said) != 0 || len(leads) != 1 || !leads[0].act || !strings.Contains(leads[0].text, "agent-02") {
		t.Fatalf("told %q and the lead %+v, want only the lead, with a turn", said, leads)
	}
	if a, _ := d.srv.store.Agent(ctx, "hello-stack", "agent-02"); a.BaseRef != "agentbox/agent-01" {
		t.Errorf("agent-02's base moved to %s", a.BaseRef)
	}
}

// A merged fork pull request whose head is called like a branch of the
// project's own is nothing anyone stood on: it moves and says nothing.
func TestRestackIgnoresAForkPullRequest(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	rec := recordPRWatch(d)
	_, _ = pullsProject(t, d, "agent-01", "agent-02")
	ctx := context.Background()
	if err := d.srv.store.SetAgentBase(ctx, "hello-stack", "agent-02", "main", "abc"); err != nil {
		t.Fatal(err)
	}
	p, _ := d.srv.store.Project(ctx, "hello-stack")
	// The client points nowhere: asking GitHub for the pull requests onto main would fail the test's log, and moving any would fail.
	d.srv.restack(ctx, p, github.Client{BaseURL: "http://127.0.0.1:1"}, github.Repo{Owner: "acme", Name: "hello-stack"},
		stackBase{Number: 526, State: "closed", Head: "main", Base: "develop", Fork: true})
	said, leads := rec.take()
	if len(said) != 0 || len(leads) != 0 {
		t.Fatalf("told %q and the lead %+v, want nobody", said, leads)
	}
	if a, _ := d.srv.store.Agent(ctx, "hello-stack", "agent-02"); a.BaseRef != "main" {
		t.Errorf("agent-02's base moved to %s", a.BaseRef)
	}
}

// A parent merged outside the app is seen by the pull request watch, which
// restacks its children the same way.
func TestPRWatchRestacksWhenTheParentMerges(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	gh := newFakeGraphQL(t, d)
	rec := recordPRWatch(d)
	_, agents := pullsProject(t, d, "agent-01", "agent-02")
	ctx := context.Background()
	head := commitOn(t, agents["agent-01"], "parent.txt")
	// The fake's pull requests are all on feat/reminders.
	if err := d.srv.store.SetAgentBase(ctx, "hello-stack", "agent-02", "feat/reminders", head); err != nil {
		t.Fatal(err)
	}
	gh.set(12, head, "OPEN", "MERGEABLE", "SUCCESS", "")
	pollNow(d)
	rec.take()
	gh.set(12, head, "MERGED", "UNKNOWN", "SUCCESS", "")
	pollNow(d)
	said, _ := rec.take()
	if len(said) != 1 || !strings.HasPrefix(said[0], "agent-02: ") || !strings.Contains(said[0], "git rebase --onto origin/main "+head) {
		t.Fatalf("told %q, want agent-02 to rebase from the parent's old tip", said)
	}
	if a, _ := d.srv.store.Agent(ctx, "hello-stack", "agent-02"); a.BaseRef != "main" {
		t.Errorf("agent-02's base = %s, want main", a.BaseRef)
	}
}
