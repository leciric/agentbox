package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"

	"agentbox/internal/agent"
	"agentbox/internal/android"
	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/hostsetup"
	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// setup checks what AgentBox needs on this machine, for the app's Setup page
// and `agentbox host check`.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var checks []api.SetupCheck
	check := func(c api.SetupCheck, ok bool, okDetail string) {
		if ok {
			c.Status, c.Detail = api.SetupOK, okDetail
		}
		checks = append(checks, c)
	}

	_, incusErr := s.cfg.Incus.Run(ctx, "query", "/1.0")
	incusCheck := api.SetupCheck{ID: "incus", Title: "Incus", Required: true, Status: api.SetupMissing, Detail: firstLine(incusErr), Fix: hostsetup.Command}
	if errors.Is(incusErr, exec.ErrNotFound) {
		incusCheck.Detail = "Incus isn't installed"
	}
	// Host setup puts an ACL for this user on the Incus socket, which applies
	// to processes already running, so joining incus-admin is the fallback
	// rather than the route. Only when that ACL isn't there — no setfacl, or a
	// filesystem without ACLs — is logging in again what's left to do.
	if incusErr != nil && !incus.CanOpenSocket() && joinedButInactive(s.cfg.User.Name, "incus-admin") {
		incusCheck.Detail = "you're in incus-admin, but this session started before that: log out and back in, then open AgentBox again"
		incusCheck.Fix = "log out and back in"
	}
	check(incusCheck, incusErr == nil, "installed, and your user can use it")

	hostErr := image.CheckHost(s.cfg.User)
	check(api.SetupCheck{ID: "host", Title: "User mapping", Required: true, Status: api.SetupMissing, Detail: firstLine(hostErr), Fix: hostsetup.Command},
		hostErr == nil, "agents write files in their worktrees as you")

	// What the next build would include, and what the image really has: they
	// differ after someone turns a component on and hasn't rebuilt yet.
	wanted, err := s.imageComponents(ctx)
	if err != nil {
		return err
	}
	installed, _ := image.InstalledBuild(ctx, s.cfg.Incus)

	built, _ := image.Ready(ctx, s.cfg.Incus)

	base := api.SetupCheck{ID: "image", Title: "Base image", Required: true, Status: api.SetupMissing, Detail: "needs Incus first", Fix: "agentbox image build"}
	if incusErr == nil {
		base = s.baseImageCheck(image.PlanFor(built, installed, wanted), installed, wanted)
	}
	checks = append(checks, base)

	creds := s.manager(nil).Creds
	accounts, _ := creds.ClaudeAccounts()
	claude := api.SetupCheck{ID: "claude", Title: "Claude Code login", Status: api.SetupMissing, Detail: "agents can't run Claude Code yet", Fix: "agentbox auth claude"}
	if len(accounts) > 0 {
		claude.Status, claude.Detail = api.SetupOK, describeClaudeAccounts(accounts)
		if def, _ := creds.DefaultClaudeAccount(); creds.ClaudeValidity(def).State == credentials.TokenRejected {
			// A stored token still counts as a login: the rejection is worth
			// surfacing, not a reason to hold up the wizard on it. It also isn't
			// proof the token is dead on its own — the periodic check calls
			// /api/oauth/profile, which a `setup-token` login may not have the
			// scope for even while the token keeps working for real chat
			// traffic, so treat this as a caveat, not a missing login.
			claude.Status = api.SetupWarn
			claude.Detail = fmt.Sprintf("Anthropic rejected the token of %q: log in again to store a new one", def)
		}
	}
	checks = append(checks, claude)
	// Whatever was said above is the last answer written down; this asks for a
	// new one when that has aged out, for the next poll to show.
	s.refreshClaudeTokens(creds)
	// Codex is optional in the image as well as in the credentials, and the
	// image comes first: a login is no use to an agent whose machine has no
	// Codex to run. An image from before options were recorded reads as having
	// none, which is fair — it is outdated, so it needs a rebuild either way.
	codex := api.SetupCheck{ID: "codex", Title: "Codex login", Status: api.SetupOptional, Detail: "optional: agents can't run Codex yet", Fix: "agentbox auth codex"}
	if built && !installed.Components.Codex {
		codex.Detail, codex.Fix = "optional: "+image.CodexMissing, "agentbox image build --codex"
		checks = append(checks, codex)
	} else {
		check(codex, creds.HasCodexLogin(), "AgentBox has its own login for agents")
	}

	// OpenCode is optional in the image and in the credentials too, and reads
	// the same way as Codex above: the image first, then the login.
	opencodeCheck := api.SetupCheck{ID: "opencode", Title: "OpenCode login", Status: api.SetupOptional,
		Detail: "optional: agents can't run OpenCode yet", Fix: "agentbox auth opencode"}
	if built && !installed.Components.OpenCode {
		opencodeCheck.Detail, opencodeCheck.Fix = "optional: "+image.OpenCodeMissing, "agentbox image build --opencode"
		checks = append(checks, opencodeCheck)
	} else {
		check(opencodeCheck, creds.HasOpenCodeLogin(), "AgentBox has its own OpenCode login for agents")
	}

	if sdk, err := s.manager(nil).AndroidHost(); err == nil {
		checks = append(checks, api.SetupCheck{ID: "android", Title: "Android emulators", Status: api.SetupOK,
			Detail: fmt.Sprintf("KVM, and the Android SDK in %s with %s", sdk.Path, sdk.Images[0].ID)})
	} else if agent.AndroidUnsupported() != nil {
		// Nothing to install makes them work here, so there's no fix to offer.
		checks = append(checks, api.SetupCheck{ID: "android", Title: "Android emulators", Status: api.SetupOptional,
			Detail: "off: " + firstLine(err)})
	} else {
		checks = append(checks, api.SetupCheck{ID: "android", Title: "Android emulators", Status: api.SetupOptional,
			Detail: "optional: " + firstLine(err), Fix: android.InstallHint})
	}

	s.mu.Lock()
	preview := s.previewAddr
	s.mu.Unlock()
	check(api.SetupCheck{ID: "preview", Title: "Preview URLs", Status: api.SetupOptional, Detail: "off: another program uses the port, or AGENTBOX_PREVIEW_ADDR turned it off"},
		preview != "", "http://<port>.<agent>.<project>.localhost:"+preview[strings.LastIndex(preview, ":")+1:])

	ready := true
	for _, c := range checks {
		// An image whose tools are being updated is still the one agents use.
		if c.Required && c.Status != api.SetupOK && c.Status != api.SetupUpdating {
			ready = false
		}
	}
	return writeJSON(w, http.StatusOK, api.SetupStatus{
		Ready:  ready,
		Checks: checks,
		Image: api.ImageBuild{
			Version:    image.Version,
			Components: toAPIImageComponents(wanted),
			Installed:  toAPIImageComponents(installed.Components),
			Downloads:  apiDownloads(image.Downloads),
			Hint:       image.DownloadsHint,
		},
	})
}

// imageComponents is the optional components chosen for this installation's
// base image. Nothing chosen means none, so a first build is the smallest one.
func (s *Server) imageComponents(ctx context.Context) (image.Components, error) {
	android, err := s.store.Flag(ctx, state.SettingImageAndroid)
	if err != nil {
		return image.Components{}, err
	}
	codex, err := s.store.Flag(ctx, state.SettingImageCodex)
	if err != nil {
		return image.Components{}, err
	}
	opencode, err := s.store.Flag(ctx, state.SettingImageOpenCode)
	if err != nil {
		return image.Components{}, err
	}
	devCaches, err := s.store.Flag(ctx, state.SettingImageDevCaches)
	if err != nil {
		return image.Components{}, err
	}
	return image.Components{Android: android, Codex: codex, OpenCode: opencode, DevCaches: devCaches}, nil
}

func toAPIImageComponents(c image.Components) api.ImageComponents {
	return api.ImageComponents{Android: c.Android, Codex: c.Codex, OpenCode: c.OpenCode, DevCaches: c.DevCaches}
}

func apiDownloads(downloads []image.Download) []api.ImageDownload {
	out := make([]api.ImageDownload, 0, len(downloads))
	for _, d := range downloads {
		out = append(out, api.ImageDownload{Name: d.Name, Purpose: d.Purpose, MB: d.MB, Option: d.Option})
	}
	return out
}

// describeClaudeAccounts says which accounts agents can be given, and which one
// they get by default.
func describeClaudeAccounts(accounts []credentials.ClaudeAccount) string {
	if len(accounts) == 1 {
		return "AgentBox has its own login for agents"
	}
	names := make([]string, 0, len(accounts))
	for _, a := range accounts {
		if a.Default {
			names = append(names, a.Name+" (default)")
			continue
		}
		names = append(names, a.Name)
	}
	return fmt.Sprintf("%d accounts for agents: %s", len(accounts), strings.Join(names, ", "))
}

func (s *Server) saveClaudeToken(w http.ResponseWriter, r *http.Request) error {
	var req api.ClaudeTokenRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Token) == "" {
		return errors.New("the token is empty: run claude setup-token in a terminal, and paste the token it prints")
	}
	if err := s.manager(nil).Creds.SaveClaudeToken(req.Account, req.Token); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// claudeLogin is one in-app `claude setup-token` run: what the app shows while
// it waits, and the way back in for the code a browser that can't reach this
// machine ends on.
type claudeLogin struct {
	account string
	codes   chan string

	mu      sync.Mutex
	url     string
	codeURL string
}

func (l *claudeLogin) setURL(url string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.url = url
}

func (l *claudeLogin) setCodeURL(url string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.codeURL = url
}

// urls is the page to approve and the fallback one, each empty until Claude
// Code has produced it. The fallback deliberately doesn't stand in for the
// first: they are two different logins, and opening both would leave the user
// with two browser tabs and only one of them worth finishing.
func (l *claudeLogin) urls() (url, codeURL string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.url, l.codeURL
}

// startClaudeLogin runs `claude setup-token` on this machine and stores the
// token it mints, so logging in never means a terminal and a paste (D59). It
// waits for a browser, so it is a job: the app follows it, shows the page to
// approve, and can cancel it.
func (s *Server) startClaudeLogin(w http.ResponseWriter, r *http.Request) error {
	var req api.ClaudeLoginRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	account := strings.TrimSpace(req.Account)
	if account == "" {
		account = credentials.DefaultAccount
	}
	if err := credentials.ValidateAccount(account); err != nil {
		return err
	}
	job, err := s.claimClaudeLogin(&claudeLogin{account: account, codes: make(chan string, 1)})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, job)
}

// claimClaudeLogin starts the login's job, under the lock that decides there is
// only one: a second login would share this machine's browser and the first
// one's prompt, so it is refused rather than left to confuse it. Finished
// logins are forgotten here, which is the only place the map grows.
func (s *Server) claimClaudeLogin(login *claudeLogin) (api.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.claudeLogins {
		if j, ok := s.jobs.get(id); ok && !j.snapshot().Done() {
			return api.Job{}, fmt.Errorf("a Claude Code login is already running (job %s): finish it, or cancel it, before starting another", id)
		}
		delete(s.claudeLogins, id)
	}
	// The job mustn't run before it is one the app can ask about: it reports
	// the page to approve within a second of starting.
	registered := make(chan struct{})
	j, err := s.jobs.start("claude-login", login.account, func(ctx context.Context, log io.Writer) (any, error) {
		<-registered
		return s.runClaudeLogin(ctx, log, login)
	})
	if err != nil {
		return api.Job{}, err
	}
	s.claudeLogins[j.snapshot().ID] = login
	close(registered)
	return j.snapshot(), nil
}

func (s *Server) runClaudeLogin(ctx context.Context, log io.Writer, login *claudeLogin) (any, error) {
	status := func(detail string) { fmt.Fprintf(log, "==> %s\n", detail) }
	status("Logging in to Claude Code as the account " + login.account)
	token, err := s.manager(log).SetupToken(ctx, agent.SetupTokenEvents{
		Status: status,
		Browser: func(url string) {
			login.setURL(url)
			status("Waiting for you to approve the login in your browser")
		},
		Paste: func(url string) { login.setCodeURL(url) },
	}, login.codes)
	if err != nil {
		return nil, err
	}
	status("Saving the token for agents")
	if err := s.manager(nil).Creds.SaveClaudeToken(login.account, token); err != nil {
		return nil, err
	}
	return map[string]string{"account": login.account}, nil
}

// claudeLoginStatus is what the app polls while a login runs: the job's status,
// and the pages to open.
func (s *Server) claudeLoginStatus(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("job")
	info, _, err := s.jobs.lookup(r.Context(), id)
	if err != nil {
		return err
	}
	out := api.ClaudeLogin{Job: info.ID, Account: info.Target, Status: info.Status, Error: info.Error}
	if login := s.claudeLoginByJob(id); login != nil {
		out.Account = login.account
		out.URL, out.CodeURL = login.urls()
	}
	return writeJSON(w, http.StatusOK, out)
}

// claudeLoginCode types the code from the fallback page into the waiting
// Claude Code, for a browser that couldn't reach this machine's callback.
func (s *Server) claudeLoginCode(w http.ResponseWriter, r *http.Request) error {
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
	login := s.claudeLoginByJob(id)
	if login == nil || info.Done() {
		return fmt.Errorf("the Claude Code login %s isn't running any more: start another one", id)
	}
	select {
	case login.codes <- code:
	case <-r.Context().Done():
		return r.Context().Err()
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) claudeLoginByJob(id string) *claudeLogin {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claudeLogins[id]
}

func (s *Server) removeClaudeAccount(w http.ResponseWriter, r *http.Request) error {
	// Agents already created keep the token that was written into them. A
	// project still pointing at this account is left alone on purpose: its next
	// agent fails with a clear error, rather than quietly billing another account.
	if err := s.manager(nil).Creds.RemoveClaudeAccount(r.PathValue("account")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) setDefaultClaudeAccount(w http.ResponseWriter, r *http.Request) error {
	if err := s.manager(nil).Creds.SetDefaultClaudeAccount(r.PathValue("account")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// renameClaudeAccount gives a stored account another name and carries every
// reference to it over: the machine default, each project's account and
// allow-list, each agent's account and the leads', and its usage-limit
// reading. The token doesn't change, so agents on it keep running as they are.
func (s *Server) renameClaudeAccount(w http.ResponseWriter, r *http.Request) error {
	var req api.RenameClaudeAccountRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	old, name := r.PathValue("account"), strings.TrimSpace(req.Name)
	creds := s.manager(nil).Creds
	// The store's checks come first, so a refused name leaves the database
	// alone; the move itself runs again inside the transaction.
	if err := credentials.ValidateAccount(name); err != nil {
		return err
	}
	if taken, err := creds.HasClaudeAccount(name); err != nil {
		return err
	} else if taken && name != old {
		return fmt.Errorf("there is already a Claude Code account named %q: remove it first, or pick another name", name)
	}
	if err := s.claudeLoginFor(old, name); err != nil {
		return err
	}
	moved := false
	done, err := s.store.RenameClaudeAccount(r.Context(), old, name, func() error {
		if err := creds.RenameClaudeAccount(old, name); err != nil {
			return err
		}
		moved = true
		return nil
	})
	if err != nil {
		if moved {
			// The transaction didn't commit, so the token goes back to the
			// name the database still has.
			if undo := creds.RenameClaudeAccount(name, old); undo != nil {
				s.logf("renaming the Claude Code account %q back from %q: %v", old, name, undo)
			}
		}
		return err
	}
	s.logf("Renamed the Claude Code account %q to %q", old, name)
	s.chat.RenameClaudeAccount(old, name)
	// Every project's chat brief may list the account by name, whether or not
	// the project picked it, so each lead gets its brief written again.
	if projects, err := s.store.Projects(r.Context()); err == nil {
		for _, p := range projects {
			if err := s.manager(nil).ReconfigureLead(r.Context(), p.Name); err != nil {
				s.logf("reconfiguring the %s chat: %v", p.Name, err)
			}
		}
	}
	for _, p := range done.Projects {
		s.events.publish(api.EventProject, api.ProjectChange{Name: p})
	}
	out := api.RenamedClaudeAccount{Old: old, Name: name, Projects: done.Projects, Agents: done.Agents}
	if out.Projects == nil {
		out.Projects = []string{}
	}
	if out.Agents == nil {
		out.Agents = []string{}
	}
	return writeJSON(w, http.StatusOK, out)
}

// claudeLoginFor refuses a rename while a login is running for either name:
// it would store its token under a name that has just been freed or taken.
func (s *Server) claudeLoginFor(names ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, login := range s.claudeLogins {
		if j, ok := s.jobs.get(id); ok && !j.snapshot().Done() && slices.Contains(names, login.account) {
			return fmt.Errorf("a Claude Code login for %q is running (job %s): finish it, or cancel it, before renaming", login.account, id)
		}
	}
	return nil
}

// groupFile is where the machine's groups are listed. A variable so tests can
// put a group this process isn't in somewhere harmless.
var groupFile = "/etc/group"

// joinedButInactive reports whether /etc/group lists user in group while this
// process doesn't have it: the user joined it after logging in.
func joinedButInactive(user, group string) bool {
	data, err := os.ReadFile(groupFile)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 4 || fields[0] != group || !slices.Contains(strings.Split(fields[3], ","), user) {
			continue
		}
		gid, err := strconv.Atoi(fields[2])
		if err != nil {
			return false
		}
		groups, _ := os.Getgroups()
		return !slices.Contains(groups, gid)
	}
	return false
}

func firstLine(err error) string {
	if err == nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(err.Error()), "\n")
	return line
}

func cmpOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
