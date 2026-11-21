package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// longTask is a task like the ones create_agent was given when agent-861 and
// agent-864 lost theirs: many lines, a list, a blank line or two.
const longTask = "Agents' worktrees live under the host's home, shared into the VM by virtiofs.\n\n" +
	"Move them onto the VM's own disk:\n- keep Open in editor working\n- leave the hub alone\n\nDone is a PR with before/after numbers."

func keepTask(t *testing.T, store *state.Store, a state.Agent, text string) {
	t.Helper()
	if err := store.SaveAgentTask(context.Background(), state.AgentTask{Project: a.Project, Agent: a.Name, Text: text, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func taskKept(t *testing.T, store *state.Store, a state.Agent) bool {
	t.Helper()
	_, ok, err := store.AgentTaskOf(context.Background(), a.Project, a.Name)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// The task as the agent's first message is the normal case: it goes once,
// as it is, and is handed over as its prompt goes.
func TestATaskSentAsTheFirstMessageIsHandedOver(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	store := openStore(t)
	m, _ := newManager(t, store, f)
	keepTask(t, store, testAgent, longTask)

	if _, err := m.Send(testAgent, longTask); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the task's turn to end", turnsEnded(1))
	if got := promptText(t, f, 0); got != longTask {
		t.Errorf("the first prompt is %q, want the task as it is", got)
	}
	if taskKept(t, store, testAgent) {
		t.Error("the task is still kept after its prompt went to the AI tool")
	}
}

// A task that never reached the AI tool — AgentBox stopped between making
// the agent and its first prompt — goes with whatever the first prompt is,
// once: "Start your task now" no longer reaches a session that was never
// told one.
func TestATaskThatNeverReachedTheToolGoesWithTheNextPrompt(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	store := openStore(t)
	m, _ := newManager(t, store, f)
	keepTask(t, store, testAgent, longTask)

	nudge := "You haven't started: Claude Code never ran your first turn. Start your task now."
	if _, err := m.Send(testAgent, nudge); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the nudge's turn to end", turnsEnded(1))
	first := promptText(t, f, 0)
	if !strings.HasPrefix(first, taskNote+"\n\n"+longTask+"\n\n---\n\n") || !strings.HasSuffix(first, nudge) {
		t.Errorf("the first prompt is %q, want the task ahead of the nudge", first)
	}
	if taskKept(t, store, testAgent) {
		t.Error("the task is still kept after a prompt carried it")
	}
	var told *api.ChatItem
	for i := range th.Items {
		if it := th.Items[i]; it.Kind == "notice" && strings.Contains(it.Text, "hadn't reached Claude Code") {
			told = &th.Items[i]
		}
	}
	if told == nil || told.Handoff != longTask {
		t.Errorf("the conversation doesn't show the task that went with the turn: %+v", th.Items)
	}

	if _, err := m.Send(testAgent, "carry on"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the second turn to end", turnsEnded(2))
	if got := promptText(t, f, 1); got != "carry on" {
		t.Errorf("the second prompt is %q: the task went twice", got)
	}
}

// A task whose own turn couldn't start its AI tool is carried by the next
// turn's handoff (carryUndelivered): it goes once, not twice.
func TestATaskCarriedByAHandoffGoesOnce(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	store := openStore(t)
	m, _ := newManager(t, store, f)
	keepTask(t, store, testAgent, longTask)
	c, err := m.conversation(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	c.carryUndelivered(&turn{text: longTask})
	c.mu.Unlock()

	if _, err := m.Send(testAgent, "carry on"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if got := promptText(t, f, 0); strings.Count(got, "keep Open in editor working") != 1 || strings.Contains(got, taskNote) {
		t.Errorf("the prompt is %q, want the task once, in the handoff", got)
	}
	if taskKept(t, store, testAgent) {
		t.Error("the task is still kept after the handoff carried it")
	}
}

// RestoreTask puts a task AgentBox stopped before sending into the chat as
// the turn the stop cut short; carried on, that turn's prompt is the task
// itself rather than "continue where you left off". A chat that holds the
// task already is left alone.
func TestARestoredTaskIsCarriedOnAsItself(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	store := openStore(t)
	m, _ := newManager(t, store, f)
	keepTask(t, store, testAgent, longTask)

	restored, err := m.RestoreTask(testAgent, longTask)
	if err != nil || !restored {
		t.Fatalf("RestoreTask() = %v, %v", restored, err)
	}
	turns, err := store.RunningTurns(context.Background())
	if err != nil || len(turns) != 1 || turns[0].Kind != state.TurnOfAgent || turns[0].Resumes != 0 {
		t.Fatalf("running turns = %+v, %v; want the task's turn recorded", turns, err)
	}
	th, err := m.Thread(testAgent)
	if err != nil {
		t.Fatal(err)
	}
	if user := find(th, "user", 0); user.Text != longTask || user.ID != turns[0].Turn || user.Hidden {
		t.Errorf("the chat's first message is %+v, want the task, shown, heading the recorded turn", user)
	}
	if again, err := m.RestoreTask(testAgent, longTask); err != nil || again {
		t.Errorf("RestoreTask() again = %v, %v; want the chat that holds it left alone", again, err)
	}

	resumed, err := m.Resume(testAgent, turns[0])
	if err != nil || !resumed {
		t.Fatalf("Resume() = %v, %v", resumed, err)
	}
	waitThread(t, m, testAgent, "the carried-on turn to end", turnsEnded(1))
	if got := promptText(t, f, 0); got != taskNote+"\n\n"+longTask {
		t.Errorf("the carried-on turn's prompt is %q, want the task", got)
	}
	if n := len(f.called(acp.MethodSessionPrompt)); n != 1 {
		t.Errorf("%d prompts sent, want 1", n)
	}
	if taskKept(t, store, testAgent) {
		t.Error("the task is still kept after its prompt went")
	}
}

// A lead's chat has no task of its own to look for.
func TestALeadsPromptsCarryNoTask(t *testing.T) {
	t.Parallel()
	f := newFakeTool(answerHello)
	store := openStore(t)
	m, _ := newManager(t, store, f)
	keepTask(t, store, testLead, longTask) // can't happen: shows it isn't read

	if _, err := m.Send(testLead, "plan the release"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testLead, "the turn to end", turnsEnded(1))
	if got := promptText(t, f, 0); strings.Contains(got, longTask) {
		t.Errorf("the lead's prompt carries a task: %q", got)
	}
}
