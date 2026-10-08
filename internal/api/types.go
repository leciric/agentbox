// Package api defines the AgentBox daemon's HTTP API: the JSON types it
// exchanges and a client for them. The daemon serves it on a unix socket; the
// CLI and the desktop app are both clients.
package api

import (
	"encoding/json"
	"time"

	"agentbox/hubapi"
)

// LeadName is the reserved agent name of a project's chat. Its ref,
// "<project>/lead", and the bare project name mean the same conversation.
const LeadName = "lead"

// HomeProject is the key the Home chat — the user's main chat, across every
// project — is kept under, in place of a project's name: its chat is at
// /v1/projects/_home/chat and its ref is "_home/lead". No project can be
// called this (state.HomeProject).
const HomeProject = "_home"

// AgentModelAuto is the Project.AgentModel that asks a project's chat to
// choose a model for each agent it creates, from how hard the task is, rather
// than every agent of the project taking the same one. It is not a model name:
// an agent created without one still falls back to the model new agents start
// on, so the chat leaving it out never fails.
const AgentModelAuto = "auto"

// Which model distils a project's events into memories (D78).
// ConsolidationModelCheap is the cheap model of whichever AI tool the project
// runs, resolved by the daemon rather than named here, and
// ConsolidationModelChat is the project chat's own model, in the chat's own
// session. Anything else is a model id.
const (
	ConsolidationModelCheap = "cheap"
	ConsolidationModelChat  = ""
)

type Project struct {
	// Name is the project's slug: the identifier in its API paths, refs
	// ("<name>/<agent>"), containers and folders, made from DisplayName when
	// the project was added and never changed afterwards. Requests also take
	// DisplayName wherever they take Name.
	Name string `json:"name"`
	// DisplayName is what the user calls the project, free-form, and what
	// is shown wherever they see it; UpdateProjectRequest renames it.
	DisplayName string   `json:"displayName"`
	Root        string   `json:"root"`
	Branch      string   `json:"branch"` // checked out in the main checkout; new agents start here
	EnvFiles    []string `json:"envFiles"`
	Android     bool     `json:"android"` // it builds an Android app, so its agents can use emulators
	// ClaudeAccount is the Claude Code account this project's new agents use;
	// empty means the machine's default account.
	ClaudeAccount string `json:"claudeAccount"`
	// ClaudeAccounts are the Claude Code accounts this project's agents may
	// use; empty allows every account on the machine.
	ClaudeAccounts []string `json:"claudeAccounts"`
	// GitHubAccount is the GitHub account this project's new agents use;
	// empty means the machine's default account.
	GitHubAccount string `json:"githubAccount"`
	// Autonomy is how much this project's chat does on its own: "ask" (propose
	// and wait for you) or "on" (act, within a budget of turns).
	Autonomy string `json:"autonomy"`
	// AgentModel is the model this project's new agents are created on: ""
	// follows the model new agents start on everywhere, a model name is what
	// every agent of this project gets unless one is named for it, and "auto"
	// asks the project's chat to choose per task.
	AgentModel string `json:"agentModel"`
	// AgentSize is the size of the agents the project's chat creates
	// (CreateAgentRequest.Size): "" lets the chat choose for each, "light",
	// "normal" or "heavy" is what each gets whatever the chat asks for.
	AgentSize string `json:"agentSize"`
	// BranchPrefix comes before the slug in the branch an agent is created
	// on: "agentbox/" (the default) makes agentbox/fix-login. It may be empty,
	// or nested like "thiago/agentbox/". Existing agents keep their branches.
	BranchPrefix string `json:"branchPrefix"`
	// FinishNotices is what happens when one of this project's agents
	// finishes: "chat" (tell the project's chat, and let it decide what
	// happens next), "off" (record it in the chat's history, without
	// starting a turn), or "lead" (let the agent that finished decide, with
	// its own FinishNotice — the default for a new project). Questions from
	// agents always start a turn.
	FinishNotices string `json:"finishNotices"`
	// RolloverThreshold is how full this project's chat lets the model's
	// context get, as a percentage of it, before AgentBox consolidates the
	// conversation into the project's memory and carries it on in a fresh
	// session; 80 by default, and 0 switches that off.
	RolloverThreshold int `json:"rolloverThreshold"`
	// ContextBudget is how many estimated tokens one context built from this
	// project's memory may cost — the chat's recap, a worker brief's "What the
	// project knows", or an answer to POST /context; 4,000 by default. A
	// worker agent gets a quarter of it (D75).
	ContextBudget int `json:"contextBudget"`
	// Consolidation is how many new events this project gathers before its
	// chat is asked to distil them into memories (D76); 0 switches
	// consolidation off, both halves of it.
	Consolidation int `json:"consolidation"`
	// ConsolidationModel is the model that distils them (D78): "cheap" for
	// the cheap model of whichever AI tool the project runs, a model id to
	// name one, or "" for whatever the project's chat is running on. "cheap"
	// by default — reading a window of history is not work that needs the
	// model the user chats on.
	ConsolidationModel string `json:"consolidationModel"`
	// Section is the id of the sidebar section the project is in, and "" for
	// a project in no section — which is where every project starts, and
	// where an installation that never makes a section keeps all of them (D79).
	Section string `json:"section"`
	// Position is where the project sits in its list, from 1. Zero means
	// nobody has placed it by hand: it comes after the placed ones, by name.
	Position int `json:"position"`
	// Nesting is whether this project's agents run a real Incus daemon of
	// their own, inside their own container, to test AgentBox features that
	// touch agent machines for real. Off by default: it costs isolation, and
	// needs the base image built with Incus.
	Nesting bool `json:"nesting"`
	// AgentPRs is whether this project's agents push their own branch and
	// open their own pull request when they finish. Off by default: a push
	// publishes, with the user's GitHub token.
	AgentPRs bool `json:"agentPRs"`
	// PRWatch is whether the daemon watches this project's agents' pull
	// requests: "" to follow Settings.PRWatch, "on" or "off" to override it.
	PRWatch string `json:"prWatch"`
	// SyncBase is whether the daemon keeps this project's base branch (main)
	// up to date with its remote: it fetches every few minutes and before it
	// creates an agent, and fast-forwards the local branch when it is strictly
	// behind — never a diverged branch, and a checked-out one only when its
	// checkout has no changes. On by default.
	SyncBase bool `json:"syncBase"`
	// Slots is how many of the project's agents may run at once before its
	// queued ones wait: 0 is auto, shared out from memory (QueueStatus).
	Slots int `json:"slots"`
	// AlwaysQueue queues the project's new agents unless a create says
	// otherwise (CreateAgentRequest.Queue).
	AlwaysQueue bool `json:"alwaysQueue"`
	// PRWatching is what that comes to: whether they are watched now.
	PRWatching bool      `json:"prWatching"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Section is a group of projects in the sidebar (D79). It is a thing of its
// own rather than a name on each project, so renaming one is one write and an
// empty section can wait to be filled.
type Section struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Position is where the section sits among the sections, from 1.
	Position int `json:"position"`
	// Collapsed is whether the user folded it away. The daemon remembers it,
	// so the sidebar looks the same from another machine.
	Collapsed bool      `json:"collapsed"`
	CreatedAt time.Time `json:"createdAt"`
}

// AddSectionRequest makes an empty section at the end of the list.
type AddSectionRequest struct {
	Name string `json:"name"`
}

// UpdateSectionRequest renames a section or folds it away; a nil field stays
// as it is.
type UpdateSectionRequest struct {
	Name      *string `json:"name,omitempty"`
	Collapsed *bool   `json:"collapsed,omitempty"`
}

// ProjectLayout is the whole of how the projects are organised: the sections
// in order, what is in each, and the projects in no section. A reorder sends
// all of it rather than one move, so two of them can't interleave into an
// order neither asked for, and the daemon writes it in one transaction (D79).
//
// It doesn't have to be complete. Anything it doesn't name — a project or a
// section made while the user was dragging — keeps its place, at the end of
// the list it is already in, and a project it names that has since been
// removed is skipped.
type ProjectLayout struct {
	Sections []SectionProjects `json:"sections"`
	// Loose are the projects in no section, in order.
	Loose []string `json:"loose"`
}

// SectionProjects is one section of a ProjectLayout: which one, and what is
// in it, in order.
type SectionProjects struct {
	ID       string   `json:"id"`
	Projects []string `json:"projects"`
}

// Notes are a project's notes for its agents: what everyone working on it
// should know, folded into every agent's brief. The user writes them on the
// project's Settings tab, under Notes and brief, and the lead adds what it learns.
type Notes struct {
	Text string `json:"text"` // markdown; empty when the project has no notes
	// UpdatedAt is when the notes were last written, absent when there are none.
	UpdatedAt time.Time `json:"updatedAt,omitzero,omitempty"`
}

// NotesRequest is new notes for a project: the whole text when it replaces
// them, one entry when a lead appends.
type NotesRequest struct {
	Text string `json:"text"`
}

// EditNoteRequest names one entry of a project's notes by quoting it, and says
// what it becomes. Text is empty when the entry is being removed. There are no
// ids in the file to name an entry by, on purpose: it is markdown a person
// edits by hand.
type EditNoteRequest struct {
	Match string `json:"match"`
	Text  string `json:"text,omitempty"`
}

// NoteChange is what an edit or a removal did, with the notes as they now are:
// the entry as it was and as it now is, so a lead reports what it changed
// rather than only that it changed something.
type NoteChange struct {
	Notes
	Was string `json:"was"`           // the entry before the change
	Now string `json:"now,omitempty"` // after it, absent when it was removed
	// Section is the heading the entry sits under, and FromLead whether that
	// is the lead's own section: a change to the user's own text is the
	// user's to be told about.
	Section  string `json:"section,omitempty"`
	FromLead bool   `json:"fromLead"`
}

// UpdateProjectRequest changes what's set; a nil field stays as it is.
type UpdateProjectRequest struct {
	// DisplayName renames the project: what it is called changes, its slug
	// (Project.Name) never does. Anything but empty, if no other project
	// answers to it in any case.
	DisplayName   *string `json:"displayName,omitempty"`
	ClaudeAccount *string `json:"claudeAccount,omitempty"` // "" goes back to the machine's default
	// ClaudeAccounts replaces the accounts the project's agents may use; an
	// empty list allows every account. It must include the project's own
	// account, as it is after ClaudeAccount is applied.
	ClaudeAccounts *[]string `json:"claudeAccounts,omitempty"`
	GitHubAccount  *string   `json:"githubAccount,omitempty"` // "" goes back to the machine's default
	// MoveClaudeAgents, alongside ClaudeAccount, also moves the project's
	// agents still on the account it replaces to the new one; ignored without
	// ClaudeAccount, and when there's nothing on the old account to move.
	MoveClaudeAgents bool `json:"moveClaudeAgents,omitempty"`
	// MoveGitHubAgents is MoveClaudeAgents for GitHubAccount.
	MoveGitHubAgents bool `json:"moveGitHubAgents,omitempty"`
	// Autonomy is how much the project's chat does on its own: ask or on.
	Autonomy *string `json:"autonomy,omitempty"`
	// AgentModel is the model the project's new agents are created on: "" to
	// follow the model new agents start on, a model name, or "auto".
	AgentModel *string `json:"agentModel,omitempty"`
	// AgentSize is the size of the agents the project's chat creates: "" to
	// let it choose, or "light", "normal" or "heavy".
	AgentSize *string `json:"agentSize,omitempty"`
	// BranchPrefix is what the project's new agents' branches are named with,
	// before the agent's name: "" for none. It must make a valid branch name.
	BranchPrefix *string `json:"branchPrefix,omitempty"`
	// FinishNotices is what a finishing agent does to the project's chat:
	// chat, off or lead.
	FinishNotices *string `json:"finishNotices,omitempty"`
	// RolloverThreshold is how full the chat's context gets before the
	// conversation is compacted, as a percentage of it: 0 switches it off.
	RolloverThreshold *int `json:"rolloverThreshold,omitempty"`
	// ContextBudget is how many estimated tokens one context built from this
	// project's memory may cost.
	ContextBudget *int `json:"contextBudget,omitempty"`
	// Consolidation is how many new events the project gathers before its
	// chat is asked to distil them into memories; 0 switches it off.
	Consolidation *int `json:"consolidation,omitempty"`
	// ConsolidationModel is the model that distils them: "cheap", a model id,
	// or "" for whatever the project's chat runs on.
	ConsolidationModel *string `json:"consolidationModel,omitempty"`
	// Nesting turns this project's agents' nesting on or off: a real Incus
	// daemon of their own, inside their own container. It needs the base
	// image built with Incus (Setup's image.components.incus).
	Nesting *bool `json:"nesting,omitempty"`
	// AgentPRs turns on or off whether this project's agents push their own
	// branch and open their own pull request, in place of the lead.
	AgentPRs *bool `json:"agentPRs,omitempty"`
	// PRWatch is "on" or "off" to override the installation's pull request
	// watch for this project, or "" to follow it again.
	PRWatch *string `json:"prWatch,omitempty"`
	// SyncBase turns on or off keeping this project's base branch up to date
	// with its remote.
	SyncBase *bool `json:"syncBase,omitempty"`
	// Slots pins how many of the project's agents run at once; 0 goes back
	// to auto.
	Slots *int `json:"slots,omitempty"`
	// AlwaysQueue sets whether the project's new agents queue unless a
	// create says otherwise.
	AlwaysQueue *bool `json:"alwaysQueue,omitempty"`
}

type AddProjectRequest struct {
	Path string `json:"path"`
	// Name is what the project is called, anything but empty if no other
	// project answers to it in any case; empty uses the folder's name. Its
	// slug is made from it (Project.Name).
	Name string `json:"name,omitempty"`
	// ClaudeAccount and GitHubAccount are the project's accounts, chosen when
	// it is added rather than afterwards. Empty means the machine's default,
	// and an account that isn't stored is refused, as it is in
	// UpdateProjectRequest.
	ClaudeAccount string `json:"claudeAccount,omitempty"`
	GitHubAccount string `json:"githubAccount,omitempty"`
	// CopyToLinux, in WSL, makes a Path on a Windows drive a clone of it in
	// ~/src/<name> on the distro's own disk, and adds that clone instead of
	// refusing the path (D91). Elsewhere it changes nothing.
	CopyToLinux bool `json:"copyToLinux,omitempty"`
	// Create makes Path a new repository, on main with an initial commit, and
	// adds that: a folder that doesn't exist yet is made, and one that's the top
	// of a repository with commits is added as it is. A folder with files in
	// it and no commits is refused with ErrorFolderNotEmpty unless
	// CommitFiles, which makes them the initial commit; a folder inside
	// another repository is refused.
	Create      bool `json:"create,omitempty"`
	CommitFiles bool `json:"commitFiles,omitempty"`
}

// HomeAddProjectRequest is the Home chat adding a project: a folder on this
// machine (Path), or a repository cloned from URL first, into Path when it's
// given and ~/src/<name> when it isn't. Name defaults to the folder's, or the
// repository's.
type HomeAddProjectRequest struct {
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
	Name string `json:"name,omitempty"`
}

// Settings belong to this installation rather than to one project.
type Settings struct {
	// DefaultClaudeModel is the model new Claude Code agents start on. Empty
	// means AgentBox's own default. Agents that already exist keep whatever
	// they have: this only sets the starting point for the next one.
	DefaultClaudeModel string `json:"defaultClaudeModel"`
	// DefaultAgentContextWindow is the context window new Claude Code agents
	// start with: "" for the installation's compact window (the first of
	// ClaudeContextWindows), or "1000000" for the model's whole window.
	DefaultAgentContextWindow string `json:"defaultAgentContextWindow"`
	// EnforceAgentDefaults makes DefaultClaudeModel and
	// DefaultAgentContextWindow the only model and window the lead may create
	// an agent on. Off, they are its ceiling: it may choose a cheaper model or
	// a shorter window for an easy task, never a dearer one.
	EnforceAgentDefaults bool `json:"enforceAgentDefaults"`
	// DefaultLeadModel is the model a project's lead chats on when its own
	// composer hasn't chosen one. Empty means Claude Code's own default, not
	// AgentBox's. Unlike the agents' default it reaches leads that already
	// exist, the next time their chat starts.
	DefaultLeadModel string `json:"defaultLeadModel"`
	// DefaultLeadContextWindow is DefaultAgentContextWindow for the lead's
	// chat, applied the way DefaultLeadModel is.
	DefaultLeadContextWindow string `json:"defaultLeadContextWindow"`
	// ClaudeModelChoices is the model menu the Claude Code adapter last
	// advertised for this account, plus AgentBox's own small pinned list
	// (D69, marked in each choice's Description). The adapter's part is
	// empty until a Claude Code chat has started at least once; the pinned
	// part is always there.
	ClaudeModelChoices []ChatOptionChoice `json:"claudeModelChoices"`
	// ClaudeContextWindows are the context windows a new agent may choose
	// for each model in ClaudeModelChoices, and for "default", in tokens and
	// smallest first (D91). The first is the installation's compact window,
	// which is what an agent gets when nobody chooses. A model with one entry
	// has no choice to offer.
	ClaudeContextWindows map[string][]int64 `json:"claudeContextWindows"`
	// ClaudeMenuKnown is whether a Claude Code adapter has advertised a menu
	// yet. Until it has, ClaudeModelChoices is only the pinned list and
	// ClaudeEffortChoices is empty, and the app says where the rest comes from.
	ClaudeMenuKnown bool `json:"claudeMenuKnown"`
	// DefaultClaudeEffort is how hard new Claude Code agents think. Empty
	// means AgentBox's own default, like DefaultClaudeModel.
	DefaultClaudeEffort string `json:"defaultClaudeEffort"`
	// ClaudeEffortChoices is the effort menu the Claude Code adapter last
	// advertised, remembered the same way as ClaudeModelChoices. The adapter
	// sends the levels available for the model then running, and some models
	// have none at all, so this is the levels Claude Code has been seen to
	// name rather than a promise about any one model.
	ClaudeEffortChoices []ChatOptionChoice `json:"claudeEffortChoices"`
	// OpenCodeModelChoices is the model menu OpenCode last named, as
	// "provider/model" ids. Like the Claude Code menus it is remembered rather
	// than composed: it comes from an OpenCode chat's ACP session, or from
	// `opencode models` run against AgentBox's own login, and is empty until
	// one of those has happened.
	OpenCodeModelChoices []ChatOptionChoice `json:"openCodeModelChoices"`
	// OpenCodeReady is true when agents can actually run OpenCode: the base
	// image has it and AgentBox has a login for it. A project's chat reads
	// this to know whether it may create OpenCode agents at all.
	OpenCodeReady bool `json:"openCodeReady"`
	// ResumeAfterLimit says whether a chat whose turn was cut short by a
	// Claude usage limit carries on by itself once the limit resets. On
	// unless it was turned off, and unlike the settings above it applies to
	// every agent that already exists, not only to the next one.
	ResumeAfterLimit bool `json:"resumeAfterLimit"`
	// ContinueAfterRestart says whether the agents and chats that AgentBox
	// stopped mid-turn — an update, a crash, a quit, a reboot — start again
	// and carry on with that turn when it starts again. On unless it was
	// turned off.
	ContinueAfterRestart bool `json:"continueAfterRestart"`
	// ClaudeCompactWindow is how many tokens of context a Claude Code or Codex
	// chat holds before its tool compacts it (D83); 0 is the model's whole
	// window. OpenCode has no equivalent to set: its own compaction settings
	// are relative to the model's context, not a fixed token count. Every
	// model call sends the whole context again, so this is what decides what
	// a long piece of work costs per step. Like ResumeAfterLimit it reaches
	// agents that already exist, from their next chat.
	ClaudeCompactWindow int64 `json:"claudeCompactWindow"`
	// UpdateCheck says whether the daemon asks once a day whether a newer
	// AgentBox is out, which is also how installations are counted. On unless
	// it was turned off; see UpdateStatus for what else can keep it off.
	UpdateCheck bool `json:"updateCheck"`
	// UsageStats says whether the update check also sends how many times each
	// feature was used, by day: the Feature keys and their counts, nothing
	// else. On unless it was turned off, and never sent while UpdateCheck is
	// off or something blocks it.
	UsageStats bool `json:"usageStats"`
	// ErrorReports says whether the app sends a report of each uncaught error
	// by itself (ReportKindError). Off unless the user turned it on;
	// ErrorReportsAsked says whether they were ever asked, which the app does
	// the first time an error happens.
	ErrorReports      bool `json:"errorReports"`
	ErrorReportsAsked bool `json:"errorReportsAsked"`
	// PRWatch says whether the daemon watches every agent's open pull request
	// until it's merged or closed, and tells the agent when it conflicts with
	// its base, its checks fail or a reviewer asks for changes. On unless it
	// was turned off; a project can override it (Project.PRWatch).
	PRWatch bool `json:"prWatch"`
	// MediaRetention is how long a removed agent's media is kept before the
	// daemon purges it: one of the MediaRetention values.
	MediaRetention string `json:"mediaRetention"`
	// Language is the language the desktop app speaks, a BCP 47 tag like
	// "en-US" or "pt-BR": DefaultLanguage until somebody picks another. The
	// daemon only keeps it; which languages exist is the app's to say, so a
	// new one is a catalog there and nothing here. The CLI, the brief and the
	// chats stay in English whatever it is.
	Language string `json:"language"`
	// DefaultClaudeCompactWindow is what ClaudeCompactWindow is when nobody
	// chose, so a client can offer to go back to it.
	DefaultClaudeCompactWindow int64 `json:"defaultClaudeCompactWindow"`
	// DiskFloorMin and DiskFloorPercent are the free space the disk guard
	// keeps on every disk AgentBox writes to: the larger of DiskFloorMin bytes
	// and DiskFloorPercent of the disk (DiskGuard).
	DiskFloorMin     int64   `json:"diskFloorMin"`
	DiskFloorPercent float64 `json:"diskFloorPercent"`
	// AutoStopIdle and IdleTimeSeconds are "auto-stop idle agents": while on,
	// the daemon stops a running or paused agent once it has gone
	// IdleTimeSeconds with no chat turn in progress, no running job, no
	// pending question or credential request, no terminal input and no
	// recording. Off unless it was turned on. Stopping keeps the worktree and
	// branch, like stopping by hand.
	AutoStopIdle bool `json:"autoStopIdle"`
	// DockerPruneOnStop says whether stopping an agent — by hand, retired,
	// idle or every agent at once — first frees its Docker space: its build
	// cache and every image no container uses, never its volumes. On unless
	// it was turned off.
	DockerPruneOnStop bool `json:"dockerPruneOnStop"`
	// IdleTimeSeconds is how long an agent may go idle before AutoStopIdle
	// stops it; DefaultIdleTimeSeconds when nobody chose.
	IdleTimeSeconds int `json:"idleTimeSeconds"`
	// AgentQueue is "agent queue": while on, a create may queue its agent
	// (CreateAgentRequest.Queue, Project.AlwaysQueue) and queued agents wait
	// for one of their project's slots (QueueStatus). Off unless it was
	// turned on, and off, no slots are enforced; an agent the VM has no
	// memory for queues either way (CreateAgentRequest.Size).
	AgentQueue bool `json:"agentQueue"`
	// TaskTarget is "tasks go to": where a task of the Tasks tab goes when
	// it starts, unless the task chose for itself (Task.Route) — "agent", a
	// new agent of its own, the default, or "lead", the project's chat, which
	// may split it across several agents.
	TaskTarget string `json:"taskTarget"`
	// LeadRecheck is "lead rechecks agents": while on, every
	// LeadRecheckMinutes the daemon wakes each project's chat with a short
	// status of its running agents and its queue — only when an agent has
	// been idle a while or agents are queued, and not twice for the same
	// state — so it can retire finished agents and free their slots.
	LeadRecheck        bool `json:"leadRecheck"`
	LeadRecheckMinutes int  `json:"leadRecheckMinutes"`
	// ImageCache is "shared image cache": while on, every agent's Docker
	// pulls Docker Hub's images through one cache in AgentBox's VM, so an
	// image is downloaded and stored once. On unless it was turned off.
	// Images from other registries (ghcr.io, quay.io…) are pulled directly.
	ImageCache bool `json:"imageCache"`
	// ImageCacheMaxBytes is the most the cache may hold, the images read
	// longest ago going first; DefaultImageCacheMaxBytes when nobody chose.
	// It also never takes the disk below the disk floor.
	ImageCacheMaxBytes        int64 `json:"imageCacheMaxBytes"`
	DefaultImageCacheMaxBytes int64 `json:"defaultImageCacheMaxBytes"`
	// ImageCacheBytes is how much the cache holds now.
	ImageCacheBytes int64 `json:"imageCacheBytes"`
	// PackageCache is "shared package caches": while on, every agent's pnpm,
	// npm, Yarn, Go, pip, uv, Corepack and Playwright download into one
	// directory in AgentBox's VM, so a dependency or a browser is downloaded
	// once. On unless it was turned off.
	PackageCache bool `json:"packageCache"`
	// PackageCacheMaxBytes is the most the caches may hold together, what
	// was used longest ago going first; DefaultPackageCacheMaxBytes when
	// nobody chose. They also never keep the disk below the disk floor.
	PackageCacheMaxBytes        int64 `json:"packageCacheMaxBytes"`
	DefaultPackageCacheMaxBytes int64 `json:"defaultPackageCacheMaxBytes"`
	// PackageCacheBytes is how much they held when last measured.
	PackageCacheBytes int64 `json:"packageCacheBytes"`
}

// UpdateSettingsRequest changes what's set; a nil field stays as it is.
type UpdateSettingsRequest struct {
	// DefaultClaudeModel is "" to go back to AgentBox's own default.
	DefaultClaudeModel *string `json:"defaultClaudeModel,omitempty"`
	// DefaultAgentContextWindow and DefaultLeadContextWindow are "200k" (or
	// "") for the installation's compact window, or "1m" for the model's
	// whole window, which is refused for a default model without one, like
	// Haiku. A request that moves a role's model to one without a 1M window
	// has to bring its window back to 200k in the same request.
	DefaultAgentContextWindow *string `json:"defaultAgentContextWindow,omitempty"`
	// EnforceAgentDefaults turns Settings.EnforceAgentDefaults on or off.
	EnforceAgentDefaults *bool `json:"enforceAgentDefaults,omitempty"`
	// DefaultLeadModel is "" to go back to Claude Code's own default.
	DefaultLeadModel         *string `json:"defaultLeadModel,omitempty"`
	DefaultLeadContextWindow *string `json:"defaultLeadContextWindow,omitempty"`
	// DefaultClaudeEffort is "" to go back to AgentBox's own default.
	DefaultClaudeEffort *string `json:"defaultClaudeEffort,omitempty"`
	// ResumeAfterLimit turns the automatic resume after a Claude usage limit
	// on or off, for every agent.
	ResumeAfterLimit *bool `json:"resumeAfterLimit,omitempty"`
	// ContinueAfterRestart turns "Continue agents after restarts" on or off.
	ContinueAfterRestart *bool `json:"continueAfterRestart,omitempty"`
	// ClaudeCompactWindow is 0 for the model's whole window, or between
	// 100000 and 1000000 tokens.
	ClaudeCompactWindow *int64 `json:"claudeCompactWindow,omitempty"`
	// UpdateCheck turns the daily update check on or off.
	UpdateCheck *bool `json:"updateCheck,omitempty"`
	// UpdateChannel picks what the update check offers: UpdateChannelStable
	// or UpdateChannelNightly. Changing it checks again at once.
	UpdateChannel *string `json:"updateChannel,omitempty"`
	// UsageStats turns the anonymous usage stats on or off. Off also forgets
	// the counts not sent yet.
	UsageStats *bool `json:"usageStats,omitempty"`
	// ErrorReports turns automatic error reports on or off.
	ErrorReports *bool `json:"errorReports,omitempty"`
	// PRWatch turns the pull request watch on or off.
	PRWatch *bool `json:"prWatch,omitempty"`
	// MediaRetention is one of the MediaRetention values.
	MediaRetention *string `json:"mediaRetention,omitempty"`
	// Language sets Settings.Language: a language tag, "" going back to
	// DefaultLanguage.
	Language *string `json:"language,omitempty"`
	// AutoStopIdle turns "auto-stop idle agents" on or off.
	AutoStopIdle *bool `json:"autoStopIdle,omitempty"`
	// DockerPruneOnStop turns freeing an agent's Docker space on stop on or
	// off.
	DockerPruneOnStop *bool `json:"dockerPruneOnStop,omitempty"`
	// IdleTimeSeconds is how long AutoStopIdle waits before stopping an idle
	// agent, at least 60.
	IdleTimeSeconds *int `json:"idleTimeSeconds,omitempty"`
	// AgentQueue turns "agent queue" on or off.
	AgentQueue *bool `json:"agentQueue,omitempty"`
	// TaskTarget sets "tasks go to": "agent" or "lead".
	TaskTarget *string `json:"taskTarget,omitempty"`
	// LeadRecheck turns "lead rechecks agents" on or off, and
	// LeadRecheckMinutes is how often, from 5 to 1440.
	LeadRecheck        *bool `json:"leadRecheck,omitempty"`
	LeadRecheckMinutes *int  `json:"leadRecheckMinutes,omitempty"`
	// DiskFloorMin sets Settings.DiskFloorMin, in bytes, 0 going back to the
	// default; DiskFloorPercent sets Settings.DiskFloorPercent, a negative
	// one going back to the default.
	DiskFloorMin     *int64   `json:"diskFloorMin,omitempty"`
	DiskFloorPercent *float64 `json:"diskFloorPercent,omitempty"`
	// ImageCache turns the shared image cache on or off, for running agents
	// at once and for the others as they start.
	ImageCache *bool `json:"imageCache,omitempty"`
	// ImageCacheMaxBytes sets its cap, 0 going back to the default; at least
	// 1 GiB otherwise.
	ImageCacheMaxBytes *int64 `json:"imageCacheMaxBytes,omitempty"`
	// ClearImageCache empties it.
	ClearImageCache bool `json:"clearImageCache,omitempty"`
	// PackageCache turns the shared package caches on or off, for running
	// agents at once and for the others as they start.
	PackageCache *bool `json:"packageCache,omitempty"`
	// PackageCacheMaxBytes sets their cap, 0 going back to the default; at
	// least 1 GiB otherwise.
	PackageCacheMaxBytes *int64 `json:"packageCacheMaxBytes,omitempty"`
	// ClearPackageCache empties them.
	ClearPackageCache bool `json:"clearPackageCache,omitempty"`
}

// DefaultLanguage is Settings.Language when nobody chose one.
const DefaultLanguage = "en-US"

// How long a removed agent's media is kept (Settings.MediaRetention).
// Immediately deletes it with the agent; forever never purges it.
const (
	MediaRetentionImmediately = "immediately"
	MediaRetentionDay         = "1d"
	MediaRetentionWeek        = "7d"
	MediaRetentionMonth       = "30d"
	MediaRetentionForever     = "forever"
)

type Agent struct {
	Ref        string `json:"ref"`
	Project    string `json:"project"`
	Name       string `json:"name"`
	Title      string `json:"title"` // what the user calls the agent; may be empty
	Instance   string `json:"instance"`
	AI         string `json:"ai"`
	Autonomous bool   `json:"autonomous"`
	Branch     string `json:"branch"`
	BaseRef    string `json:"baseRef"`
	BaseCommit string `json:"baseCommit"`
	Worktree   string `json:"worktree"`
	Source     string `json:"source"`
	// ClaudeAccount is the Claude Code account whose token the agent holds.
	ClaudeAccount string `json:"claudeAccount"`
	// GitHubAccount is the GitHub account whose token the agent holds; empty
	// means it has none.
	GitHubAccount string `json:"githubAccount"`
	// Interface is how you work with the AI tool: chat, in the app's Chat tab,
	// or cli, its own command line in the terminal.
	Interface string `json:"interface"`
	Chat      string `json:"chat,omitempty"` // the chat session's state, while this daemon has one
	// StalledSince is set while its chat's running turn has stalled
	// (ChatSession.StalledSince): its chat still says running, but nothing
	// has come of it since then.
	StalledSince *time.Time `json:"stalledSince,omitempty"`
	// Background is what its chat left running when its last turn ended — a
	// background command, a monitor — by description (ChatSession.Background).
	// While any runs, an agent whose chat is ready is awaiting, not idle: the
	// work's end wakes it, and it hasn't finished.
	Background []string `json:"background,omitempty"`
	State      string   `json:"state"` // running, stopped, paused, initializing, incomplete, missing or queued
	// QueuePosition is a queued agent's place in its project's queue, 1 for
	// the next to start; absent for any other agent.
	QueuePosition int `json:"queuePosition,omitempty"`
	// Waiting says, in one line, why a queued agent hasn't started yet: its
	// project's slots, or the VM's memory, which every project's agents
	// share. Absent for any other agent.
	Waiting string `json:"waiting,omitempty"`
	// Size is how much of the VM's burst pool the agent takes in its heavy
	// phases (tests, builds, the browser, a recording): "light", "normal" or
	// "heavy", or "" for its project's learned burst (agent.Burst).
	Size      string    `json:"size,omitempty"`
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"createdAt"`
}

// WorktreeFiles is an agent's or a project's lead's worktree files, for @
// mentions in the composer: paths, relative to its root, tracked plus
// untracked-but-not-ignored. Truncated means there were more than were sent.
type WorktreeFiles struct {
	Files     []string `json:"files"`
	Truncated bool     `json:"truncated"`
}

type CreateAgentRequest struct {
	// Task, when set, is sent to the agent as its first message once it is
	// ready. A project's chat uses it to hand work over in one step.
	Task    string `json:"task,omitempty"`
	Project string `json:"project"`
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	// Branch is the slug the agent's branch is named with, after the
	// project's prefix: lowercase kebab-case, like "fix-login-redirect".
	// Empty makes one from Title, then Task, then the agent's name; a
	// branch already taken gets -2, -3… appended.
	Branch    string `json:"branch,omitempty"`
	AI        string `json:"ai"`
	Interface string `json:"interface,omitempty"` // chat (the default) or cli
	// Autonomous starts the AI tool without permission prompts, the agent's
	// own machine being the sandbox. Absent means autonomous, which is what
	// every surface that creates an agent already does; send false explicitly
	// for an agent that asks first.
	Autonomous *bool `json:"autonomous,omitempty"`
	// Model is the model this one agent starts on, whatever the installation's
	// default is. Absent falls back to that default, and then to AgentBox's
	// own. It is stored as given, not checked against the account's menu: the
	// tool resolves a preference itself, and one it refuses is reported in the
	// agent's chat rather than quietly swapped. For AI "claude" it is a Claude
	// Code model name, and for "opencode" one of OpenCode's "provider/model"
	// ids; sending it for Codex or for an agent with no AI tool is an error,
	// so a choice that could never apply fails where it was made.
	Model *string `json:"model,omitempty"`
	// Effort is how hard this one agent thinks, with the same fallback as
	// Model. It is a Claude Code setting only: no other tool has one. Unlike a model, an effort the
	// adapter has never advertised is refused outright rather than resolved,
	// so a value outside the remembered menu is rejected here.
	Effort *string `json:"effort,omitempty"`
	// ContextWindow is where this agent's chat compacts, like "200k" or
	// "1m", and must be one of its model's (D91). Omitted is the
	// installation's compact window. Claude Code agents only.
	ContextWindow *string `json:"contextWindow,omitempty"`
	From          string  `json:"from,omitempty"`
	// ClaudeAccount overrides the project's Claude Code account for this agent.
	ClaudeAccount string `json:"claudeAccount,omitempty"`
	// GitHubAccount overrides the project's GitHub account for this agent.
	GitHubAccount string `json:"githubAccount,omitempty"`
	NoEnv         bool   `json:"noEnv,omitempty"`
	Clean         bool   `json:"clean,omitempty"`
	// CatchUp brings the new agent's machine up to the base image AgentBox
	// makes now, before it is given its task: the system packages and the
	// agent tools its project's base is behind on. It is how refreshing a
	// project base catches the base up, and the task is told what was done,
	// or what failed. An agent that isn't behind is left as it is.
	CatchUp bool `json:"catchUp,omitempty"`
	// FinishNotice is this one agent's own choice of what it does to the
	// project's chat when it genuinely finishes: "chat" wakes it and "off"
	// only records the finish. It only matters when the project's own
	// FinishNotices is "lead" — otherwise the project decides for every
	// agent and this is ignored. Empty defers to the project, which for
	// "lead" means the same as "chat".
	FinishNotice string `json:"finishNotice,omitempty"`
	// Queue puts the agent in its project's queue rather than making it now:
	// it is given its name, title, branch and task at once, and its machine
	// when one of the project's slots is free and the VM has its memory
	// (QueueStatus). Absent follows
	// the project's AlwaysQueue; false makes it now whatever that says. The
	// job the request starts ends as soon as the agent is queued, with the
	// agent in state "queued".
	Queue *bool `json:"queue,omitempty"`
	// Size is what the agent reserves of the VM's memory in its heavy
	// phases, tests, builds, the browser or a recording, which take it from
	// the VM's burst pool and wait when that has none: "light" (~2 GB in
	// all), "heavy" (~8 GB), "normal" (its project's learned burst), or
	// "auto", like absent, which is normal unless the project chose a size
	// for the agents its chat creates (Project.AgentSize). Whether it starts
	// at all waits on its project's learned baseline, what an agent writing
	// code uses. Neither is a cap: an agent may use more while the VM has
	// memory to spare. Whatever Queue
	// says, an agent that doesn't fit in the VM's memory waits in the queue.
	Size string `json:"size,omitempty"`
	// TaskID is the task in the project's plan the agent is made for, which
	// it then works on rather than a task of its own. Task defaults to that
	// task's goal and detail, and Title to its goal.
	TaskID string `json:"taskId,omitempty"`
	// Connectors limits which of the project's connectors the agent is
	// given, by name; absent gives it every one, and an empty list none. An
	// agent's own connectors, added to it later, aren't limited by it.
	Connectors *[]string `json:"connectors,omitempty"`
}

// QueueStatus is the agent queue: how many agents each project may run at
// once, and the agents waiting for one of those slots.
type QueueStatus struct {
	// Enabled is Settings.AgentQueue: off, nothing queues and the slots
	// below are only what they would be.
	Enabled bool `json:"enabled"`
	// Budget is the memory auto slots are shared from, in bytes: the VM's
	// memory. Reserve is what's kept free of it, so auto slots share Budget -
	// Reserve.
	Budget  int64 `json:"budget"`
	Reserve int64 `json:"reserve"`
	// Reserved is what the agents that hold memory, in every project, count
	// for out of Budget - Reserve: each the larger of its reservation and
	// what it uses now. An agent starts only when its own reservation fits
	// beside them.
	Reserved int64 `json:"reserved"`
	// Projects is every project, the ones with nothing queued or running
	// included: their Slots say what they would get alongside the others.
	Projects []ProjectSlots `json:"projects"`
	// Queued is every queued agent, by project and then place in line.
	Queued []QueuedAgent `json:"queued"`
}

// ProjectSlots is one project's share of the agents that run at once.
type ProjectSlots struct {
	Project string `json:"project"`
	// Slots is how many of its agents may run at once: Pinned when the user
	// chose a number, otherwise its share of the budget.
	Slots  int `json:"slots"`
	Pinned int `json:"pinned"` // 0 is auto
	// Peak is the memory one of its agents is expected to reach, in bytes:
	// the median of its latest agents' peaks when PeakLearned, and before any
	// was seen, the memory limit new agents get.
	Peak        int64 `json:"peak"`
	PeakLearned bool  `json:"peakLearned"`
	// Running is how many of its agents hold a slot: running, paused or
	// being made. Queued is how many wait.
	Running int `json:"running"`
	Queued  int `json:"queued"`
	// Agents are its agents that hold a slot, with what they use, as last
	// sampled (every half a minute while the queue or the recheck is on).
	Agents []SlotAgent `json:"agents"`
}

// SlotAgent is what one agent holding a slot uses: now, as last sampled, and
// the most it was ever seen at, which is what its project's peak is learned
// from.
type SlotAgent struct {
	Name       string  `json:"name"`
	Title      string  `json:"title"`
	State      string  `json:"state"`
	Memory     int64   `json:"memory"`     // bytes
	MemoryPeak int64   `json:"memoryPeak"` // bytes
	CPU        float64 `json:"cpu"`        // percent; 100 is one core
	CPUPeak    float64 `json:"cpuPeak"`
}

// QueuedAgent is one agent waiting in its project's queue.
type QueuedAgent struct {
	Ref      string    `json:"ref"`
	Project  string    `json:"project"`
	Name     string    `json:"name"`
	Title    string    `json:"title"`
	Branch   string    `json:"branch"`
	Task     string    `json:"task,omitempty"`
	TaskID   string    `json:"taskId,omitempty"`
	Position int       `json:"position"` // 1 is next
	QueuedAt time.Time `json:"queuedAt"`
	// Waiting is why it hasn't started yet (Agent.Waiting), and Reserved what
	// it will reserve of the VM's memory when it does.
	Waiting  string `json:"waiting,omitempty"`
	Reserved int64  `json:"reserved"`
}

// MoveQueuedRequest puts a queued agent at Position in its project's queue,
// 1 for next; past the end is the end.
type MoveQueuedRequest struct {
	Position int `json:"position"`
}

type ForkRequest struct {
	Name     string `json:"name,omitempty"`
	Title    string `json:"title,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`
	// Checkpoint forks from one of the source's checkpoints instead (its ID,
	// or a turn's number): the branch starts from its files, and the new
	// agent's chat from the conversation up to that turn.
	Checkpoint string `json:"checkpoint,omitempty"`
}

// A Checkpoint is an agent's worktree at the end of one of its chat turns
// (kind "turn"), or as it was just before a rollback (kind "saved"), which
// can be forked from but not rolled back to.
type Checkpoint struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Turn is the chat item ID of the user message heading the turn, and
	// Number which turn of the conversation it is, from 1.
	Turn      string    `json:"turn,omitempty"`
	Number    int       `json:"number"`
	Prompt    string    `json:"prompt"`
	Commit    string    `json:"commit"`
	Head      string    `json:"head"`
	CreatedAt time.Time `json:"createdAt"`
}

// RollbackRequest puts an agent's worktree and conversation back to the end
// of a turn: Checkpoint is its checkpoint's ID, or the turn's number.
type RollbackRequest struct {
	Checkpoint string `json:"checkpoint"`
}

// RollbackResult says where what the agent had before the rollback was saved.
type RollbackResult struct {
	Saved Checkpoint `json:"saved"`
}

// UpdateAgentRequest changes what's set; a nil field stays as it is.
type UpdateAgentRequest struct {
	Title *string `json:"title,omitempty"`
	// ClaudeAccount writes another stored account's token into the agent;
	// "" goes back to the project's account.
	ClaudeAccount *string `json:"claudeAccount,omitempty"`
	// GitHubAccount writes another stored account's token into the agent;
	// "" goes back to the project's account.
	GitHubAccount *string `json:"githubAccount,omitempty"`
	// Interface switches between the chat and the AI tool's command line.
	Interface *string `json:"interface,omitempty"`
}

type Snapshot struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	Head      string    `json:"head"`
}

type SnapshotRequest struct {
	Name       string `json:"name,omitempty"`
	Consistent bool   `json:"consistent,omitempty"`
}

type RestoreRequest struct {
	Snapshot string `json:"snapshot"`
}

// RecreateRequest gives an agent whose machine is gone a new one from the base
// image, for its worktree, branch and chat, which stay as they are: what
// `agentbox vm migrate` does for each agent it moves into AgentBox's VM.
type RecreateRequest struct {
	// Home is a directory, on the daemon's machine, whose contents go into
	// the agent user's home before its AI tool starts: the old machine's chat
	// sessions, so its chat resumes where it was.
	Home string `json:"home,omitempty"`
	// Stopped leaves the new machine stopped, for an agent that wasn't running.
	Stopped bool `json:"stopped,omitempty"`
}

// MigrationCheckRequest asks the daemon to compare what it has with a copy of
// another installation's state.db it was made from (POST /v1/migration/check).
type MigrationCheckRequest struct {
	// Backup is that copy, a path on the daemon's machine.
	Backup string `json:"backup"`
}

// MigrationCheck is what the daemon found: everything Backup had, and whether
// it's here. OK is no problems.
type MigrationCheck struct {
	OK bool `json:"ok"`
	// Found are the things that arrived, one line each, like "2 projects:
	// blog, shop".
	Found []string `json:"found"`
	// Problems are what's missing, one line each.
	Problems []string `json:"problems"`
}

type Base struct {
	Snapshot  string    `json:"snapshot"`
	SavedFrom string    `json:"savedFrom"`
	SavedAt   time.Time `json:"savedAt"`
	// Image is the version of the base image the base descends from, and
	// Tools the version of its agent tools, as the machine it was saved from
	// recorded them; either is empty for a base from before it was recorded.
	Image string `json:"image,omitempty"`
	Tools string `json:"tools,omitempty"`
	// Behind is what the base image AgentBox makes now has that this base
	// doesn't; nil when it has everything, or on Previous. Refreshing the base
	// with CatchUp brings it up to date.
	Behind *BaseBehind `json:"behind,omitempty"`
	// Previous is the base this one replaced, kept by the save so it can be
	// undone; nil when there is nothing to go back to. It is one step only:
	// the next save keeps this base and drops that one.
	Previous *Base `json:"previous,omitempty"`
}

// BaseBehind is how far a project base is behind the base image.
type BaseBehind struct {
	// ImageTo is the image version the base would catch up to, set when the
	// image moved on since ImageFrom, which is empty when the base doesn't
	// record one. Changes are what each version since changed, in words.
	ImageFrom string            `json:"imageFrom,omitempty"`
	ImageTo   string            `json:"imageTo,omitempty"`
	Changes   []BaseImageChange `json:"changes,omitempty"`
	// Components are the optional parts of the image the base lacks, named
	// for a person: "Incus", "Codex".
	Components []string `json:"components,omitempty"`
	// Tools are the agent tools that move, and ToolsUnknown says the base
	// doesn't record its tools, so a catch-up installs every pinned one.
	Tools        []BaseToolChange `json:"tools,omitempty"`
	ToolsUnknown bool             `json:"toolsUnknown,omitempty"`
}

type BaseImageChange struct {
	Version string `json:"version"`
	What    string `json:"what"`
}

// BaseToolChange is one agent tool that moves: From is empty for one the base
// hasn't got, To for one that goes.
type BaseToolChange struct {
	Name string `json:"name"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

type SaveBaseRequest struct {
	Agent string `json:"agent"` // agent name within the project
}

// StopAgentsRequest is POST /v1/agents/stop, "Free resources": stop every
// running or paused agent of every project, or only Refs when it names some.
// It runs as a job of kind "stop-agents", whose result is a StopAgentsResult.
type StopAgentsRequest struct {
	Refs []string `json:"refs,omitempty"`
}

// StopAgentsResult is what stopping the agents gave back. Each agent's
// Memory and CPU are what it held and used just before it was stopped, so
// FreedMemory and FreedCPU are their sums; HostMemoryBefore and
// HostMemoryAfter are the host's own memory in use around the whole job,
// which is what the user actually gets back once the kernel settles.
type StopAgentsResult struct {
	Stopped          []StoppedAgent     `json:"stopped"`
	Failed           []StopAgentFailure `json:"failed,omitempty"`
	FreedMemory      int64              `json:"freedMemory"` // bytes of RAM and swap
	FreedCPU         float64            `json:"freedCPU"`    // percent; 100 is one full core
	HostMemoryBefore int64              `json:"hostMemoryBefore"`
	HostMemoryAfter  int64              `json:"hostMemoryAfter"`
}

type StoppedAgent struct {
	Ref     string  `json:"ref"`
	Title   string  `json:"title,omitempty"`
	Memory  int64   `json:"memory"` // RAM and swap its cgroup held, in bytes
	CPU     float64 `json:"cpu"`    // percent; 100 is one full core
	Working bool    `json:"working"`
}

type StopAgentFailure struct {
	Ref   string `json:"ref"`
	Title string `json:"title,omitempty"`
	Error string `json:"error"`
}

type Job struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`   // create, fork, restore, base-save, image-build
	Target     string          `json:"target"` // project or agent ref
	Status     string          `json:"status"`
	Error      string          `json:"error,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
}

const (
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobCancelled = "cancelled"
)

func (j Job) Done() bool { return j.Status != JobRunning }

type HostUsage struct {
	CPU       float64 `json:"cpu"` // percent of all cores
	Cores     int     `json:"cores"`
	MemUsed   int64   `json:"memUsed"`
	MemTotal  int64   `json:"memTotal"`
	PoolUsed  int64   `json:"poolUsed"`
	PoolTotal int64   `json:"poolTotal"`
	// DiskRead and DiskWrite are what the host's physical disks read and
	// wrote over the sample, in bytes a second: whole disks only, so neither
	// a partition nor a device-mapper device counts the same bytes twice.
	DiskRead  int64 `json:"diskRead"`
	DiskWrite int64 `json:"diskWrite"`
	// Pressure is how much the host is stalling on disk and memory; nil when
	// its kernel doesn't keep pressure stall information.
	Pressure *HostPressure `json:"pressure,omitempty"`
}

// HostPressure is the kernel's pressure stall information (PSI) for the
// host, each the share of the last ten seconds in percent ("avg10"): "some"
// while at least one task was stalled waiting on disk or memory, "full"
// while every non-idle task was at once, which is what freezes the desktop.
type HostPressure struct {
	IOSome     float64 `json:"ioSome"`
	IOFull     float64 `json:"ioFull"`
	MemorySome float64 `json:"memorySome"`
	MemoryFull float64 `json:"memoryFull"`
	// Stalling is set while io full or memory full is past 10%, where the
	// user's desktop starts to freeze.
	Stalling bool `json:"stalling"`
}

type AgentUsage struct {
	Ref       string  `json:"ref"`
	State     string  `json:"state"`
	CPU       float64 `json:"cpu"` // percent; 100 is one full core
	Memory    int64   `json:"memory"`
	Processes int64   `json:"processes"`
	// DiskRead and DiskWrite are what it read from and wrote to the host's
	// disks over the sample, in bytes a second, from its cgroup's io.stat.
	DiskRead  int64 `json:"diskRead"`
	DiskWrite int64 `json:"diskWrite"`
}

type Usage struct {
	Host   HostUsage    `json:"host"`
	Agents []AgentUsage `json:"agents"`
}

// DiskUsageItem is one thing measured: an agent's machine, a project's saved
// base, a project's media, and so on.
type DiskUsageItem struct {
	Label string `json:"label"`
	Bytes int64  `json:"bytes"`
}

// DiskUsageCategory groups items of one kind, largest first, with its own
// total.
type DiskUsageCategory struct {
	Kind  string          `json:"kind"` // DiskMachines, DiskBases, DiskWorktrees, DiskMedia or DiskState
	Label string          `json:"label"`
	Bytes int64           `json:"bytes"`
	Items []DiskUsageItem `json:"items,omitempty"`
}

// Kinds of DiskUsageCategory. In VM mode the machines and bases are on the
// VM's pool disk, the worktrees and media in the host's home, and state.db on
// the VM's system disk.
const (
	DiskMachines  = "machines"
	DiskBases     = "bases"
	DiskWorktrees = "worktrees"
	DiskMedia     = "media"
	DiskState     = "state"
)

// DiskUsage is what AgentBox uses on disk, broken down by kind, largest
// first, with a total. The "Storage pool" indicator in the top bar opens it,
// computed when the popover opens rather than kept warm on a poll: it walks
// every worktree and media directory on the host, and queries Incus for every
// machine and saved base.
type DiskUsage struct {
	Total      int64               `json:"total"`
	Categories []DiskUsageCategory `json:"categories"`
}

// AgentDisk is what one agent takes up on disk, for its info card: its
// machine's root disk (the container's own volume on the storage pool) and its
// worktree on the host, measured separately because they are different disks
// and grow for different reasons. A size is left out when it couldn't be
// measured, such as a machine whose volume Incus can't read. The daemon
// caches each agent's for a minute, so MeasuredAt says how fresh it is.
type AgentDisk struct {
	Machine    *int64    `json:"machine,omitempty"`
	Worktree   *int64    `json:"worktree,omitempty"`
	MeasuredAt time.Time `json:"measuredAt"`
}

// MemoryUsageAgent is one agent's share of the host's memory: what its own
// cgroup holds, running or paused — a paused agent still holds every page it
// had.
type MemoryUsageAgent struct {
	Ref    string `json:"ref"`
	Title  string `json:"title,omitempty"`
	State  string `json:"state"`
	Memory int64  `json:"memory"`
	Swap   int64  `json:"swap"`
}

// ZramUsage is what a host's zram swap devices report: SwapBytes is the
// logical swap they hold, and RealBytes is what that actually costs in RAM
// once compressed.
type ZramUsage struct {
	SwapBytes int64 `json:"swapBytes"`
	RealBytes int64 `json:"realBytes"`
}

// MemoryUsage is the host's memory, broken down the way the "Host memory"
// popover shows it: every agent, largest first, and what's left once they're
// accounted for. Computed when the popover opens, not kept warm on a poll.
type MemoryUsage struct {
	HostTotal  int64              `json:"hostTotal"`
	AgentsUsed int64              `json:"agentsUsed"`
	OtherUsed  int64              `json:"otherUsed"`
	SwapTotal  int64              `json:"swapTotal"`
	SwapUsed   int64              `json:"swapUsed"`
	AgentsSwap int64              `json:"agentsSwap"`
	Zram       *ZramUsage         `json:"zram,omitempty"`
	Agents     []MemoryUsageAgent `json:"agents"`
}

// CPUUsageAgent is one agent's share of the host's CPU: its current use.
type CPUUsageAgent struct {
	Ref   string  `json:"ref"`
	Title string  `json:"title,omitempty"`
	State string  `json:"state"`
	CPU   float64 `json:"cpu"`
}

// CPUUsage is the host's CPU, broken down the way the "Host CPU" popover
// shows it: every agent, largest first, and what the host itself is using
// outside them. Sampled over an interval when the popover opens, the same as
// Usage.
type CPUUsage struct {
	HostCPU   float64         `json:"hostCPU"`
	HostCores int             `json:"hostCores"`
	OtherCPU  float64         `json:"otherCPU"`
	Agents    []CPUUsageAgent `json:"agents"`
}

// ClaudeAccount is one stored Claude Code login agents can be given.
type ClaudeAccount struct {
	Name    string `json:"name"`
	Default bool   `json:"default"` // used when neither the agent nor its project names one
	// SavedAt is the day the token was stored. It is the zero time for a token
	// stored before AgentBox recorded it, which the app shows as unknown.
	SavedAt time.Time `json:"savedAt"`
	// Valid is what Anthropic last said about the token: "valid", "rejected",
	// or "" when it hasn't been checked or couldn't be. A check costs no model
	// tokens and its answer stands for an hour.
	Valid string `json:"valid,omitempty"`
}

type AuthStatus struct {
	Claude         bool            `json:"claude"` // at least one Claude Code account is stored
	Codex          bool            `json:"codex"`
	OpenCode       bool            `json:"opencode"` // agentbox auth opencode stored a provider login
	ClaudeAccounts []ClaudeAccount `json:"claudeAccounts"`
	// GitHub is true when at least one GitHub account is stored.
	GitHub         bool            `json:"github"`
	GitHubAccounts []GitHubAccount `json:"githubAccounts"`
	// GitHubUser and GitHubError describe the default account: who its token
	// belongs to, or why it couldn't be checked.
	GitHubUser  string `json:"githubUser,omitempty"`
	GitHubError string `json:"githubError,omitempty"`
}

// GitHubAccount is one stored GitHub login agents can be given.
type GitHubAccount struct {
	Name    string    `json:"name"`
	Default bool      `json:"default"` // used when neither the agent nor its project names one
	SavedAt time.Time `json:"savedAt"`
	// Login is who this account is on GitHub, remembered when its token was
	// saved. It is empty for an account stored before AgentBox kept logins,
	// until its token is saved again or the Setup page looks it up.
	Login string `json:"login,omitempty"`
}

// GitHubTokenRequest stores a GitHub token under an account.
type GitHubTokenRequest struct {
	Token string `json:"token"`
	// Account names the login; empty means the account called "default".
	Account string `json:"account,omitempty"`
}

// RenameGitHubAccountRequest gives a stored GitHub account another name.
type RenameGitHubAccountRequest struct {
	Name string `json:"name"`
}

// RenamedGitHubAccount is what a rename carried over to the new name. The
// token is the same, so the agents holding it keep running: nothing needs a
// restart.
type RenamedGitHubAccount struct {
	Old  string `json:"old"`
	Name string `json:"name"`
	// Projects are the projects whose new agents get the account.
	Projects []string `json:"projects"`
	// Agents are the agents holding its token, by project/name.
	Agents []string `json:"agents"`
}

// PullRequest is what GitHub knows about a pull request.
type PullRequest struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	State     string     `json:"state"`  // open, closed or merged
	Checks    string     `json:"checks"` // passing, failing, pending, or empty for none
	URL       string     `json:"url"`
	Draft     bool       `json:"draft,omitempty"`
	Additions int        `json:"additions,omitempty"`
	Deletions int        `json:"deletions,omitempty"`
	Comments  int        `json:"comments,omitempty"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
	// BaseBranch and HeadBranch are what it would merge into, and its own
	// branch.
	BaseBranch string `json:"baseBranch,omitempty"`
	HeadBranch string `json:"headBranch,omitempty"`
	// HeadSHA is the commit its branch is at. That, not HeadBranch, is what
	// ties it to an agent: an agent's work is pushed under any branch name.
	HeadSHA string `json:"headSha,omitempty"`
	// Author is who opened it: their GitHub login, and avatar when GitHub
	// sent one.
	Author       string `json:"author,omitempty"`
	AuthorAvatar string `json:"authorAvatar,omitempty"`
	// Agent is the name of this project's agent whose commits it carries,
	// when there is one; only the project pull requests list sets it.
	Agent string `json:"agent,omitempty"`
	// Conflict, Review and Watched come from the pull request watch, which
	// reads an agent's open pull request on its own schedule: whether it
	// conflicts with its base, GitHub's review decision (approved,
	// changes_requested, review_required, or ""), and whether the watch has
	// read it at all — without it, Conflict says nothing either way.
	Conflict bool   `json:"conflict,omitempty"`
	Review   string `json:"review,omitempty"`
	Watched  bool   `json:"watched,omitempty"`
}

// Why a project's pull requests couldn't be read. The app says a different
// sentence for each, because the fix is different: no account is stored at
// all, the account used can't see the repository, GitHub refused its token,
// or something else GitHub said.
const (
	GitHubNoAccount = "noAccount"
	GitHubNoAccess  = "noAccess"
	GitHubBadToken  = "badToken"
	GitHubOtherErr  = "other"
)

// GitHubError is a failure to read GitHub, named well enough for the app to
// turn it into a sentence with a fix in it. Which account was used matters as
// much as what went wrong: a repository one account can't see is another
// account's everyday repository.
type GitHubError struct {
	// Kind is one of GitHubNoAccount, GitHubNoAccess, GitHubBadToken or
	// GitHubOtherErr.
	Kind string `json:"kind"`
	// Message is what went wrong in AgentBox's or GitHub's own words, for the
	// kinds the app has nothing better to say about.
	Message string `json:"message,omitempty"`
	// Account is the stored account this was read with, and Login who that
	// account is on GitHub, when it is known. Repo is what was being read.
	Account string `json:"account,omitempty"`
	Login   string `json:"login,omitempty"`
	Repo    string `json:"repo,omitempty"`
}

// ProjectPullRequests is a project repository's pull requests, and whether
// its GitHub account can merge one.
type ProjectPullRequests struct {
	Project string `json:"project"`
	// GitHub is the repository they belong to, matching Fleet.GitHub.
	GitHub string `json:"github,omitempty"`
	// GitHubAccount is the stored account they were read with: the project's,
	// or the machine's default when it doesn't pick one.
	GitHubAccount string        `json:"githubAccount,omitempty"`
	GitHubError   *GitHubError  `json:"githubError,omitempty"` // why they couldn't be read
	PullRequests  []PullRequest `json:"pullRequests"`
	// Why there is no repository to read, when there is none. NoOrigin is a
	// checkout with no origin remote at all; NonGitHubRemote is the origin it
	// does have, with any credentials redacted, when that origin doesn't
	// resolve to a GitHub repository. Neither is a GitHubError — nothing was
	// read and no account is at fault — but they are different problems with
	// different fixes, and one message for both cost a user a long debugging
	// session (D57).
	NoOrigin        bool   `json:"noOrigin,omitempty"`
	NonGitHubRemote string `json:"nonGitHubRemote,omitempty"`
	// CanMerge is whether the account can merge into this repository.
	// CanMergeKnown is false when GitHub didn't say — some fine-grained and
	// GitHub App tokens omit the field — in which case CanMerge isn't
	// meaningful and the UI should not rely on it alone.
	CanMerge      bool `json:"canMerge,omitempty"`
	CanMergeKnown bool `json:"canMergeKnown,omitempty"`
	// MergeMethods lists the merge methods ("merge", "squash", "rebase")
	// this repository accepts.
	MergeMethods []string `json:"mergeMethods,omitempty"`
	// FetchedAt is when GitHub last answered for this repository; absent
	// means it never has yet. The daemon serves what it has at once and
	// re-reads GitHub behind the answer, so this can be a few seconds old.
	FetchedAt *time.Time `json:"fetchedAt,omitempty"`
	// Refreshing is true while a re-read is running. With no FetchedAt, it
	// means the first read is still out: an empty list isn't an empty
	// repository yet.
	Refreshing bool `json:"refreshing,omitempty"`
}

// MergePullRequestRequest merges a pull request with the given method:
// "merge", "squash" or "rebase".
type MergePullRequestRequest struct {
	Method string `json:"method"`
}

// FleetAgent is one agent of a project, with enough to follow it without
// opening it: what it is, what its chat is doing, what it has changed, what it
// has shown, and where its branch stands on GitHub.
type FleetAgent struct {
	Agent
	Changes AgentChanges `json:"changes"`
	Media   int          `json:"media"` // items it has kept
	PR      *PullRequest `json:"pr,omitempty"`
	// Busy is true while its AI tool is working or waiting for an answer.
	Busy bool `json:"busy,omitempty"`
	// Idle is true when it has finished what it was doing and is holding a
	// machine for nothing. LastActive is when it last did anything.
	Idle       bool       `json:"idle,omitempty"`
	LastActive *time.Time `json:"lastActive,omitempty"`
	// Retire says whether it can be retired now, and what keeps its work.
	Retire RetireAdvice `json:"retire"`
}

// RetireAdvice is whether an agent can be retired, and what would keep its
// work. An agent's deliverable is its branch, which outlives the agent.
type RetireAdvice struct {
	// Safe is false when the agent has work that isn't committed, so retiring
	// it by destroying the machine would lose something.
	Safe   bool   `json:"safe"`
	Reason string `json:"reason,omitempty"` // why it isn't safe, or isn't idle
	Branch string `json:"branch,omitempty"` // what keeps the work afterwards
}

// Retire ways, in order of how much they free.
const (
	// RetirePause freezes it: no CPU, memory kept, back in an instant.
	RetirePause = "pause"
	// RetireStop shuts the machine down: memory freed, disk kept, back in seconds.
	RetireStop = "stop"
	// RetireDestroy removes the machine and the worktree. The branch stays.
	RetireDestroy = "destroy"
)

// Question is an agent asking its project's chat for a decision it can't make
// alone. The lead answers it, or passes it to the user when it can't.
//
// A question can also be a credential request (request_credential, D95):
// Kind github or secret, which only the user answers, from the app, and whose
// Answer is what happened ("pushing works now"), never the credential.
type Question struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Agent   string `json:"agent"`
	Ref     string `json:"ref"`
	// Kind is empty for a decision, github or secret for a credential, or
	// QuestionConnector for a connector request.
	Kind string `json:"kind,omitempty"`
	// SecretName is the variable a secret request's value goes into.
	SecretName string `json:"secretName,omitempty"`
	// Connector is the connector a connector request (Kind
	// QuestionConnector) asks for, and ConnectorURL its server: the
	// project's connector's, or the one the agent gave for a new one.
	Connector    string `json:"connector,omitempty"`
	ConnectorURL string `json:"url,omitempty"`
	// Question is what is asked; on a credential request, the agent's reason.
	Question string `json:"question"`
	Context  string `json:"context,omitempty"` // what the agent was doing
	// Status is pending (waiting for the chat), escalated (waiting for you),
	// answered or cancelled.
	Status     string     `json:"status"`
	Answer     string     `json:"answer,omitempty"`
	AnsweredBy string     `json:"answeredBy,omitempty"` // lead or user
	Escalation string     `json:"escalation,omitempty"` // why the chat couldn't answer
	CreatedAt  time.Time  `json:"createdAt"`
	AnsweredAt *time.Time `json:"answeredAt,omitempty"`
}

// AskRequest is an agent asking its project's chat. The call waits for the answer.
type AskRequest struct {
	Question string `json:"question"`
	Context  string `json:"context,omitempty"`
}

type AnswerQuestionRequest struct {
	Answer string `json:"answer"`
}

// The kinds of credential an agent can ask the user for.
const (
	CredentialGitHub = "github"
	CredentialSecret = "secret"
)

// CredentialRequest is an agent asking the user for a credential it lacks.
// The call waits for the answer, which says what happened and never carries
// the credential.
type CredentialRequest struct {
	Kind   string `json:"kind"`           // github or secret
	Name   string `json:"name,omitempty"` // the variable a secret goes into
	Reason string `json:"reason"`         // what failed, and what it is for
}

// AnswerCredentialRequest is the user's answer to a credential request, from
// the app: a GitHub account (already stored — a new one is saved first, with
// the same route as agentbox auth github), a secret's value, or a refusal.
// Exactly one of them.
//
// A connector request (QuestionConnector) is answered on the same route with
// Connector, the connector's name, once the user has added and connected it
// (the Connectors routes), or with a refusal. Answering turns the connector on
// if it was off and gives it to the agent if its create_agent limit left it
// out; a connector that can't be used yet is refused, and the request keeps
// waiting. The request is also answered on its own when the connector the
// agent asked for becomes usable — a sign-in finishing — so answering one
// already answered that way succeeds with it as it is.
type AnswerCredentialRequest struct {
	GitHubAccount string `json:"githubAccount,omitempty"`
	Value         string `json:"value,omitempty"`
	Connector     string `json:"connector,omitempty"`
	Refuse        bool   `json:"refuse,omitempty"`
	Reason        string `json:"reason,omitempty"` // why it was refused, for the agent
}

// EscalateQuestionRequest passes a question to the user.
type EscalateQuestionRequest struct {
	Why string `json:"why,omitempty"`
}

const EventQuestion = "question"

// RetireRequest retires a project's finished agents.
type RetireRequest struct {
	How string `json:"how"` // pause, stop (the default) or destroy
	// IdleFor is how long an agent must have been idle, like "30m". Empty
	// means any agent that isn't busy right now.
	IdleFor string `json:"idleFor,omitempty"`
	// Agents names the ones to retire; empty means every idle one.
	Agents []string `json:"agents,omitempty"`
	// Force retires an agent whose work isn't committed. Its branch is kept
	// unless merged or pushed, but uncommitted changes in a destroyed
	// worktree are lost.
	Force bool `json:"force,omitempty"`
	// DryRun says what would happen without doing it.
	DryRun bool `json:"dryRun,omitempty"`
}

// RetireResult is what happened, or what would have.
type RetireResult struct {
	How     string         `json:"how"`
	DryRun  bool           `json:"dryRun,omitempty"`
	Retired []RetiredAgent `json:"retired"`
	Skipped []RetiredAgent `json:"skipped"`
}

type RetiredAgent struct {
	Name   string `json:"name"`
	Title  string `json:"title,omitempty"`
	Branch string `json:"branch,omitempty"` // where its work stays
	Reason string `json:"reason,omitempty"` // why it was skipped, or what failed
}

// AgentChanges is the size of an agent's diff against the commit it started from.
type AgentChanges struct {
	Files      int  `json:"files"`
	Insertions int  `json:"insertions"`
	Deletions  int  `json:"deletions"`
	Dirty      bool `json:"dirty"` // it has uncommitted work
}

// AgentEvent is one thing an agent reported: it was created, it finished, it
// asked its project's chat something, or that question was answered. The app
// shows these in the project chat's rail, a thread per agent, instead of the
// prose notices the lead is told the same things in — the notice is written
// for a model reading a conversation, and reads badly in a timeline.
//
// An event is a record of a moment, kept as it was: what the agent had
// changed when it finished, not what it has changed since. The one thing read
// live is the question behind Question, so the thread can show its answer and
// offer to answer it.
type AgentEvent struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Agent   string `json:"agent"`
	Ref     string `json:"ref"`
	Title   string `json:"title,omitempty"` // what the agent was called at the time
	// Kind is created, finished, asked or answered.
	Kind string `json:"kind"`
	// Summary is what there is to read: the agent's last message on a finish,
	// cut down (see Cut), or the task it was given on a creation. A finish
	// with nothing worth quoting leaves it empty.
	Summary string `json:"summary,omitempty"`
	Cut     bool   `json:"cut,omitempty"` // Summary is the start of a longer one
	// Changes and PR are what the agent had to show for itself when it
	// finished, and are only set on a finish.
	Changes *AgentChanges `json:"changes,omitempty"`
	PR      *PullRequest  `json:"pr,omitempty"`
	// Question is the ID of the question this event is about, on asked and
	// answered.
	Question string    `json:"question,omitempty"`
	At       time.Time `json:"at"`
}

// The kinds of AgentEvent.
const (
	AgentCreated  = "created"
	AgentFinished = "finished"
	AgentAsked    = "asked"
	AgentAnswered = "answered"
	// AgentIdleStopped is the daemon stopping an agent on its own, because
	// "auto-stop idle agents" found it idle for as long as the setting allows.
	// Summary is what to show for it, like "Stopped after 2h idle".
	AgentIdleStopped = "idle_stopped"
	// AgentDockerPruned is the daemon freeing an agent's Docker space as it
	// stopped it (Settings.DockerPruneOnStop). Summary says how much, like
	// "Freed 24.0 GiB of Docker images and build cache". Only recorded when
	// it freed something.
	AgentDockerPruned = "docker_pruned"
	// AgentPRBroken is the pull request watch finding the agent's pull
	// request newly broken: Summary says how, PR is the pull request.
	AgentPRBroken = "pr_broken"
	// AgentStalled is the stall watch finding the agent's running turn
	// stalled: no progress for as long as it allows. Summary says for how
	// long.
	AgentStalled = "stalled"
	// AgentChatLost is the agent's AI tool exiting in the middle of a turn:
	// killed, crashed, or cut off with its machine. Summary is what the chat
	// said about it.
	AgentChatLost = "chat_lost"
)

// EventAgentEvent carries an AgentEvent on the event stream.
const EventAgentEvent = "agent.event"

// Fleet is a project's agents, and anything still being created.
type Fleet struct {
	Project  string       `json:"project"`
	Agents   []FleetAgent `json:"agents"`
	Creating []Job        `json:"creating"` // create jobs still running
	// Idle is how many agents finished and are holding a machine for nothing.
	Idle int `json:"idle"`
	// GitHub is the repository the project pushes to, when it has one, and
	// GitHubAccount the stored account it is read with.
	GitHub        string       `json:"github,omitempty"`
	GitHubAccount string       `json:"githubAccount,omitempty"`
	GitHubError   *GitHubError `json:"githubError,omitempty"` // why pull requests couldn't be read
	// PullsFetchedAt is when GitHub last answered about this repository's
	// pull requests, and PullsRefreshing whether a re-read is running. The
	// rest of the fleet is read fresh; only the pull requests are served
	// from cache.
	PullsFetchedAt  *time.Time `json:"pullsFetchedAt,omitempty"`
	PullsRefreshing bool       `json:"pullsRefreshing,omitempty"`
}

// Event is one message on the /v1/events stream.
type Event struct {
	Type string          `json:"type"` // job, job.log, agent, usage
	Time time.Time       `json:"time"`
	Data json.RawMessage `json:"data"`
}

const (
	EventJob     = "job"
	EventJobLog  = "job.log"
	EventAgent   = "agent"
	EventUsage   = "usage"
	EventProject = "project"
	// EventPulls says a repository's pull requests were re-read and
	// something moved, so a client can refresh instead of waiting for its
	// next poll.
	EventPulls = "pulls"
	// EventTheme says the look AgentBox should wear has changed: the host's
	// Omarchy theme, or the setting that decides whether to follow it.
	EventTheme = "theme"
	// EventUpdate carries a new UpdateStatus: a check found something, or the
	// setting behind it changed.
	EventUpdate = "update"
	// EventIncus carries a new IncusStatus: Incus stopped answering the
	// daemon, or answers again.
	EventIncus = "incus"
	// EventDisk carries a new DiskGuard: a disk AgentBox writes to came near
	// its floor of free space, reached it, or has room again, or the guard
	// paused or resumed an agent.
	EventDisk = "disk"
)

// DiskGuard is what the daemon's disk guard last found (GET /v1/disk, and
// EventDisk). It keeps a floor of free space on every disk AgentBox writes
// to: nearing it, it warns; at it, it refuses new agents, forks, image builds
// and saved bases, and pauses the agents writing the most, which it resumes
// once there's room again. It never stops or deletes anything.
type DiskGuard struct {
	// Level is the worst disk's: DiskOK, DiskLow or DiskFull.
	Level string          `json:"level"`
	Disks []DiskGuardDisk `json:"disks"`
	// Paused are the agents the guard paused, by ref.
	Paused []string `json:"paused"`
	// Since is when Level last changed.
	Since time.Time `json:"since"`
	// Message says where things stand, in one line.
	Message string `json:"message"`
}

// DiskGuardDisk is one disk the guard watches, in bytes.
type DiskGuardDisk struct {
	// Label says what's on it: "Storage pool", "Worktrees", a few joined
	// when they share one file system.
	Label string `json:"label"`
	// Path is what was measured; "" for the storage pool.
	Path  string `json:"path,omitempty"`
	Free  int64  `json:"free"`
	Total int64  `json:"total"`
	Floor int64  `json:"floor"`
	Level string `json:"level"`
	// Advice says what frees this disk or gives it room: the command or the
	// Settings change, and what can be cleared.
	Advice string `json:"advice,omitempty"`
}

// Disk guard levels.
const (
	DiskOK   = "ok"
	DiskLow  = "low"
	DiskFull = "full"
)

// IncusStatus is what the daemon's watch on Incus last found
// (daemon/incuswatch.go). While Incus doesn't answer, every call the daemon
// makes to it fails at once with Detail, instead of waiting on it.
type IncusStatus struct {
	Answering bool `json:"answering"`
	// Since is when Incus stopped answering; absent while it answers.
	Since *time.Time `json:"since,omitempty"`
	// Detail says what is wrong, and what the daemon did about it.
	Detail string `json:"detail,omitempty"`
	// Restarted is when the daemon last restarted incus.service, which it
	// does in AgentBox's VM, where it may.
	Restarted *time.Time `json:"restarted,omitempty"`
}

// Appearance is the setting behind Theme: what AgentBox wears.
const (
	// AppearanceFollow wears the desktop theme this machine is running, in
	// whichever mode that theme is. The default, so a machine running Omarchy
	// matches the desktop around it without anyone finding a switch.
	AppearanceFollow = "follow"
	// AppearanceDark and AppearanceLight pin AgentBox to its own colours, one
	// way round or the other, whatever the desktop around it is doing.
	AppearanceDark  = "dark"
	AppearanceLight = "light"
)

// Theme is the look AgentBox wears: the desktop theme this machine is running,
// and the appearance setting, which says whether to follow it and — when it
// doesn't — which way round to be. It is reported whether or not it is
// followed, so the app can say what it found and offer to use it.
//
// The colours are a deliberately small fixed set, because every one of them
// has to mean something both to a stylesheet in the Electron app and to an
// agent's tint2 dock and openbox window decorations. They are always plain
// "#rrggbb", or all empty when there is no theme to report.
type Theme struct {
	// Appearance is the setting: AppearanceFollow, AppearanceLight or
	// AppearanceDark.
	Appearance string `json:"appearance"`
	// Available says a theme was found on this machine. False on every
	// machine without Omarchy, which is most of them.
	Available bool `json:"available"`
	// Name is the theme's, as Omarchy records it ("tokyo-night"), and "" when
	// there is none.
	Name string `json:"name"`
	// Mode is "dark" or "light", and "" when there is no theme.
	Mode string `json:"mode"`
	// Background is the base background, Surface what sits on it (a panel, a
	// dock), Foreground the text on that, Muted the dimmed text, and Accent
	// the one highlight colour that carries most of a theme's character.
	Background string `json:"background"`
	Surface    string `json:"surface"`
	Foreground string `json:"foreground"`
	Muted      string `json:"muted"`
	Accent     string `json:"accent"`
}

// Applied says whether a client should actually wear this theme: there is one,
// and the setting says to follow it. A pinned light or dark is AgentBox's own
// colours, so it is not "applied" even though it changes how the app looks.
func (t Theme) Applied() bool { return t.Appearance == AppearanceFollow && t.Available }

// Describe names the theme for a log line.
func (t Theme) Describe() string {
	if !t.Applied() {
		return "AgentBox's own colours"
	}
	if t.Name == "" {
		return "this machine's theme"
	}
	return t.Name
}

// UpdateThemeRequest sets the appearance: AppearanceFollow, AppearanceLight or
// AppearanceDark.
type UpdateThemeRequest struct {
	Appearance *string `json:"appearance"`
}

// PullsChange reports that a project repository's pull requests were re-read
// and are not what they were.
type PullsChange struct {
	Project   string    `json:"project"`
	GitHub    string    `json:"github"`
	FetchedAt time.Time `json:"fetchedAt"`
}

// ProjectChange reports a project added or removed, from the CLI or the app.
// An empty Name means the list itself changed rather than one project: a
// section was made, renamed or deleted, or the projects were reordered (D79).
type ProjectChange struct {
	Name    string `json:"name"`
	Removed bool   `json:"removed,omitempty"`
}

type JobLogLine struct {
	Job  string `json:"job"`
	N    int    `json:"n"` // the line's position in the log, from 0
	Line string `json:"line"`
}

// AgentChange reports a new state for an agent, or its removal.
type AgentChange struct {
	Ref     string `json:"ref"`
	State   string `json:"state"`
	IP      string `json:"ip,omitempty"`
	Removed bool   `json:"removed,omitempty"`
	// QueuePosition is a queued agent's place in its project's queue
	// (Agent.QueuePosition), so the sidebar's "Queued #N" follows it.
	QueuePosition int `json:"queuePosition,omitempty"`
	// Waiting is why a queued agent hasn't started (Agent.Waiting).
	Waiting string `json:"waiting,omitempty"`
}

// BurstRequest asks, from inside an agent, for a lease on the VM's burst
// pool for a heavy phase: a test run, a build, the browser, a recording. An
// agent holds one lease however many of its phases run at once; Key names
// the phase, and asking again with a key it holds renews it.
type BurstRequest struct {
	Key string `json:"key"`
	// TTLSeconds is how long the phase may hold the lease without renewing
	// or releasing it, the backstop for one that crashed: 15 minutes when 0.
	TTLSeconds int `json:"ttlSeconds,omitempty"`
	// WaitSeconds is how long to wait for the pool, at most 600; 0 waits
	// that long.
	WaitSeconds int `json:"waitSeconds,omitempty"`
}

// BurstLease is the answer to a BurstRequest or its release.
type BurstLease struct {
	Granted bool `json:"granted"`
	// Bytes is the agent's burst, what the lease holds of the pool.
	Bytes int64 `json:"bytes,omitempty"`
	// Why is why it wasn't granted, a clause like "it needs 3 GB of the VM's
	// memory; 2 other agents' tests and builds hold 9 GB and 1 GB is free".
	Why string `json:"why,omitempty"`
	// Env is what the agent's commands run with while it holds a lease: its
	// test runners' parallelism held to the lease. Empty once it holds none.
	Env map[string]string `json:"env,omitempty"`
}

// Self is what the in-agent API reports about the agent calling it.
type Self struct {
	Ref      string `json:"ref"`
	Project  string `json:"project"`
	Agent    string `json:"agent"`
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
	IP       string `json:"ip"`
	State    string `json:"state"`
}

type Error struct {
	Error string `json:"error"`
	// Code, when there is one, says which refusal this is, for a client that
	// offers a way past it rather than only showing the message.
	Code string `json:"code,omitempty"`
}

// ErrorFolderNotEmpty is the Code of AddProjectRequest.Create refusing a
// folder that has files in it: the same request with CommitFiles gets past it.
const ErrorFolderNotEmpty = "folder-not-empty"

// VersionInfo is what GET /v1/version reports about the daemon.
type VersionInfo struct {
	Version string `json:"version"`
	// The daemon process's groups, on the host API. A process keeps the groups
	// it started with, so clients can tell a daemon that started before its
	// user joined incus-admin.
	Groups []int `json:"groups,omitempty"`
	// Whether the daemon can reach Incus: the command on its PATH, and a socket
	// it may open. A daemon that started before host setup says false while the
	// app and the command line say true, and is restarted. Absent from a daemon
	// older than this field, which is left alone.
	Incus *bool `json:"incus,omitempty"`
}

// UpdateStatus is what GET /v1/update reports: whether the daily update check
// runs, and what it last found.
type UpdateStatus struct {
	// Current is the daemon's own version.
	Current string `json:"current"`
	// Enabled is the setting (Settings.UpdateCheck).
	Enabled bool `json:"enabled"`
	// Channel is what the check offers: UpdateChannelStable, only stable
	// releases, or UpdateChannelNightly, the nightly builds as well. It
	// defaults to the channel Current came from.
	Channel string `json:"channel"`
	// Nightly says Current is a nightly build.
	Nightly bool `json:"nightly,omitempty"`
	// Blocked says why the check is off whatever the setting says — a
	// development build, AGENTBOX_NO_UPDATE_CHECK=1 or DO_NOT_TRACK=1 in the
	// daemon's environment — and is empty when nothing is in its way.
	Blocked string `json:"blocked,omitempty"`
	// Available is the newer release the last check found; absent when there
	// is none, or none is known.
	Available *UpdateAvailable `json:"available,omitempty"`
	// CheckedAt is when a check last got an answer.
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
}

// The update channels (UpdateStatus.Channel).
const (
	UpdateChannelStable  = "stable"
	UpdateChannelNightly = "nightly"
)

// UpdateAvailable is a newer release of AgentBox, and its release page. On
// the stable channel, running a nightly, it can be a lower version: the
// latest stable, to go back to.
type UpdateAvailable struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

// TerminalResize is sent as a text frame on a terminal WebSocket to resize it.
// Binary frames carry raw terminal bytes in both directions.
type TerminalResize struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// BrowserStatus describes an agent's desktop and the browser on it: Chromium
// on the agent's virtual display, which the agent drives and you can watch.
// The display runs without Chromium, so the two are reported apart.
type BrowserStatus struct {
	Display bool          `json:"display"` // the display and its VNC server
	Running bool          `json:"running"` // Chromium itself
	Version string        `json:"version,omitempty"`
	Pages   []BrowserPage `json:"pages"` // most recently used first
}

type BrowserPage struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

type BrowserOpenRequest struct {
	URL string `json:"url"`
}

// PreviewInfo is where the preview proxy listens, which serves
// http://<port>.<agent>.<project>.localhost:<its port>. Addr is empty when it's off.
type PreviewInfo struct {
	Addr string `json:"addr"`
}

// MediaItem is something kept as proof of an agent's work.
type MediaItem struct {
	ID    string `json:"id"`
	Agent string `json:"agent"` // project/agent
	// AgentName and AgentTitle say which agent this came from and what it was
	// for, so a project's media can be labelled and filtered without looking
	// each agent up. AgentTitle is empty for an agent without one.
	AgentName  string `json:"agentName,omitempty"`
	AgentTitle string `json:"agentTitle,omitempty"`
	// AgentGone is true when the agent that made this item has been
	// destroyed: it was kept past its agent, per the project's media
	// retention, rather than deleted with it.
	AgentGone bool      `json:"agentGone,omitempty"`
	Kind      string    `json:"kind"` // screenshot, recording, report, log, note or file
	Name      string    `json:"name"`
	File      string    `json:"file,omitempty"` // the file or directory name; empty for a note
	Path      string    `json:"path,omitempty"` // on the host API: where it's stored
	Mime      string    `json:"mime,omitempty"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256,omitempty"`
	Source    string    `json:"source"` // agent or user
	Text      string    `json:"text,omitempty"`
	Meta      MediaMeta `json:"meta"`
	CreatedAt time.Time `json:"createdAt"`
	// ExpiresAt is when a kept item is swept, for an item whose agent is
	// gone; unset for one whose agent still exists.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Removed   bool       `json:"removed,omitempty"` // in events: the item was deleted
	// Unseen is true, in GET /v1/media, for an item the user was told about
	// and hasn't opened or marked seen yet.
	Unseen bool `json:"unseen,omitempty"`
	// Favorite items are never removed automatically, by the retention
	// sweep or with their agent, so they have no ExpiresAt.
	Favorite bool `json:"favorite,omitempty"`
}

// UpdateMediaRequest is PATCH /v1/media/{id}: what to change about an item.
// Unfavoriting one whose agent is gone starts its retention clock then.
type UpdateMediaRequest struct {
	Favorite *bool `json:"favorite,omitempty"`
}

type MediaMeta struct {
	Width    int         `json:"width,omitempty"`
	Height   int         `json:"height,omitempty"`
	Duration float64     `json:"duration,omitempty"` // seconds
	Target   string      `json:"target,omitempty"`   // browser or display
	URL      string      `json:"url,omitempty"`
	Entry    string      `json:"entry,omitempty"` // for a directory, the file to open
	Tests    *TestCounts `json:"tests,omitempty"`
}

type TestCounts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

type ScreenshotRequest struct {
	Target   string `json:"target,omitempty"` // browser (default), display or android
	FullPage bool   `json:"fullPage,omitempty"`
	Name     string `json:"name,omitempty"`
}

type RecordRequest struct {
	Target       string `json:"target,omitempty"` // auto (default), display, browser or android
	Input        string `json:"input,omitempty"`  // playwright (default) or desktop, which overlays the keys and shows the cursor
	Name         string `json:"name,omitempty"`
	LimitSeconds int    `json:"limitSeconds,omitempty"`
}

type RecordingStatus struct {
	Recording    bool       `json:"recording"`
	Target       string     `json:"target,omitempty"`
	Input        string     `json:"input,omitempty"`
	Name         string     `json:"name,omitempty"`
	Source       string     `json:"source,omitempty"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	LimitSeconds int        `json:"limitSeconds,omitempty"`
}

type AddMediaRequest struct {
	Path string `json:"path"`
	Kind string `json:"kind,omitempty"`
	Name string `json:"name,omitempty"`
}

type NoteRequest struct {
	Text string `json:"text"`
	Name string `json:"name,omitempty"`
}

type LogsRequest struct {
	Service  string `json:"service,omitempty"`
	Since    string `json:"since,omitempty"`
	Terminal bool   `json:"terminal,omitempty"`
	Window   string `json:"window,omitempty"`
	Android  bool   `json:"android,omitempty"` // the emulator's logcat
	Package  string `json:"package,omitempty"` // for logcat: only this app's lines
	Name     string `json:"name,omitempty"`
}

type ExportRequest struct {
	Dir string `json:"dir,omitempty"`
}

type ExportResult struct {
	Dir   string `json:"dir"`
	Items int    `json:"items"`
}

// DeleteMediaRequest deletes several items at once: the ones named by ID, or,
// with All, everything the list's own filters match. An ID that is already
// gone is not an error, since the end it asks for is the end it gets.
type DeleteMediaRequest struct {
	IDs []string `json:"ids,omitempty"`
	All bool     `json:"all,omitempty"`
	// Agent and Kind narrow All the same way they narrow the project's list.
	// On an agent's own route the agent is the route's, whatever Agent says.
	Agent string `json:"agent,omitempty"`
	Kind  string `json:"kind,omitempty"`
}

// DeleteMediaResult is what a bulk delete took away. Bytes is what that freed
// on disk, which is why people delete media in the first place.
type DeleteMediaResult struct {
	Deleted int   `json:"deleted"`
	Bytes   int64 `json:"bytes"`
}

const EventMedia = "media"

// Notification is one entry in the app's history of what agents did, across
// projects (GET /v1/notifications, and EventNotification as it happens): an
// agent finishing, asking something, or keeping a screenshot or recording.
// Clicking one goes to its agent, and for media to the item itself.
type Notification struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"` // NotifyFinished, NotifyQuestion or NotifyMedia
	Project string `json:"project"`
	Agent   string `json:"agent"` // the agent's name
	Ref     string `json:"ref"`
	Title   string `json:"title,omitempty"` // what the agent was called at the time
	// Text is what to show under the headline: the finish's summary, the
	// question, or the media item's name.
	Text string `json:"text,omitempty"`
	// Status is a finish's last report status (done, partial, blocked or
	// failed), when the agent reported one before it finished.
	Status string       `json:"status,omitempty"`
	PR     *PullRequest `json:"pr,omitempty"`    // on a finish
	Media  *MediaItem   `json:"media,omitempty"` // on NotifyMedia
	// Question is the question's ID, on NotifyQuestion.
	Question string    `json:"question,omitempty"`
	At       time.Time `json:"at"`
	Seen     bool      `json:"seen,omitempty"`
}

// The kinds of Notification.
const (
	NotifyFinished = "finished"
	NotifyQuestion = "question"
	NotifyMedia    = "media"
)

// SeeNotificationsRequest marks notifications seen: those listed by ID, those
// about the listed media items, or all of them.
type SeeNotificationsRequest struct {
	IDs   []string `json:"ids,omitempty"`
	Media []string `json:"media,omitempty"`
	All   bool     `json:"all,omitempty"`
}

// SeeNotificationsResult is how many were newly marked seen.
type SeeNotificationsResult struct {
	Seen int `json:"seen"`
}

// EventNotification carries a Notification as it is made, and EventNotificationsSeen
// a SeeNotificationsRequest once applied, so every window's bell agrees.
const (
	EventNotification      = "notification"
	EventNotificationsSeen = "notification.seen"
)

// SetupCheck is one thing AgentBox needs, or can use, on this machine.
type SetupCheck struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"` // ok, missing, outdated, optional, warn or updating
	Required bool   `json:"required"`
	Detail   string `json:"detail,omitempty"`
	Fix      string `json:"fix,omitempty"` // a command that fixes it
	// Job is the background job the daemon is fixing it with by itself, whose
	// log the app can follow: only while one runs.
	Job string `json:"job,omitempty"`
}

type SetupStatus struct {
	Ready  bool         `json:"ready"` // every required check is ok
	Checks []SetupCheck `json:"checks"`
	// Image is what building the base image would fetch, so the app can show
	// it before the build starts rather than only a spinner while it runs.
	Image ImageBuild `json:"image"`
}

// ImageComponents are the optional parts of the base image. Both are off by
// default: each is a download most agents never need.
type ImageComponents struct {
	// Android adds scrcpy, which mirrors an agent's Android emulator.
	Android bool `json:"android"`
	// Codex adds the Codex CLI and the ACP adapter the chat drives it with.
	Codex bool `json:"codex"`
	// OpenCode adds the OpenCode CLI, which is its own ACP adapter.
	OpenCode bool `json:"opencode"`
	// DevCaches fills the Go, npm and Electron caches from AgentBox's own
	// repository, for agents that work on AgentBox itself.
	DevCaches bool `json:"devCaches"`
	// Incus adds Incus itself, so a project that turns nesting on can run a
	// real Incus daemon inside an agent's container.
	Incus bool `json:"incus"`
}

// ImageBuild describes the base image build: the version it would produce, the
// optional components chosen for it, and what a local build downloads.
type ImageBuild struct {
	// Version is the image version this AgentBox builds.
	Version string `json:"version"`
	// Components are the optional parts the next build will include, as saved
	// on this installation.
	Components ImageComponents `json:"components"`
	// Installed are the components the base image on this machine really has.
	// They differ from Components after someone turns one on and hasn't
	// rebuilt.
	Installed ImageComponents `json:"installed"`
	// Downloads is everything a local build can fetch, optional parts
	// included, so the app can show what turning one on would cost.
	Downloads []ImageDownload `json:"downloads"`
	// Hint is one line about the sizes, to show with the list.
	Hint string `json:"hint"`
}

// ImageDownload is one thing a local base image build fetches.
type ImageDownload struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"` // one sentence on why an agent has it
	MB      int    `json:"mb"`      // approximate download size in megabytes
	// Option is the component that fetches it: empty for every build,
	// otherwise "android", "codex", "opencode" or "dev-caches".
	Option string `json:"option,omitempty"`
}

// BuildImageRequest asks for the agents' base image to be made. A nil
// component keeps what the installation already chose, so rebuilding never
// silently drops one someone turned on.
type BuildImageRequest struct {
	Android   *bool `json:"android,omitempty"`
	Codex     *bool `json:"codex,omitempty"`
	OpenCode  *bool `json:"opencode,omitempty"`
	DevCaches *bool `json:"devCaches,omitempty"`
	Incus     *bool `json:"incus,omitempty"`
}

const (
	SetupOK       = "ok"
	SetupMissing  = "missing"
	SetupOutdated = "outdated"
	SetupOptional = "optional"
	// SetupWarn is something present and usable, but with a caveat worth
	// reading — unlike missing, it never blocks the setup wizard: a Claude
	// Code token Anthropic rejected, or a base image whose agent tools failed
	// to update in place.
	SetupWarn = "warn"
	// SetupUpdating is something usable that the daemon is bringing up to date
	// by itself, in the background (SetupCheck.Job): the base image while its
	// agent tools move on. Like ok, it doesn't block the setup wizard.
	SetupUpdating = "updating"
)

type ClaudeTokenRequest struct {
	Token string `json:"token"`
	// Account names the login; empty means the account called "default".
	Account string `json:"account,omitempty"`
}

// RenameClaudeAccountRequest gives a stored Claude Code account another name.
type RenameClaudeAccountRequest struct {
	Name string `json:"name"`
}

// RenamedClaudeAccount is what a rename carried over to the new name. The
// token is the same, so the agents on it keep running: nothing needs a restart.
type RenamedClaudeAccount struct {
	Old  string `json:"old"`
	Name string `json:"name"`
	// Projects are the projects whose own account or allow-list named it.
	Projects []string `json:"projects"`
	// Agents are the agents on it, by project/name, the projects' chats
	// included.
	Agents []string `json:"agents"`
}

// ClaudeLoginRequest starts an in-app Claude Code login: AgentBox runs
// `claude setup-token` itself and stores what it mints (D59).
type ClaudeLoginRequest struct {
	// Account names the login; empty means the account called "default".
	Account string `json:"account,omitempty"`
}

// ClaudeLogin is how far a login has got, and what it needs from you. It is
// polled while its job runs: Status is the job's, and the URLs appear a moment
// after it starts.
type ClaudeLogin struct {
	Job     string `json:"job"`
	Account string `json:"account"`
	// Status is the login job's: running, succeeded, failed or cancelled.
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	// URL is the page to approve, empty until Claude Code asks for a browser.
	// Approving it finishes the login on its own, because it comes back to
	// Claude Code on this machine.
	URL string `json:"url,omitempty"`
	// CodeURL is a second, parallel login, for a browser that can't reach this
	// machine: approving it ends in a code, which goes back through
	// ClaudeLoginCodeRequest. Only one of the two is worth finishing.
	CodeURL string `json:"codeUrl,omitempty"`
}

// ClaudeLoginCodeRequest hands a running login the code copied out of CodeURL.
type ClaudeLoginCodeRequest struct {
	Code string `json:"code"`
}

// AndroidStatus describes an agent's Android emulator.
type AndroidStatus struct {
	Available bool     `json:"available"`         // this machine can run emulators: KVM and an Android SDK
	Problem   string   `json:"problem,omitempty"` // why it can't
	SDK       string   `json:"sdk,omitempty"`     // the Android SDK on the host, shared read-only with agents
	Images    []string `json:"images"`            // installed system images, best first
	Running   bool     `json:"running"`           // the emulator runs
	Booted    bool     `json:"booted"`            // and Android has finished starting
	Image     string   `json:"image,omitempty"`   // the running device's system image
	Device    string   `json:"device,omitempty"`  // like "Pixel 7, Android 15 (API 35)"
}

type AndroidStartRequest struct {
	Image    string `json:"image,omitempty"`    // a system image; default: the best installed one
	MemoryMB int    `json:"memoryMB,omitempty"` // default 2048
	Cores    int    `json:"cores,omitempty"`    // default 4
}

type AndroidInstallRequest struct {
	Path string `json:"path"` // an APK in the agent; relative paths are relative to its worktree
}

type AndroidInstallResult struct {
	Output string `json:"output"`
}

// InAgentSocket is where the in-agent API socket appears inside every agent.
const InAgentSocket = "/run/agentbox.sock"

// InAgentMarker is a file provision.sh writes into the agent base image, so
// code can tell it's running on an agent's machine without relying on
// InAgentSocket, which a boot race can hide for a while (replugHiddenSocket).
const InAgentMarker = "/etc/agentbox-image"

// The hub: accounts, and the environments that connect to it. A hub serves
// these, not the daemon, so they are defined in agentbox/hubapi, which the hub
// imports too. They are aliased here because the CLI, the desktop app and the
// generated TypeScript all read the API's types from one place.

type (
	HubUser                     = hubapi.HubUser
	HubSignupRequest            = hubapi.HubSignupRequest
	HubLoginRequest             = hubapi.HubLoginRequest
	HubSession                  = hubapi.HubSession
	HubEnvironment              = hubapi.HubEnvironment
	HubCreateEnvironmentRequest = hubapi.HubCreateEnvironmentRequest
	HubEnvironmentToken         = hubapi.HubEnvironmentToken
)

// RemoteStatus is this machine's connection to a hub, as an environment.
type RemoteStatus struct {
	Configured bool      `json:"configured"`
	Hub        string    `json:"hub,omitempty"`
	Connected  bool      `json:"connected"`
	Since      time.Time `json:"since,omitzero,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type RemoteConnectRequest struct {
	Hub   string `json:"hub"`
	Token string `json:"token"`
}

// The features anonymous usage stats count (Settings.UsageStats). A key names
// what was used and nothing about what it was used on: no names, paths,
// repositories or text ever go into one, which is why they are a fixed list
// rather than made up where they are counted. The model, effort and context
// keys are the one exception, still from fixed lists: a prefix below, the AI
// tool, and a value matched against the daemon's own list of known models,
// efforts and windows (internal/daemon/usagemodels.go), anything else
// "other", like turn.model.claude.opus_5_5 or agent.create.context.claude.1m. The daemon counts
// the first group at its own chokepoints; the app counts the second, the ones
// only it can see, through POST /v1/usage-stats/{feature}, which takes nothing
// but a key on this list. The keys go to agentbox.linting.dev as they are, so
// renaming one splits its history there: add a key rather than reuse one.
const (
	FeatureAgentCreateClaude   = "agent.create.claude"
	FeatureAgentCreateCodex    = "agent.create.codex"
	FeatureAgentCreateOpenCode = "agent.create.opencode"
	FeatureAgentCreateByLead   = "agent.create.by_lead"
	FeatureAgentDestroy        = "agent.destroy"
	FeatureAgentRetire         = "agent.retire"
	FeatureAgentFork           = "agent.fork"
	FeatureAgentSnapshot       = "agent.snapshot"
	FeatureAgentRestore        = "agent.restore"
	FeatureAgentRollback       = "agent.rollback"
	FeatureAgentForkTurn       = "agent.fork.turn"
	FeatureAgentTurn           = "agent.turn"
	FeatureLeadTurnClaude      = "lead.turn.claude"
	FeatureLeadTurnCodex       = "lead.turn.codex"
	FeatureLeadTurnOpenCode    = "lead.turn.opencode"
	FeatureAgentCreateModel    = "agent.create.model"
	FeatureAgentCreateEffort   = "agent.create.effort"
	FeatureAgentCreateContext  = "agent.create.context"
	FeatureTurnModel           = "turn.model"
	FeatureTurnEffort          = "turn.effort"
	FeatureTurnContext         = "turn.context"
	FeatureChatModel           = "chat.model.change"
	FeatureChatEffort          = "chat.effort.change"
	FeatureChatWindow          = "chat.window.change"
	FeatureChatMode            = "chat.mode.change"
	FeatureProjectAdd          = "project.add"
	FeatureNotesSave           = "notes.save"
	FeaturePullMerge           = "pr.merge"
	FeatureQuestionAnswer      = "question.answer"
	FeatureSecretSet           = "secret.set"
	FeatureImageBuild          = "image.build"
	FeatureSettingsChange      = "settings.change"

	FeatureDesktopOpen         = "desktop.open"
	FeatureTerminalOpen        = "terminal.open"
	FeatureAndroidOpen         = "android.open"
	FeatureAgentMediaView      = "media.view.agent"
	FeatureProjectMediaView    = "media.view.project"
	FeaturePullList            = "pr.list"
	FeatureMemoryView          = "memory.view"
	FeatureTokensView          = "tokens.view"
	FeatureSettingsEnvironment = "settings.view.environment"
	FeatureSettingsAccounts    = "settings.view.accounts"
	FeatureSettingsLead        = "settings.view.lead"
	FeatureSettingsAgents      = "settings.view.agents"
	FeatureSettingsGeneral     = "settings.view.general"
	FeatureSettingsModels      = "settings.view.models"
	FeatureSettingsResources   = "settings.view.resources"
	FeatureSettingsProject     = "settings.view.project"
	FeatureSettingsSearch      = "settings.search"
	FeatureMenuOpenChat        = "menu.agent.open_chat"
	FeatureMenuOpenTerminal    = "menu.agent.open_terminal"
	FeatureMenuInfo            = "menu.agent.info"
	FeatureMenuLifecycle       = "menu.agent.lifecycle"
	FeatureMenuRetire          = "menu.agent.retire"
	FeatureMenuCopyBranch      = "menu.agent.copy_branch"
	FeatureMenuOpenPullRequest = "menu.agent.open_pr"
	FeatureMenuDestroy         = "menu.agent.destroy"
)

// UsageStatsPending is GET /v1/usage-stats/pending: exactly what the next
// update check would send, for Settings to show. Usage and Events are the
// JSON bodies as they would be posted, "" when nothing waits.
type UsageStatsPending struct {
	// On is whether anything is counted and sent at all (Settings.UsageStats,
	// and nothing blocking the update check).
	On        bool   `json:"on"`
	UsageURL  string `json:"usageUrl"`
	Usage     string `json:"usage"`
	EventsURL string `json:"eventsUrl"`
	Events    string `json:"events"`
	// EventsWaiting is every event not sent yet, of which Events holds the
	// first report's worth.
	EventsWaiting int `json:"eventsWaiting"`
}

// AppFeatures are the keys the app may count through the API.
var AppFeatures = []string{
	FeatureDesktopOpen, FeatureTerminalOpen, FeatureAndroidOpen, FeatureAgentMediaView, FeatureProjectMediaView,
	FeaturePullList, FeatureMemoryView, FeatureTokensView,
	FeatureSettingsEnvironment, FeatureSettingsAccounts, FeatureSettingsLead, FeatureSettingsAgents,
	FeatureSettingsGeneral, FeatureSettingsModels, FeatureSettingsResources, FeatureSettingsProject, FeatureSettingsSearch,
	FeatureMenuOpenChat, FeatureMenuOpenTerminal, FeatureMenuInfo, FeatureMenuLifecycle, FeatureMenuRetire,
	FeatureMenuCopyBranch, FeatureMenuOpenPullRequest, FeatureMenuDestroy,
}

// Problem reports (internal/report, internal/daemon/report.go): what a user
// sends AgentBox's developers from the app's "Report a problem" or `agentbox
// report`, and what the app sends by itself about an uncaught error once
// Settings.ErrorReports is on. The daemon assembles, redacts and sends every
// one, to agentbox.linting.dev, so there is one redaction for all of it.

// The kinds of report.
const (
	ReportKindProblem = "problem" // the user's own, with their message
	ReportKindError   = "error"   // an uncaught error the app sent by itself
)

// The sections a report may carry, by ID. The daemon makes the first four;
// the app adds its own.
const (
	ReportSectionSystem    = "system"     // version, OS, mode and setup state
	ReportSectionDaemonLog = "daemon-log" // the end of daemon.log
	ReportSectionVMLog     = "vm-log"     // the end of the VM supervisor's vm.log, in a VM
	ReportSectionAppLog    = "app-log"    // the end of the app's main-process log
	ReportSectionAppErrors = "app-errors" // the app's recent uncaught errors, main process and windows
	ReportSectionError     = "error"      // the one error an error report is about
)

// ReportSection is one part of a report, which the user sees in full and
// may leave out before it's sent.
type ReportSection struct {
	ID      string `json:"id"` // one of the ReportSection* IDs, or another [a-z0-9-] name
	Title   string `json:"title"`
	Content string `json:"content"`
}

// ReportDraftRequest is POST /v1/reports/draft: the sections only the client
// can collect, to be redacted alongside the daemon's.
type ReportDraftRequest struct {
	Sections []ReportSection `json:"sections"`
}

// ReportDraft is everything a report would send, already redacted: what the
// user is shown before sending it.
type ReportDraft struct {
	// Install, Version, OS and Arch go with every report, as they go with the
	// update check: the install is the same random ID.
	Install  string          `json:"install"`
	Version  string          `json:"version"`
	OS       string          `json:"os"`
	Arch     string          `json:"arch"`
	Sections []ReportSection `json:"sections"`
	// Endpoint is where the report goes.
	Endpoint string `json:"endpoint"`
}

// ReportRequest is POST /v1/reports: send a report. The daemon redacts the
// message and every section again before sending them.
type ReportRequest struct {
	Kind     string          `json:"kind"` // ReportKindProblem or ReportKindError
	Message  string          `json:"message"`
	Sections []ReportSection `json:"sections"`
}

// ReportSent is the answer to a report that was sent.
type ReportSent struct {
	ID string `json:"id"` // the server's ID for it, to quote when asking about it
}
