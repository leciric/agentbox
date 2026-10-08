package chat

import (
	"context"
	"errors"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

// approvalTool's turn asks the user through Approve, the way the lead's MCP
// tools do through the daemon while the tool call runs, and says what it was
// told.
func approvalTool(m **Manager, ctx context.Context, got chan<- error, approved chan<- bool) *fakeTool {
	return newFakeTool(func(f *fakeTool, s, _ string) acp.PromptResponse {
		ok, err := (*m).Approve(ctx, testAgent, api.ChatPermission{
			Title: "Edit the skill review-pr", Approval: "skill",
			Diffs: []api.ChatDiff{{Path: "review-pr/SKILL.md", OldText: "old\n", NewText: "new\n"}},
		})
		got <- err
		approved <- ok
		return acp.PromptResponse{StopReason: "end_turn"}
	})
}

func TestApproveWaitsForTheUser(t *testing.T) {
	t.Parallel()
	for _, option := range []string{api.ApproveOption, api.RefuseOption} {
		t.Run(option, func(t *testing.T) {
			t.Parallel()
			var m *Manager
			errs, approved := make(chan error, 1), make(chan bool, 1)
			m, _ = newManager(t, openStore(t), approvalTool(&m, context.Background(), errs, approved))
			if _, err := m.Send(testAgent, "change it"); err != nil {
				t.Fatal(err)
			}
			th := waitThread(t, m, testAgent, "the card", func(th api.ChatThread) bool { return th.Session.State == api.ChatWaiting })
			card := find(th, "permission", 0)
			if p := card.Permission; p.Approval != "skill" || len(p.Diffs) != 1 || len(p.Options) != 2 || p.Outcome != "" {
				t.Fatalf("the card is %+v", p)
			}
			select {
			case <-approved:
				t.Fatal("it went ahead before the user answered")
			default:
			}
			if _, err := m.Answer(testAgent, card.ID, option); err != nil {
				t.Fatal(err)
			}
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
			if ok := <-approved; ok != (option == api.ApproveOption) {
				t.Errorf("answered %s, Approve said %v", option, ok)
			}
			th = waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
			if outcome := find(th, "permission", 0).Permission.Outcome; outcome != option {
				t.Errorf("the card's outcome is %q", outcome)
			}
		})
	}
}

func TestApproveIsRefusedByACancelledTurn(t *testing.T) {
	t.Parallel()
	var m *Manager
	errs, approved := make(chan error, 1), make(chan bool, 1)
	m, _ = newManager(t, openStore(t), approvalTool(&m, context.Background(), errs, approved))
	if _, err := m.Send(testAgent, "change it"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the card", func(th api.ChatThread) bool { return th.Session.State == api.ChatWaiting })
	if _, err := m.Cancel(testAgent); err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if <-approved {
		t.Error("a cancelled turn approved the change")
	}
}

func TestApproveGivesUpWithItsCaller(t *testing.T) {
	t.Parallel()
	var m *Manager
	ctx, cancel := context.WithCancel(context.Background())
	errs, approved := make(chan error, 1), make(chan bool, 1)
	m, _ = newManager(t, openStore(t), approvalTool(&m, ctx, errs, approved))
	if _, err := m.Send(testAgent, "change it"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the card", func(th api.ChatThread) bool { return th.Session.State == api.ChatWaiting })
	cancel()
	if err := <-errs; !errors.Is(err, context.Canceled) || <-approved {
		t.Errorf("Approve returned %v", err)
	}
	th := waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if outcome := find(th, "permission", 0).Permission.Outcome; outcome != "cancelled" {
		t.Errorf("the card's outcome is %q", outcome)
	}
}

func TestApproveNeedsATurn(t *testing.T) {
	t.Parallel()
	m, _ := newManager(t, openStore(t), newFakeTool(answerHello))
	if _, err := m.Approve(context.Background(), testAgent, api.ChatPermission{Title: "x"}); !errors.Is(err, errNoTurn) {
		t.Errorf("Approve outside a turn returned %v", err)
	}
}
