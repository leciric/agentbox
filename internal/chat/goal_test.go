package chat

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// claudeGoalTurn is a /goal turn the way claude-agent-acp 0.93–0.100 sends it
// to an AIR client (checked against a real run): the goal snapshot, the CLI's
// "Goal set" line as a message of its own, then the work, with the Stop hook's
// continuations inside the one prompt, and no word of the goal being met.
func claudeGoalTurn(f *fakeTool, s, text string) acp.PromptResponse {
	if !strings.HasPrefix(text, "/goal ") {
		f.update(s, `{"sessionUpdate":"agent_message_chunk","messageId":"m9","content":{"type":"text","text":"Sure."}}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	}
	f.update(s, `{"sessionUpdate":"session_info_update","_meta":{"jetbrains":{"air":{"version":1,"goal":{"objective":"hello.txt has 3 lines","status":"active","controlMethod":"_session/goal"}}}}}`)
	f.update(s, `{"sessionUpdate":"agent_message_chunk","messageId":"b2119fff-a714","content":{"type":"text","text":"Goal set: hello.txt has 3 lines"}}`)
	f.update(s, `{"sessionUpdate":"agent_message_chunk","messageId":"msg_1","content":{"type":"text","text":"Line one written."}}`)
	f.update(s, `{"sessionUpdate":"agent_message_chunk","messageId":"msg_2","content":{"type":"text","text":"All three lines are there."}}`)
	return acp.PromptResponse{StopReason: "end_turn"}
}

type goalCalls struct {
	mu    sync.Mutex
	goals []api.AgentGoal
}

func (g *goalCalls) changed(_ state.Agent, goal api.AgentGoal) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.goals = append(g.goals, goal)
}

func (g *goalCalls) statuses() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, goal := range g.goals {
		out = append(out, goal.Status)
	}
	return out
}

func TestAToolGoalShowsAsAGoalRowAndIsMetWhenItsTurnEnds(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(claudeGoalTurn)
	m, _ := newManager(t, store, f)
	calls := &goalCalls{}
	m.GoalChanged = calls.changed

	if _, err := m.Send(testAgent, "/goal hello.txt has 3 lines"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the /goal turn", turnsEnded(1))
	var goals []api.ChatItem
	for _, it := range th.Items {
		if it.Kind == "assistant" && strings.HasPrefix(it.Text, "Goal set") {
			t.Errorf("the tool's Goal set line is a message too: %q", it.Text)
		}
		if it.Kind == "goal" {
			goals = append(goals, it)
		}
	}
	if len(goals) != 2 || goals[0].Goal.Status != api.GoalActive || goals[1].Goal.Status != api.GoalMet {
		t.Fatalf("goal rows = %+v, want set then met (%v)", goals, kinds(th))
	}
	if g := goals[0].Goal; g.Objective != "hello.txt has 3 lines" || g.Source != api.GoalSourceTool {
		t.Errorf("goal = %+v", g)
	}
	if got := find(th, "assistant", 0).Text; got != "Line one written." {
		t.Errorf("first message = %q", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(calls.statuses()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := strings.Join(calls.statuses(), ","); got != "active,met" {
		t.Errorf("GoalChanged told %s, want active,met", got)
	}

	// The next turn is an ordinary one: the met goal stays met.
	if _, err := m.Send(testAgent, "thanks"); err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m, testAgent, "the next turn", turnsEnded(2))
	n := 0
	for _, it := range th.Items {
		if it.Kind == "goal" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d goal rows after an ordinary turn, want 2", n)
	}
}

func TestAToolGoalClearedOrComplete(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		switch text {
		case "/goal clear":
			f.update(s, `{"sessionUpdate":"session_info_update","_meta":{"goal":null}}`)
			f.update(s, `{"sessionUpdate":"agent_message_chunk","messageId":"c1","content":{"type":"text","text":"Goal cleared: ship"}}`)
		default:
			f.update(s, `{"sessionUpdate":"session_info_update","_meta":{"goal":{"objective":"ship","status":"active"}}}`)
			f.update(s, `{"sessionUpdate":"agent_message_chunk","messageId":"x1","content":{"type":"text","text":"Working on it."}}`)
		}
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	codex := testAgent
	codex.AI = "codex"
	if _, err := m.Send(codex, "/goal ship"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, codex, "the /goal turn", turnsEnded(1))
	// Codex says when its goal is complete: a turn ending isn't that.
	if g := find(th, "goal", 1); g.Goal != nil {
		t.Fatalf("a Codex turn ending met its goal: %+v", g.Goal)
	}
	if _, err := m.Send(codex, "/goal clear"); err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m, codex, "the clear", turnsEnded(2))
	if g := find(th, "goal", 1); g.Goal == nil || g.Goal.Status != api.GoalCleared {
		t.Fatalf("second goal row = %+v (%v)", g, kinds(th))
	}
	for _, it := range th.Items {
		if it.Kind == "assistant" && strings.HasPrefix(it.Text, "Goal cleared") {
			t.Errorf("the tool's Goal cleared line is a message too")
		}
	}
}

func TestShowGoalRows(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	m, _ := newManager(t, store, newFakeTool(answerHello))
	rows := func() []string {
		t.Helper()
		th, err := m.Thread(testAgent)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range th.Items {
			out = append(out, it.Text)
		}
		return out
	}
	// AgentBox's: a row at every check, the set row kept.
	g := api.AgentGoal{Objective: "ci green", Source: api.GoalSourceAgentBox, Status: api.GoalActive, Budget: 3}
	if _, err := m.ShowGoal(testAgent, g, "set"); err != nil {
		t.Fatal(err)
	}
	g.Checked, g.Continuations, g.LastUnmet = 1, 1, "checks still running"
	if _, err := m.ShowGoal(testAgent, g, "checked"); err != nil {
		t.Fatal(err)
	}
	if got := rows(); len(got) != 2 || got[0] != "Goal set: ci green" || got[1] != "Goal not met yet: checks still running" {
		t.Fatalf("rows = %q", got)
	}
	// The tool's: its checks update the check row, never the set row.
	tg := api.AgentGoal{Objective: "ship", Source: api.GoalSourceTool, Status: api.GoalActive}
	if _, err := m.ShowGoal(testAgent, tg, "set"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		tg.Checked, tg.LastUnmet = i, fmt.Sprintf("try %d", i)
		if _, err := m.ShowGoal(testAgent, tg, "checked"); err != nil {
			t.Fatal(err)
		}
	}
	if got := rows(); len(got) != 4 || got[2] != "Goal set: ship" || got[3] != "Goal not met yet: try 3" {
		t.Fatalf("rows = %q", got)
	}
}
