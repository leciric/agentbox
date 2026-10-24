package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Moving a running agent to another account of its AI tool, as a lead's
// composer moves the lead (leadrunner.go): from the agent's own composer, or
// the lead's switch_agent_account, typically when the account it was on hit
// its usage limit. The tool stays; the login is swapped in its machine
// (agent.Manager.SetAccount) and its chat restarts on it, resuming the same
// session where the adapter can (chat.Manager.AccountChanged).

// switchAgentAccount moves a to the account want names for its tool. It is
// refused while a turn runs: the tool reads its login only as it starts, and
// Codex rewrites its own login file as it refreshes its token, so swapping
// the file under a working turn could have the old account written back.
// What the chat did is returned as its notice says it, "" when no chat was
// running to restart.
func (s *Server) switchAgentAccount(ctx context.Context, a state.Agent, want agent.Logins) (state.Agent, string, error) {
	if s.chat.Busy(a) {
		return a, "", fmt.Errorf("%s is in the middle of a turn: switch its account once the turn ends, or once it stops on its usage limit", a.Name)
	}
	limited := false
	if session, err := s.chat.Session(a); err == nil {
		limited = session.Limited
	}
	moved, err := s.manager(s.cfg.Log).SetAccount(ctx, a, want)
	if err != nil {
		return a, "", err
	}
	note := s.chat.AccountChanged(moved)
	if limited && s.chat.Busy(moved) {
		// The turn waiting out the old account's limit carries on now.
		s.setOutcome(moved, "", "")
	}
	s.logf("Moved %s to the %s account %s", a.Ref(), agent.ChatAdapters[a.AI].Tool, accountOf(moved))
	s.events.publish(api.EventAgent, api.AgentChange{Ref: a.Ref()})
	return moved, note, nil
}

// accountOf is the account an agent's tool runs on, as the lead and the log
// say it.
func accountOf(a state.Agent) string {
	switch a.AI {
	case "claude":
		return cmpOr(a.ClaudeAccount, "(the machine's default)")
	case "codex":
		return a.CodexAccount
	case "cursor":
		return a.CursorAccount
	case "opencode":
		return formatOpenCode(a.OpenCodeAccounts)
	}
	return ""
}

// agentRunners answers GET /v1/agents/{project}/{agent}/chat/runners: the
// accounts of its own tool the agent's composer can move it to, in the shape
// a lead's composer reads (chatRunners), with the one tool an agent has.
func (s *Server) agentRunners(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	p, err := s.store.Project(ctx, a.Project)
	if err != nil {
		return err
	}
	out := api.LeadRunners{Tool: a.AI, ToolAccounts: map[string]string{}, ToolChosen: map[string]string{}}
	adapter, ok := agent.ChatAdapters[a.AI]
	if !ok {
		return writeJSON(w, http.StatusOK, out)
	}
	row := api.LeadRunner{Tool: a.AI, Name: adapter.Tool, Ready: true, Installed: true}
	switch a.AI {
	case "claude":
		out.Account = a.ClaudeAccount
		if row.Accounts, err = s.leadAccountRows(ctx, p); err != nil {
			return err
		}
	case "codex", "cursor":
		out.ToolAccounts[a.AI] = accountOf(a)
		if row.Accounts, err = s.leadToolRows(p, a.AI); err != nil {
			return err
		}
	case "opencode":
		// One per provider: the lead's switch_agent_account chooses them.
		out.ToolAccounts[a.AI] = accountOf(a)
	}
	out.Runners = []api.LeadRunner{row}
	return writeJSON(w, http.StatusOK, out)
}

// setAgentRunner answers PUT /v1/agents/{project}/{agent}/chat/runner: the
// agent's composer moving it to another account of its tool, at once. An
// agent's tool is fixed, so another tool is refused.
func (s *Server) setAgentRunner(w http.ResponseWriter, r *http.Request) error {
	var req api.LeadRunnerRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	if tool := strings.TrimSpace(req.Tool); tool != "" && tool != a.AI {
		return fmt.Errorf("%s runs %s, and an agent keeps its AI tool: only its account can change", a.Name, agent.ChatAdapters[a.AI].Tool)
	}
	moved, _, err := s.switchAgentAccount(r.Context(), a, loginsFor(a.AI, strings.TrimSpace(req.Account), nil))
	if err != nil {
		return err
	}
	session, err := s.chat.Session(moved)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, session)
}

// loginsFor names account for tool, as agent.Logins has it.
func loginsFor(tool, account string, openCode map[string]string) agent.Logins {
	switch tool {
	case "claude":
		return agent.Logins{Claude: account}
	case "codex":
		return agent.Logins{Codex: account}
	case "cursor":
		return agent.Logins{Cursor: account}
	}
	return agent.Logins{OpenCode: state.AccountMap(openCode)}
}

// leadSwitchAccount answers the lead's POST /v1/project/agents/{agent}/account
// (switch_agent_account).
func (s *Server) leadSwitchAccount(w http.ResponseWriter, r *http.Request) error {
	var req api.AgentAccountRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	account := strings.TrimSpace(req.Account)
	if a.AI == "opencode" && account != "" {
		return errors.New("OpenCode has an account per provider: pass opencode_accounts, like {\"anthropic\": \"work\"}")
	}
	if a.AI != "opencode" && len(req.OpenCodeAccounts) > 0 {
		return fmt.Errorf("%s runs %s: pass account, not opencode_accounts", a.Name, agent.ChatAdapters[a.AI].Tool)
	}
	moved, note, err := s.switchAgentAccount(r.Context(), a, loginsFor(a.AI, account, req.OpenCodeAccounts))
	if err != nil {
		return err
	}
	out := api.AgentAccountSwitch{Agent: a.Name, Tool: agent.ChatAdapters[a.AI].Tool, Account: accountOf(moved), Note: note}
	if session, err := s.chat.Session(moved); err == nil {
		out.Resumes = !session.NoResume
	}
	return writeJSON(w, http.StatusOK, out)
}
