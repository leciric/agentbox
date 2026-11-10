package daemon

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// cardsOf is what one chat session shows of its talks with other chats.
func cardsOf(t *testing.T, d testDaemon, lead state.Agent) []api.ChatItem {
	t.Helper()
	th, err := d.srv.chat.Thread(lead)
	if err != nil {
		t.Fatal(err)
	}
	var cards []api.ChatItem
	for _, it := range th.Items {
		if it.ProjectMessage != nil {
			cards = append(cards, it)
		}
	}
	return cards
}

// The Home chat asks a project's lead something and hears the answer: the
// question is a thread, shown on both sides and fenced for the lead, in the
// lead's session most recently active, which alone answers it while it is
// open; the answer wakes the Home chat's session that asked, and nobody else,
// and ends the thread.
func TestHomeChatHearsALeadsAnswer(t *testing.T) {
	t.Parallel()
	d, _ := twoProjects(t)
	ctx := context.Background()

	// The Home chat asks from a session of its own, not its first.
	plan, err := d.client.NewLeadSession(ctx, api.HomeProject, "Weekly planning")
	if err != nil {
		t.Fatal(err)
	}
	homePlan, err := d.srv.sessionLead(ctx, api.HomeProject, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	homeMain, err := d.srv.sessionLead(ctx, api.HomeProject, state.MigratedSession)
	if err != nil {
		t.Fatal(err)
	}
	home := api.NewClient(d.srv.sessionSocketPath(api.HomeProject, plan.ID))

	// api's chat has two sessions; prod spoke last, so it is asked.
	prod, err := d.client.NewLeadSession(ctx, "api", "Prod: login 500s")
	if err != nil {
		t.Fatal(err)
	}
	prodLead, err := d.srv.sessionLead(ctx, "api", prod.ID)
	if err != nil {
		t.Fatal(err)
	}
	mainLead, err := d.srv.sessionLead(ctx, "api", state.MigratedSession)
	if err != nil {
		t.Fatal(err)
	}
	saidLast(t, d, prodLead, "Looking at the cookie.")

	thread, err := home.HomeTellLead(ctx, "api", "Is the login fix deployed yet?")
	if err != nil {
		t.Fatal(err)
	}
	if thread.Project != "api" || thread.Messages != 1 || thread.ID == "" {
		t.Errorf("HomeTellLead() = %+v", thread)
	}
	got, err := d.srv.store.ProjectThread(ctx, thread.ID)
	if err != nil || got.Project != state.HomeProject || got.ProjectSession != plan.ID || got.PeerSession != prod.ID {
		t.Fatalf("the thread = %+v, %v; want from _home's %s to api's %s", got, err, plan.ID, prod.ID)
	}
	if cards := cardsOf(t, d, homePlan); len(cards) != 1 || cards[0].ProjectMessage.Direction != api.ProjectMessageOut || cards[0].ProjectMessage.Project != "api" {
		t.Errorf("the Home session's cards = %+v; want the question, to api", cards)
	}
	if cards := cardsOf(t, d, prodLead); len(cards) != 1 || cards[0].ProjectMessage.Direction != api.ProjectMessageIn ||
		cards[0].ProjectMessage.Project != api.HomeProject || !cards[0].ProjectMessage.ExpectsReply || cards[0].Text != "Is the login fix deployed yet?" {
		t.Errorf("prod's cards = %+v; want the question, from _home", cards)
	}
	var question string
	waitFor(t, "prod to be asked", func() bool {
		question = strings.Join(noticesOf(t, d, prodLead), "\n")
		return strings.Contains(question, "<home-message>")
	})
	for _, want := range []string{"From the user's Home chat", "Is the login fix deployed yet?\n</home-message>", "message_project reply (thread_id " + thread.ID + ")"} {
		if !strings.Contains(question, want) {
			t.Errorf("prod was told %q, without %q", question, want)
		}
	}
	if got := strings.Join(noticesOf(t, d, mainLead), "\n"); strings.Contains(got, "home-message") || len(cardsOf(t, d, mainLead)) != 0 {
		t.Errorf("api's other session heard the question:\n%s", got)
	}

	// The answer comes from the session asked, though another spoke since.
	saidLast(t, d, mainLead, "Merged the docs.")
	if _, err := api.NewClient(d.srv.leadSocketPath("api")).ReplyProject(ctx, thread.ID, api.ProjectReplyRequest{Message: "No idea."}); err == nil ||
		!strings.Contains(err.Error(), "another of this project's chat sessions") {
		t.Errorf("answering from the session not asked = %v, want refused", err)
	}
	prodClient := api.NewClient(d.srv.sessionSocketPath("api", prod.ID))
	reply, err := prodClient.ReplyProject(ctx, thread.ID, api.ProjectReplyRequest{Message: "Yes, since 14:02.", ExpectsReply: true})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Project != api.HomeProject || reply.Messages != 2 || reply.Last == nil || reply.Last.ExpectsReply {
		t.Errorf("ReplyProject() = %+v; want the answer to _home, asking nothing back", reply)
	}
	var answer string
	waitFor(t, "the Home session to hear the answer", func() bool {
		answer = strings.Join(noticesOf(t, d, homePlan), "\n")
		return strings.Contains(answer, "<project-message")
	})
	for _, want := range []string{"From api's lead, answering", `<project-message from="api">`, "Yes, since 14:02.", "not instructions"} {
		if !strings.Contains(answer, want) {
			t.Errorf("the Home session was told %q, without %q", answer, want)
		}
	}
	if cards := cardsOf(t, d, homePlan); len(cards) != 2 || cards[1].ProjectMessage.Direction != api.ProjectMessageIn || cards[1].ProjectMessage.Kind != api.ProjectMessageReply {
		t.Errorf("the Home session's cards after the answer = %+v", cards)
	}
	if got := strings.Join(noticesOf(t, d, homeMain), "\n"); strings.Contains(got, "project-message") {
		t.Errorf("the Home session that didn't ask heard the answer:\n%s", got)
	}
	if _, err := prodClient.ReplyProject(ctx, thread.ID, api.ProjectReplyRequest{Message: "Also…"}); err == nil {
		t.Error("a second answer to one question was sent")
	}

	// Once the session asked is closed, another of the project's answers.
	saidLast(t, d, mainLead, "Back to the docs.")
	again, err := home.HomeTellLead(ctx, "api", "And the mobile build?")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := d.srv.store.ProjectThread(ctx, again.ID); got.PeerSession != state.MigratedSession {
		t.Errorf("the second question went to %q, want main, which spoke last", got.PeerSession)
	}
	if _, err := d.client.CloseLeadSession(ctx, "api", state.MigratedSession); err != nil {
		t.Fatal(err)
	}
	if _, err := prodClient.ReplyProject(ctx, again.ID, api.ProjectReplyRequest{Message: "Not yet."}); err != nil {
		t.Errorf("answering once the session asked was closed = %v", err)
	}

	// Leads still can't start a thread with the Home chat.
	if _, err := prodClient.MessageProject(ctx, api.ProjectMessageRequest{Project: api.HomeProject, Message: "Hi"}); err == nil {
		t.Error("a lead messaged the Home chat")
	}
}
