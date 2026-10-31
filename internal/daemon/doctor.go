package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/chat"
	"agentbox/internal/credentials"
	"agentbox/internal/image"
	"agentbox/internal/redact"
	"agentbox/internal/report"
	"agentbox/internal/state"
)

// The doctor (`agentbox doctor`, and the list at the top of Setup) is Setup's
// checks put as what's wrong and what fixes it, with the few Setup doesn't
// make: disk space, the network, and the Home chat's tool. It reuses Setup's
// own checks of Incus, the user mapping, the storage pool and the base image
// rather than a second opinion of each. The front end on the user's machine
// adds what only it can see — the VM, KVM, its own version (hostvm/doctor.go,
// cli/doctor.go) — and asks the daemon for the rest here.
//
// What is safe to try again it fixes when asked: the base image (a download,
// or a build when that fails), and the Home chat's tool and the GitHub CLI
// (both installs). Everything else has a fix someone runs, and what has none
// the Home chat can be asked to look into (doctorHome).

// doctorProbeTimeout bounds each network check: a host that hasn't answered
// in this long is as good as unreachable to a download.
const doctorProbeTimeout = 5 * time.Second

// defaultGitHubAPI is where GitHub's API is, unless Config.GitHubAPI says.
const defaultGitHubAPI = "https://api.github.com"

// probeURL is nil when url answers at all: any HTTP status is an answer,
// since all a check wants to know is that this machine reaches the host.
func probeURL(ctx context.Context, u string) error {
	ctx, cancel := context.WithTimeout(ctx, doctorProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func (s *Server) doctor(w http.ResponseWriter, r *http.Request) error {
	found, err := s.doctorReport(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, found)
}

// doctorReport runs the checks, in the order a problem blocks the next: the
// machine, its disks and network, the image agents start from, then the
// logins they run with.
func (s *Server) doctorReport(ctx context.Context) (api.DoctorReport, error) {
	checks := []api.DoctorCheck{{ID: "daemon", Title: "Daemon", Status: api.DoctorOK, Message: "running AgentBox " + Version}}

	incus, incusErr := s.incusSetupCheck(ctx)
	checks = append(checks, fromSetupCheck(incus))
	hostCheck := api.DoctorCheck{ID: "host", Title: "User mapping", Status: api.DoctorOK, Message: "agents write files in their worktrees as you"}
	if err := image.CheckHost(s.cfg.User); err != nil {
		hostCheck = api.DoctorCheck{ID: "host", Title: "User mapping", Status: api.DoctorFail, Message: firstLine(err), Fix: incus.Fix}
		if incus.Status == api.SetupOK {
			hostCheck.Fix = "sudo agentbox host setup"
		}
	}
	checks = append(checks, hostCheck)
	if incusErr == nil {
		pool := fromSetupCheck(s.storagePoolCheck(ctx))
		if pool.Status != api.DoctorOK {
			// Nothing breaks: agents are made more slowly, and only a new pool
			// changes that.
			pool.Fix = "nothing is broken; a pool on btrfs or zfs, which host setup makes where the kernel has btrfs, makes agents faster"
		}
		checks = append(checks, pool)
	}
	checks = append(checks, diskCheck(s.diskStatus()))
	checks = append(checks, s.networkChecks(ctx)...)

	wanted, err := s.imageComponents(ctx)
	if err != nil {
		return api.DoctorReport{}, err
	}
	installed, _ := image.InstalledBuild(ctx, s.cfg.Incus)
	built, _ := image.Ready(ctx, s.cfg.Incus)
	base, err := s.imageSetupCheck(ctx, incusErr == nil, built, installed, wanted)
	if err != nil {
		return api.DoctorReport{}, err
	}
	imageCheck := fromSetupCheck(base)
	// A build is a download first, and starting one again is what Setup's
	// button does: safe whenever there's Incus to build it with and none runs.
	imageCheck.AutoFix = imageCheck.Status == api.DoctorFail && incusErr == nil && imageCheck.Job == ""
	if incusErr != nil {
		// Incus failing is the failure: the image only waits for it, and
		// isn't a second one to fix or hand over.
		imageCheck.Status, imageCheck.Message = api.DoctorWarn, "waits for Incus"
		imageCheck.Fix = "fix Incus first, then agentbox doctor --fix"
	}
	checks = append(checks, imageCheck)

	checks = append(checks, s.homeToolChecks(ctx)...)
	checks = append(checks, s.githubChecks()...)
	return api.DoctorReport{Version: Version, Checks: checks}, nil
}

// fromSetupCheck is one of Setup's checks as the doctor says it: a required
// step not done fails, and anything else short of done is a warning.
func fromSetupCheck(c api.SetupCheck) api.DoctorCheck {
	d := api.DoctorCheck{ID: c.ID, Title: c.Title, Message: c.Detail, Job: c.Job}
	switch {
	case c.Status == api.SetupOK:
		d.Status = api.DoctorOK
	case c.Required && (c.Status == api.SetupMissing || c.Status == api.SetupOutdated):
		d.Status, d.Fix = api.DoctorFail, c.Fix
	default:
		d.Status, d.Fix = api.DoctorWarn, c.Fix
	}
	if d.Message == "" {
		d.Message = c.Status
	}
	return d
}

// diskCheck is the disk guard's last measure: a disk under its floor warns,
// one full fails, and its advice is the fix.
func diskCheck(st agent.DiskStatus) api.DoctorCheck {
	c := api.DoctorCheck{ID: "disk", Title: "Disk space", Status: api.DoctorOK}
	worst, found := st.Worst()
	if !found {
		if len(st.Disks) == 0 {
			c.Message = "not measured yet"
			return c
		}
		least := slices.MinFunc(st.Disks, func(a, b agent.DiskCheck) int { return cmp.Compare(a.Free, b.Free) })
		c.Message = fmt.Sprintf("every disk has room: %s free on the fullest", gibs(least.Free))
		return c
	}
	c.Status = api.DoctorWarn
	if worst.Level == agent.DiskFull {
		c.Status = api.DoctorFail
	}
	c.Message = fmt.Sprintf("%s has %s free, under the %s AgentBox keeps free", worst.Label, gibs(worst.Free), gibs(worst.Floor))
	c.Fix = strings.Join(worst.Advice, " ")
	if c.Fix == "" {
		c.Fix = "free some space on that disk"
	}
	return c
}

// networkChecks are whether this machine reaches where the base image is
// downloaded from, and GitHub, both at once.
func (s *Server) networkChecks(ctx context.Context) []api.DoctorCheck {
	probe := s.probe
	if probe == nil {
		probe = probeURL
	}
	images := image.ImagesURLs()
	github := cmp.Or(s.cfg.GitHubAPI, defaultGitHubAPI)
	var wg sync.WaitGroup
	imageErrs := make([]error, len(images))
	for i, u := range images {
		wg.Go(func() { imageErrs[i] = probe(ctx, u) })
	}
	var githubErr error
	wg.Go(func() { githubErr = probe(ctx, github) })
	wg.Wait()

	var out []api.DoctorCheck
	// AGENTBOX_IMAGES_URL=off downloads nothing, so there's nothing to reach.
	if len(images) > 0 {
		c := api.DoctorCheck{ID: "network-images", Title: "Image downloads", Status: api.DoctorOK}
		if i := slices.IndexFunc(imageErrs, func(err error) bool { return err == nil }); i >= 0 {
			c.Message = "reaches " + urlHost(images[i])
			if i > 0 {
				c.Status = api.DoctorWarn
				c.Message = fmt.Sprintf("%s doesn't answer (%s), but %s does", urlHost(images[0]), firstLine(imageErrs[0]), urlHost(images[i]))
				c.Fix = "nothing, unless downloads are slow: check what blocks " + urlHost(images[0])
			}
		} else {
			c.Status = api.DoctorFail
			c.Message = fmt.Sprintf("can't reach %s: %s", urlHost(images[0]), firstLine(imageErrs[0]))
			c.Fix = "check this machine's internet connection, and any proxy or firewall in the way of " + urlHost(images[0])
		}
		out = append(out, c)
	}
	c := api.DoctorCheck{ID: "network-github", Title: "GitHub", Status: api.DoctorOK, Message: "reaches " + urlHost(github)}
	if githubErr != nil {
		c.Status = api.DoctorFail
		c.Message = fmt.Sprintf("can't reach %s: %s", urlHost(github), firstLine(githubErr))
		c.Fix = "check this machine's internet connection, and any proxy or firewall in the way of github.com"
	}
	return append(out, c)
}

// urlHost is a URL's host, for saying where something couldn't be reached.
func urlHost(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// homeTool is the AI tool the Home chat runs: its most recent session's.
func (s *Server) homeTool(ctx context.Context) state.Agent {
	session := state.MigratedSession
	if ls, err := s.store.LastActiveLeadSession(ctx, state.HomeProject); err == nil {
		session = ls.ID
	}
	return s.manager(nil).HomeSession(session)
}

// homeToolChecks are the Home chat's AI tool: its login, which nothing but
// the user can give, and whether it's installed, which a first message does
// anyway and the doctor can do ahead of it.
func (s *Server) homeToolChecks(ctx context.Context) []api.DoctorCheck {
	m := s.manager(nil)
	tool := s.homeTool(ctx).AI
	name := agent.ChatAdapters[tool].Tool
	login := api.DoctorCheck{ID: api.DoctorAILogin, Title: name + " login", Status: api.DoctorOK,
		Message: "the Home chat and agents can run " + name}
	if err := m.LeadToolLogin(state.Project{Name: state.HomeProject}, tool); err != nil {
		login.Status, login.Message, login.Fix = api.DoctorFail, firstLine(err), "agentbox auth "+tool
	}
	installed := api.DoctorCheck{ID: "ai-tool", Title: name, Status: api.DoctorOK, Message: "installed for the Home chat and projects' chats"}
	if !m.LeadToolInstalled(ctx, tool) {
		installed.Status = api.DoctorWarn
		installed.Message = "not installed yet: a chat's first message installs it, and waits for that"
		installed.Fix, installed.AutoFix = "agentbox doctor --fix", true
	}
	if j := s.runningDoctorFix("ai-tool"); j != "" {
		installed.Job, installed.AutoFix = j, false
	}
	return []api.DoctorCheck{login, installed}
}

// githubChecks are the GitHub CLI chats open pull requests with, and the
// account agents push with.
func (s *Server) githubChecks() []api.DoctorCheck {
	m := s.manager(nil)
	gh := api.DoctorCheck{ID: "gh", Title: "GitHub CLI", Status: api.DoctorOK, Message: "installed for chats"}
	if !m.GHInstalled() {
		gh.Status = api.DoctorWarn
		gh.Message = "not installed yet: a chat's first message installs it"
		gh.Fix, gh.AutoFix = "agentbox doctor --fix", true
	}
	if j := s.runningDoctorFix("gh"); j != "" {
		gh.Job, gh.AutoFix = j, false
	}
	account := api.DoctorCheck{ID: "github", Title: "GitHub account", Status: api.DoctorOK}
	accounts, err := m.Creds.GitHubAccounts()
	switch {
	case err != nil:
		account.Status, account.Message, account.Fix = api.DoctorWarn, "couldn't read the stored accounts: "+firstLine(err), "agentbox auth github"
	case len(accounts) == 0:
		account.Status = api.DoctorWarn
		account.Message = "none stored: agents can't push their branches or open pull requests"
		account.Fix = "agentbox auth github"
	default:
		account.Message = describeGitHubAccounts(accounts)
	}
	return []api.DoctorCheck{gh, account}
}

func describeGitHubAccounts(accounts []credentials.GitHubAccount) string {
	var names []string
	for _, a := range accounts {
		n := a.Name
		if a.Login != "" {
			n += " (" + a.Login + ")"
		}
		names = append(names, n)
	}
	return "agents push as " + strings.Join(names, ", ")
}

// doctorFix fixes what it can of what's wrong: each check named, or all.
// The base image is a job, as Setup's button makes it; the installs are jobs
// too, since a tool's download can take minutes.
func (s *Server) doctorFix(w http.ResponseWriter, r *http.Request) error {
	var req api.DoctorFixRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	found, err := s.doctorReport(ctx)
	if err != nil {
		return err
	}
	var fixes []api.DoctorFix
	for _, c := range found.Checks {
		if c.Status == api.DoctorOK || !c.AutoFix || (len(req.Checks) > 0 && !slices.Contains(req.Checks, c.ID)) {
			continue
		}
		fix := api.DoctorFix{ID: c.ID}
		job, err := s.startDoctorFix(ctx, c.ID)
		fix.Job = job
		if err != nil {
			fix.Error = err.Error()
		}
		fixes = append(fixes, fix)
	}
	for _, id := range req.Checks {
		if !slices.ContainsFunc(fixes, func(f api.DoctorFix) bool { return f.ID == id }) {
			fixes = append(fixes, api.DoctorFix{ID: id, Error: "nothing to fix by itself: it's fine, or its fix is one to run"})
		}
	}
	if found, err = s.doctorReport(ctx); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, api.DoctorFixResult{Fixes: fixes, Report: found})
}

// startDoctorFix starts the job that fixes one check.
func (s *Server) startDoctorFix(ctx context.Context, id string) (string, error) {
	m := s.manager(nil)
	var run func(ctx context.Context, status func(string)) error
	switch id {
	case "image":
		j, err := s.startImageBuild(ctx, api.BuildImageRequest{})
		return j.ID, err
	case "ai-tool":
		tool := s.homeTool(ctx).AI
		run = func(ctx context.Context, status func(string)) error { return m.InstallLeadTool(ctx, tool, status) }
	case "gh":
		run = m.EnsureGH
	default:
		return "", fmt.Errorf("%s has no fix of its own", id)
	}
	j, err := s.launchJob("doctor-fix", id, func(ctx context.Context, log io.Writer) (any, error) {
		return nil, run(ctx, func(line string) { _, _ = fmt.Fprintln(log, line) })
	})
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.doctorFixes == nil {
		s.doctorFixes = map[string]string{}
	}
	s.doctorFixes[id] = j.ID
	s.mu.Unlock()
	return j.ID, nil
}

// runningDoctorFix is the job fixing check id, while one runs.
func (s *Server) runningDoctorFix(id string) string {
	s.mu.Lock()
	job := s.doctorFixes[id]
	s.mu.Unlock()
	if job == "" || s.jobs == nil {
		return ""
	}
	if j, _ := s.jobs.get(job); j != nil && !j.snapshot().Done() {
		return job
	}
	return ""
}

// doctorLogLines is how much of the daemon's log the Home chat is handed:
// enough for the error and what led to it, little enough to read at once.
const doctorLogLines = 40

// doctorHome hands a failing check to the Home chat: a session of its own,
// on the Home chat's own tool and account, told what failed and the end of
// the daemon's log, and asked to fix it with the troubleshooting-setup
// skill. Only a failure is handed over, only one with no fix of the doctor's
// own, and never the Home chat's own login, without which it couldn't run.
func (s *Server) doctorHome(w http.ResponseWriter, r *http.Request) error {
	var req api.DoctorHomeRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	found, err := s.doctorReport(ctx)
	if err != nil {
		return err
	}
	c, ok := findCheck(found.Checks, req.Check)
	if !ok {
		return fmt.Errorf("no check %q: %w", req.Check, state.ErrNotFound)
	}
	if err := handOffRefusal(c); err != nil {
		return err
	}
	// Without its login the session would be opened, and its first turn fail.
	if login, _ := findCheck(found.Checks, api.DoctorAILogin); login.Status == api.DoctorFail {
		return fmt.Errorf("the Home chat can't run until you log in (%s): %s", login.Message, login.Fix)
	}
	// Asked before the new session exists, which would be the most recent:
	// it starts on the tool, account and model the Home chat was last on.
	last, lastErr := s.store.LastActiveLeadSession(ctx, state.HomeProject)
	ls, err := s.store.AddLeadSession(ctx, state.HomeProject, "Fix: "+c.Title, time.Now())
	if err != nil {
		return err
	}
	if lastErr == nil {
		if err := s.inheritSessionSettings(ctx, state.HomeProject, last.ID, ls.ID); err != nil {
			s.logf("starting the Home chat's %s session on %s's settings: %v", ls.ID, last.ID, err)
		}
	}
	s.sessionsChanged(ctx, state.HomeProject)
	lead, err := s.sessionLead(ctx, state.HomeProject, ls.ID)
	if err != nil {
		return err
	}
	tail := report.TailFile(s.cfg.Paths.DaemonLog(), 16<<10)
	if err := s.chat.Notice(lead, doctorHandOff(c, tail), chat.NoticeOptions{Act: true}); err != nil {
		return err
	}
	s.logf("doctor: handed the %s check to the Home chat's %s session", c.ID, ls.ID)
	return writeJSON(w, http.StatusCreated, api.DoctorHomeResult{Session: ls.ID})
}

func findCheck(checks []api.DoctorCheck, id string) (api.DoctorCheck, bool) {
	i := slices.IndexFunc(checks, func(c api.DoctorCheck) bool { return c.ID == id })
	if i < 0 {
		return api.DoctorCheck{}, false
	}
	return checks[i], true
}

// handOffRefusal says why a check isn't the Home chat's to fix, or nil.
func handOffRefusal(c api.DoctorCheck) error {
	switch {
	case c.Status != api.DoctorFail:
		return fmt.Errorf("the %s check didn't fail: nothing to hand over", c.ID)
	case c.ID == api.DoctorAILogin:
		return errors.New("the Home chat runs on this login, so it can't fix it: " + c.Fix)
	case c.AutoFix:
		return fmt.Errorf("the %s check fixes itself: agentbox doctor --fix, or Fix in Setup", c.ID)
	}
	return nil
}

// doctorHandOff is what the Home chat is told of a failing check: the check,
// what it found, what the doctor would have the user run, and the end of the
// daemon's log, with secrets taken out.
func doctorHandOff(c api.DoctorCheck, logTail string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From Setup: the user asked you to fix a failing check of `agentbox doctor`. Use the troubleshooting-setup skill.\n\n")
	fmt.Fprintf(&b, "Check: %s (%s)\nFound: %s\n", c.Title, c.ID, c.Message)
	if c.Fix != "" {
		fmt.Fprintf(&b, "Suggested fix: %s\n", c.Fix)
	}
	lines := strings.Split(strings.TrimRight(redact.Secrets(logTail), "\n"), "\n")
	if len(lines) > doctorLogLines {
		lines = lines[len(lines)-doctorLogLines:]
	}
	if tail := strings.TrimSpace(strings.Join(lines, "\n")); tail != "" {
		fmt.Fprintf(&b, "\nThe end of the daemon's log:\n\n```\n%s\n```\n", tail)
	}
	b.WriteString("\nFind out why, fix what you can from here, and tell the user, as commands to run, what only they can do on their own machine. Run `agentbox doctor` again when you're done.")
	return b.String()
}
