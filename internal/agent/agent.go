// Package agent creates and manages agents. An agent is an Incus instance
// copied from a base, plus a git worktree on its own branch. The worktree and
// the repository's .git directory are mounted into the instance at their host
// paths.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"agentbox/internal/android"
	"agentbox/internal/brief"
	"agentbox/internal/credentials"
	"agentbox/internal/gitrepo"
	"agentbox/internal/hostos"
	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/naming"
	"agentbox/internal/notes"
	"agentbox/internal/paths"
	"agentbox/internal/secrets"
	"agentbox/internal/state"
)

const (
	// MaxNameLen keeps ab-<project>-<agent> within Incus' 63-character limit.
	MaxNameLen   = 24
	session      = "main"
	readyTimeout = 2 * time.Minute
)

// Tool is an AI command line an agent starts in tmux window 1.
type Tool struct {
	Command    string
	Autonomous string // used with --autonomous, where the agent's machine is the sandbox
}

var Tools = map[string]Tool{
	"claude": {Command: "claude", Autonomous: "claude --dangerously-skip-permissions"},
	"codex":  {Command: "codex", Autonomous: "codex --dangerously-bypass-approvals-and-sandbox"},
	// OpenCode's terminal interface is its TUI, and --auto is its own way of
	// saying "the machine is the sandbox": it approves everything that isn't
	// explicitly denied, which is what an autonomous agent needs.
	"opencode": {Command: "opencode", Autonomous: "opencode --auto"},
	"none":     {},
}

// claudeChatDefaults are the chat settings a new Claude Code agent starts
// with. Each is resolved on its own — what was chosen for this agent, then the
// project's own choice where it has one, then the installation's setting, then
// AgentBox's own default — rather than one choice deciding both: picking a
// model for an agent shouldn't quietly reset how hard it thinks. Applied once,
// at creation, so agents made before a default changed don't move out from
// under whatever they already have.
//
// Only the model has a project step. A project on state.AgentModelAuto names no
// model at all — its chat chooses one per agent, and an agent it chose nothing
// for lands on the installation's setting, exactly as it would in a project
// that asks for nothing (see Project.DirectAgentModel).
//
// "model" is the one setting wanted() lets through even when this exact value
// isn't among the account's own choices, because the adapter resolves it
// itself (a stored "opus[1m]" from before D91 still resolves to the account's
// Opus 5, and the context window is now a choice of its own). A model that the adapter then refuses outright is reported in the chat
// rather than swallowed. Every other id or value going away falls back to the
// tool's own default instead of failing, which is why an effort is checked
// here, at creation, where saying no is still visible (see ChatChoices).
func (m *Manager) claudeChatDefaults(ctx context.Context, p state.Project, model, effort, window *string) (map[string]string, error) {
	chosenModel, err := m.chosenSetting(ctx, model, p.DirectAgentModel(), state.SettingDefaultClaudeModel, state.DefaultClaudeModel)
	if err != nil {
		return nil, err
	}
	chosenEffort, err := m.chosenSetting(ctx, effort, "", state.SettingDefaultClaudeEffort, state.DefaultClaudeEffort)
	if err != nil {
		return nil, err
	}
	options := map[string]string{"model": chosenModel, "effort": chosenEffort}
	if window != nil {
		// The context window (D91) is checked against the model it will run
		// on, so "1m" for Haiku is refused here rather than ignored later.
		// Nobody choosing is the installation's compact window.
		w, err := m.Store.ClaudeWindows(ctx)
		if err != nil {
			return nil, err
		}
		installation, err := m.Store.ClaudeCompactWindow(ctx)
		if err != nil {
			return nil, err
		}
		chosen, err := w.ContextWindowChoice(w.NormalizeClaudeModel(chosenModel), *window, installation)
		if err != nil {
			return nil, err
		}
		options[state.ChatOptionContextWindow] = chosen
	}
	return options, nil
}

// chosenSetting resolves one chat setting: the choice made for this agent, or
// the project's, or the installation's setting, or AgentBox's own default.
// explicit is a pointer so that "nobody chose" can be told apart from a value —
// the settings key itself uses "" to mean "back to AgentBox's default", and
// letting a request mean that too would turn an empty, mistyped choice into a
// silent fallback, which is the failure this whole area exists to avoid.
// ChatChoices rejects an explicit empty value before it reaches here. project is
// "" for a setting no project can hold, and for a project that holds none.
func (m *Manager) chosenSetting(ctx context.Context, explicit *string, project, key, builtin string) (string, error) {
	if explicit != nil {
		return strings.TrimSpace(*explicit), nil
	}
	if project != "" {
		return project, nil
	}
	value, err := m.Store.Setting(ctx, key)
	if err != nil {
		return "", err
	}
	if value == "" {
		return builtin, nil
	}
	return value, nil
}

// ChatChoices checks a model and an effort chosen for one agent, before
// anything is built. It validates as much as can honestly be known here and no
// more:
//
//   - They are Claude Code settings. A Codex agent or a shell-only agent has
//     no chat options to seed, so asking for a model there is refused instead
//     of stored somewhere nothing will read it.
//   - An empty choice is refused. Leaving the field out is how you ask for the
//     installation's setting; sending "" is a mistake worth seeing.
//   - The model is not checked against the account's menu. The adapter
//     resolves a preference itself, so a literal check would reject exactly
//     the aliases that do work — "opus[1m]" on an account listing only "opus"
//     (D45). A model the adapter
//     really refuses fails loudly in the chat, where the account's own answer
//     is the one being reported.
//   - The effort is checked against the menu Claude Code last advertised,
//     because that option is a closed list: probed live, "ultra" and "EXTREME"
//     are refused outright, with no alias resolution to rescue them, and
//     wanted() then drops an unoffered value in silence. The menu is only ever
//     one that really arrived over ACP. Two honest limits: a model can offer
//     no effort levels at all (Haiku 4.5 sends no effort option), so this says
//     the level is one Claude Code names, not that this agent will have it;
//     and an installation whose chat has never started has no menu to check
//     against, so nothing is rejected there.
func (m *Manager) ChatChoices(ctx context.Context, ai string, model, effort *string) error {
	if model == nil && effort == nil {
		return nil
	}
	if ai == "none" {
		return errors.New("an agent with no AI tool has no model or effort to run on: leave them out")
	}
	if ai == "opencode" {
		// OpenCode has a model and no effort. Its models are provider/model
		// ids and, like Claude Code's, they aren't checked here: what a login
		// can run is OpenCode's answer, given when the session starts, and a
		// model it refuses is reported in the chat rather than swallowed.
		if effort != nil {
			return errors.New("the effort is a Claude Code setting, and OpenCode agents have none: leave it out")
		}
		if strings.TrimSpace(*model) == "" {
			return errors.New("an empty model isn't a choice: leave it out to let OpenCode use the model its own configuration names")
		}
		return nil
	}
	if ai != "claude" {
		return fmt.Errorf("the model and the effort are Claude Code settings, and %s agents have none: leave them out", ai)
	}
	for name, chosen := range map[string]*string{"model": model, "effort": effort} {
		if chosen != nil && strings.TrimSpace(*chosen) == "" {
			return fmt.Errorf("an empty %s isn't a choice: leave it out to use the %s new agents start on", name, name)
		}
	}
	if effort == nil {
		return nil
	}
	raw, err := m.Store.Setting(ctx, state.SettingClaudeEffortChoices)
	if err != nil {
		return err
	}
	offered := state.ChoiceValues(raw)
	if want := strings.TrimSpace(*effort); len(offered) > 0 && !slices.Contains(offered, want) {
		return fmt.Errorf("Claude Code doesn't offer the effort %q: it offers %s", want, strings.Join(offered, ", "))
	}
	return nil
}

type Manager struct {
	Store *state.Store
	Incus incus.Client
	Paths paths.Paths
	Creds credentials.Store
	// Secrets are the keys and tokens the user hands to agents, delivered as a
	// file inside each one (secrets.go).
	Secrets secrets.Store
	User    image.User
	Log     io.Writer // progress messages; may be nil

	// AgentSocket, when set, returns the host path of an agent's in-agent API
	// socket; the agent reaches it at api.InAgentSocket.
	AgentSocket func(instance string) string
	// Binary, when set, is copied into agents as /usr/local/bin/agentbox.
	Binary string
	// BrowserSocket, when set, returns the host path where an agent's service
	// (one of HostServices) is reachable.
	BrowserSocket func(instance, service string) string
	// AndroidSDK, when set, finds the host's Android SDK, for agents' emulators.
	AndroidSDK func() (android.SDK, error)
	// LeadSocketPath, when set, returns the socket a project's chat reaches
	// AgentBox through. Its tools are registered against it.
	LeadSocketPath func(project string) string
	// DesktopTheme, when set, is the palette each agent's desktop is painted
	// in — the host's Omarchy theme, when AgentBox is following one. Unset is
	// BrandDesktopTheme, which is what the command-line tool gets.
	DesktopTheme func() DesktopTheme
}

func InstanceName(project, agent string) string { return "ab-" + project + "-" + agent }

// ParseRef splits "<project>/<agent>".
func ParseRef(ref string) (project, agent string, err error) {
	project, agent, ok := strings.Cut(ref, "/")
	if !ok || project == "" || agent == "" || strings.Contains(agent, "/") {
		return "", "", fmt.Errorf("invalid agent %q: use <project>/<agent>, like pawly/agent-01", ref)
	}
	return project, agent, nil
}

func (m *Manager) Get(ctx context.Context, ref string) (state.Agent, error) {
	project, name, err := ParseRef(ref)
	if err != nil {
		return state.Agent{}, err
	}
	return m.Store.Agent(ctx, project, name)
}

type CreateOptions struct {
	Name  string // default: the next free agent-NN
	Title string // what the user calls the agent, shown next to its name
	// Branch is the slug its branch is named with, after the project's
	// prefix, like "fix-login-redirect". Empty makes one from Title, then
	// Task, then the agent's name. Either way a taken branch gets -2, -3….
	Branch     string
	AI         string // claude, codex, opencode or none
	Interface  string // chat (the default) or cli
	Autonomous bool
	// Model and Effort are the Claude Code chat settings chosen for this one
	// agent. nil is "nobody chose", which falls back to the installation's
	// setting and then to AgentBox's own default, per field. Only for AI
	// "claude"; see ChatChoices.
	Model  *string
	Effort *string
	// ContextWindow is where this agent's chat compacts, like "200k" or "1m",
	// checked against its model (D91). nil is the installation's compact
	// window. Only for AI "claude".
	ContextWindow *string
	From          string // default: the branch checked out in the project
	// ClaudeAccount picks a stored Claude Code account for this agent;
	// empty means the project's account, then the machine's default.
	ClaudeAccount string
	// GitHubAccount picks a stored GitHub account for this agent; empty means
	// the project's account, then the machine's default, then none.
	GitHubAccount string
	CopyEnv       bool // copy gitignored env files from the project
	Clean         bool // start from the base image even if the project has a saved base
	// Limits caps this one agent's machine. A field nobody set falls back to
	// the installation's default; a field set to "" removes that cap for this
	// agent alone. See LimitChoice.
	Limits LimitChoice
	// FinishNotice is this agent's own choice of what it does to its
	// project's chat when it genuinely finishes: state.FinishNoticesChat,
	// state.FinishNoticesOff, or "" to leave it unsaid. It only matters when
	// the project's own FinishNotices is state.FinishNoticesLead.
	FinishNotice string
	// Task is what the agent is about to be asked to do. It is not stored and
	// not sent — the daemon sends it as the agent's first message — it only
	// seeds the "What the project knows" section of the brief, so an agent
	// about to work on OAuth starts with what the project learned about OAuth
	// (D75). Every brief written afterwards recovers it from the agent_created
	// event instead.
	Task string
}

// Create builds an agent from the project's saved base, or from the base image.
func (m *Manager) Create(ctx context.Context, project string, opts CreateOptions) (state.Agent, error) {
	if _, ok := Tools[opts.AI]; !ok {
		return state.Agent{}, fmt.Errorf("unknown AI tool %q: use claude, codex, opencode or none", opts.AI)
	}
	if opts.FinishNotice != "" && opts.FinishNotice != state.FinishNoticesChat && opts.FinishNotice != state.FinishNoticesOff {
		return state.Agent{}, fmt.Errorf("unknown finish notice %q: use chat or off, or leave it out to follow the project", opts.FinishNotice)
	}
	// Before a machine is copied: a choice that could never apply says so now.
	if err := m.ChatChoices(ctx, opts.AI, opts.Model, opts.Effort); err != nil {
		return state.Agent{}, err
	}
	if err := validateName(opts.Name); err != nil {
		return state.Agent{}, err
	}
	if err := CheckBranchSlug(opts.Branch); err != nil {
		return state.Agent{}, err
	}
	title, err := CleanTitle(opts.Title)
	if err != nil {
		return state.Agent{}, err
	}
	iface, err := interfaceFor(opts.AI, opts.Interface)
	if err != nil {
		return state.Agent{}, err
	}
	p, repo, err := m.project(ctx, project)
	if err != nil {
		return state.Agent{}, err
	}
	// Before anything is copied: the agent needs a login for its AI tool, and
	// a machine with the tool on it.
	account, err := m.CheckLogin(opts.AI, p, opts.ClaudeAccount)
	if err != nil {
		return state.Agent{}, err
	}
	if opts.ContextWindow != nil {
		if opts.AI != "claude" {
			return state.Agent{}, fmt.Errorf("the context window is a Claude Code setting, and %s agents have none: leave it out", opts.AI)
		}
		if _, err := m.claudeChatDefaults(ctx, p, opts.Model, opts.Effort, opts.ContextWindow); err != nil {
			return state.Agent{}, err
		}
	}
	if err := m.checkImageTool(ctx, opts.AI); err != nil {
		return state.Agent{}, err
	}
	ghAccount, err := m.GitHubAccountFor(p, opts.GitHubAccount)
	if err != nil {
		return state.Agent{}, err
	}
	source := image.SnapshotRef()
	base, hasBase, err := m.ProjectBase(ctx, p.Name)
	switch {
	case err != nil:
		return state.Agent{}, err
	case hasBase && !opts.Clean:
		source = base.SnapshotRef()
	default:
		ready, err := image.Ready(ctx, m.Incus)
		if err != nil {
			return state.Agent{}, err
		}
		if !ready {
			return state.Agent{}, errors.New("the base image isn't built: run agentbox image build")
		}
	}
	defaults, err := m.Defaults(ctx)
	if err != nil {
		return state.Agent{}, err
	}
	limits, err := opts.Limits.Resolve(defaults)
	if err != nil {
		return state.Agent{}, err
	}
	from := opts.From
	if from == "" {
		from = repo.CurrentBranch()
	}
	commit, err := repo.ResolveCommit(from)
	if err != nil {
		return state.Agent{}, err
	}
	return m.build(ctx, plan{
		project:       p,
		repo:          repo,
		name:          opts.Name,
		branch:        opts.Branch,
		title:         title,
		ai:            opts.AI,
		autonomous:    opts.Autonomous,
		model:         opts.Model,
		effort:        opts.Effort,
		contextWindow: opts.ContextWindow,
		claudeAccount: account,
		githubAccount: ghAccount,
		iface:         iface,
		source:        source,
		baseRef:       from,
		baseCommit:    commit,
		copyEnv:       opts.CopyEnv,
		limits:        limits,
		finishNotice:  opts.FinishNotice,
		task:          opts.Task,
	})
}

// CleanTitle collapses a title's whitespace and checks it fits on one line in lists.
func CleanTitle(title string) (string, error) {
	title = strings.Join(strings.Fields(title), " ")
	if len([]rune(title)) > 80 {
		return "", errors.New("the title is longer than 80 characters")
	}
	return title, nil
}

func validateName(name string) error {
	if name == "" {
		return nil
	}
	if err := naming.Validate("agent", name, MaxNameLen); err != nil {
		return err
	}
	if name == baseName || name == previousBaseName {
		return fmt.Errorf("%q is reserved for the project's saved base", name)
	}
	if name == state.LeadName {
		return fmt.Errorf("%q is reserved for the project's chat", state.LeadName)
	}
	return nil
}

func (m *Manager) project(ctx context.Context, name string) (state.Project, gitrepo.Repo, error) {
	p, err := m.Store.Project(ctx, name)
	if err != nil {
		return state.Project{}, gitrepo.Repo{}, err
	}
	repo, err := gitrepo.Open(p.Root)
	return p, repo, err
}

// plan is everything build needs; Create and Fork fill it in differently.
type plan struct {
	project       state.Project
	repo          gitrepo.Repo
	name          string // empty picks the next free agent-NN
	branch        string // the slug asked for; empty makes one, see branchFor
	title         string
	ai            string
	autonomous    bool
	model         *string // the Claude Code model chosen for it; nil falls back
	effort        *string // how hard it thinks; nil falls back
	contextWindow *string // where its chat compacts; nil is the installation's
	claudeAccount string  // the stored Claude Code account whose token it gets
	githubAccount string  // the stored GitHub account whose token it gets; "" for none
	iface         string  // chat or cli
	source        string  // instance snapshot to copy
	baseRef       string
	baseCommit    string // the new branch starts here
	tree          string // optional snapshot commit whose files are applied on top
	copyEnv       bool
	limits        Limits // already resolved: what this machine is capped at
	finishNotice  string // this agent's own choice; see CreateOptions.FinishNotice
	task          string // what it is about to be asked to do; see CreateOptions.Task
}

// build creates an agent step by step. If a step fails, or ctx is cancelled,
// the finished steps are undone.
func (m *Manager) build(ctx context.Context, pl plan) (state.Agent, error) {
	name := pl.name
	if name == "" {
		name = m.nextName(ctx, pl.project)
	}
	branch := m.branchFor(ctx, pl.project, pl.repo, pl.branch, pl.title, pl.task, name)
	a := state.Agent{
		Project:    pl.project.Name,
		Name:       name,
		Title:      pl.title,
		Instance:   InstanceName(pl.project.Name, name),
		AI:         pl.ai,
		Autonomous: pl.autonomous,
		Branch:     branch,
		BaseRef:    pl.baseRef,
		BaseCommit: pl.baseCommit,
		Worktree:   m.Paths.Worktree(pl.project.Name, name),
		Status:     state.AgentCreating,
		CreatedAt:  time.Now(),
		Source:     pl.source,

		ClaudeAccount: pl.claudeAccount,
		GitHubAccount: pl.githubAccount,
		Interface:     pl.iface,
		FinishNotice:  pl.finishNotice,
	}
	if _, err := os.Stat(a.Worktree); err == nil {
		return state.Agent{}, fmt.Errorf("%s already exists: remove it or choose another --name", a.Worktree)
	}

	// Undo steps run even when ctx was cancelled (Ctrl-C).
	cleanup := context.WithoutCancel(ctx)
	var undo []func()
	fail := func(step string, err error) (state.Agent, error) {
		m.logf("%s failed; rolling back", step)
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		return state.Agent{}, fmt.Errorf("creating %s: %s: %w", a.Ref(), step, err)
	}

	if err := m.Store.AddAgent(ctx, a); err != nil {
		return state.Agent{}, err
	}
	undo = append(undo, func() { m.Store.RemoveAgent(cleanup, a.Project, a.Name) })

	if a.AI == "claude" {
		options, err := m.claudeChatDefaults(ctx, pl.project, pl.model, pl.effort, pl.contextWindow)
		if err != nil {
			return fail("chat defaults", err)
		}
		if err := m.Store.SaveChat(ctx, a.Project, a.Name, state.Chat{Options: options}); err != nil {
			return fail("chat defaults", err)
		}
	}
	// OpenCode has no installation-wide default model of its own — its models
	// are a provider's, not a plan's — so only a model chosen for this one
	// agent is stored. The chat applies it over ACP when the session starts.
	if a.AI == "opencode" && pl.model != nil {
		options := map[string]string{"model": strings.TrimSpace(*pl.model)}
		if err := m.Store.SaveChat(ctx, a.Project, a.Name, state.Chat{Options: options}); err != nil {
			return fail("chat defaults", err)
		}
	}

	m.logf("Creating worktree %s on branch %s (from %s)", a.Worktree, a.Branch, a.BaseRef)
	if err := pl.repo.AddWorktree(a.Worktree, a.Branch, a.BaseCommit); err != nil {
		return fail("worktree", err)
	}
	undo = append(undo, func() {
		pl.repo.RemoveWorktree(a.Worktree)
		pl.repo.DeleteBranch(a.Branch)
		deleteAgentRefs(pl.repo, a.Name)
	})
	if pl.tree != "" {
		if err := gitrepo.ApplyTree(a.Worktree, pl.tree); err != nil {
			return fail("worktree", err)
		}
	}

	var envFiles []string
	if pl.copyEnv {
		var err error
		if envFiles, err = pl.repo.EnvFiles(); err != nil {
			return fail("env files", err)
		}
		if err := copyFiles(pl.repo.Root, a.Worktree, envFiles); err != nil {
			return fail("env files", err)
		}
		if len(envFiles) > 0 {
			m.logf("Copied %s from the project", strings.Join(envFiles, ", "))
		}
	}

	// Instance commands run to completion even after Ctrl-C, and cancellation is
	// checked between them, so the rollback never races an unfinished Incus operation.
	incusStep := func(args ...string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := m.Incus.Run(cleanup, args...)
		return err
	}

	m.logf("Creating instance %s from %s", a.Instance, pl.source)
	if err := incusStep("copy", pl.source, a.Instance); err != nil {
		return fail("instance", err)
	}
	undo = append(undo, func() { m.Incus.Run(cleanup, "delete", "--force", a.Instance) })

	copied, err := m.Incus.Details(cleanup, a.Instance)
	if err != nil {
		return fail("instance", err)
	}
	var steps [][]string
	// A copy of another agent brings that agent's devices along: replace them.
	for _, device := range copiedDevices {
		if _, ok := copied.Devices[device]; ok {
			steps = append(steps, []string{"config", "device", "remove", a.Instance, device})
		}
	}
	steps = append(steps,
		[]string{"config", "set", a.Instance, "raw.idmap=" + image.IDMap(m.User)},
		// Same paths as on the host, so the worktree's .git pointer resolves inside the agent.
		[]string{"config", "device", "add", a.Instance, "worktree", "disk", "source=" + a.Worktree, "path=" + a.Worktree},
		[]string{"config", "device", "add", a.Instance, "gitdir", "disk", "source=" + pl.repo.GitDir, "path=" + pl.repo.GitDir},
	)
	steps = append(steps, limitSteps(a.Instance, pl.limits, copied.Config)...)
	steps = append(steps, []string{"start", a.Instance})
	for _, args := range steps {
		if err := incusStep(args...); err != nil {
			return fail("instance", err)
		}
	}
	inst, err := m.Incus.WaitReady(ctx, a.Instance, readyTimeout)
	if err != nil {
		return fail("instance", err)
	}
	// A mount can be hidden by one the agent's OS makes at boot, so check, as the
	// agent's user, that git works in the worktree.
	var gitOut bytes.Buffer
	if err := m.Incus.UserExec(ctx, a.Instance, m.User.Name, "git -C "+shellQuote(a.Worktree)+" rev-parse --git-dir", nil, &gitOut, &gitOut); err != nil {
		return fail("instance", fmt.Errorf("the worktree isn't usable inside the agent: %w: %s", err, strings.TrimSpace(gitOut.String())))
	}
	// Tools that find a monorepo's root through git write next to the main
	// checkout's .git: Turborepo keeps its cache there. Inside the agent that
	// directory only holds the .git mount, and root made it. Give it to the
	// agent's user, so those writes land in the agent's own filesystem; the
	// project's checkout on the host isn't mounted, and stays untouched.
	if _, err := m.Incus.Run(ctx, "exec", a.Instance, "--", "chown", fmt.Sprintf("%d:%d", m.User.UID, m.User.GID), filepath.Dir(pl.repo.GitDir)); err != nil {
		return fail("instance", err)
	}
	if err := m.EnsureAgentAPI(ctx, a); err != nil {
		return fail("in-agent API", err)
	}

	m.logf("Configuring git, AI tool logins and the agent brief")
	if err := m.configure(ctx, a, inst.IPv4(), envFiles, pl.task); err != nil {
		return fail("configure", err)
	}
	m.logf("Starting the tmux session")
	if err := m.ensureSession(ctx, a); err != nil {
		return fail("session", err)
	}
	m.logf("Taking the %q snapshot", initialSnapshot)
	if _, err := m.takeSnapshot(ctx, a, initialSnapshot, false); err != nil {
		return fail("snapshot", err)
	}
	if err := m.Store.SetAgentStatus(ctx, a.Project, a.Name, state.AgentReady); err != nil {
		return fail("state", err)
	}
	a.Status = state.AgentReady
	m.EnsureBrowser(ctx, a)
	return a, nil
}

// CheckLogin reports that AgentBox can log the new agent in to its AI tool,
// and returns the Claude Code account it will use ("" for the other tools).
// Exported so a caller that starts a job for Create can run this check first,
// synchronously, rather than let the job fail after it has already answered
// that the agent is on its way (see createAgentFrom).
func (m *Manager) CheckLogin(ai string, p state.Project, account string) (string, error) {
	if account != "" && ai != "claude" {
		return "", fmt.Errorf("a Claude Code account (%s) only applies to agents that run Claude Code, and this one runs %s", account, ai)
	}
	switch ai {
	case "claude":
		return m.ClaudeAccountFor(p, account)
	case "codex":
		if !m.Creds.HasCodexLogin() {
			return "", errors.New("agents have no Codex login: run agentbox auth codex (or use --ai none)")
		}
	case "opencode":
		if !m.Creds.HasOpenCodeLogin() {
			return "", errors.New("agents have no OpenCode login: run agentbox auth opencode (or use --ai claude)")
		}
	}
	return "", nil
}

// checkImageTool reports that the base image has the agent's AI tool built
// into it. Codex and OpenCode are optional in the image (image.Options), so an
// agent asking for one on an image built without it is refused here, rather
// than starting and failing to find the command. An image that can't be read at all is left
// to the checks that follow, which say that it isn't built.
func (m *Manager) checkImageTool(ctx context.Context, ai string) error {
	if ai != "codex" && ai != "opencode" {
		return nil
	}
	installed, err := image.InstalledBuild(ctx, m.Incus)
	if err != nil {
		return nil
	}
	if ai == "opencode" {
		if installed.Components.OpenCode {
			return nil
		}
		return fmt.Errorf("OpenCode is %s: run agentbox image build --opencode (or use --ai claude)", image.OpenCodeMissing)
	}
	if installed.Components.Codex {
		return nil
	}
	return fmt.Errorf("Codex is %s: run agentbox image build --codex (or use --ai claude)", image.CodexMissing)
}

// OpenCodeReady reports whether an agent could run OpenCode right now: the
// base image was built with it, and AgentBox has a login to give it. Both
// halves matter and the image comes first, exactly as they do for Codex — a
// login is no use to an agent whose machine has no OpenCode to run.
func (m *Manager) OpenCodeReady(ctx context.Context) (bool, error) {
	on, err := m.Store.Flag(ctx, state.SettingImageOpenCode)
	if err != nil || !on {
		return false, err
	}
	return m.Creds.HasOpenCodeLogin(), nil
}

// ClaudeAccountFor resolves which stored Claude Code account an agent uses: the
// one asked for, else the project's, else the machine's default account —
// refused if the project's allow-list leaves it out. Every way an agent
// gets an account comes through here, the lead's own included.
func (m *Manager) ClaudeAccountFor(p state.Project, account string) (string, error) {
	name, err := m.resolveClaudeAccount(p, account)
	if err != nil {
		return "", err
	}
	if len(p.ClaudeAccounts) > 0 && !slices.Contains(p.ClaudeAccounts, name) {
		return "", fmt.Errorf("%s may only use the Claude Code accounts %s, not %q", p.Name, strings.Join(p.ClaudeAccounts, ", "), name)
	}
	return name, nil
}

func (m *Manager) resolveClaudeAccount(p state.Project, account string) (string, error) {
	for _, name := range []string{account, p.ClaudeAccount} {
		if name == "" {
			continue
		}
		ok, err := m.Creds.HasClaudeAccount(name)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("no Claude Code account named %q: %s", name, m.claudeAccountsHint())
		}
		return name, nil
	}
	name, err := m.Creds.DefaultClaudeAccount()
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", errors.New("agents have no Claude Code login: run agentbox auth claude (or use --ai none)")
	}
	return name, nil
}

// claudeAccountsHint says what to do about an unknown account name: add it, or
// pick one of the ones this machine already has.
func (m *Manager) claudeAccountsHint() string {
	accounts, err := m.Creds.ClaudeAccounts()
	if err != nil || len(accounts) == 0 {
		return "add it with agentbox auth claude --account <name>"
	}
	names := make([]string, len(accounts))
	for i, a := range accounts {
		names[i] = a.Name
	}
	return "this machine has " + strings.Join(names, ", ")
}

// GitHubAccountFor resolves which stored GitHub account an agent uses: the
// one asked for, else the project's, else the machine's default account.
// Unlike Claude Code, GitHub is optional: an agent gets no token when none is
// stored, rather than failing to build.
func (m *Manager) GitHubAccountFor(p state.Project, account string) (string, error) {
	for _, name := range []string{account, p.GitHubAccount} {
		if name == "" {
			continue
		}
		ok, err := m.Creds.HasGitHubAccount(name)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("no GitHub account named %q: add it with agentbox auth github --account %s, or list the accounts with agentbox auth github list", name, name)
		}
		return name, nil
	}
	return m.Creds.DefaultGitHubAccount()
}

// nextName returns the first agent-NN not taken by an agent or a worktree
// directory. Branches don't come into it: an agent's branch is named after its
// work (branchFor), and makes its own way around the ones that are taken.
func (m *Manager) nextName(ctx context.Context, p state.Project) string {
	taken := map[string]bool{}
	if agents, err := m.Store.Agents(ctx, p.Name); err == nil {
		for _, a := range agents {
			taken[a.Name] = true
		}
	}
	for i := 1; ; i++ {
		name := fmt.Sprintf("agent-%02d", i)
		if _, err := os.Stat(m.Paths.Worktree(p.Name, name)); !taken[name] && errors.Is(err, fs.ErrNotExist) {
			return name
		}
	}
}

// tmuxConfig matches the desktop app's dark theme, and lets the mouse wheel
// scroll back through the session.
const tmuxConfig = `# Written by AgentBox.
set -g mouse on
set -g history-limit 50000
set -g status-style "bg=#0b0b11,fg=#71717a"
set -g status-left "#[fg=#a78bfa,bold] ◆ agentbox #[default]"
set -g status-left-length 24
set -g status-right "#[fg=#52525b]%H:%M "
set -g window-status-format " #I #W "
set -g window-status-current-format "#[bg=#7c3aed,fg=#ffffff,bold] #I #W #[default]"
set -g pane-border-style "fg=#27272a"
set -g pane-active-border-style "fg=#7c3aed"
set -g message-style "bg=#18181b,fg=#e4e4e7"
set -g mode-style "bg=#7c3aed,fg=#ffffff"
`

// configure writes the agent's git identity, AI tool logins and brief.
// Logins are AgentBox's own (see package credentials), and every file is
// written inside the agent; nothing from the host's home is mounted.
func (m *Manager) configure(ctx context.Context, a state.Agent, ip string, envFiles []string, task string) error {
	home := "/home/" + m.User.Name
	type file struct {
		path    string
		content string
		mode    os.FileMode
	}
	var files []file

	if name, email := gitIdentity(); name != "" && email != "" {
		files = append(files, file{home + "/.gitconfig", fmt.Sprintf("[user]\n\tname = %s\n\temail = %s\n", gitQuote(name), gitQuote(email)), 0o644})
	}

	env, err := m.agentEnv(a)
	if err != nil {
		return err
	}
	secretsEnv, err := m.secretsFile(ctx, a)
	if err != nil {
		return err
	}
	files = append(files,
		file{home + "/.config/agentbox/env", env, 0o600},
		file{m.SecretsPath(), secretsEnv, 0o600},
		file{home + "/.tmux.conf", tmuxConfig, 0o644},
	)

	if m.Creds.HasCodexLogin() {
		auth, err := os.ReadFile(m.Creds.CodexAuthPath())
		if err != nil {
			return err
		}
		files = append(files, file{home + "/.codex/auth.json", string(auth), 0o600})
	}
	// OpenCode keeps every provider's key in one auth.json, and reads it from
	// $XDG_DATA_HOME/opencode — the agent's own home, never the host's (D6).
	if m.Creds.HasOpenCodeLogin() {
		auth, err := os.ReadFile(m.Creds.OpenCodeAuthPath())
		if err != nil {
			return err
		}
		files = append(files, file{home + "/.local/share/opencode/auth.json", string(auth), 0o600})
	}

	// Skip Claude Code's onboarding and the tools' trust prompts for the
	// workspace, and give every AI tool the MCP servers AgentBox provides: two
	// that drive the agent's display — Playwright inside Chromium's pages, and
	// AgentBox's own desktop server for everything outside them
	// (D64) — and one for the
	// project's memory. In Claude Code the desktop server is its desktop
	// subagent's rather than the agent's own
	// (D83).
	servers := agentMCPServers(home)
	// Claude Code gets every server but the desktop one, which is declared in
	// the desktop subagent's definition instead, so that screenshots land in
	// the subagent's context rather than the agent's (subagents.go, D83).
	claudeServers := map[string]any{}
	var desktopDefinition string
	for _, s := range servers {
		if s.name == "desktop" {
			if desktopDefinition, err = desktopAgent(s); err != nil {
				return err
			}
			continue
		}
		claudeServers[s.name] = map[string]any{"type": "stdio", "command": s.command, "args": s.args}
	}
	exploreDefinition, err := exploreAgent()
	if err != nil {
		return err
	}
	claudeState, err := json.Marshal(map[string]any{
		"hasCompletedOnboarding": true,
		"projects":               map[string]any{a.Worktree: map[string]any{"hasTrustDialogAccepted": true}},
		"mcpServers":             claudeServers,
	})
	if err != nil {
		return err
	}
	// Read once and used for both Codex's config.toml below and, for Claude
	// Code, the settings.json block at the end of this function (D83): the
	// window chosen for this agent's chat, if one was (D91).
	window, err := m.agentCompactWindow(ctx, a)
	if err != nil {
		return err
	}
	codexConfig := codexConfigFor(a.Worktree, servers, window)
	opencodeConfig, err := openCodeConfig(servers, a.Autonomous)
	if err != nil {
		return err
	}
	files = append(files,
		file{home + "/.claude.json", string(claudeState), 0o600},
		file{home + "/" + desktopAgentFile, desktopDefinition, 0o644},
		file{home + "/" + exploreAgentFile, exploreDefinition, 0o644},
		file{home + "/.codex/config.toml", codexConfig, 0o600},
		file{home + "/.config/opencode/opencode.json", string(opencodeConfig), 0o600},
	)

	text, err := m.brief(ctx, a, ip, envFiles, task)
	if err != nil {
		return err
	}
	for _, path := range m.briefPaths() {
		files = append(files, file{path, text, 0o644})
	}

	for _, f := range files {
		if err := m.Incus.WriteFile(ctx, a.Instance, f.path, []byte(f.content), m.User.UID, m.User.GID, f.mode); err != nil {
			return err
		}
	}
	// The compact window, the subagent limits and the output caps go in now
	// rather than waiting for the first chat, because the agent's terminal
	// starts Claude Code straight away and reads the same settings.json
	// (D83, D84). The chat merges them again every time it starts, so this is
	// only ever the terminal's head start. The model is left to the chat: the
	// terminal has always started on what the image's settings say. Codex's and
	// OpenCode's terminals have no equivalent head start to give: their
	// config.toml and opencode.json above already carry the same window and
	// caps, written whole rather than merged, so there is nothing more to layer
	// on here.
	if a.AI == "claude" {
		if err := m.prepareAgentClaudeSettings(ctx, a, func(b []byte) ([]byte, error) {
			return withClaudeSettings(b,
				func(b []byte) ([]byte, error) { return withClaudeCompactWindow(b, window) },
				func(b []byte) ([]byte, error) { return withClaudeEnv(b, subagentLimits) },
				func(b []byte) ([]byte, error) { return withClaudeEnv(b, outputCaps) },
			)
		}); err != nil {
			return err
		}
	}
	return nil
}

// agentCompactWindow is where an agent's Claude Code compacts: the context
// window chosen for its chat (D91), else the installation's compact window.
func (m *Manager) agentCompactWindow(ctx context.Context, a state.Agent) (int64, error) {
	installation, err := m.Store.ClaudeCompactWindow(ctx)
	if err != nil || a.AI != "claude" {
		return installation, err
	}
	chat, err := m.Store.Chat(ctx, a.Project, a.Name)
	if err != nil || chat.Options[state.ChatOptionContextWindow] == "" {
		return installation, nil
	}
	w, err := m.Store.ClaudeWindows(ctx)
	if err != nil {
		return installation, nil
	}
	return w.CompactWindow(w.NormalizeClaudeModel(chat.Options["model"]), chat.Options[state.ChatOptionContextWindow], installation), nil
}

// openCodeConfig is OpenCode's ~/.config/opencode/opencode.json: the MCP
// servers every AI tool gets, and — for an autonomous agent — the permissions
// it runs with.
//
// An MCP server is a "local" one there: the command and its arguments as a
// single argv array, rather than Claude Code's command-and-args pair or
// Codex's TOML table. "permission": "allow" at the top level is OpenCode's
// shorthand for every permission, and is what --dangerously-skip-permissions
// is for Claude Code: the agent's machine is the sandbox and there is nobody
// at its chat to answer. An agent that stops to ask gets no permission key at
// all, so OpenCode asks and the chat shows the request.
func openCodeConfig(servers []mcpServer, autonomous bool) ([]byte, error) {
	mcp := map[string]any{}
	for _, s := range servers {
		mcp[s.name] = map[string]any{
			"type":    "local",
			"command": append([]string{s.command}, s.args...),
			"enabled": true,
		}
	}
	// subagent_depth prevents a subagent from launching one of its own, the
	// same ceiling Claude Code's CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH holds it
	// to (D84) — it is OpenCode's own default already, written explicitly so
	// it doesn't quietly change under an upgrade. OpenCode has no concurrency
	// cap to set beside it, and no absolute compact window either: its
	// "compaction" settings are all relative to the model's own context
	// (a fraction reserved, a number of turns kept), not a fixed token count
	// like Claude Code's autoCompactWindow or Codex's
	// model_auto_compact_token_limit, so there is nothing here for
	// state.ClaudeCompactWindow to drive (D83).
	config := map[string]any{
		"$schema":        "https://opencode.ai/config.json",
		"mcp":            mcp,
		"subagent_depth": opencodeSubagentDepth,
	}
	if autonomous {
		config["permission"] = "allow"
	}
	return json.Marshal(config)
}

// codexSubagentLimit and opencodeSubagentDepth are Codex's and OpenCode's own
// equivalents of subagentLimits (D84): Codex's [agents] table has a
// concurrency cap but no depth cap it still honours (max_depth exists in the
// binary but is documented there as ignored since its V2 agent runtime), and
// OpenCode has a depth cap but no concurrency cap at all.
const (
	codexSubagentLimit    = 3
	opencodeSubagentDepth = 1
)

// codexConfigFor is Codex's ~/.codex/config.toml: the worktree trusted so its
// onboarding is skipped, every MCP server AgentBox gives every tool, and,
// where compactWindow is set, the same context Claude Code compacts at and a
// subagent concurrency cap (D83, D84). The two bare keys have to come before
// any [table] header — TOML reads a key that follows one as belonging to that
// table rather than to the document root, so model_auto_compact_token_limit
// is written first and [agents] last.
func codexConfigFor(worktree string, servers []mcpServer, compactWindow int64) string {
	var config strings.Builder
	if compactWindow > 0 {
		fmt.Fprintf(&config, "model_auto_compact_token_limit = %d\n", compactWindow)
	}
	fmt.Fprintf(&config, "[projects.%q]\ntrust_level = \"trusted\"\n", worktree)
	for _, s := range servers {
		config.WriteString("\n" + s.codex())
	}
	fmt.Fprintf(&config, "\n[agents]\nmax_concurrent_threads_per_session = %d\n", codexSubagentLimit)
	return config.String()
}

// agentMCPServers is the MCP servers every AI tool is given, written into
// Claude Code's ~/.claude.json, Codex's ~/.codex/config.toml and OpenCode's
// ~/.config/opencode/opencode.json — at creation, and again whenever
// PrepareChatModel rewrites Codex's or OpenCode's configuration.
func agentMCPServers(home string) []mcpServer {
	return []mcpServer{
		// Playwright's page snapshots go outside the worktree, so they don't
		// end up on the agent's branch. --image-responses omit keeps a
		// screenshot out of the agent's own context the way the desktop tools
		// already are (D83): measured on a real page, it cut
		// browser_take_screenshot's result from 193,976 bytes to 511 with no
		// image, and left browser_snapshot's 49,893-byte accessibility tree
		// — what an agent actually clicks by — untouched.
		{"playwright", home + "/.local/share/mise/shims/playwright-mcp",
			[]string{"--cdp-endpoint", BrowserDevTools, "--output-dir", home + "/.cache/playwright-mcp", "--image-responses", "omit"}},
		{"desktop", AgentBinaryPath, []string{"desktop", "mcp"}},
		// What the project remembers, which is the same binary again: search
		// it, report on the task, record what was produced
		// (D72).
		{"memory", AgentBinaryPath, []string{"memory", "mcp"}},
	}
}

// mcpServer is one MCP server every AI tool is given, written into Claude
// Code's ~/.claude.json, Codex's ~/.codex/config.toml and OpenCode's
// ~/.config/opencode/opencode.json.
type mcpServer struct {
	name    string
	command string
	args    []string
}

// codex renders the server as Codex's TOML table. Every value goes through %q,
// so a path or an argument with a quote in it stays one argument.
func (s mcpServer) codex() string {
	quoted := make([]string, len(s.args))
	for i, arg := range s.args {
		quoted[i] = fmt.Sprintf("%q", arg)
	}
	return fmt.Sprintf("[mcp_servers.%s]\ncommand = %q\nargs = [%s]\n", s.name, s.command, strings.Join(quoted, ", "))
}

// brief renders what one agent is told: its machine, its branch, the secrets it
// was given, the project's notes, which the user and the project's lead write
// for every agent of it, and this agent's slice of what the project remembers.
//
// task is what the agent is about to be asked to do, when the caller knows it;
// an empty one is recovered from the agent_created event. It only decides what
// the memory section is about.
func (m *Manager) brief(ctx context.Context, a state.Agent, ip string, envFiles []string, task string) (string, error) {
	secretNames, err := m.Secrets.NamesForAgent(ctx, a.Project, a.Name)
	if err != nil {
		return "", err
	}
	projectNotes, err := notes.Read(m.Paths.ProjectNotes(a.Project))
	if err != nil {
		return "", err
	}
	// What the project knows about this agent's task. A memory that can't be
	// read costs the brief its last section and nothing else: the brief is
	// what makes the machine usable, and it is worth writing without it.
	knowledge, err := m.projectKnowledge(ctx, a, task)
	if err != nil {
		m.logf("couldn't build %s's memory section: %v", a.Ref(), err)
		knowledge = ""
	}
	compactWindow, err := m.agentCompactWindow(ctx, a)
	if err != nil {
		return "", err
	}
	return brief.Render(brief.Data{
		Project:  a.Project,
		Agent:    a.Name,
		Title:    a.Title,
		Worktree: a.Worktree,
		Branch:   a.Branch,
		BaseRef:  a.BaseRef,
		IP:       ip,
		EnvFiles: envFiles,
		Secrets:  secretNames,
		Android:  android.IsProject(a.Worktree),
		GitHub:   m.Creds.HasGitHubLogin(),
		VM:       hostos.InVM(),
		Host:     hostos.Name(),
		Notes:    projectNotes,

		Knowledge:     knowledge,
		CompactWindow: compactWindow,
	})
}

// briefPaths are the files the brief is written to inside an agent: one for any
// tool, and the one Claude Code and Codex each read before their first token.
func (m *Manager) briefPaths() []string {
	home := "/home/" + m.User.Name
	return []string{home + "/AGENTBOX.md", home + "/.claude/CLAUDE.md", home + "/.codex/AGENTS.md"}
}

// EnvPath is the shell file every agent sources, with its Compose project name
// and, for Claude Code agents, the token of the account they were given.
func (m *Manager) EnvPath() string { return "/home/" + m.User.Name + "/.config/agentbox/env" }

func (m *Manager) agentEnv(a state.Agent) (string, error) {
	// Every agent of a project uses the same Compose project name, so a fork or
	// a project base finds the containers and volumes it was copied with.
	env := "# Written by AgentBox.\nexport COMPOSE_PROJECT_NAME=" + shellQuote(a.Project) + "\n"
	// The GitHub token, when the agent has an account, so gh and the API work in it.
	gh, err := m.Creds.GitHubToken(a.GitHubAccount)
	if err != nil {
		return "", err
	}
	if gh != "" {
		env += "export GH_TOKEN=" + shellQuote(gh) + "\nexport GITHUB_TOKEN=" + shellQuote(gh) + "\n"
	}
	if a.AI == "claude" {
		token, err := m.Creds.ClaudeToken(a.ClaudeAccount)
		if err != nil {
			return "", err
		}
		if token != "" {
			env += "export CLAUDE_CODE_OAUTH_TOKEN=" + shellQuote(token) + "\n"
		}
	}
	// The secrets the user gave this agent, in a file of their own so that
	// adding or removing one never rewrites a login. It is sourced last, and a
	// secret can't be named after anything above (secrets.ValidateName), so
	// nothing here is silently replaced. The test matters when the agent's
	// machine was copied from a base that had the file scrubbed away.
	env += "if [ -r " + shellQuote(m.SecretsPath()) + " ]; then . " + shellQuote(m.SecretsPath()) + "; fi\n"
	return env, nil
}

// RewriteAgentEnv writes every ready agent's env file again, so a credential
// you started or stopped sharing reaches the agents that already exist. Their
// AI tool and shells pick it up the next time they start. Agents whose machine
// isn't running are skipped: creating one writes the file fresh anyway.
func (m *Manager) RewriteAgentEnv(ctx context.Context) error {
	agents, err := m.Store.Agents(ctx, "")
	if err != nil {
		return err
	}
	var failed []string
	for _, a := range agents {
		if a.IsLead() || a.Status != state.AgentReady {
			continue
		}
		if inst, err := m.Incus.Instance(ctx, a.Instance); err != nil || inst.Status != "Running" {
			continue
		}
		env, err := m.agentEnv(a)
		if err != nil {
			failed = append(failed, a.Ref())
			continue
		}
		if err := m.Incus.WriteFile(ctx, a.Instance, m.EnvPath(), []byte(env), m.User.UID, m.User.GID, 0o600); err != nil {
			failed = append(failed, a.Ref())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("couldn't update %s", strings.Join(failed, ", "))
	}
	return nil
}

// RewriteBriefs writes the brief again in every running, ready agent of a
// project, and rebuilds its lead's, so a change to the project's notes reaches
// the agents that already exist instead of only the next one made. An AI tool
// reads its brief when it starts, so an agent that is mid-conversation gets the
// new notes in its next session.
//
// Agents whose machine isn't running are skipped: nothing in a stopped machine
// would read the file. Start writes the brief again when one comes back up.
func (m *Manager) RewriteBriefs(ctx context.Context, project string) error {
	p, repo, err := m.project(ctx, project)
	if err != nil {
		return err
	}
	envFiles, err := repo.EnvFiles()
	if err != nil {
		return err
	}
	agents, err := m.Store.Agents(ctx, project)
	if err != nil {
		return err
	}
	var failed []string
	for _, a := range agents {
		if a.Status != state.AgentReady {
			continue
		}
		if a.IsLead() {
			// The lead has no machine: its brief is a file in its private HOME.
			if err := m.configureLead(ctx, a, p, repo.Root, m.LeadSocket(a.Project)); err != nil {
				failed = append(failed, a.Ref())
			}
			continue
		}
		inst, err := m.Incus.Instance(ctx, a.Instance)
		if err != nil || inst.Status != "Running" {
			continue
		}
		if err := m.writeBrief(ctx, a, inst.IPv4(), envFiles); err != nil {
			failed = append(failed, a.Ref())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("couldn't update %s", strings.Join(failed, ", "))
	}
	return nil
}

// writeBrief writes an agent's brief into its machine, over the copy it was
// created with.
func (m *Manager) writeBrief(ctx context.Context, a state.Agent, ip string, envFiles []string) error {
	// No task here: a rewrite is a brief for an agent that already exists, and
	// what it was asked to do is in the agent_created event by now.
	text, err := m.brief(ctx, a, ip, envFiles, "")
	if err != nil {
		return err
	}
	for _, path := range m.briefPaths() {
		if err := m.Incus.WriteFile(ctx, a.Instance, path, []byte(text), m.User.UID, m.User.GID, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// projectEnvFiles lists a project's gitignored env files for a brief written
// after the agent was created. It is the same list creating one copies, unless
// the project gained an env file since.
func (m *Manager) projectEnvFiles(ctx context.Context, project string) []string {
	_, repo, err := m.project(ctx, project)
	if err != nil {
		return nil
	}
	files, err := repo.EnvFiles()
	if err != nil {
		return nil
	}
	return files
}

// SetClaudeAccount moves a running agent to another stored Claude Code account:
// it writes that account's token into the agent and records the change. The AI
// tool picks the new token up the next time it starts.
func (m *Manager) SetClaudeAccount(ctx context.Context, a state.Agent, account string) (state.Agent, error) {
	if a.AI != "claude" {
		return a, fmt.Errorf("%s runs %s, not Claude Code: there is no account to change", a.Ref(), a.AI)
	}
	p, err := m.Store.Project(ctx, a.Project)
	if err != nil {
		return a, err
	}
	name, err := m.ClaudeAccountFor(p, account)
	if err != nil {
		return a, err
	}
	// The token is a file inside the agent, so the machine has to be up.
	if err := m.requireRunning(ctx, a); err != nil {
		return a, err
	}
	a.ClaudeAccount = name
	env, err := m.agentEnv(a)
	if err != nil {
		return a, err
	}
	if err := m.Incus.WriteFile(ctx, a.Instance, m.EnvPath(), []byte(env), m.User.UID, m.User.GID, 0o600); err != nil {
		return a, err
	}
	if err := m.Store.SetAgentClaudeAccount(ctx, a.Project, a.Name, name); err != nil {
		return a, err
	}
	return a, nil
}

// SetGitHubAccount moves a running agent to another stored GitHub account, or
// (with an empty account) back to none: it writes that account's token into
// the agent and records the change. A new shell picks up the new token, and
// so does the AI tool the next time it starts.
func (m *Manager) SetGitHubAccount(ctx context.Context, a state.Agent, account string) (state.Agent, error) {
	p, err := m.Store.Project(ctx, a.Project)
	if err != nil {
		return a, err
	}
	name, err := m.GitHubAccountFor(p, account)
	if err != nil {
		return a, err
	}
	// The token is a file inside the agent, so the machine has to be up.
	if err := m.requireRunning(ctx, a); err != nil {
		return a, err
	}
	a.GitHubAccount = name
	env, err := m.agentEnv(a)
	if err != nil {
		return a, err
	}
	if err := m.Incus.WriteFile(ctx, a.Instance, m.EnvPath(), []byte(env), m.User.UID, m.User.GID, 0o600); err != nil {
		return a, err
	}
	if err := m.Store.SetAgentGitHubAccount(ctx, a.Project, a.Name, name); err != nil {
		return a, err
	}
	return a, nil
}

// ensureSession starts the agent's tmux session unless it is running: window 0
// is a shell, and window 1 runs the AI tool's command line, unless you use the
// tool through the chat.
func (m *Manager) ensureSession(ctx context.Context, a state.Agent) error {
	script := fmt.Sprintf("tmux has-session -t %[1]s 2>/dev/null && exit 0\ntmux new-session -d -s %[1]s -n shell -c %[2]s\n", session, shellQuote(a.Worktree))
	script += toolWindowScript(a)
	var out bytes.Buffer
	if err := m.Incus.UserExec(ctx, a.Instance, m.User.Name, script, nil, &out, &out); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(out.String()))
	}
	return nil
}

// PrepareShell checks that the agent is running and its tmux session exists.
func (m *Manager) PrepareShell(ctx context.Context, a state.Agent) error {
	if err := m.requireRunning(ctx, a); err != nil {
		return err
	}
	return m.ensureSession(ctx, a)
}

// requireRunning returns an actionable error unless the agent is running.
func (m *Manager) requireRunning(ctx context.Context, a state.Agent) error {
	inst, err := m.Incus.Instance(ctx, a.Instance)
	if err != nil {
		return err
	}
	switch inst.Status {
	case "Running":
		return nil
	case "Frozen":
		return fmt.Errorf("%s is paused: run agentbox resume %s", a.Ref(), a.Ref())
	default:
		return fmt.Errorf("%s is %s: run agentbox start %s", a.Ref(), strings.ToLower(inst.Status), a.Ref())
	}
}

// ShellArgs is the command that attaches the terminal to the agent's tmux session.
func (m *Manager) ShellArgs(a state.Agent) []string {
	return ShellCommand(m.Incus.Path(), a.Instance, m.User.Name, a.Worktree)
}

// ShellCommand is the incus command that attaches a terminal to an agent's tmux session.
func ShellCommand(incusPath, instance, user, worktree string) []string {
	return []string{incusPath, "exec", instance, "-t", "--", "runuser", "-l", user, "-c",
		fmt.Sprintf("tmux new-session -A -s %s -c %s", session, shellQuote(worktree))}
}

// ExecCommand is the shell command Exec runs inside the agent.
func ExecCommand(worktree, command string) string {
	return "cd " + shellQuote(worktree) + " && " + command
}

// Exec runs a command in the agent's worktree, in a login shell as the host user.
func (m *Manager) Exec(ctx context.Context, a state.Agent, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := m.requireRunning(ctx, a); err != nil {
		return err
	}
	return m.Incus.UserExec(ctx, a.Instance, m.User.Name, "cd "+shellQuote(a.Worktree)+" && "+command, stdin, stdout, stderr)
}

// Start boots a stopped agent (or resumes a paused one) and restores its tmux session.
func (m *Manager) Start(ctx context.Context, a state.Agent) (incus.Instance, error) {
	inst, err := m.Incus.Instance(ctx, a.Instance)
	if err != nil {
		return inst, err
	}
	switch inst.Status {
	case "Running":
	case "Frozen":
		if _, err := m.Incus.Run(ctx, "resume", a.Instance); err != nil {
			return inst, err
		}
	default:
		if _, err := m.Incus.Run(ctx, "start", a.Instance); err != nil {
			return inst, err
		}
	}
	if inst, err = m.Incus.WaitReady(ctx, a.Instance, readyTimeout); err != nil {
		return inst, err
	}
	if err := m.EnsureAgentAPI(ctx, a); err != nil {
		return inst, err
	}
	// Before the tmux session, so its shells and the AI tool start with
	// whatever the agent's secrets are now, not what they were when it stopped.
	if err := m.WriteSecrets(ctx, a); err != nil {
		return inst, err
	}
	// The same for the project's notes, which may have changed while the
	// machine was down: nothing else rewrites the brief of an agent that
	// already exists. Never let it keep an agent from starting.
	if err := m.writeBrief(ctx, a, inst.IPv4(), m.projectEnvFiles(ctx, a.Project)); err != nil {
		m.logf("couldn't write %s's brief: %v", a.Ref(), err)
	}
	if err := m.ensureSession(ctx, a); err != nil {
		return inst, err
	}
	m.EnsureBrowser(ctx, a)
	return inst, nil
}

func (m *Manager) Stop(ctx context.Context, a state.Agent) error {
	if _, err := m.Incus.Run(ctx, "stop", a.Instance, "--timeout", "30"); err != nil {
		_, err = m.Incus.Run(ctx, "stop", a.Instance, "--force")
		return err
	}
	return nil
}

// Pause freezes every process in the agent. It keeps its memory but uses no CPU.
func (m *Manager) Pause(ctx context.Context, a state.Agent) error {
	_, err := m.Incus.Run(ctx, "pause", a.Instance)
	return err
}

func (m *Manager) Resume(ctx context.Context, a state.Agent) error {
	_, err := m.Incus.Run(ctx, "resume", a.Instance)
	return err
}

type DestroyOptions struct {
	Force        bool // discard uncommitted changes
	DeleteBranch bool
	// DeleteMedia removes the agent's screenshots, recordings, reports, logs
	// and notes along with everything else. Left false, the default, they
	// survive the agent: they're often the best proof of what it did, and
	// deleting is the one part of a destroy that can't be undone. Kept media
	// still expires on its own, per the project's media retention.
	DeleteMedia bool
}

// Destroy deletes the agent's instance, snapshots and worktree. Its branch,
// with every commit the agent made, stays unless DeleteBranch is set. Its
// media stays too, findable in the project's media view, unless DeleteMedia
// is set.
func (m *Manager) Destroy(ctx context.Context, a state.Agent, opts DestroyOptions) error {
	_, repo, err := m.project(ctx, a.Project)
	if err != nil {
		return err
	}
	if !opts.Force {
		if dirty, err := gitrepo.Dirty(a.Worktree); err == nil && dirty {
			return fmt.Errorf("%s has uncommitted changes: commit them in the agent, or use --force to discard them", a.Ref())
		}
	}

	inst, err := m.Incus.Instance(ctx, a.Instance)
	switch {
	case err == nil:
		if inst.Status == "Running" {
			m.handBackFiles(ctx, a)
		}
		m.logf("Deleting instance %s", a.Instance)
		if _, delErr := m.Incus.Run(ctx, "delete", "--force", a.Instance); delErr != nil {
			// The instance may have been destroyed by an earlier attempt, or
			// concurrently, between the check above and this call. Only a
			// delete that still finds something there is a real failure.
			if _, checkErr := m.Incus.Instance(ctx, a.Instance); !errors.Is(checkErr, incus.ErrNotFound) {
				return delErr
			}
			m.logf("Instance %s was already gone: %v", a.Instance, delErr)
		}
	case !errors.Is(err, incus.ErrNotFound):
		return err
	}

	m.logf("Removing worktree %s", a.Worktree)
	if err := repo.RemoveWorktree(a.Worktree); err != nil {
		return err
	}
	deleteAgentRefs(repo, a.Name)
	if opts.DeleteBranch && repo.BranchExists(a.Branch) {
		if err := repo.DeleteBranch(a.Branch); err != nil {
			return err
		}
	}
	if opts.DeleteMedia {
		if err := m.deleteAgentMedia(ctx, a); err != nil {
			return err
		}
	} else if err := m.keepAgentMedia(ctx, a); err != nil {
		return err
	}
	os.RemoveAll(m.Paths.ChatImages(a.Project, a.Name)) // its conversation goes with the row
	return m.Store.RemoveAgent(ctx, a.Project, a.Name)
}

// handBackFiles gives files that container root created in the worktree
// (Docker bind mounts, sudo) back to the host user, so git and the host can
// manage them. It only works while the agent is running.
func (m *Manager) handBackFiles(ctx context.Context, a state.Agent) {
	m.Incus.Run(ctx, "exec", a.Instance, "--", "chown", "-R", fmt.Sprintf("%d:%d", m.User.UID, m.User.GID), a.Worktree)
}

// Status is an agent plus its live instance state.
type Status struct {
	state.Agent
	State string // running, stopped, paused, incomplete (unfinished create) or missing
	IP    string
	// Limits is what the machine is capped at, read from the same `incus list`
	// that gives the state: the machine is the truth about its own limits, and
	// nothing has to be remembered alongside it.
	Limits Limits
}

func (m *Manager) List(ctx context.Context, project string) ([]Status, error) {
	agents, err := m.Store.Agents(ctx, project)
	if err != nil || len(agents) == 0 {
		return nil, err
	}
	instances, err := m.Incus.Instances(ctx)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]incus.Instance, len(instances))
	for _, inst := range instances {
		byName[inst.Name] = inst
	}
	statuses := make([]Status, 0, len(agents))
	for _, a := range agents {
		s := Status{Agent: a, State: "missing"}
		switch {
		case a.IsLead():
			// No machine to look up: the lead is ready once its worktree is there.
			s.State = "host"
			if _, err := os.Stat(a.Worktree); err != nil {
				s.State = "missing"
			}
		default:
			if inst, ok := byName[a.Instance]; ok {
				s.State, s.IP = displayState(inst.Status), inst.IPv4()
				s.Limits = LimitsOf(inst.ExpandedConfig)
			}
		}
		if a.Status == state.AgentCreating {
			s.State = "incomplete"
		}
		statuses = append(statuses, s)
	}
	return statuses, nil
}

func displayState(status string) string {
	if status == "Frozen" {
		return "paused"
	}
	return strings.ToLower(status)
}

// Diff shows everything the agent changed since it was created: commits,
// uncommitted edits and untracked files.
func (m *Manager) Diff(a state.Agent, stat bool, paths ...string) (string, error) {
	return gitrepo.DiffWorktree(a.Worktree, a.BaseCommit, stat, paths...)
}

func (m *Manager) logf(format string, args ...any) {
	if m.Log != nil {
		fmt.Fprintf(m.Log, "==> "+format+"\n", args...)
	}
}

func copyFiles(srcRoot, dstRoot string, rel []string) error {
	for _, r := range rel {
		src, dst := filepath.Join(srcRoot, r), filepath.Join(dstRoot, r)
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, content, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func gitIdentity() (name, email string) {
	get := func(key string) string {
		out, _ := exec.Command("git", "config", "--global", key).Output()
		return strings.TrimSpace(string(out))
	}
	return get("user.name"), get("user.email")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func gitQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
