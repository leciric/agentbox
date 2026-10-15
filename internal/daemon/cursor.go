package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/cursor"
	"agentbox/internal/state"
)

// Cursor's sign-in and model menu. Both are the adapter script's own commands
// run on this machine (agent.Manager.CursorHelper), against AgentBox's own
// credentials files (a Cursor account, credentials.Store.ToolAccountPath),
// never the user's ~/.cursor: agents get a copy of their account's file, as
// they get Codex's auth.json.

// cursorModelsEvery is how often the daemon asks Cursor for its menu again.
// Asking is one request, but the menu only changes when Cursor ships a model.
const cursorModelsEvery = time.Hour

// cursorHelper is the adapter's host commands: Manager.CursorHelper, which
// installs Node and the SDK the first time, or a test's stand-in.
func (s *Server) cursorHelper(ctx context.Context) (cursor.Helper, error) {
	if s.cursorHelperFor != nil {
		return s.cursorHelperFor(ctx)
	}
	return s.manager(nil).CursorHelper(ctx, func(string) {})
}

// saveCursorKey keeps an API key from Cursor's dashboard once Cursor says
// whose it is: a key Cursor doesn't know is refused here rather than in every
// agent that would have been given it.
func (s *Server) saveCursorKey(w http.ResponseWriter, r *http.Request) error {
	var req api.CursorKeyRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	key := strings.TrimSpace(req.APIKey)
	if key == "" {
		return errors.New("no API key: make one in Cursor's dashboard, under Integrations → API Keys")
	}
	account := cmp.Or(strings.TrimSpace(req.Account), credentials.DefaultAccount)
	if err := credentials.ValidateAccount(account); err != nil {
		return err
	}
	creds := s.manager(nil).Creds
	helper, err := s.cursorHelper(r.Context())
	if err != nil {
		return err
	}
	scratch, err := cursorScratch(creds, account)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(scratch) }()
	email, err := helper.CheckKey(r.Context(), key, scratch)
	if err != nil {
		return err
	}
	if err := keepCursorLogin(creds, account, scratch); err != nil {
		return err
	}
	s.logf("Stored the Cursor account %q", account)
	s.refreshCursorModels(true)
	return writeJSON(w, http.StatusOK, api.CursorKeyResponse{Email: email})
}

// cursorScratch is where the SDK writes a sign-in for an account before it is
// kept: beside the accounts, under a name that isn't one.
func cursorScratch(creds credentials.Store, account string) (string, error) {
	path := creds.ToolAccountPath(credentials.ToolCursor, "", account) + ".signin"
	return path, os.MkdirAll(filepath.Dir(path), 0o700)
}

// keepCursorLogin stores the sign-in the SDK wrote as the account.
func keepCursorLogin(creds credentials.Store, account, scratch string) error {
	b, err := os.ReadFile(scratch)
	if err != nil {
		return err
	}
	return creds.SaveToolAccount(credentials.ToolCursor, "", account, b)
}

// removeCursorLogin forgets the default Cursor account (DELETE
// /v1/auth/cursor, from before Cursor had accounts): removeToolAccount with
// its name.
func (s *Server) removeCursorLogin(w http.ResponseWriter, r *http.Request) error {
	account, err := s.manager(nil).Creds.DefaultToolAccount(credentials.ToolCursor, "")
	if err != nil {
		return err
	}
	if account != "" {
		if err := s.forgetToolAccount(r.Context(), credentials.ToolCursor, "", account); err != nil {
			return err
		}
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// cursorAccountGone stops a browser sign-in under way for an account being
// removed, so it can't write one back, and forgets the model menu when no
// Cursor account is left to ask.
func (s *Server) cursorAccountGone(ctx context.Context, account string) error {
	s.mu.Lock()
	if s.cursorLoginCancel != nil && s.cursorLogin.Account == account {
		s.cursorLoginCancel()
		s.cursorLogin = api.CursorLogin{State: api.CursorLoginIdle}
	}
	s.mu.Unlock()
	if s.manager(nil).Creds.HasCursorLogin("") {
		return nil
	}
	return s.store.SetSetting(ctx, state.SettingCursorModelChoices, "")
}

// startCursorLogin starts Cursor's browser sign-in, unless one is already
// under way, and answers with its progress at once: the page to open arrives a
// moment later (after installing the SDK, the first time), so the caller polls
// cursorLoginStatus for it.
func (s *Server) startCursorLogin(w http.ResponseWriter, r *http.Request) error {
	var req api.CursorLoginRequest
	if r.ContentLength != 0 {
		if err := readJSON(r, &req); err != nil {
			return err
		}
	}
	account := cmp.Or(strings.TrimSpace(req.Account), credentials.DefaultAccount)
	if err := credentials.ValidateAccount(account); err != nil {
		return err
	}
	s.mu.Lock()
	if s.cursorLogin.State == api.CursorLoginStarting || s.cursorLogin.State == api.CursorLoginWaiting {
		out := s.cursorLogin
		s.mu.Unlock()
		return writeJSON(w, http.StatusOK, out)
	}
	ctx, cancel := context.WithTimeout(s.background(), cursor.LoginTimeout)
	s.cursorLogin, s.cursorLoginCancel = api.CursorLogin{State: api.CursorLoginStarting, Account: account}, cancel
	out := s.cursorLogin
	s.mu.Unlock()
	go s.runCursorLogin(ctx, cancel, account)
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) runCursorLogin(ctx context.Context, cancel context.CancelFunc, account string) {
	defer cancel()
	// A sign-in a sign-out cancelled has nothing left to say; one that ran out
	// of time says so.
	set := func(update func(*api.CursorLogin)) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !errors.Is(ctx.Err(), context.Canceled) {
			update(&s.cursorLogin)
		}
	}
	fail := func(err error) {
		set(func(l *api.CursorLogin) {
			*l = api.CursorLogin{State: api.CursorLoginFailed, Account: account, Error: err.Error()}
		})
	}
	creds := s.manager(nil).Creds
	helper, err := s.cursorHelper(ctx)
	if err != nil {
		fail(err)
		return
	}
	scratch, err := cursorScratch(creds, account)
	if err != nil {
		fail(err)
		return
	}
	defer func() { _ = os.Remove(scratch) }()
	email, err := helper.Login(ctx, scratch, func(url string) {
		set(func(l *api.CursorLogin) {
			*l = api.CursorLogin{State: api.CursorLoginWaiting, Account: account, URL: url}
		})
	})
	if err == nil && !errors.Is(ctx.Err(), context.Canceled) {
		err = keepCursorLogin(creds, account, scratch)
	}
	if err != nil {
		fail(err)
		return
	}
	set(func(l *api.CursorLogin) {
		*l = api.CursorLogin{State: api.CursorLoginDone, Account: account, Email: email}
	})
	s.refreshCursorModels(true)
}

func (s *Server) cursorLoginStatus(w http.ResponseWriter, _ *http.Request) error {
	s.mu.Lock()
	out := s.cursorLogin
	s.mu.Unlock()
	if out.State == "" {
		out.State = api.CursorLoginIdle
	}
	return writeJSON(w, http.StatusOK, out)
}

// refreshCursorModels asks Cursor which models the sign-in can run and
// remembers the answer, in the background and at most once an hour unless
// force says the sign-in just changed. Nothing here fails a request: no
// sign-in, no Node, or no answer leaves the menu as it was.
func (s *Server) refreshCursorModels(force bool) {
	creds := s.manager(nil).Creds
	login, ok := creds.CursorLogin("")
	if !ok {
		return
	}
	s.mu.Lock()
	if s.cursorModels.running || !force && time.Since(s.cursorModels.last) < cursorModelsEvery {
		s.mu.Unlock()
		return
	}
	s.cursorModels.running, s.cursorModels.last = true, time.Now()
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			s.cursorModels.running = false
			s.mu.Unlock()
		}()
		ctx := s.background()
		helper, err := s.cursorHelper(ctx)
		if err != nil {
			s.logf("asking Cursor which models it can run: %v", err)
			return
		}
		models, err := helper.Models(ctx, login.APIKey)
		if err != nil {
			s.logf("asking Cursor which models it can run: %v", err)
			return
		}
		if len(models) == 0 {
			return
		}
		raw, err := json.Marshal(cursorChoices(models))
		if err != nil {
			return
		}
		if err := s.store.SetSetting(ctx, state.SettingCursorModelChoices, string(raw)); err != nil {
			s.logf("remembering Cursor's model menu: %v", err)
		}
	}()
}

// cursorChoices turns Cursor's models into menu entries, each with the
// effort levels it takes.
func cursorChoices(models []cursor.Model) []api.ChatOptionChoice {
	choices := make([]api.ChatOptionChoice, 0, len(models))
	for _, m := range models {
		if m.Value == "" {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.Value
		}
		choices = append(choices, api.ChatOptionChoice{Value: m.Value, Name: name, Description: m.Description, Efforts: m.Efforts})
	}
	return choices
}
