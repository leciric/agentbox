package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// geminiModelsEvery is how often the daemon asks Gemini CLI for its menu
// again while no chat has named one.
const geminiModelsEvery = time.Hour

// refreshGeminiModels asks Gemini CLI on this machine which models the
// default login can run (agent.GeminiModels) and remembers the answer, in the
// background and at most once an hour unless force says the login just
// changed. A Gemini CLI chat's session replaces it with its own menu
// (chat.rememberChoices). Nothing here fails a request: no login, no Node, or
// no answer leaves the menu as it was.
func (s *Server) refreshGeminiModels(force bool) {
	m := s.manager(nil)
	if !m.GeminiReady() {
		return
	}
	s.mu.Lock()
	if s.geminiModels.running || !force && time.Since(s.geminiModels.last) < geminiModelsEvery {
		s.mu.Unlock()
		return
	}
	s.geminiModels.running, s.geminiModels.last = true, time.Now()
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			s.geminiModels.running = false
			s.mu.Unlock()
		}()
		ctx := s.background()
		ask := func(ctx context.Context) ([]acp.ConfigChoice, error) { return m.GeminiModels(ctx, "") }
		if s.geminiModelsFor != nil {
			ask = s.geminiModelsFor
		}
		models, err := ask(ctx)
		if err != nil {
			s.logf("asking Gemini CLI which models it can run: %v", err)
			return
		}
		if len(models) == 0 {
			return
		}
		choices := make([]api.ChatOptionChoice, len(models))
		for i, c := range models {
			choices[i] = api.ChatOptionChoice{Value: c.Value, Name: c.Name, Description: c.Description}
		}
		raw, err := json.Marshal(choices)
		if err != nil {
			return
		}
		if err := s.store.SetSetting(ctx, state.SettingGeminiModelChoices, string(raw)); err != nil {
			s.logf("remembering Gemini CLI's model menu: %v", err)
		}
	}()
}

// startGeminiLogin answers POST /v1/auth/gemini/login: Gemini CLI's Google
// sign-in run on this machine (agent.GeminiSignIn), kept as an account. It
// waits on the browser, so it is a job, followed like a Claude Code login:
// the app or the CLI shows the page to approve and sends the code it ends on.
func (s *Server) startGeminiLogin(w http.ResponseWriter, r *http.Request) error {
	var req api.ClaudeLoginRequest
	if r.ContentLength != 0 {
		if err := readJSON(r, &req); err != nil {
			return err
		}
	}
	account := cmp.Or(strings.TrimSpace(req.Account), credentials.DefaultAccount)
	if err := credentials.ValidateAccount(account); err != nil {
		return err
	}
	login := &claudeLogin{account: account, codes: make(chan string, 1)}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.geminiLogins {
		if j, ok := s.jobs.get(id); ok && !j.snapshot().Done() {
			return fmt.Errorf("a Gemini CLI sign-in is already running (job %s): finish it, or cancel it, before starting another", id)
		}
		delete(s.geminiLogins, id)
	}
	registered := make(chan struct{})
	j, err := s.jobs.start("gemini-login", account, func(ctx context.Context, log io.Writer) (any, error) {
		<-registered
		began := time.Now()
		out, err := s.runGeminiLogin(ctx, log, login)
		signedIn := setupReport{step: setupSignedIn, tool: "gemini", took: time.Since(began)}
		if err != nil {
			s.setupStepFailed(context.WithoutCancel(ctx), signedIn, err)
		} else {
			s.setupReached(ctx, signedIn)
		}
		return out, err
	})
	if err != nil {
		return err
	}
	s.geminiLogins[j.snapshot().ID] = login
	close(registered)
	return writeJSON(w, http.StatusAccepted, j.snapshot())
}

func (s *Server) runGeminiLogin(ctx context.Context, log io.Writer, login *claudeLogin) (any, error) {
	status := func(detail string) { _, _ = fmt.Fprintf(log, "==> %s\n", detail) }
	status("Signing in to Gemini CLI as the account " + login.account)
	creds, err := s.manager(log).GeminiSignIn(ctx, agent.SetupTokenEvents{
		Status: status,
		Paste: func(url string) {
			login.setCodeURL(url)
			status("Waiting for you to approve the sign-in in your browser and paste the code it gives you")
		},
	}, login.codes)
	if err != nil {
		return nil, err
	}
	status("Saving the sign-in for agents")
	if err := s.manager(nil).Creds.SaveToolAccount(credentials.ToolGemini, "", login.account, creds); err != nil {
		return nil, err
	}
	s.refreshGeminiModels(true)
	s.followAllToolAccounts(context.WithoutCancel(ctx))
	return map[string]string{"account": login.account}, nil
}

// geminiLoginStatus answers GET /v1/auth/gemini/login/{job}, in a Claude Code
// login's shape: the page to approve is CodeURL, as it ends in a code.
func (s *Server) geminiLoginStatus(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("job")
	info, _, err := s.jobs.lookup(r.Context(), id)
	if err != nil {
		return err
	}
	out := api.ClaudeLogin{Job: info.ID, Account: info.Target, Status: info.Status, Error: info.Error}
	s.mu.Lock()
	login := s.geminiLogins[id]
	s.mu.Unlock()
	if login != nil {
		out.Account = login.account
		_, out.CodeURL = login.urls()
	}
	return writeJSON(w, http.StatusOK, out)
}

// geminiLoginCode answers POST /v1/auth/gemini/login/{job}/code: the code the
// approved page showed, typed at the waiting Gemini CLI's prompt.
func (s *Server) geminiLoginCode(w http.ResponseWriter, r *http.Request) error {
	var req api.ClaudeLoginCodeRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		return errors.New("no code: copy it from the page you approved")
	}
	id := r.PathValue("job")
	info, _, err := s.jobs.lookup(r.Context(), id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	login := s.geminiLogins[id]
	s.mu.Unlock()
	if login == nil || info.Done() {
		return fmt.Errorf("the Gemini CLI sign-in %s isn't running any more: start another one", id)
	}
	select {
	case login.codes <- code:
	case <-r.Context().Done():
		return r.Context().Err()
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
