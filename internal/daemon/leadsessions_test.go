package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// noticesOf is every notice a session's lead was told, in order.
func noticesOf(t *testing.T, d testDaemon, lead state.Agent) []string {
	t.Helper()
	th, err := d.srv.chat.Thread(lead)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range th.Items {
		if it.Kind == "notice" && it.Text != "" {
			out = append(out, it.Text)
		}
	}
	return out
}

// Two sessions of one project's chat, each with an agent it made: each hears
// only its own agent's finish, an agent nobody owns goes to the session most
// recently active, and deleting a session leaves its agents to nobody.
func TestEachSessionHearsOnlyItsOwnAgents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	// A new project starts with one session, the one its chat always had.
	sessions, err := d.client.LeadSessions(ctx, "hello-stack")
	if err != nil || len(sessions) != 1 || sessions[0].ID != state.MigratedSession || sessions[0].Ref != "hello-stack/lead" {
		t.Fatalf("LeadSessions() = %+v, %v; want the one session a new project has", sessions, err)
	}
	main := leadReadyToChat(t, d, "hello-stack")
	prod, err := d.client.NewLeadSession(ctx, "hello-stack", "Prod: login 500s")
	if err != nil {
		t.Fatal(err)
	}
	if prod.ID != "prod-login-500s" || prod.Ref != "hello-stack/lead.prod-login-500s" {
		t.Fatalf("NewLeadSession() = %+v", prod)
	}
	prodLead, err := d.srv.sessionLead(ctx, "hello-stack", prod.ID)
	if err != nil {
		t.Fatal(err)
	}
	if prodLead.Name != "lead.prod-login-500s" || prodLead.Worktree != main.Worktree {
		t.Errorf("the session's lead = %+v, want its own name on the chat's worktree", prodLead)
	}

	// Each session has a socket of its own.
	for _, session := range []string{state.MigratedSession, prod.ID} {
		if _, err := api.NewClient(d.srv.sessionSocketPath("hello-stack", session)).ProjectFleet(ctx); err != nil {
			t.Fatalf("the %s session's socket: %v", session, err)
		}
	}
	a1 := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	addAgent(t, d, repo, "hello-stack", "agent-02", "Fix the login 500s")
	a3 := addAgent(t, d, repo, "hello-stack", "agent-03", "The user's own")
	if err := d.srv.store.SetAgentSession(ctx, "hello-stack", "agent-02", prod.ID); err != nil {
		t.Fatal(err)
	}
	a2, err := d.srv.store.Agent(ctx, "hello-stack", "agent-02")
	if err != nil || a2.Session() != prod.ID {
		t.Fatalf("agent-02 = %+v, %v; want the prod session's", a2, err)
	}
	if a1.Session() != "" || a3.Session() != "" {
		t.Fatalf("an agent nobody handed to a session has one: %q, %q", a1.Session(), a3.Session())
	}
	if err := d.srv.store.SetAgentSession(ctx, "hello-stack", "agent-01", state.MigratedSession); err != nil {
		t.Fatal(err)
	}
	a1.CreatedBySession = state.MigratedSession

	d.srv.stallTell = func(context.Context, state.Agent, string) {}
	d.srv.agentFinished(a1, chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "completed", StopReason: "end_turn"}})
	d.srv.agentFinished(a2, chat.TurnEnd{ChatTurnResult: api.ChatTurnResult{State: "completed", StopReason: "end_turn"}})
	waitFor(t, "both sessions to be told", func() bool {
		return len(noticesOf(t, d, main)) > 0 && len(noticesOf(t, d, prodLead)) > 0
	})
	mainHeard, prodHeard := strings.Join(noticesOf(t, d, main), "\n"), strings.Join(noticesOf(t, d, prodLead), "\n")
	if !strings.Contains(mainHeard, "agent-01") || strings.Contains(mainHeard, "agent-02") {
		t.Errorf("the first session heard:\n%s\nwant agent-01's finish only", mainHeard)
	}
	if !strings.Contains(prodHeard, "agent-02") || strings.Contains(prodHeard, "agent-01") {
		t.Errorf("the prod session heard:\n%s\nwant agent-02's finish only", prodHeard)
	}

	// Nobody's agent goes to the session most recently active.
	saidLast(t, d, prodLead, "Looking at the cookie.")
	if ls, err := d.srv.store.LastActiveLeadSession(ctx, "hello-stack"); err != nil || ls.ID != prod.ID {
		t.Fatalf("LastActiveLeadSession() = %+v, %v; want prod, which spoke last", ls, err)
	}
	d.srv.tellOwner(ctx, a3, "agent-03 needs a look", false)
	if got := noticesOf(t, d, prodLead); !strings.Contains(strings.Join(got, "\n"), "agent-03") {
		t.Errorf("nobody's agent's notice went elsewhere: prod heard %q", got)
	}

	// Deleting prod leaves agent-02 to nobody, and takes its conversation.
	if err := d.client.DeleteLeadSession(ctx, "hello-stack", prod.ID); err != nil {
		t.Fatal(err)
	}
	if a2, err = d.srv.store.Agent(ctx, "hello-stack", "agent-02"); err != nil || a2.Session() != "" {
		t.Errorf("after its session was deleted agent-02 = %+v, %v; want it nobody's", a2, err)
	}
	if _, err := d.srv.store.Agent(ctx, "hello-stack", prodLead.Name); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the deleted session's lead is still there: %v", err)
	}
	if rows, err := d.srv.store.ChatItems(ctx, "hello-stack", prodLead.Name); err != nil || len(rows) != 0 {
		t.Errorf("the deleted session's conversation = %d rows, %v", len(rows), err)
	}
	if _, err := d.client.Chat(ctx, api.SessionRef("hello-stack", prod.ID)); err == nil {
		t.Error("a deleted session's chat can still be read")
	}
}

// A project whose sessions were all deleted opens one for what starts a turn,
// titled after it, and drops what would only wait to be read.
func TestAProjectWithNoSessionOpensOneForAnEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	leadReadyToChat(t, d, "hello-stack")
	if err := d.client.DeleteLeadSession(ctx, "hello-stack", state.MigratedSession); err != nil {
		t.Fatal(err)
	}
	if got, err := d.client.LeadSessions(ctx, "hello-stack"); err != nil || len(got) != 0 {
		t.Fatalf("LeadSessions() = %+v, %v; want none", got, err)
	}
	// The chat reads as empty, with nothing made.
	if th, err := d.client.Chat(ctx, "hello-stack"); err != nil || len(th.Items) != 0 {
		t.Fatalf("Chat() = %+v, %v", th, err)
	}
	d.srv.tellLead(ctx, "hello-stack", "Nothing to act on", false)
	if got, _ := d.client.LeadSessions(ctx, "hello-stack"); len(got) != 0 {
		t.Fatalf("a notice that only waits opened %+v", got)
	}
	lead, err := d.srv.eventLead(ctx, "hello-stack", eventTopic("Nightly: check the dependencies\nand the rest"), true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.client.LeadSessions(ctx, "hello-stack")
	if err != nil || len(got) != 1 || got[0].Topic != "Nightly: check the dependencies" || lead.Ref() != got[0].Ref {
		t.Fatalf("LeadSessions() = %+v, %v (lead %s); want one opened for the event", got, err, lead.Ref())
	}
}

// A session's lead telling another session's agent waits for the user's yes;
// its own, and nobody's, it tells freely.
func TestTellingAnotherSessionsAgentAsks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	prod, err := d.client.NewLeadSession(ctx, "hello-stack", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	mine := addAgent(t, d, repo, "hello-stack", "agent-01", "Mine")
	theirs := addAgent(t, d, repo, "hello-stack", "agent-02", "Theirs")
	if err := d.srv.store.SetAgentSession(ctx, "hello-stack", "agent-01", prod.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.srv.store.SetAgentSession(ctx, "hello-stack", "agent-02", state.MigratedSession); err != nil {
		t.Fatal(err)
	}
	mine.CreatedBySession, theirs.CreatedBySession = prod.ID, state.MigratedSession
	var asked []string
	answer := false
	d.srv.approve = func(ctx context.Context, project string, req api.ChatPermission) (bool, error) {
		asked = append(asked, leadSessionOf(ctx)+": "+req.Title)
		return answer, nil
	}
	in := withLeadSession(ctx, prod.ID)
	if err := d.srv.crossSession(in, prod.ID, mine, "Tell"); err != nil || len(asked) != 0 {
		t.Errorf("its own agent: %v, asked %q", err, asked)
	}
	if err := d.srv.crossSession(in, prod.ID, state.Agent{Project: "hello-stack", Name: "agent-09"}, "Tell"); err != nil || len(asked) != 0 {
		t.Errorf("nobody's agent: %v, asked %q", err, asked)
	}
	err = d.srv.crossSession(in, prod.ID, theirs, "Tell")
	if !errors.Is(err, errRefused) || len(asked) != 1 || !strings.HasPrefix(asked[0], prod.ID+": Tell agent-02") || !strings.Contains(asked[0], `"Chat"`) {
		t.Errorf("another session's agent, refused: %v, asked %q", err, asked)
	}
	answer = true
	if err := d.srv.crossSession(in, prod.ID, theirs, "Retire"); err != nil || len(asked) != 2 {
		t.Errorf("another session's agent, approved: %v, asked %q", err, asked)
	}
}

// The session routes are the project chat's, under /sessions/{session}.
func TestSessionChatRoutes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	prod, err := d.client.NewLeadSession(ctx, "hello-stack", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	saidLast(t, d, state.Agent{Project: "hello-stack", Name: state.LeadSessionName(prod.ID)}, "In prod.")
	th, err := d.client.Chat(ctx, api.SessionRef("hello-stack", prod.ID))
	if err != nil || len(th.Items) == 0 {
		t.Fatalf("the prod session's chat = %+v, %v", th, err)
	}
	if main, err := d.client.Chat(ctx, api.SessionRef("hello-stack", state.MigratedSession)); err != nil || len(main.Items) != 0 {
		t.Errorf("the first session's chat = %+v, %v; want its own, empty", main, err)
	}
	sessions, err := d.client.LeadSessions(ctx, "hello-stack")
	if err != nil || len(sessions) != 2 || sessions[1].Ref != agent.SessionRef("hello-stack", prod.ID) || sessions[1].Last == nil {
		t.Errorf("LeadSessions() = %+v, %v; want prod second, with what it said", sessions, err)
	}
	if renamed, err := d.client.RenameLeadSession(ctx, "hello-stack", prod.ID, "Prod: fixed"); err != nil || renamed.Topic != "Prod: fixed" || renamed.ID != prod.ID {
		t.Errorf("RenameLeadSession() = %+v, %v", renamed, err)
	}
}

// A new session answers before every other session's brief is rewritten to
// name it, which a project with many sessions and a real memory took most of
// a second over; the rewrite still happens, aside.
func TestANewSessionIsNamedInTheOthersBriefs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	creds := credentials.Store{Dir: d.paths.Credentials()}
	if err := creds.SaveClaudeToken("default", "sk-ant-oat01-default"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.srv.manager(nil).EnsureLead(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	for _, topic := range []string{"Prod", "Docs", "Billing"} {
		if _, err := d.client.NewLeadSession(ctx, "hello-stack", topic); err != nil {
			t.Fatal(err)
		}
	}
	d.srv.briefRewrites.Wait()
	brief, err := os.ReadFile(filepath.Join(d.paths.LeadSessionHome("hello-stack", state.MigratedSession), ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, topic := range []string{"Prod", "Docs", "Billing"} {
		if !strings.Contains(string(brief), topic) {
			t.Errorf("the first session's brief doesn't name %q, made after it:\n%s", topic, brief)
		}
	}
}

// A session opened with no topic is named after its lead's first turn, by
// the cheap model reading the conversation's opening, and only while the user
// hasn't named it.
func TestAnUntitledSessionIsNamedAfterItsFirstTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	var asked []string
	answer, answerErr := `Title: "Prod: login 500s"`, error(nil)
	d.srv.askAside = func(_ context.Context, a state.Agent, model, ask string) (string, string, error) {
		if model != state.CheapModelFor("claude") {
			t.Errorf("named on %q, want the cheap model", model)
		}
		asked = append(asked, ask)
		return answer, model, answerErr
	}
	open := func() (api.LeadSession, state.Agent) {
		t.Helper()
		ls, err := d.client.OpenLeadSession(ctx, "hello-stack", api.NewLeadSessionRequest{})
		if err != nil || !ls.Untitled || ls.Topic != state.UntitledTopic {
			t.Fatalf("OpenLeadSession() = %+v, %v; want an untitled one", ls, err)
		}
		return ls, state.Agent{Project: "hello-stack", Name: state.LeadSessionName(ls.ID), Role: state.RoleLead, AI: "claude"}
	}
	topic := func(id string) api.LeadSession {
		t.Helper()
		sessions, err := d.client.LeadSessions(ctx, "hello-stack")
		if err != nil {
			t.Fatal(err)
		}
		for _, ls := range sessions {
			if ls.ID == id {
				return ls
			}
		}
		t.Fatalf("no session %s in %+v", id, sessions)
		return api.LeadSession{}
	}

	ls, lead := open()
	// A turn that ends before the user said anything (an agent's notice) has
	// nothing to name it after.
	d.srv.leadIdle(lead)
	if got := topic(ls.ID); !got.Untitled || len(asked) != 0 {
		t.Fatalf("named with nothing said: %+v, asked %q", got, asked)
	}
	saidLast(t, d, lead, "The 500s come from the session cookie.")
	d.srv.leadIdle(lead)
	if got := topic(ls.ID); got.Topic != "Prod: login 500s" || got.Untitled {
		t.Errorf("after the first turn = %+v; want it named", got)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "Developer: Do the thing.") || !strings.Contains(asked[0], "AI: The 500s come from the session cookie.") {
		t.Errorf("the cheap model was asked %q; want the conversation's opening", asked)
	}
	// Named once: later turns leave it.
	answer = "Something else"
	d.srv.leadIdle(lead)
	if got := topic(ls.ID); got.Topic != "Prod: login 500s" || len(asked) != 1 {
		t.Errorf("a later turn renamed it: %+v, asked %d times", got, len(asked))
	}

	// One the user renamed before its first turn ended keeps their name.
	mine, mineLead := open()
	if _, err := d.client.RenameLeadSession(ctx, "hello-stack", mine.ID, "Mine"); err != nil {
		t.Fatal(err)
	}
	saidLast(t, d, mineLead, "Done.")
	d.srv.leadIdle(mineLead)
	if got := topic(mine.ID); got.Topic != "Mine" || got.Untitled {
		t.Errorf("the user's session = %+v; want their name", got)
	}

	// Without an answer, the user's first line names it.
	answerErr = errors.New("no cheap model today")
	plain, plainLead := open()
	saidLast(t, d, plainLead, "Done.")
	d.srv.leadIdle(plainLead)
	if got := topic(plain.ID); got.Topic != "Do the thing." || got.Untitled {
		t.Errorf("named without the cheap model = %+v; want the first line", got)
	}
}

// A new session starts on the AI tool, model, permission mode and effort of
// the session it was opened from, kept as its own chat settings so they
// survive a restart; planning stays with the session that was planning.
func TestANewSessionStartsOnTheSettingsItWasOpenedFrom(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	chosen := state.Chat{
		SessionID: "the-main-session",
		Options:   map[string]string{"mode": "bypassPermissions", "effort": "high", "model": "opus", api.ChatOptionPlan: "true", "codex/plan": "true", "codex/model": "gpt-5"},
		Account:   "work",
	}
	if err := d.srv.store.SaveChat(ctx, "hello-stack", state.LeadName, chosen); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"mode": "bypassPermissions", "effort": "high", "model": "opus", "codex/model": "gpt-5"}
	check := func(ls api.LeadSession) {
		t.Helper()
		got, err := d.srv.store.Chat(ctx, "hello-stack", state.LeadSessionName(ls.ID))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Options) != len(want) || got.Tool != "claude" || got.Account != "work" || got.SessionID != "" {
			t.Errorf("%s's settings = %+v; want %v on account work, and no session of its own yet", ls.ID, got, want)
		}
		for k, v := range want {
			if got.Options[k] != v {
				t.Errorf("%s's %s = %q, want %q", ls.ID, k, got.Options[k], v)
			}
		}
	}
	from, err := d.client.OpenLeadSession(ctx, "hello-stack", api.NewLeadSessionRequest{From: state.MigratedSession})
	if err != nil {
		t.Fatal(err)
	}
	check(from)
	// Naming no session, it is the one most recently active: main, whose
	// conversation is the newest.
	saidLast(t, d, state.Agent{Project: "hello-stack", Name: state.LeadName}, "Hi.")
	last, err := d.client.OpenLeadSession(ctx, "hello-stack", api.NewLeadSessionRequest{Topic: "Prod"})
	if err != nil || last.Untitled || last.Topic != "Prod" {
		t.Fatalf("OpenLeadSession(Prod) = %+v, %v", last, err)
	}
	check(last)
}

// Closing a session (the tab's close) keeps it in the chat's history: its
// conversation stays, a new session never takes its id or what is kept under
// it, its agents' notices go to the open session most recently active while
// it is closed, a turn in it is refused, and reopening it brings all of that
// back. A session continued from it is a new one, told where it left off.
func TestAClosedSessionGoesToTheHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	main := leadReadyToChat(t, d, "hello-stack")
	first, err := d.client.OpenLeadSession(ctx, "hello-stack", api.NewLeadSessionRequest{})
	if err != nil || first.ID != "session" {
		t.Fatalf("a new session = %+v, %v", first, err)
	}
	firstLead := state.Agent{Project: "hello-stack", Name: state.LeadSessionName(first.ID)}
	saidLast(t, d, firstLead, "The cookie is the culprit.")
	addAgent(t, d, repo, "hello-stack", "agent-01", "Fix the cookie")
	if err := d.srv.store.SetAgentSession(ctx, "hello-stack", "agent-01", first.ID); err != nil {
		t.Fatal(err)
	}

	closed, err := d.client.CloseLeadSession(ctx, "hello-stack", first.ID)
	if err != nil || closed.ClosedAt == nil {
		t.Fatalf("CloseLeadSession() = %+v, %v", closed, err)
	}
	open, err := d.client.LeadSessions(ctx, "hello-stack")
	if err != nil || len(open) != 1 || open[0].ID != state.MigratedSession {
		t.Errorf("open sessions = %+v, %v; want main alone", open, err)
	}
	history, err := d.client.ClosedLeadSessions(ctx, "hello-stack")
	if err != nil || len(history) != 1 || history[0].ID != first.ID || history[0].Last == nil || history[0].Agents != 1 {
		t.Fatalf("the history = %+v, %v; want the closed session, with what it said and its agent", history, err)
	}
	if th, err := d.client.Chat(ctx, first.Ref); err != nil || len(th.Items) == 0 {
		t.Errorf("the closed session's chat = %d items, %v; want it readable", len(th.Items), err)
	}
	if _, err := d.client.SendChat(ctx, first.Ref, "Still there?"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("a message to a closed session: %v; want it refused", err)
	}
	if err := d.client.HandOverAgent(ctx, "hello-stack/agent-01", first.ID); err == nil {
		t.Error("an agent was handed to a closed session")
	}

	// Ctrl+T: an empty session under an id of its own.
	fresh, err := d.client.OpenLeadSession(ctx, "hello-stack", api.NewLeadSessionRequest{})
	if err != nil || fresh.ID == first.ID || fresh.Ref == first.Ref {
		t.Fatalf("a new session after closing = %+v, %v; want an id of its own", fresh, err)
	}
	if th, err := d.client.Chat(ctx, fresh.Ref); err != nil || len(th.Items) != 0 {
		t.Errorf("the new session's chat = %d items, %v; want it empty", len(th.Items), err)
	}

	// agent-01's notices go to the open session most recently active.
	saidLast(t, d, main, "Here.")
	a1, err := d.srv.store.Agent(ctx, "hello-stack", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	d.srv.tellOwner(ctx, a1, "agent-01 has a question", false)
	if got := strings.Join(noticesOf(t, d, main), "\n"); !strings.Contains(got, "agent-01 has a question") {
		t.Errorf("main heard %q; want the closed session's agent's notice", got)
	}

	// Continued from: a new session on its topic, told where it left off.
	cont, err := d.client.OpenLeadSession(ctx, "hello-stack", api.NewLeadSessionRequest{ContinueFrom: first.ID})
	if err != nil || cont.ID == first.ID || cont.ID == fresh.ID {
		t.Fatalf("a session continued from the closed one = %+v, %v", cont, err)
	}
	// Shown before its lead is made, which needs a login: the app reads it
	// at once.
	if _, err := d.srv.store.Agent(ctx, "hello-stack", state.LeadSessionName(cont.ID)); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("continuing made the new session's lead: %v", err)
	}
	th, err := d.client.Chat(ctx, cont.Ref)
	if err != nil {
		t.Fatal(err)
	}
	var told string
	for _, it := range th.Items {
		if it.Kind == "notice" && !it.Hidden {
			told += it.Text
		}
	}
	if !strings.Contains(told, "The cookie is the culprit.") || !strings.Contains(told, first.ID) {
		t.Errorf("the continued session shows %q; want where %s left off", told, first.ID)
	}
	if a1, err = d.srv.store.Agent(ctx, "hello-stack", "agent-01"); err != nil || a1.Session() != first.ID {
		t.Errorf("agent-01 = %+v, %v; want it still the closed session's", a1, err)
	}

	// Reopened, it is open again with its conversation, and hears its agent.
	reopened, err := d.client.ReopenLeadSession(ctx, "hello-stack", first.ID)
	if err != nil || reopened.ClosedAt != nil {
		t.Fatalf("ReopenLeadSession() = %+v, %v", reopened, err)
	}
	if history, err := d.client.ClosedLeadSessions(ctx, "hello-stack"); err != nil || len(history) != 0 {
		t.Errorf("the history after reopening = %+v, %v; want it empty", history, err)
	}
	firstLead, err = d.srv.sessionLead(ctx, "hello-stack", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.srv.tellOwner(ctx, a1, "agent-01 is done", false)
	if got := strings.Join(noticesOf(t, d, firstLead), "\n"); !strings.Contains(got, "agent-01 is done") {
		t.Errorf("the reopened session heard %q; want its agent's notice", got)
	}
}
