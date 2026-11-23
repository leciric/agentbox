package chat

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

func TestIsCommand(t *testing.T) {
	skills := map[string]bool{"review": true, "deploy-app": true}
	for text, want := range map[string]bool{
		"/compact":                     true,
		"  /compact  ":                 true,
		"/rename Fix the login":        true,
		"/compact\nkeep the plan":      true,
		"/plugin:skill some args":      true,
		"/home/me/notes.txt is broken": false, // a path: a name holds no slash
		"/tmp/x":                       false,
		"/":                            false,
		"/ compact":                    false,
		"please /compact":              false,
		"$review the last commit":      true, // a skill, as the "/" list puts it
		"$deploy-app":                  true,
		"$HOME is unset":               false, // no skill by that name
		"$5 is the price":              false,
		"use $review on it":            false, // a skill mid-sentence is words to the model
		"Stop, /compact can wait":      false,
		"":                             false,
	} {
		if got := isCommand(text, skills); got != want {
			t.Errorf("isCommand(%q) = %v, want %v", text, got, want)
		}
	}
}

// prompts records every prompt a fake tool is given; the first blocks on
// release, so messages can arrive while it runs.
type prompts struct {
	mu      sync.Mutex
	texts   []string
	release chan struct{}
}

func (p *prompts) turn(_ *fakeTool, _, text string) acp.PromptResponse {
	p.mu.Lock()
	first := len(p.texts) == 0
	p.texts = append(p.texts, text)
	p.mu.Unlock()
	if first {
		<-p.release
	}
	return acp.PromptResponse{StopReason: "end_turn"}
}

func (p *prompts) got() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.texts)
}

// A slash command sent mid-turn doesn't join that turn, where the model would
// read it as text behind midTurn's note: it waits, marked queued, and goes in
// as a prompt of its own once the turn ends, so the tool runs it. A plain
// message sent after it still joins the running turn.
func TestCommandSentMidTurnWaitsAndGoesAlone(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	p := &prompts{release: make(chan struct{})}
	f := newSteeringTool(p.turn)
	m, _ := newManager(t, store, f)

	first, err := m.Send(testAgent, "Start")
	if err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to start", func(th api.ChatThread) bool { return len(p.got()) == 1 })

	cmd, err := m.Send(testAgent, "/compact")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Kind != "aside" || cmd.Turn != first.ID || cmd.Delivery != api.ChatAsideQueued {
		t.Errorf("the command = %+v, want an aside of turn %s, queued", cmd, first.ID)
	}
	if _, err := m.Send(testAgent, "Actually, stop at ten"); err != nil {
		t.Fatal(err)
	}
	// The plain message joins the turn on its own, without the command.
	if got := f.waitSteer(t); !strings.HasSuffix(got, "\n\nActually, stop at ten") || strings.Contains(got, "/compact") {
		t.Errorf("the running turn was given %q", got)
	}
	th := waitThread(t, m, testAgent, "the message to be marked sent", func(th api.ChatThread) bool {
		return find(th, "aside", 1).Delivery == api.ChatAsideSent
	})
	if d := find(th, "aside", 0).Delivery; d != api.ChatAsideQueued {
		t.Errorf("the command is %q while the turn runs, want queued", d)
	}

	close(p.release)
	th = waitThread(t, m, testAgent, "both turns to end", turnsEnded(2))
	// Exactly as typed: no note in front of it, which would keep the tool from
	// seeing a command.
	if got := p.got(); !slices.Equal(got, []string{"Start", "/compact"}) {
		t.Errorf("prompts %q, want the command alone after the first turn", got)
	}
	if n := f.steerCount(); n != 1 {
		t.Errorf("%d steers, want only the plain message's", n)
	}
	// The command heads its own turn where it stood; the plain message stays
	// in the turn it joined.
	if k := kinds(th); !slices.Equal(k, []string{"user", "user", "aside"}) {
		t.Errorf("items %v", k)
	}
	if c := find(th, "user", 1); c.ID != cmd.ID || c.Turn != c.ID || c.Delivery != "" || c.Result == nil {
		t.Errorf("the command's turn = %+v", c)
	}
	if a := find(th, "aside", 0); a.Turn != first.ID || a.Delivery != api.ChatAsideSent {
		t.Errorf("the plain message = %+v", a)
	}
}

// With a tool that can't take messages mid-turn, everything waits for the turn
// to end and then goes in the order it was sent: plain messages together as
// today, a command always alone.
func TestCommandsWaitingWithMessagesGoInOrder(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	p := &prompts{release: make(chan struct{})}
	f := newFakeTool(p.turn)
	m, _ := newManager(t, store, f)

	if _, err := m.Send(testAgent, "Start"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to start", func(th api.ChatThread) bool { return len(p.got()) == 1 })
	for _, text := range []string{"first", "second", "/compact", "/rename Release notes", "third"} {
		if _, err := m.Send(testAgent, text); err != nil {
			t.Fatalf("sending %q: %v", text, err)
		}
	}
	th := waitThread(t, m, testAgent, "the messages to be held back", func(th api.ChatThread) bool {
		return find(th, "aside", 4).Delivery == api.ChatAsideDeferred
	})
	want := []string{api.ChatAsideDeferred, api.ChatAsideDeferred, api.ChatAsideQueued, api.ChatAsideQueued, api.ChatAsideDeferred}
	for i, d := range want {
		if got := find(th, "aside", i); got.Delivery != d {
			t.Errorf("%q is %q, want %q", got.Text, got.Delivery, d)
		}
	}

	close(p.release)
	th = waitThread(t, m, testAgent, "every turn to end", turnsEnded(5))
	if got := p.got(); !slices.Equal(got, []string{"Start", "first\n\nsecond", "/compact", "/rename Release notes", "third"}) {
		t.Errorf("prompts %q", got)
	}
	for _, it := range th.Items {
		if it.Delivery != "" {
			t.Errorf("%s %q is left %q", it.Kind, it.Text, it.Delivery)
		}
	}
}

// A skill picked from the composer's "/" list goes in as $name, which only
// becomes what the tool runs as a prompt (skillPrompt): mid-turn, it waits
// for the turn too. One named mid-sentence is words to the model, and joins.
func TestSkillSentMidTurnWaitsForTheTurn(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	now := time.Now()
	if err := store.SetSkill(context.Background(), state.Skill{Name: "review", Enabled: true, UpdatedAt: now},
		[]state.SkillFile{{Path: "SKILL.md", Content: []byte("---\nname: review\ndescription: Review.\n---\n")}}); err != nil {
		t.Fatal(err)
	}
	p := &prompts{release: make(chan struct{})}
	f := newSteeringTool(p.turn)
	m, _ := newManager(t, store, f)

	if _, err := m.Send(testAgent, "Start"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the turn to start", func(th api.ChatThread) bool { return len(p.got()) == 1 })
	skill, err := m.Send(testAgent, "$review the last commit")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Delivery != api.ChatAsideQueued {
		t.Errorf("the skill is %q, want queued", skill.Delivery)
	}
	if _, err := m.Send(testAgent, "and use $review after"); err != nil {
		t.Fatal(err)
	}
	if got := f.waitSteer(t); !strings.HasSuffix(got, "\n\nand use $review after") {
		t.Errorf("the running turn was given %q", got)
	}

	close(p.release)
	waitThread(t, m, testAgent, "both turns to end", turnsEnded(2))
	// Claude Code runs a skill as /name, the prompt's first word.
	if got := p.got(); !slices.Equal(got, []string{"Start", "/review the last commit"}) {
		t.Errorf("prompts %q", got)
	}
}

// What AgentBox tells a lead before its next prompt doesn't go in front of a
// slash command, which the tool would then not see as one: it goes with the
// turn after.
func TestNotesWaitPastACommand(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	var mu sync.Mutex
	var got []string
	f := newFakeTool(func(_ *fakeTool, _, text string) acp.PromptResponse {
		mu.Lock()
		got = append(got, text)
		mu.Unlock()
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m, _ := newManager(t, store, f)
	lead := state.Agent{Project: "hello", Name: state.LeadName, Role: state.RoleLead, AI: "claude", Worktree: "/work/hello"}

	if _, err := m.Told(lead, "agent-02", "Fix the login", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Send(lead, "/compact"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, lead, "the command's turn to end", turnsEnded(1))
	if _, err := m.Send(lead, "What next?"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, lead, "the next turn to end", turnsEnded(2))
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != "/compact" {
		t.Fatalf("prompts %q, want the command as typed first", got)
	}
	if !strings.Contains(got[1], "User told agent-02: Fix the login") || !strings.HasSuffix(got[1], "What next?") {
		t.Errorf("the turn after the command was given %q, want the note it carried over", got[1])
	}
}
