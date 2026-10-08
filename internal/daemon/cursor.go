package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/cursor"
	"agentbox/internal/state"
)

// Cursor's sign-in and model menu. Both are the adapter script's own commands
// run on this machine (agent.Manager.CursorHelper), against AgentBox's own
// credentials file (credentials.Store.CursorAuthPath), never the user's
// ~/.cursor: agents get a copy of that file, as they get Codex's auth.json.

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
	m := s.manager(nil)
	helper, err := s.cursorHelper(r.Context())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.Creds.CursorHome(), 0o700); err != nil {
		return err
	}
	email, err := helper.CheckKey(r.Context(), key, m.Creds.CursorAuthPath())
	if err != nil {
		return err
	}
	s.refreshCursorModels(true)
	return writeJSON(w, http.StatusOK, api.CursorKeyResponse{Email: email})
}

// removeCursorLogin forgets the sign-in, stopping a browser sign-in under way
// first so it can't write one back.
func (s *Server) removeCursorLogin(w http.ResponseWriter, _ *http.Request) error {
	s.mu.Lock()
	if s.cursorLoginCancel != nil {
		s.cursorLoginCancel()
	}
	s.cursorLogin = api.CursorLogin{State: api.CursorLoginIdle}
	s.mu.Unlock()
	if err := s.manager(nil).Creds.RemoveCursorLogin(); err != nil {
		return err
	}
	if err := s.store.SetSetting(context.Background(), state.SettingCursorModelChoices, ""); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// startCursorLogin starts Cursor's browser sign-in, unless one is already
// under way, and answers with its progress at once: the page to open arrives a
// moment later (after installing the SDK, the first time), so the caller polls
// cursorLoginStatus for it.
func (s *Server) startCursorLogin(w http.ResponseWriter, _ *http.Request) error {
	s.mu.Lock()
	if s.cursorLogin.State == api.CursorLoginStarting || s.cursorLogin.State == api.CursorLoginWaiting {
		out := s.cursorLogin
		s.mu.Unlock()
		return writeJSON(w, http.StatusOK, out)
	}
	ctx, cancel := context.WithTimeout(s.background(), cursor.LoginTimeout)
	s.cursorLogin, s.cursorLoginCancel = api.CursorLogin{State: api.CursorLoginStarting}, cancel
	out := s.cursorLogin
	s.mu.Unlock()
	go s.runCursorLogin(ctx, cancel)
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) runCursorLogin(ctx context.Context, cancel context.CancelFunc) {
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
		set(func(l *api.CursorLogin) { *l = api.CursorLogin{State: api.CursorLoginFailed, Error: err.Error()} })
	}
	m := s.manager(nil)
	helper, err := s.cursorHelper(ctx)
	if err != nil {
		fail(err)
		return
	}
	if err := os.MkdirAll(m.Creds.CursorHome(), 0o700); err != nil {
		fail(err)
		return
	}
	email, err := helper.Login(ctx, m.Creds.CursorAuthPath(), func(url string) {
		set(func(l *api.CursorLogin) { *l = api.CursorLogin{State: api.CursorLoginWaiting, URL: url} })
	})
	if err != nil {
		fail(err)
		return
	}
	set(func(l *api.CursorLogin) { *l = api.CursorLogin{State: api.CursorLoginDone, Email: email} })
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
	login, ok := creds.CursorLogin()
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
