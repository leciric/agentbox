package daemon

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// Codex, OpenCode and Cursor accounts (credentials/toolaccounts.go): stored,
// renamed, made the default and removed under Settings → Accounts or with
// `agentbox auth <tool>`, and picked per project. Cursor's sign-ins are
// cursor.go's; a ChatGPT or OAuth sign-in for Codex or OpenCode runs the tool's
// own login, which only the CLI can (cli/auth.go).

// accountMap is an AccountMap as the API has it: never nil.
func accountMap(m state.AccountMap) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// toolAccounts lists a tool's stored accounts for the API.
func toolAccounts(creds credentials.Store, tool string) ([]api.ToolAccount, error) {
	stored, err := creds.ToolAccounts(tool)
	if err != nil {
		return nil, err
	}
	out := make([]api.ToolAccount, 0, len(stored))
	for _, a := range stored {
		acc := api.ToolAccount{Tool: a.Tool, Provider: a.Provider, Name: a.Name, Default: a.Default, SavedAt: a.SavedAt,
			Kind: a.Kind, Email: a.Email, Expired: a.Expired}
		if !a.Expiry.IsZero() {
			expiry := a.Expiry
			acc.Expiry = &expiry
		}
		out = append(out, acc)
	}
	return out, nil
}

// checkToolAccount refuses a Codex or Cursor account that isn't stored, and
// an OpenCode account a provider doesn't have; "" is the machine's default.
func (s *Server) checkToolAccount(tool, provider, account string) error {
	if account == "" {
		return nil
	}
	ok, err := s.manager(nil).Creds.HasToolAccount(tool, provider, account)
	if err != nil {
		return err
	}
	if !ok {
		if provider != "" {
			return fmt.Errorf("no OpenCode account named %q for %s: add it with agentbox auth opencode --account %s", account, provider, account)
		}
		return fmt.Errorf("no %s account named %q: add it with agentbox auth %s --account %s", credentials.ToolName(tool), account, tool, account)
	}
	return nil
}

// checkOpenCodeAccounts cleans and checks a project's OpenCode accounts.
func (s *Server) checkOpenCodeAccounts(accounts map[string]string) (state.AccountMap, error) {
	clean := state.AccountMap{}
	for provider, account := range accounts {
		provider, account = strings.TrimSpace(provider), strings.TrimSpace(account)
		if account == "" {
			continue
		}
		if err := s.checkToolAccount(credentials.ToolOpenCode, provider, account); err != nil {
			return nil, err
		}
		clean[provider] = account
	}
	return clean, nil
}

// newProjectToolAccounts are the other tools' accounts a project is added
// with, checked.
func (s *Server) newProjectToolAccounts(req api.AddProjectRequest) (state.Project, error) {
	p := state.Project{CodexAccount: strings.TrimSpace(req.CodexAccount), CursorAccount: strings.TrimSpace(req.CursorAccount)}
	if err := s.checkToolAccount(credentials.ToolCodex, "", p.CodexAccount); err != nil {
		return p, err
	}
	if err := s.checkToolAccount(credentials.ToolCursor, "", p.CursorAccount); err != nil {
		return p, err
	}
	var err error
	p.OpenCodeAccounts, err = s.checkOpenCodeAccounts(req.OpenCodeAccounts)
	return p, err
}

// updateProjectToolAccounts applies UpdateProjectRequest's Codex, OpenCode
// and Cursor accounts. New agents get them; the project's chat moves to them
// at once when it runs that tool (followToolAccounts).
func (s *Server) updateProjectToolAccounts(ctx context.Context, p *state.Project, req api.UpdateProjectRequest) error {
	changed := false
	for tool, account := range map[string]*string{credentials.ToolCodex: req.CodexAccount, credentials.ToolCursor: req.CursorAccount} {
		if account == nil {
			continue
		}
		name := strings.TrimSpace(*account)
		if err := s.checkToolAccount(tool, "", name); err != nil {
			return err
		}
		if err := s.store.SetProjectToolAccount(ctx, p.Name, tool, name); err != nil {
			return err
		}
		if tool == credentials.ToolCodex {
			p.CodexAccount = name
		} else {
			p.CursorAccount = name
		}
		changed = true
	}
	if req.OpenCodeAccounts != nil {
		accounts, err := s.checkOpenCodeAccounts(*req.OpenCodeAccounts)
		if err != nil {
			return err
		}
		if err := s.store.SetProjectOpenCodeAccounts(ctx, p.Name, accounts); err != nil {
			return err
		}
		p.OpenCodeAccounts = accounts
		changed = true
	}
	if !changed {
		return nil
	}
	s.followToolAccounts(ctx, p.Name)
	s.events.publish(api.EventProject, api.ProjectChange{Name: p.Name})
	return nil
}

// followToolAccounts moves a project's chat, when it runs Codex, OpenCode or
// Cursor, to the account it should run on now: after the project's account
// changed, or an account was made the default, renamed or removed. Its
// adapter restarts on it (chat.Manager.AccountChanged), which writes the new
// login into its HOME as it starts.
func (s *Server) followToolAccounts(ctx context.Context, project string) {
	lead, err := s.store.Agent(ctx, project, state.LeadName)
	if err != nil || lead.Status == "" || !slices.Contains(credentials.AccountTools, lead.AI) {
		return
	}
	moved, err := s.leadOnAccount(ctx, lead, state.Project{})
	if err != nil {
		s.logf("moving the %s chat to its %s account: %v", project, credentials.ToolName(lead.AI), err)
		return
	}
	s.chat.AccountChanged(moved)
}

// followAllToolAccounts is followToolAccounts for every project.
func (s *Server) followAllToolAccounts(ctx context.Context) {
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return
	}
	for _, p := range projects {
		s.followToolAccounts(ctx, p.Name)
	}
}

// saveToolAccount answers POST /v1/auth/accounts/{tool}: a Codex or OpenCode
// login from an API key. Cursor's key is checked with Cursor first
// (saveCursorKey).
func (s *Server) saveToolAccount(w http.ResponseWriter, r *http.Request) error {
	tool := r.PathValue("tool")
	if tool == credentials.ToolCursor {
		return s.saveCursorKey(w, r)
	}
	var req api.ToolAccountRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	account := cmp.Or(strings.TrimSpace(req.Account), credentials.DefaultAccount)
	provider := strings.TrimSpace(req.Provider)
	var auth []byte
	var err error
	switch tool {
	case credentials.ToolCodex:
		auth, err = credentials.CodexAPIKeyAuth(req.APIKey)
	case credentials.ToolOpenCode:
		auth, err = credentials.OpenCodeAPIKeyAuth(req.APIKey)
	default:
		return fmt.Errorf("%q has no accounts: use codex, opencode or cursor", tool)
	}
	if err != nil {
		return err
	}
	if err := s.manager(nil).Creds.SaveToolAccount(tool, provider, account, auth); err != nil {
		return err
	}
	s.logf("Stored the %s account %q", credentials.ToolName(tool), account)
	s.followAllToolAccounts(r.Context())
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// toolAccountFromPath is the tool, OpenCode's provider and the account a path
// names.
func toolAccountFromPath(r *http.Request) (tool, provider, account string) {
	return r.PathValue("tool"), strings.TrimSpace(r.URL.Query().Get("provider")), r.PathValue("account")
}

// removeToolAccount answers DELETE /v1/auth/accounts/{tool}/{account}.
// Agents already created keep the login they were given.
func (s *Server) removeToolAccount(w http.ResponseWriter, r *http.Request) error {
	tool, provider, account := toolAccountFromPath(r)
	if err := s.forgetToolAccount(r.Context(), tool, provider, account); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) forgetToolAccount(ctx context.Context, tool, provider, account string) error {
	if err := s.manager(nil).Creds.RemoveToolAccount(tool, provider, account); err != nil {
		return err
	}
	s.logf("Removed the %s account %q", credentials.ToolName(tool), account)
	if tool == credentials.ToolCursor {
		if err := s.cursorAccountGone(ctx, account); err != nil {
			return err
		}
	}
	s.followAllToolAccounts(ctx)
	return nil
}

// setDefaultToolAccount answers POST /v1/auth/accounts/{tool}/{account}/default.
func (s *Server) setDefaultToolAccount(w http.ResponseWriter, r *http.Request) error {
	tool, provider, account := toolAccountFromPath(r)
	if err := s.manager(nil).Creds.SetDefaultToolAccount(tool, provider, account); err != nil {
		return err
	}
	if tool == credentials.ToolCursor {
		s.refreshCursorModels(true)
	}
	s.followAllToolAccounts(r.Context())
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// renameToolAccount answers POST /v1/auth/accounts/{tool}/{account}/rename,
// carrying every reference to the account over (state.RenameToolAccount). The
// login doesn't change, so agents holding it keep running.
func (s *Server) renameToolAccount(w http.ResponseWriter, r *http.Request) error {
	var req api.RenameToolAccountRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	tool, provider, old := toolAccountFromPath(r)
	name := strings.TrimSpace(req.Name)
	creds := s.manager(nil).Creds
	// The store's checks come first, so a refused name leaves the database
	// alone; the move itself runs again inside the transaction.
	if err := credentials.ValidateAccount(name); err != nil {
		return err
	}
	if ok, err := creds.HasToolAccount(tool, provider, old); err != nil {
		return err
	} else if !ok {
		return s.checkToolAccount(tool, provider, old)
	}
	moved := false
	done, err := s.store.RenameToolAccount(r.Context(), tool, provider, old, name, func() error {
		if err := creds.RenameToolAccount(tool, provider, old, name); err != nil {
			return err
		}
		moved = true
		return nil
	})
	if err != nil {
		if moved {
			if undo := creds.RenameToolAccount(tool, provider, name, old); undo != nil {
				s.logf("renaming the %s account %q back from %q: %v", credentials.ToolName(tool), old, name, undo)
			}
		}
		return err
	}
	s.logf("Renamed the %s account %q to %q", credentials.ToolName(tool), old, name)
	for _, p := range done.Projects {
		s.events.publish(api.EventProject, api.ProjectChange{Name: p})
	}
	// The chats holding it carry on with the same login, now under its new name.
	for _, ref := range done.Agents {
		project, agentName, _ := strings.Cut(ref, "/")
		if a, err := s.store.Agent(r.Context(), project, agentName); err == nil && a.IsLead() {
			s.chat.AccountRenamed(a)
		}
	}
	out := api.RenamedToolAccount{Tool: tool, Provider: provider, Old: old, Name: name,
		Projects: nonNil(done.Projects), Agents: nonNil(done.Agents)}
	return writeJSON(w, http.StatusOK, out)
}

// leadToolLogins is the chat.Manager hook that gives a lead switching to
// Codex, OpenCode or Cursor the accounts it runs that tool on.
func (s *Server) leadToolLogins(a state.Agent) (state.Agent, error) {
	logins, err := s.manager(nil).LeadLogins(context.Background(), a)
	if err != nil {
		return a, err
	}
	a.CodexAccount, a.OpenCodeAccounts, a.CursorAccount = logins.Codex, logins.OpenCode, logins.Cursor
	return a, nil
}

// leadToolRows are the Codex or Cursor accounts a lead's composer offers,
// with the project's marked.
func (s *Server) leadToolRows(p state.Project, tool string) ([]api.LeadAccount, error) {
	accounts, err := s.manager(nil).Creds.ToolAccounts(tool)
	if err != nil {
		return nil, err
	}
	own := map[string]string{credentials.ToolCodex: p.CodexAccount, credentials.ToolCursor: p.CursorAccount}[tool]
	if own == "" {
		for _, a := range accounts {
			if a.Default {
				own = a.Name
			}
		}
	}
	// Only Codex has usage readings (codexStatus); Cursor's rows have none.
	readings := map[string]state.ToolLimitReading{}
	if tool == credentials.ToolCodex {
		list, err := s.store.ToolLimits(context.Background(), tool)
		if err != nil {
			return nil, err
		}
		for _, r := range list {
			readings[r.Account] = r
		}
	}
	var rows []api.LeadAccount
	for _, a := range accounts {
		if a.Expired {
			continue
		}
		row := api.LeadAccount{Name: a.Name, Default: a.Default, Project: a.Name == own}
		if r, ok := readings[a.Name]; ok {
			if limit, ok := toCodexLimit(r, a.Default); ok {
				row.Limit = &limit
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// formatOpenCode says which account each OpenCode provider is on.
func formatOpenCode(m state.AccountMap) string {
	var parts []string
	for _, p := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, p+": "+m[p])
	}
	return strings.Join(parts, ", ")
}
