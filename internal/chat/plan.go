package chat

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"agentbox/internal/acp"
	"agentbox/internal/api"
)

// Plan is a toggle beside the permission mode, not one of its choices. The AI
// tools have one mode setting, plan among its choices, so choosing plan used
// to replace the permission level you had picked: a chat on bypass
// permissions asked about everything once it planned, and went on asking
// after the plan was approved, since approving it moved Claude Code to
// "default".
//
// So AgentBox keeps the two apart. The stored "mode" is the permission level
// you chose, never plan; api.ChatOptionPlan says whether the chat plans. The
// tool is sent its plan mode while that is on and the level otherwise
// (wanted), and the session goes out with the mode menu showing the level and
// a boolean "plan" option beside it (view). A tool with no plan mode (Codex)
// gets no toggle; OpenCode's plan agent is one.
//
// Planning on bypass permissions asks nothing that plan mode allows
// (autoAllow): Claude Code bypasses its own prompts in plan mode only for an
// interactive session, and under the SDK every one reaches AgentBox instead.

// planChoice is the value of the mode option's plan choice, "" when it has none.
func planChoice(o api.ChatOption) string {
	if o.Category != "mode" {
		return ""
	}
	for _, ch := range o.Choices {
		if ch.Kind == "plan" {
			return ch.Value
		}
	}
	return ""
}

// modeOption is the session's permission mode setting. Caller holds c.mu.
func (c *conversation) modeOption() (api.ChatOption, bool) {
	i := slices.IndexFunc(c.session.Options, func(o api.ChatOption) bool { return o.Category == "mode" && o.Type == "select" })
	if i < 0 {
		return api.ChatOption{}, false
	}
	return c.session.Options[i], true
}

// planning reports whether the chat is to plan: the toggle, or a mode stored
// as plan before plan was a toggle. Caller holds c.mu.
func (c *conversation) planning(o api.ChatOption) bool {
	plan := planChoice(o)
	return plan != "" && (c.stored.Options[api.ChatOptionPlan] == "true" || c.stored.Options[o.ID] == plan)
}

// level is the permission mode chosen beside Plan, which the chat runs on when
// it doesn't plan: the stored one, full access for an autonomous agent that
// chose none, else the tool's own standard one. ok is false when there is
// nothing to choose from. Caller holds c.mu.
func (c *conversation) level(o api.ChatOption) (string, bool) {
	plan := planChoice(o)
	if v, ok := c.stored.Options[o.ID]; ok && v != plan && validValue(o, v) {
		return v, true
	}
	kinds := []string{"standard", ""}
	if c.agent.Autonomous {
		kinds = append([]string{"full_access"}, kinds...)
	}
	for _, kind := range kinds {
		for _, ch := range o.Choices {
			if ch.Kind == kind && ch.Value != plan {
				return ch.Value, true
			}
		}
	}
	if o.Value != plan && o.Value != "" {
		return o.Value, true
	}
	return "", false
}

// wantedMode is wanted's value for the permission mode: plan while the chat
// plans, the level otherwise. A tool without plan keeps the old rule: what you
// chose, or full access for an autonomous agent. Caller holds c.mu.
func (c *conversation) wantedMode(o api.ChatOption) (string, bool) {
	if planChoice(o) == "" {
		value, ok := c.stored.Options[o.ID]
		if !ok && c.agent.Autonomous {
			for _, ch := range o.Choices {
				if ch.Kind == "full_access" {
					return ch.Value, true
				}
			}
		}
		return value, ok
	}
	if c.planning(o) {
		return planChoice(o), true
	}
	if _, chosen := c.stored.Options[o.ID]; !chosen && !c.agent.Autonomous {
		return "", false // the tool's own default
	}
	return c.level(o)
}

// levelKind is the kind of the level, like full_access. Caller holds c.mu.
func (c *conversation) levelKind(o api.ChatOption) string {
	v, ok := c.level(o)
	if !ok {
		return ""
	}
	if i := slices.IndexFunc(o.Choices, func(ch api.ChatOptionChoice) bool { return ch.Value == v }); i >= 0 {
		return o.Choices[i].Kind
	}
	return ""
}

// view is the session as it goes out: the mode menu without its plan choice,
// showing the level, and Plan beside it as a boolean. Caller holds c.mu.
func (c *conversation) view() api.ChatSession {
	s := clone(c.session)
	i := slices.IndexFunc(s.Options, func(o api.ChatOption) bool { return o.Category == "mode" && o.Type == "select" })
	if i < 0 {
		return s
	}
	o := s.Options[i]
	plan := planChoice(o)
	if plan == "" {
		return s
	}
	planning := o.Value == plan || (c.adapter == nil || !c.adapter.ready) && c.planning(o)
	if level, ok := c.level(o); ok && (planning || o.Value == "") {
		o.Value = level
	}
	name := "Plan"
	for _, ch := range o.Choices {
		if ch.Value == plan {
			name = ch.Name
		}
	}
	o.Choices = slices.DeleteFunc(o.Choices, func(ch api.ChatOptionChoice) bool { return ch.Value == plan })
	s.Options[i] = o
	s.Options = slices.Insert(s.Options, i+1, api.ChatOption{
		ID: api.ChatOptionPlan, Name: name, Description: "Plan before changing anything, then run on the permission mode beside it",
		Category: api.ChatOptionPlan, Type: "boolean", Value: fmt.Sprint(planning), Choices: []api.ChatOptionChoice{},
	})
	return s
}

// setPlan is SetOption for Plan, and for a mode set to plan by someone who
// doesn't know it's a toggle now (agentbox chat --set mode=plan). Caller holds
// c.mu, and this unlocks it.
func (m *Manager) setPlan(ctx context.Context, c *conversation, on bool) (api.ChatSession, error) {
	o, ok := c.modeOption()
	plan := planChoice(o)
	if !ok || plan == "" {
		c.mu.Unlock()
		return api.ChatSession{}, fmt.Errorf("%s has no plan mode", ToolNames[c.agent.AI])
	}
	if on {
		c.stored.Options[api.ChatOptionPlan] = "true"
	} else {
		delete(c.stored.Options, api.ChatOptionPlan)
	}
	if c.stored.Options[o.ID] == plan {
		delete(c.stored.Options, o.ID) // plan stored as a mode, from before the toggle
	}
	target, _ := c.level(o)
	if on {
		target = plan
	}
	return m.sendMode(ctx, c, o, target)
}

// setLevel is SetOption for the permission mode of a tool with plan: the level
// is kept, and sent only while the chat isn't planning. Caller holds c.mu, and
// this unlocks it.
func (m *Manager) setLevel(ctx context.Context, c *conversation, o api.ChatOption, value string) (api.ChatSession, error) {
	if !validValue(o, value) {
		c.mu.Unlock()
		return api.ChatSession{}, fmt.Errorf("%q isn't a choice for %s", value, o.Name)
	}
	c.stored.Options[o.ID] = value
	if c.planning(o) {
		// Not sent: the plan's approval, or turning Plan off, runs on it.
		defer c.mu.Unlock()
		if err := m.Store.SaveChat(context.Background(), c.agent.Project, c.agent.Name, c.stored); err != nil {
			return api.ChatSession{}, err
		}
		c.markSession()
		c.flush(false)
		return c.view(), nil
	}
	return m.sendMode(ctx, c, o, value)
}

// sendMode saves the stored choices and moves the session's mode to value, now
// when the tool is up and when it starts otherwise. Caller holds c.mu, and
// this unlocks it.
func (m *Manager) sendMode(ctx context.Context, c *conversation, o api.ChatOption, value string) (api.ChatSession, error) {
	if err := m.Store.SaveChat(context.Background(), c.agent.Project, c.agent.Name, c.stored); err != nil {
		c.mu.Unlock()
		return api.ChatSession{}, err
	}
	ad := c.adapter
	if ad == nil || !ad.ready || value == "" || value == o.Value {
		defer c.mu.Unlock()
		c.markSession()
		c.flush(false)
		return c.view(), nil
	}
	req := setRequest(ad.sessionID, o, value)
	c.mu.Unlock()
	var out acp.SetConfigOptionResponse
	err := ad.conn.Call(ctx, acp.MethodSetConfigOption, req, &out)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		return c.view(), err
	}
	if c.adapter == ad && out.ConfigOptions != nil {
		c.setOptions(toOptions(out.ConfigOptions))
	}
	c.markSession()
	c.flush(false)
	return c.view(), nil
}

// modeMoved follows a mode change the tool made by itself: approving a plan
// (ExitPlanMode) moves it out of plan to the mode the approval chose, which
// becomes the level, and the model entering plan (EnterPlanMode) turns Plan
// on. Caller holds c.mu.
func (c *conversation) modeMoved(from string) {
	o, ok := c.modeOption()
	plan := planChoice(o)
	if !ok || plan == "" || from == "" || from == o.Value {
		return
	}
	planning := c.stored.Options[api.ChatOptionPlan] == "true"
	switch {
	case o.Value == plan && !planning:
		c.stored.Options[api.ChatOptionPlan] = "true"
	case from == plan && o.Value != plan && (planning || c.stored.Options[o.ID] == plan):
		delete(c.stored.Options, api.ChatOptionPlan)
		c.stored.Options[o.ID] = o.Value
	default:
		return
	}
	if err := c.m.Store.SaveChat(context.Background(), c.agent.Project, c.agent.Name, c.stored); err != nil {
		c.m.logf("chat %s: saving the plan's mode: %v", c.agent.Ref(), err)
	}
}

// exitPlanModes are the modes claude-agent-acp's ExitPlanMode options run the
// plan on (permissions/options/shared.js in the adapter).
var exitPlanModes = map[string]string{
	"exit-plan-bypass":             "bypassPermissions",
	"exit-plan-clear-bypass":       "bypassPermissions",
	"exit-plan-auto":               "auto",
	"exit-plan-clear-auto":         "auto",
	"exit-plan-accept-edits":       "acceptEdits",
	"exit-plan-clear-accept-edits": "acceptEdits",
	"exit-plan-default":            "default",
}

func isPlanApproval(options []acp.PermissionOption) bool {
	return slices.ContainsFunc(options, func(o acp.PermissionOption) bool { return strings.HasPrefix(o.OptionID, "exit-plan-") })
}

// markPlanApproval gives a plan's approval the mode each option runs on and
// picks the one that keeps the level chosen beside Plan, which the app makes
// its Approve; without it, Approve was the adapter's only allow_once,
// "manually approve edits", whatever you had chosen. Caller holds c.mu.
func (c *conversation) markPlanApproval(p *api.ChatPermission) {
	o, ok := c.modeOption()
	if !ok {
		return
	}
	level, _ := c.level(o)
	for i, opt := range p.Options {
		mode, ok := exitPlanModes[opt.ID]
		if !ok {
			continue
		}
		if j := slices.IndexFunc(o.Choices, func(ch api.ChatOptionChoice) bool { return ch.Value == mode }); j >= 0 {
			p.Options[i].ModeKind = o.Choices[j].Kind
		}
		if mode == level && !strings.HasPrefix(opt.ID, "exit-plan-clear-") {
			p.PlanApprove = opt.ID
		}
	}
}

// autoAllow is the option that answers a request by itself while the chat
// plans on full access: whatever the tool asks then, as the CLI allows it in
// plan mode when bypass is on. Not the plan's approval, which is yours, nor a
// request with no lasting option, which is how the adapter marks a safety
// check or your own ask rule (suppressAlwaysAllowRule, matchedAskRule): bypass
// doesn't skip those either. It answers "once", writing no rule. Caller holds
// c.mu.
func (c *conversation) autoAllow(options []acp.PermissionOption) (string, bool) {
	o, ok := c.modeOption()
	if !ok || planChoice(o) == "" || o.Value != planChoice(o) || c.levelKind(o) != "full_access" || isPlanApproval(options) {
		return "", false
	}
	if !slices.ContainsFunc(options, func(p acp.PermissionOption) bool { return p.Kind == "allow_always" }) {
		return "", false
	}
	for _, kind := range []string{"allow_once", "allow_always"} {
		for _, p := range options {
			if p.Kind == kind {
				return p.OptionID, true
			}
		}
	}
	return "", false
}
