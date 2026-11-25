package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// sessionNotices are the hidden notices the lead of one session of a
// project's chat was told.
func sessionNotices(t *testing.T, d testDaemon, project, session string) []string {
	t.Helper()
	lead, err := d.srv.sessionLead(context.Background(), project, session)
	if err != nil {
		t.Fatal(err)
	}
	th, err := d.srv.chat.Thread(lead)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range th.Items {
		if it.Kind == "notice" && it.Hidden {
			out = append(out, it.Text)
		}
	}
	return out
}

func anyContains(list []string, sub string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.Contains(s, sub) })
}

// notificationsOf is everything d keeps to tell its user, questions or not.
func notificationsOf(t *testing.T, d testDaemon) []api.Notification {
	t.Helper()
	rows, err := d.srv.store.Notifications(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var out []api.Notification
	for _, row := range rows {
		var n api.Notification
		if err := json.Unmarshal(row.Data, &n); err != nil {
			t.Fatal(err)
		}
		n.Seen = !row.SeenAt.IsZero()
		out = append(out, n)
	}
	return out
}

// twoSessions opens two sessions of project's chat, the second the most
// recently active, which hears what belongs to no session.
func twoSessions(t *testing.T, d testDaemon, project string) (first, second string) {
	t.Helper()
	ctx := context.Background()
	// Its leads start no AI tool: a notice that wakes one ends its turn at
	// once, rather than holding the next notices until it does.
	d.srv.chat.Launch = func(context.Context, state.Agent, func(string)) (*chat.Process, error) {
		return nil, errors.New("this test starts no AI tool")
	}
	// A session's lead runs on a Claude Code login of the machine's.
	if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveClaudeToken("default", "sk-ant-oat01-owner"); err != nil {
		t.Fatal(err)
	}
	s1, err := d.srv.store.AddLeadSession(ctx, project, "Reminders", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	s2, err := d.srv.store.AddLeadSession(ctx, project, "Something else", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return s1.ID, s2.ID
}

// An agent the paired AgentBox asked for on the owner's computer: its
// question reaches the session that made it, and the peer's app, which
// answers it within its access; its finish reaches both; the owner's own
// notifications stay the owner's.
func TestShareNoticesReachWhoAskedOnTheOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sp := pairShared(t)
	// The chat's leads run in the project's repository.
	p, err := sp.owner.srv.store.Project(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, p.Root, "init", "-q")
	gitIn(t, p.Root, "-c", "user.email=a@example.com", "-c", "user.name=A", "commit", "-q", "--allow-empty", "-m", "start")
	made, other := twoSessions(t, sp.owner, "shared")
	a := state.Agent{Project: "shared", Name: "agent-01", Instance: "ab-shared-agent-01", AI: "claude", Title: "Reminders",
		CreatedBySession: made, CreatedAt: time.Now()}
	if err := sp.owner.srv.store.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := sp.owner.srv.store.AddSharePeerAgent(ctx, sp.peerID, "shared", "agent-01"); err != nil {
		t.Fatal(err)
	}

	// What reaches Ana's app goes over her event stream, connected once she
	// has the project.
	waitFor(t, "Ana's event stream", func() bool {
		sh := sp.owner.srv.peering
		sh.mu.Lock()
		defer sh.mu.Unlock()
		for _, o := range sh.open[sp.peerID] {
			if o.events {
				return true
			}
		}
		return false
	})

	type answer struct {
		q   api.Question
		err error
	}
	ask := func(question string) (string, chan answer) {
		t.Helper()
		done := make(chan answer, 1)
		qctx, cancel := context.WithCancel(ctx)
		t.Cleanup(cancel)
		go func() {
			q, err := sp.owner.srv.askForTest(qctx, a, question, "")
			done <- answer{q, err}
		}()
		var id string
		waitFor(t, "the peer to be asked "+question, func() bool {
			for _, n := range notificationsOf(t, sp.peer) {
				if n.Kind == api.NotifyQuestion && n.Text == question {
					id = n.Question
					if n.Project != sp.local || n.Agent != "agent-01" || n.Session != made || n.Seen {
						t.Errorf("the peer's notification = %+v; want agent-01's question in %s, opening session %s", n, sp.local, made)
					}
					return true
				}
			}
			return false
		})
		return id, done
	}

	id, done := ask("Postgres or SQLite?")
	if got := sessionNotices(t, sp.owner, "shared", made); !anyContains(got, "Postgres or SQLite?") {
		t.Errorf("the session that made agent-01 was told %q; want its question", got)
	}
	if got := sessionNotices(t, sp.owner, "shared", other); len(got) != 0 {
		t.Errorf("the session most recently active was told %q; want nothing", got)
	}

	// Ana answers from her side, through her own AgentBox.
	if _, err := sp.peer.client.AnswerQuestion(ctx, sp.local, id, "SQLite."); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.q.Answer != "SQLite." {
			t.Errorf("agent-01 got %+v, %v; want Ana's answer", got.q, got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("agent-01 never got Ana's answer")
	}
	waitFor(t, "the lead to hear Ana answered", func() bool {
		return anyContains(sessionNotices(t, sp.owner, "shared", made), "Ana, of a paired AgentBox, answered agent-01's question")
	})
	waitFor(t, "Ana's notification to be seen once answered", func() bool {
		for _, n := range notificationsOf(t, sp.peer) {
			if n.Question == id {
				return n.Seen
			}
		}
		return false
	})

	// The owner's own notification is the owner's: the lead passing a
	// question on reaches no peer. agent-01's finish, sent after it, reaches
	// Ana, in the session that made it.
	id, _ = ask("Dark mode too?")
	q, _ := sp.owner.srv.store.Question(ctx, id)
	sp.owner.srv.notifyQuestion(ctx, q, a.Title)
	sp.owner.srv.tellLeadAbout(ctx, a, "agent-01 finished.", chat.NoticeOptions{}, peerEvent{Status: state.RemoteDone, Summary: "Added the page", PRURL: "https://github.com/acme/shared/pull/7"})
	var finished api.Notification
	waitFor(t, "Ana to hear agent-01 finished", func() bool {
		for _, n := range notificationsOf(t, sp.peer) {
			if n.Kind == api.NotifyFinished {
				finished = n
				return true
			}
		}
		return false
	})
	if finished.PRURL != "https://github.com/acme/shared/pull/7" || finished.Text != "Added the page" || finished.Session != made || finished.Project != sp.local {
		t.Errorf("Ana's finish notification = %+v", finished)
	}
	asked := 0
	for _, n := range notificationsOf(t, sp.peer) {
		if n.Kind == api.NotifyQuestion {
			asked++
		}
	}
	if asked != 2 {
		t.Errorf("Ana has %d question notifications; want the two agent-01 asked her, not the owner's own", asked)
	}
	if !anyContains(sessionNotices(t, sp.owner, "shared", made), "agent-01 finished.") {
		t.Error("the owner's session that made agent-01 didn't hear it finished")
	}
	if got := sessionNotices(t, sp.owner, "shared", other); len(got) != 0 {
		t.Errorf("the session most recently active was told %q; want nothing", got)
	}
	// Her inbox lists what she was asked, under the project as she knows it.
	inbox, err := sp.peer.client.Notifications(ctx)
	if err != nil || !slices.ContainsFunc(inbox, func(n api.Notification) bool { return n.Question == id && n.Project == sp.local }) {
		t.Errorf("Ana's inbox = %+v, %v; want agent-01's question", inbox, err)
	}

	// Past her access: once she may only look, she can't answer.
	view := state.ShareAccessView
	if _, err := sp.owner.client.UpdateSharePeer(ctx, sp.peerID, api.UpdateSharePeerRequest{Access: &view}); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.peer.client.AnswerQuestion(ctx, sp.local, id, "Yes."); err == nil || !strings.Contains(err.Error(), "Settings → Share") {
		t.Errorf("answering with view access: %v; want it refused", err)
	}
	if q, _ := sp.owner.srv.store.Question(ctx, id); q.Status != state.QuestionPending {
		t.Errorf("the question is %s after a refused answer", q.Status)
	}
}

// An agent the owner's lead made on the peer's computer: its notices reach
// the session that made it on the owner, not the one most recently active,
// and the peer's own app; the peer answers its question there, and the
// owner's lead hears it did.
func TestShareNoticesReachWhoAskedOnTheirComputer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := startShareMachines(t)
	made, other := twoSessions(t, m.alice, "hello-stack")
	if _, err := m.alice.srv.sessionLead(ctx, "hello-stack", made); err != nil {
		t.Fatal(err)
	}
	lead := api.NewClient(m.alice.srv.sessionSocketPath("hello-stack", made))
	job, err := lead.OnMachine("Bob").CreateProjectAgent(ctx, api.CreateAgentRequest{Title: "Reminders", Task: "add it", Branch: "reminders"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.bob.client.FollowJobLog(ctx, job.ID, io.Discard); err != nil {
		t.Fatal(err)
	}
	origin, err := m.bob.srv.store.PeerAgent(ctx, "hello-stack", "agent-01")
	if err != nil || origin.OriginSession != made {
		t.Fatalf("Bob's record of agent-01 = %+v, %v; want session %s", origin, err, made)
	}
	a, err := m.bob.srv.store.Agent(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan api.Question, 1)
	qctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go func() {
		q, _ := m.bob.srv.askForTest(qctx, a, "Which calendar?", "")
		done <- q
	}()
	var n api.Notification
	waitFor(t, "Bob's app to be asked", func() bool {
		for _, got := range notificationsOf(t, m.bob) {
			if got.Kind == api.NotifyQuestion {
				n = got
				return true
			}
		}
		return false
	})
	if n.Project != m.local || n.Agent != "agent-01" || n.Session != made || n.Question == "" || n.Text != "Which calendar?" {
		t.Errorf("Bob's notification = %+v; want agent-01's question in %s, opening session %s", n, m.local, made)
	}
	waitFor(t, "the session that made agent-01 to hear its question", func() bool {
		return anyContains(sessionNotices(t, m.alice, "hello-stack", made), "Which calendar?")
	})
	if got := sessionNotices(t, m.alice, "hello-stack", other); anyContains(got, "agent-01") {
		t.Errorf("the session most recently active was told %q", got)
	}

	// Bob answers in the project as he knows it: the question is on his
	// computer, not Alice's.
	if _, err := m.bob.client.AnswerQuestion(ctx, m.local, n.Question, "Google's."); err != nil {
		t.Fatal(err)
	}
	select {
	case q := <-done:
		if q.Answer != "Google's." {
			t.Errorf("agent-01 got %+v; want Bob's answer", q)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("agent-01 never got Bob's answer")
	}
	waitFor(t, "Alice's lead to hear Bob answered", func() bool {
		return anyContains(sessionNotices(t, m.alice, "hello-stack", made), "Bob answered agent-01's question")
	})

	// Its finish reaches the same session and Bob's app.
	m.bob.srv.tellLeadAbout(ctx, a, "agent-01 finished.", chat.NoticeOptions{}, peerEvent{Status: state.RemoteDone, Summary: "Opened #12", PRURL: "https://github.com/acme/hello-stack/pull/12"})
	waitFor(t, "Alice's session to hear agent-01 finished", func() bool {
		return anyContains(sessionNotices(t, m.alice, "hello-stack", made), "agent-01 finished.")
	})
	if slices.ContainsFunc(notificationsOf(t, m.bob), func(n api.Notification) bool {
		return n.Kind == api.NotifyFinished && n.PRURL == "https://github.com/acme/hello-stack/pull/12" && n.Session == made
	}) == false {
		t.Errorf("Bob's notifications = %+v; want agent-01's finish", notificationsOf(t, m.bob))
	}
	if got := sessionNotices(t, m.alice, "hello-stack", other); anyContains(got, "agent-01") {
		t.Errorf("the session most recently active was told %q", got)
	}

	// Once Alice lets Bob only look, he can't answer from his side either.
	view := state.ShareAccessView
	if _, err := m.alice.client.UpdateSharePeer(ctx, m.peer.ID, api.UpdateSharePeerRequest{Access: &view}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Bob to see he may only look", func() bool {
		c := m.bob.srv.remoteConn(m.remote.ID)
		return c != nil && m.bob.srv.remoteAccess(ctx, c, m.local) == state.ShareAccessView
	})
	go func() { _, _ = m.bob.srv.askForTest(qctx, a, "Weekly or daily?", "") }()
	var second string
	waitFor(t, "the second question", func() bool {
		qs, _ := m.bob.srv.store.Questions(ctx, "hello-stack", true)
		if len(qs) == 0 {
			return false
		}
		second = qs[0].ID
		return true
	})
	if _, err := m.bob.client.AnswerQuestion(ctx, m.local, second, "Daily."); err == nil || !strings.Contains(err.Error(), "only look") {
		t.Errorf("answering with view access: %v; want it refused", err)
	}
	if q, _ := m.bob.srv.store.Question(ctx, second); q.Status != state.QuestionPending {
		t.Errorf("the question is %s after a refused answer", q.Status)
	}
}
