package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/state"
)

// A lead's AI tool and account, chosen in its chat's composer (see
// chat/runner.go for what each switch does to the session). The choice is the chat's own, kept beside its conversation,
// not a project setting: the project's account and Settings → Models stay
// what new agents and a reset chat start from.

// leadAccount is chat.Manager's LeadAccount hook.
func (s *Server) leadAccount(a state.Agent) (string, error) {
	return s.manager(nil).LeadAccountFor(context.Background(), a)
}

// runnerProject is the project a lead resolves its logins against: the Home
// chat's has none of its own, and gets the machine's defaults.
func (s *Server) runnerProject(ctx context.Context, project string) (state.Project, error) {
	if project == state.HomeProject {
		return state.Project{Name: state.HomeProject}, nil
	}
	return s.store.Project(ctx, project)
}

// chatRunners answers GET /v1/projects/{project}/chat/runners: the tools the
// chat can switch to and the Claude Code accounts it may use.
func (s *Server) chatRunners(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := s.leadFromPath(r)
	if err != nil {
		return err
	}
	p, err := s.runnerProject(ctx, a.Project)
	if err != nil {
		return err
	}
	m := s.manager(nil)
	choice, err := m.LeadChoice(ctx, a.Project)
	if err != nil {
		return err
	}
	out := api.LeadRunners{Tool: choice.Tool, NextTool: choice.NextTool, Chosen: choice.Account}
	claude := a
	claude.AI = "claude"
	out.Account, _ = m.LeadAccountFor(ctx, claude)
	accounts, err := s.leadAccountRows(ctx, p)
	if err != nil {
		return err
	}
	for _, tool := range agent.LeadTools {
		row := api.LeadRunner{Tool: tool, Name: agent.ChatAdapters[tool].Tool, Installed: m.LeadToolInstalled(tool)}
		if err := m.LeadToolLogin(p, tool); err != nil {
			row.Reason = err.Error()
		} else {
			row.Ready = true
		}
		if tool == "claude" {
			row.Accounts = accounts
		}
		out.Runners = append(out.Runners, row)
	}
	return writeJSON(w, http.StatusOK, out)
}

// setChatRunner answers PUT /v1/projects/{project}/chat/runner: it stores the
// tool and account the chat runs from its next message. A tool AgentBox has
// no login for, or an account the project may not use, is refused here, where
// the composer can say why, rather than when the message is sent.
func (s *Server) setChatRunner(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var req api.LeadRunnerRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	req.Tool = strings.TrimSpace(req.Tool)
	a, err := s.leadFromPath(r)
	if err != nil {
		return err
	}
	p, err := s.runnerProject(ctx, a.Project)
	if err != nil {
		return err
	}
	m := s.manager(s.cfg.Log)
	if err := m.LeadToolLogin(p, req.Tool); err != nil {
		return err
	}
	if req.Tool != "claude" {
		req.Account = ""
	} else if req.Account != "" {
		if _, err := m.ClaudeAccountFor(p, req.Account); err != nil {
			return err
		}
	}
	session, err := s.chat.SetRunner(a, req.Tool, req.Account)
	if err != nil {
		return err
	}
	// The account moves now, on a chat staying on Claude Code: its row first,
	// then the session, which resumes on it (chat.Manager.AccountChanged).
	if session.Tool == "claude" && session.NextTool == "" {
		moved, err := s.leadOnAccount(ctx, a, p)
		if err != nil {
			return err
		}
		s.chat.AccountChanged(moved)
		if session, err = s.chat.Session(moved); err != nil {
			return err
		}
	}
	s.logf("The %s chat runs %s from its next message%s", a.Project, agent.ChatAdapters[cmpTool(session)].Tool, accountNote(session))
	return writeJSON(w, http.StatusOK, session)
}

func cmpTool(session api.ChatSession) string {
	if session.NextTool != "" {
		return session.NextTool
	}
	return session.Tool
}

func accountNote(session api.ChatSession) string {
	if session.Account == "" || cmpTool(session) != "claude" {
		return ""
	}
	return fmt.Sprintf(" on the account %q", session.Account)
}

// leadOnAccount is the lead with the account its chat resolves to now, stored
// on its row: a project's lead goes through EnsureLead's own sync when it has
// a row, and the Home chat, which has none, is built afresh.
func (s *Server) leadOnAccount(ctx context.Context, a state.Agent, p state.Project) (state.Agent, error) {
	m := s.manager(s.cfg.Log)
	if a.IsHome() {
		return m.Home(), nil
	}
	if a.Status == "" {
		// Never chatted with: nothing to move, and EnsureLead resolves the
		// account when the first message makes it.
		return a, nil
	}
	account, err := m.LeadAccountFor(ctx, a)
	if err != nil {
		return a, err
	}
	if account != a.ClaudeAccount {
		if err := s.store.SetAgentClaudeAccount(ctx, a.Project, a.Name, account); err != nil {
			return a, err
		}
		a.ClaudeAccount = account
	}
	return a, nil
}

// switchIfPending starts the tool switch a lead's composer chose, at a moment
// rolloverIfNeeded would compact: the session that is about to go writes the
// rollover summary into the project's memory and the next brief, and the
// chat holds what arrives until the chosen tool's session is in place. It
// reports whether it started one. A chat it can't start one on — off, busy,
// or the Home chat, which has no project memory — switches as its next turn
// begins instead, telling the new tool the conversation (chat/runner.go).
func (s *Server) switchIfPending(ctx context.Context, a state.Agent) bool {
	if a.IsHome() || s.chat.PendingTool(a) == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), compactTimeout)
	finish, err := s.chat.BeginSwitch(ctx, a, switchAsk, func(answer string, askErr error) error {
		return s.settleCompaction(ctx, a, answer, askErr)
	})
	if err != nil {
		cancel()
		if !errors.Is(err, chat.ErrBusy) && !chat.IsNoSession(err) {
			s.logf("switching the %s chat's AI tool: %v", a.Project, err)
		}
		return false
	}
	s.logf("switching the %s chat's AI tool: summarising its session first", a.Project)
	go func() {
		defer cancel()
		if err := finish(); err != nil && !errors.Is(err, chat.ErrBusy) {
			s.logf("switching the %s chat's AI tool: %v", a.Project, err)
		}
	}()
	return true
}

// switchAsk is consolidationAsk for a tool switch: the same summary, asked for
// a different reason.
var switchAsk = strings.Replace(consolidationAsk,
	"Your session has nearly filled its context, so AgentBox is about to continue this conversation in a fresh one.",
	"The user has switched this conversation to another AI tool, so AgentBox is about to continue it in a fresh session there.", 1)
