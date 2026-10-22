package chat

import (
	"context"
	"slices"
	"testing"

	"agentbox/internal/acp"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

func (f *fakeTool) value(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.values[id]
}

func option(s api.ChatSession, id string) (api.ChatOption, bool) {
	i := slices.IndexFunc(s.Options, func(o api.ChatOption) bool { return o.ID == id })
	if i < 0 {
		return api.ChatOption{}, false
	}
	return s.Options[i], true
}

// The options of Claude Code's ExitPlanMode request when the session was on
// default before it planned (claude-agent-acp's buildExitPlanModePermissionOptions).
var exitPlanOptions = []map[string]string{
	{"optionId": "exit-plan-clear-auto", "name": "Yes, clear context and use auto mode", "kind": "allow_always"},
	{"optionId": "exit-plan-auto", "name": "Yes, and use auto mode", "kind": "allow_always"},
	{"optionId": "exit-plan-bypass", "name": "Yes, and bypass permissions", "kind": "allow_always"},
	{"optionId": "exit-plan-default", "name": "Yes, manually approve edits", "kind": "allow_once"},
	{"optionId": "reject", "name": "No, keep planning", "kind": "reject_once"},
}

// startPlanning starts a chat on a tool with plan mode, on bypass permissions
// with Plan on.
func startPlanning(t *testing.T, store *state.Store, f *fakeTool) *Manager {
	t.Helper()
	f.plan = true
	m, _ := newManager(t, store, f)
	if _, err := m.Start(testAgent); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	ctx := context.Background()
	if _, err := m.SetOption(ctx, testAgent, "mode", "bypassPermissions"); err != nil {
		t.Fatal(err)
	}
	session, err := m.SetOption(ctx, testAgent, api.ChatOptionPlan, "true")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.value("mode"); got != "plan" {
		t.Errorf("with Plan on the tool's mode is %q", got)
	}
	mode, _ := option(session, "mode")
	if mode.Value != "bypassPermissions" || slices.ContainsFunc(mode.Choices, func(c api.ChatOptionChoice) bool { return c.Value == "plan" }) {
		t.Errorf("with Plan on the mode menu is %+v", mode)
	}
	if plan, ok := option(session, api.ChatOptionPlan); !ok || plan.Value != "true" || plan.Type != "boolean" {
		t.Errorf("Plan is %+v, %v", plan, ok)
	}
	return m
}

func TestPlanningOnBypassAsksOnlyForThePlan(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	auto, safety := make(chan string, 1), make(chan string, 1)
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		// What plan mode lets through is answered for you...
		auto <- f.askWith(s, `{"toolCallId":"t1","title":"git log","kind":"execute"}`, []map[string]string{
			{"optionId": "allow", "name": "Yes", "kind": "allow_once"},
			{"optionId": "allow-always", "name": "Yes, and don't ask again for git log commands", "kind": "allow_always"},
			{"optionId": "reject", "name": "No", "kind": "reject_once"},
		})
		// ...a safety check isn't, as bypass doesn't skip it either...
		safety <- f.askWith(s, `{"toolCallId":"t2","title":"rm -rf /","kind":"execute"}`, []map[string]string{
			{"optionId": "allow", "name": "Yes", "kind": "allow_once"},
			{"optionId": "reject", "name": "No", "kind": "reject_once"},
		})
		// ...and the plan's approval is yours.
		if got := f.askWith(s, `{"toolCallId":"t3","title":"Ready to code?","kind":"switch_mode"}`, exitPlanOptions); got == "exit-plan-bypass" {
			// What claude-agent-acp does once the plan is approved.
			f.mu.Lock()
			f.values["mode"] = "bypassPermissions"
			f.mu.Unlock()
			f.update(s, `{"sessionUpdate":"current_mode_update","currentModeId":"bypassPermissions"}`)
		}
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m := startPlanning(t, store, f)
	if _, err := m.Send(testAgent, "Plan the change"); err != nil {
		t.Fatal(err)
	}
	if got := <-auto; got != "allow" {
		t.Errorf("a command in plan mode on bypass was answered %q", got)
	}
	th := waitThread(t, m, testAgent, "the safety check", func(th api.ChatThread) bool { return th.Session.State == api.ChatWaiting })
	if _, err := m.Answer(testAgent, find(th, "permission", 0).ID, "allow"); err != nil {
		t.Fatal(err)
	}
	if got := <-safety; got != "allow" {
		t.Errorf("the safety check was answered %q", got)
	}
	th = waitThread(t, m, testAgent, "the plan's approval", func(th api.ChatThread) bool {
		return th.Session.State == api.ChatWaiting && find(th, "permission", 1).ID != ""
	})
	perm := find(th, "permission", 1).Permission
	if perm.PlanApprove != "exit-plan-bypass" {
		t.Errorf("the plan's approval approves with %q", perm.PlanApprove)
	}
	if i := slices.IndexFunc(perm.Options, func(o api.ChatPermissionOption) bool { return o.ID == "exit-plan-bypass" }); i < 0 || perm.Options[i].ModeKind != "full_access" {
		t.Errorf("the plan's options are %+v", perm.Options)
	}
	if _, err := m.Answer(testAgent, find(th, "permission", 1).ID, perm.PlanApprove); err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m, testAgent, "the turn to end", turnsEnded(1))
	if n := len(slices.DeleteFunc(kinds(th), func(k string) bool { return k != "permission" })); n != 2 {
		t.Errorf("%d permission requests in the chat, want 2: %v", n, kinds(th))
	}
	th = waitThread(t, m, testAgent, "Plan to turn off", func(th api.ChatThread) bool { return optionValue(th.Session, api.ChatOptionPlan) == "false" })
	if got := optionValue(th.Session, "mode"); got != "bypassPermissions" {
		t.Errorf("after the plan the mode is %q", got)
	}
	stored, err := store.Chat(context.Background(), testAgent.Project, testAgent.Name)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Options[api.ChatOptionPlan] != "" || stored.Options["mode"] != "bypassPermissions" {
		t.Errorf("after the plan the stored options are %v", stored.Options)
	}
}

func TestPlanningOnDefaultStillAsks(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		f.askWith(s, `{"toolCallId":"t1","title":"git log","kind":"execute"}`, []map[string]string{
			{"optionId": "allow", "name": "Yes", "kind": "allow_once"},
			{"optionId": "allow-always", "name": "Yes, and don't ask again", "kind": "allow_always"},
			{"optionId": "reject", "name": "No", "kind": "reject_once"},
		})
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	m := startPlanning(t, store, f)
	if _, err := m.SetOption(context.Background(), testAgent, "mode", "default"); err != nil {
		t.Fatal(err)
	}
	if got := f.value("mode"); got != "plan" {
		t.Errorf("choosing a level while planning moved the tool to %q", got)
	}
	if _, err := m.Send(testAgent, "Plan the change"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the request", func(th api.ChatThread) bool { return th.Session.State == api.ChatWaiting })
}

func TestTurningPlanOffReturnsToTheLevel(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(answerHello)
	m := startPlanning(t, store, f)
	session, err := m.SetOption(context.Background(), testAgent, api.ChatOptionPlan, "false")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.value("mode"); got != "bypassPermissions" {
		t.Errorf("with Plan off the tool's mode is %q", got)
	}
	if optionValue(session, api.ChatOptionPlan) != "false" || optionValue(session, "mode") != "bypassPermissions" {
		t.Errorf("with Plan off the options are %+v", session.Options)
	}
}

func TestPlanCarriesToTheNextSession(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	m := startPlanning(t, store, newFakeTool(answerHello))
	m.Close()

	next := newFakeTool(answerHello)
	next.plan = true
	m2, _ := newManager(t, store, next)
	if _, err := m2.Start(testAgent); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m2, testAgent, "the plan", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady && next.value("mode") == "plan" })
	if optionValue(th.Session, "mode") != "bypassPermissions" {
		t.Errorf("the next session's options are %+v", th.Session.Options)
	}
}

func TestModeSetToPlanTurnsPlanOn(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(answerHello)
	f.plan = true
	m, _ := newManager(t, store, f)
	if _, err := m.Start(testAgent); err != nil {
		t.Fatal(err)
	}
	waitThread(t, m, testAgent, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	// agentbox chat --set mode=plan, as before Plan was a toggle.
	session, err := m.SetOption(context.Background(), testAgent, "mode", "plan")
	if err != nil {
		t.Fatal(err)
	}
	if f.value("mode") != "plan" || optionValue(session, api.ChatOptionPlan) != "true" || optionValue(session, "mode") != "default" {
		t.Errorf("mode=plan gave the tool %q and the options %+v", f.value("mode"), session.Options)
	}
}

func TestTheModelEnteringPlanTurnsPlanOn(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	f := newFakeTool(func(f *fakeTool, s, text string) acp.PromptResponse {
		f.update(s, `{"sessionUpdate":"current_mode_update","currentModeId":"plan"}`)
		return acp.PromptResponse{StopReason: "end_turn"}
	})
	f.plan = true
	m, _ := newManager(t, store, f)
	if _, err := m.Send(testAgent, "Plan it first"); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "Plan", func(th api.ChatThread) bool { return optionValue(th.Session, api.ChatOptionPlan) == "true" })
	if got := optionValue(th.Session, "mode"); got != "default" {
		t.Errorf("planning by itself, the mode menu shows %q", got)
	}
}

func TestNoPlanToggleWithoutPlanMode(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	m, _ := newManager(t, store, newFakeTool(answerHello))
	if _, err := m.Start(testAgent); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "the session", func(th api.ChatThread) bool { return th.Session.State == api.ChatReady })
	if _, ok := option(th.Session, api.ChatOptionPlan); ok {
		t.Errorf("a tool with no plan mode has Plan: %+v", th.Session.Options)
	}
	if _, err := m.SetOption(context.Background(), testAgent, api.ChatOptionPlan, "true"); err == nil {
		t.Error("Plan was turned on for a tool with no plan mode")
	}
}
