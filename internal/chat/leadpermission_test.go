package chat

import (
	"strings"
	"sync"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// cards are the agents' permission requests lead's chat shows.
func cards(th api.ChatThread) []api.ChatItem {
	var out []api.ChatItem
	for _, it := range th.Items {
		if it.AgentPermission != nil {
			out = append(out, it)
		}
	}
	return out
}

// An agent's permission request shows in its lead's chat as a card that
// follows it: answered from the lead (on the agent's own route, which is what
// the card's buttons call), or given up when the agent's turn is stopped.
// The lead's model is told about them before its next prompt, and the card
// never makes the lead itself wait.
func TestAnAgentsPermissionRequestShowsInItsLeadAndStaysInStep(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	var mu sync.Mutex
	var leadPrompts []string
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		if text == "Run the tests" {
			f.update(s, `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"Run the tests","kind":"execute","status":"pending","rawInput":{"command":["bash","-lc","npm test"]}}`)
			if f.ask(s, `{"toolCallId":"t1"}`) == "allow-once" {
				f.update(s, `{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed","rawOutput":"ok"}`)
			}
			return acp.PromptResponse{StopReason: "end_turn"}
		}
		mu.Lock()
		leadPrompts = append(leadPrompts, text)
		mu.Unlock()
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	changes := make(chan api.ChatAgentPermission, 64)
	m.PermissionChanged = func(a state.Agent, p api.ChatAgentPermission) {
		if a.Ref() != testAgent.Ref() {
			t.Errorf("a change to %s's request was reported", a.Ref())
		}
		changes <- p
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for p := range changes {
			if err := m.ShowAgentPermission(testLead, p); err != nil {
				t.Error(err)
			}
		}
	}()
	t.Cleanup(func() { close(changes); <-done })

	// Asked: the card shows the agent, the request and the command.
	if _, err := m.Send(testAgent, "Run the tests"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the request", func(th api.ChatThread) bool { return th.Session.State == api.ChatWaiting })
	asked := find(th, "permission", 0)
	lead := waitThread(t, m, testLead, "the card", func(th api.ChatThread) bool { return len(cards(th)) == 1 })
	card := cards(lead)[0]
	p := card.AgentPermission
	if card.Kind != "notice" || card.Hidden || p.Ref != testAgent.Ref() || p.Agent != testAgent.Name || p.Item != asked.ID ||
		p.Permission.Title != "Run the tests" || p.Permission.Outcome != "" || len(p.Permission.Options) != 2 {
		t.Errorf("the card is %+v, %+v", card, p)
	}
	if p.Tool == nil || p.Tool.Command != "npm test" || p.Tool.Kind != "execute" {
		t.Errorf("the card's tool is %+v, want the command", p.Tool)
	}
	if card.Text != "agent-01 asks permission to Run the tests: npm test" {
		t.Errorf("the card says %q", card.Text)
	}
	if lead.Session.State == api.ChatWaiting {
		t.Error("the lead waits on its agent's request as if it were its own")
	}

	// Answered from the lead: the agent's request and the card both say so.
	if _, err := m.Answer(testAgent, p.Item, "allow-once"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	lead = waitThread(t, m, testLead, "the card to be answered", func(th api.ChatThread) bool {
		return cards(th)[0].AgentPermission.Permission.Outcome == "allow-once"
	})
	if n := len(cards(lead)); n != 1 {
		t.Errorf("%d cards, want the one, answered", n)
	}
	if th, _ := m.Thread(testAgent); find(th, "permission", 0).Permission.Outcome != "allow-once" {
		t.Errorf("the agent's request is %+v", find(th, "permission", 0).Permission)
	}

	// A request the agent's stopped turn gives up settles as cancelled.
	if _, err := m.Send(testAgent, "Run the tests"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testLead, "the second card", func(th api.ChatThread) bool { return len(cards(th)) == 2 })
	if _, err := m.Cancel(testAgent); err != nil {
		t.Fatal(err)
	}
	lead = waitThread(t, m, testLead, "the second card to settle", func(th api.ChatThread) bool {
		return cards(th)[1].AgentPermission.Permission.Outcome == "cancelled"
	})
	if _, err := m.Answer(testAgent, cards(lead)[1].AgentPermission.Item, "allow-once"); err == nil {
		t.Error("a request given up was answered from its card")
	}

	// The lead's next prompt is told both, as they stand; the one after isn't.
	for i, text := range []string{"What did agent-01 want?", "And now?"} {
		if _, err := m.Send(testLead, text); err != nil {
			t.Fatal(err)
		}
		waitThread(t, m, testLead, "the lead's turn", turnsEnded(i+1))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(leadPrompts) != 2 {
		t.Fatalf("the lead was prompted %d times, want 2", len(leadPrompts))
	}
	first := leadPrompts[0]
	for _, want := range []string{
		"Only the user answers them",
		"- agent-01 asks permission to Run the tests: npm test — the user allowed it (Yes).",
		"— no longer waiting: the agent's turn ended or it was cancelled.",
		"What did agent-01 want?",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("the lead's prompt lacks %q:\n%s", want, first)
		}
	}
	if leadPrompts[1] != "And now?" {
		t.Errorf("the second prompt is %q: the requests were told again", leadPrompts[1])
	}
}
