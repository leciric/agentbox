package daemon

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A message written in a project's chat to one of its agents goes to that
// agent's chat, and the chat keeps a notice the lead reads at its next turn
// without one being started; another session's agent is refused until the
// user says to send it anyway.
func TestAMessageToAnAgentFromTheLeadsChat(t *testing.T) {
	t.Parallel()
	w := newWakeTest(t)
	ctx := context.Background()
	leadReadyToChat(t, w.testDaemon, "hello-stack")
	prod, err := w.client.NewLeadSession(ctx, "hello-stack", "Prod incident")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.srv.store.SetAgentSession(ctx, "hello-stack", "agent-02", prod.ID); err != nil {
		t.Fatal(err)
	}
	main := api.SessionRef("hello-stack", state.MigratedSession)

	// agent-01 is nobody's: it is told, and the chat keeps a notice of it.
	item, err := w.client.SendChatMessage(ctx, main, api.ChatMessageRequest{Text: "Carry on with the reminders page.", To: "agent-01"})
	if err != nil {
		t.Fatal(err)
	}
	if item.Kind != "notice" || item.To != "agent-01" || item.Text != "Carry on with the reminders page." || item.Hidden {
		t.Errorf("the answer = %+v; want the chat's notice of the message, shown, To agent-01", item)
	}
	waitFor(t, "the message to reach agent-01", func() bool { return w.tool.got("Carry on with the reminders page.") })
	lead, err := w.client.Chat(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range lead.Items {
		if it.Kind == "user" {
			t.Errorf("the lead's chat started a turn: %+v", it)
		}
	}
	if lead.Session.TurnStartedAt != nil || lead.Session.State == api.ChatRunning {
		t.Errorf("the lead's session = %+v; want no turn running", lead.Session)
	}

	// agent-02 is the Prod session's: refused, until sent anyway.
	_, err = w.client.SendChatMessage(ctx, main, api.ChatMessageRequest{Text: "Look at the settings page.", To: "agent-02"})
	if !api.HasReason(err, api.ErrorOtherSession) {
		t.Fatalf("a message to another session's agent = %v; want it refused with %s", err, api.ErrorOtherSession)
	}
	time.Sleep(50 * time.Millisecond)
	if w.tool.got("Look at the settings page.") {
		t.Error("a refused message reached agent-02")
	}
	if _, err := w.client.SendChatMessage(ctx, main, api.ChatMessageRequest{Text: "Look at the settings page.", To: "agent-02", AnySession: true}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the message to reach agent-02", func() bool { return w.tool.got("Look at the settings page.") })
	// From its own session it goes without asking.
	if _, err := w.client.SendChatMessage(ctx, prod.Ref, api.ChatMessageRequest{Text: "And the footer.", To: "agent-02"}); err != nil {
		t.Errorf("from its own session: %v", err)
	}

	// The lead reads both at its next turn, a line each.
	if _, err := w.client.SendChat(ctx, main, "How is it going?"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the lead's turn", func() bool { return w.tool.got("How is it going?") })
	for _, line := range []string{"User told agent-01: Carry on with the reminders page.", "User told agent-02: Look at the settings page."} {
		if !w.tool.got(line) {
			t.Errorf("the lead's prompt lacks %q", line)
		}
	}
	if w.tool.got("User told agent-02: And the footer.") {
		t.Error("the main session's lead read what was written in Prod's chat")
	}

	// Not in an agent's own chat, nor to the lead itself.
	if _, err := w.client.SendChatMessage(ctx, "hello-stack/agent-01", api.ChatMessageRequest{Text: "Hi", To: "agent-02"}); err == nil {
		t.Error("an agent's chat sent to another agent")
	}
	if _, err := w.client.SendChatMessage(ctx, main, api.ChatMessageRequest{Text: "Hi", To: state.LeadName}); err == nil {
		t.Error("a message went To the lead")
	}
}
