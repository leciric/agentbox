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
	// CodexAccount and CursorAccount are the Codex and Cursor accounts this
	// project's new agents and its chat use; empty means the machine's
	// default. OpenCodeAccounts is OpenCode's, one per provider (provider →
	// account); a provider it leaves out gets that provider's default.
	CodexAccount     string            `json:"codexAccount"`
	OpenCodeAccounts map[string]string `json:"openCodeAccounts"`
	CursorAccount    string            `json:"cursorAccount"`
	// Autonomy is how much this project's chat does on its own: "ask" (propose
	// and wait for you) or "on" (act, within a budget of turns).
	Autonomy string `json:"autonomy"`
	// AgentModel is the model this project's new agents are created on: ""
	// follows the model new agents start on everywhere, a model name is what
	// every agent of this project gets unless one is named for it, and "auto"
	// asks the project's chat to choose per task.
	AgentModel string `json:"agentModel"`
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
	// FullTestSuite is whether this project's agents may run a whole test
	// suite while Settings.BlockFullSuite refuses them; it means nothing
	// while that is off. Off by default.
	FullTestSuite bool `json:"fullTestSuite"`
	// PickUpIssues is whether the project hands its open GitHub issues
	// labelled IssueLabel to new agents by itself. Off by default.
	PickUpIssues bool `json:"pickUpIssues"`
	// PRWatching is what that comes to: whether they are watched now.
	PRWatching bool      `json:"prWatching"`
	CreatedAt  time.Time `json:"createdAt"`
	// IconVersion changes whenever the project's icon does (GET
	// /v1/projects/{project}/icon): when it was last changed, in unix ms, 0
	// when it has never been looked for. The app keys its copy by it.
	IconVersion int64 `json:"iconVersion"`
}

// ProjectActivity is when a project last did anything, and when the user
// last wrote to it (GET /v1/project-activity), for ordering the sidebar.
type ProjectActivity struct {
	Project string `json:"project"`
	// LastActivity is the newest chat item of any kind in the project's
	// lead's or any of its agents' chats.
	LastActivity *time.Time `json:"lastActivity,omitempty"`
	// LastMessage is the newest message the user wrote to the lead or any of
	// its agents: not one AgentBox woke a chat with, nor a hidden prompt.
	LastMessage *time.Time `json:"lastMessage,omitempty"`
}

// ProjectIcon is what a project shows as its icon.
type ProjectIcon struct {
	// Source is "chosen" (the user's), "detected" (found in the repository)
	// or "none".
	Source string `json:"source"`
	Emoji  string `json:"emoji,omitempty"`
	// Image is a data: URL (data:image/png;base64,...).
	Image string `json:"image,omitempty"`
	// DetectedFrom is the file or URL a detected icon came from, e.g.
	// "public/favicon.svg" or "github.com/leciric".
	DetectedFrom string `json:"detectedFrom,omitempty"`
	// HasDetected is whether there is a detected icon to go back to.
	HasDetected bool `json:"hasDetected,omitempty"`
}

// SetProjectIconRequest chooses a project's icon: exactly one of Emoji and
// Image.
type SetProjectIconRequest struct {
	Emoji string `json:"emoji,omitempty"`
	// Image is a data: URL; png, jpeg, gif, webp, svg+xml or x-icon
	// (vnd.microsoft.icon), at most 512 KiB decoded.
	Image string `json:"image,omitempty"`
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

// SidebarLayout is how UI v2's sidebar arranges the projects: for now, the
// ones the user hid. It belongs to the installation, so every window and the
// phone app read the same one; v1's sections (D79) never see it, and nothing
// but the sidebar reads it. GET /v1/sidebar reads it and PUT /v1/sidebar
// replaces it whole, answering with what was kept: each project once, and
// none that's gone.
type SidebarLayout struct {
	// Hidden are the projects hidden from the sidebar, which shows one again
	// only while it has something going on.
	Hidden []string `json:"hidden"`
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
	// CodexAccount and CursorAccount pick the project's Codex and Cursor
	// accounts, "" for the machine's default; OpenCodeAccounts replaces its
	// OpenCode account per provider. Agents already created keep theirs; the
	// project's chat moves to them as its tool next starts.
	CodexAccount     *string            `json:"codexAccount,omitempty"`
	OpenCodeAccounts *map[string]string `json:"openCodeAccounts,omitempty"`
	CursorAccount    *string            `json:"cursorAccount,omitempty"`
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
	// FullTestSuite turns on or off whether this project's agents may run a
	// whole test suite while Settings.BlockFullSuite refuses them.
	FullTestSuite *bool `json:"fullTestSuite,omitempty"`
	// PickUpIssues turns on or off handing the project's issues labelled
	// IssueLabel to new agents by itself.
	PickUpIssues *bool `json:"pickUpIssues,omitempty"`
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
	// CodexAccount, OpenCodeAccounts (provider → account) and CursorAccount
	// are its accounts for the other tools, the same way.
	CodexAccount     string            `json:"codexAccount,omitempty"`
	OpenCodeAccounts map[string]string `json:"openCodeAccounts,omitempty"`
	CursorAccount    string            `json:"cursorAccount,omitempty"`
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
	// CursorModelChoices is the model menu Cursor named for AgentBox's
	// sign-in, each with the effort levels it takes: asked of Cursor on this
	// machine (internal/cursor) in the background, and empty until that has
	// answered once.
	CursorModelChoices []ChatOptionChoice `json:"cursorModelChoices"`
	// CursorReady is true when agents are signed in to Cursor, which is all
	// a Cursor agent needs: every base image has its SDK.
	CursorReady bool `json:"cursorReady"`
	// DefaultCursorModel and DefaultCursorEffort are what new Cursor agents
	// start on; "" is Cursor's Auto and the model's own default level.
	DefaultCursorModel  string `json:"defaultCursorModel"`
	DefaultCursorEffort string `json:"defaultCursorEffort"`
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
	// CreditAITool says whether agents' commits and pull requests credit the
	// AI tool that wrote them, with its Co-authored-by trailer and "Generated
	// with …" line. Off unless it was turned on; it reaches an agent as it
	// starts, and a Claude Code agent's chat as it starts too.
	CreditAITool bool `json:"creditAITool"`
	// BuiltWithBadge says whether agents end their pull requests'
	// descriptions with a "Built with AgentBox" line and badge. Off until
	// turned on.
	BuiltWithBadge bool `json:"builtWithBadge"`
	// BuiltWithBadgeOffer is true while the app should offer BuiltWithBadge:
	// an agent's pull request has been merged, and the user hasn't answered
	// yet. It is made once.
	BuiltWithBadgeOffer bool `json:"builtWithBadgeOffer"`
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
	// AgentGPU says whether every agent gets the GPU of AgentBox's VM, when
	// VMGPU finds one: added to the agents you have, and to each new one, the
	// moment it changes. Off unless it was turned on.
	AgentGPU bool `json:"agentGPU"`
	// BlockFullSuite says whether a Claude Code agent's whole test suite
	// (`go test ./...`, a bare `npm test`) is refused before it runs, with
	// the tests of what it changed to run instead, unless its project allows
	// them (Project.FullTestSuite). Experimental, off unless turned on.
	BlockFullSuite bool `json:"blockFullSuite"`
	// VMGPU is the GPU AgentBox's VM has to give agents.
	VMGPU VMGPU `json:"vmGPU"`
	// ErrorReports says whether the app sends a report of each uncaught error
	// by itself (ReportKindError). On unless the user turned it off.
	ErrorReports bool `json:"errorReports"`
	// PRWatch says whether the daemon watches every agent's open pull request
	// until it's merged or closed, and tells the agent when it conflicts with
	// its base, its checks fail or a reviewer asks for changes. On unless it
	// was turned off; a project can override it (Project.PRWatch).
	PRWatch bool `json:"prWatch"`
	// AgentFreeze is the experimental memory governor: under memory
	// pressure whole agents' machines are frozen, newest first, never the
	// oldest working one, and thaw oldest first; a new machine starts once
	// the VM really has the memory to boot it. Off, only heavy commands are
	// held, as ever.
	AgentFreeze bool `json:"agentFreeze"`
	// MediaRetention is how long a removed agent's media is kept before the
	// daemon purges it: one of the MediaRetention values.
	MediaRetention string `json:"mediaRetention"`
	// Language is the language the desktop app speaks, a BCP 47 tag like
	// "en-US" or "pt-BR": DefaultLanguage until somebody picks another. The
	// daemon only keeps it; which languages exist is the app's to say, so a
	// new one is a catalog there and nothing here. The CLI, the brief and the
	// chats stay in English whatever it is.
	Language string `json:"language"`
	// Timezone is the IANA timezone the desktop app last ran in, which
	// scheduled tasks are read in when they name none: the daemon runs in a
	// VM whose clock is in UTC. Empty until an app has said.
	Timezone string `json:"timezone"`
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
	// RemoveStopped and RemoveStoppedDays are "remove stopped agents'
	// machines": while on, the daemon removes the machine and worktree of an
	// agent stopped for RemoveStoppedDays when nothing would be lost
	// (StoppedMachines says which). The agent stays as a past agent with its
	// branch, pull request and chat. Off unless it was turned on.
	RemoveStopped     bool `json:"removeStopped"`
	RemoveStoppedDays int  `json:"removeStoppedDays"`
	// DockerPruneOnStop says whether stopping an agent — by hand, retired,
	// idle or every agent at once — first frees its Docker space: its build
	// cache and every image no container uses, never its volumes. On unless
	// it was turned off.
	DockerPruneOnStop bool `json:"dockerPruneOnStop"`
	// IdleTimeSeconds is how long an agent may go idle before AutoStopIdle
	// stops it; DefaultIdleTimeSeconds when nobody chose.
	IdleTimeSeconds int `json:"idleTimeSeconds"`
	// IdleReclaim is "reclaim idle agents' memory", an experiment: while on,
	// an agent whose chat has been idle IdleFreezeSeconds has what it left
	// running (dev servers, the desktop's browser, watchers, Docker) frozen
	// and its memory pushed out to swap, woken as soon as something touches
	// it, and one that finished and stayed idle IdleStopSeconds has its
	// machine stopped, started again by the next message. Off unless it was
	// turned on.
	IdleReclaim       bool `json:"idleReclaim"`
	IdleFreezeSeconds int  `json:"idleFreezeSeconds"`
	IdleStopSeconds   int  `json:"idleStopSeconds"`
	// LeadRecheck is "lead rechecks agents": while on, every
	// LeadRecheckMinutes the daemon wakes each project's chat with a short
	// status of its running agents — only when an agent has been idle a
	// while, and not twice for the same state — so it can retire finished
	// agents.
	LeadRecheck        bool `json:"leadRecheck"`
	LeadRecheckMinutes int  `json:"leadRecheckMinutes"`
	// LeadSleep is "stop idle chats": while on, a project's chat that sat
	// idle LeadSleepMinutes has its AI tool stopped to give its memory back,
	// and its next message, notice or schedule resumes the same session.
	// Claude Code only. On unless it was turned off.
	LeadSleep        bool `json:"leadSleep"`
	LeadSleepMinutes int  `json:"leadSleepMinutes"`
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
	// LocalNodeModules is "Keep agents' node_modules in the VM": while on, a
	// new agent's node_modules are on a disk of AgentBox's VM rather than in
	// its worktree, where the worktree is in a folder the VM shares, and
	// start filled when its lockfile was installed by an agent before. The
	// computer AgentBox runs on sees them empty. On unless it was turned off.
	LocalNodeModules bool `json:"localNodeModules"`

	// ClaudeCodeUpdate is "Keep Claude Code up to date": the daemon moving
	// Claude Code and its ACP adapter past the versions this AgentBox pins,
	// to the newest on npm, without an AgentBox release.
	ClaudeCodeUpdate ToolAutoUpdate `json:"claudeCodeUpdate"`
}

// ToolAutoUpdate is where keeping one AI tool up to date has got to. The
// versions are the tool's own (Claude Code's, not its adapter's).
type ToolAutoUpdate struct {
	// On is the setting. Off, the tool runs the version AgentBox pins.
	On bool `json:"on"`
	// Installed is the version the agents' base image has, "" before there
	// is one; Pinned the one this AgentBox pins, which it never goes below.
	Installed string `json:"installed"`
	Pinned    string `json:"pinned"`
	// Latest is the newest release the last check found, "" before one, and
	// CheckedAt when it ran.
	Latest    string     `json:"latest"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	// Error is why the last check, or the update it started, failed: agents
	// keep Installed.
	Error string `json:"error,omitempty"`
	// Checking is set while a check or its update runs, Job being the
	// update's job once it has one.
	Checking bool   `json:"checking"`
	Job      string `json:"job,omitempty"`
}

// UpdateSettingsRequest changes what's set; a nil field stays as it is.
type UpdateSettingsRequest struct {
	// DefaultClaudeModel is "" to go back to AgentBox's own default.
	DefaultClaudeModel *string `json:"defaultClaudeModel,omitempty"`
	// DefaultAgentContextWindow and DefaultLeadContextWindow are "" for the
	// installation's compact window, or "1m" for the model's whole window,
	// which is refused for a default model without one, like Haiku. A request
	// that moves a role's model to one without a 1M window has to bring its
	// window back to the compact one in the same request.
	DefaultAgentContextWindow *string `json:"defaultAgentContextWindow,omitempty"`
	// EnforceAgentDefaults turns Settings.EnforceAgentDefaults on or off.
	EnforceAgentDefaults *bool `json:"enforceAgentDefaults,omitempty"`
	// DefaultLeadModel is "" to go back to Claude Code's own default.
	DefaultLeadModel         *string `json:"defaultLeadModel,omitempty"`
	DefaultLeadContextWindow *string `json:"defaultLeadContextWindow,omitempty"`
	// DefaultClaudeEffort is "" to go back to AgentBox's own default.
	DefaultClaudeEffort *string `json:"defaultClaudeEffort,omitempty"`
	// DefaultCursorModel and DefaultCursorEffort are what new Cursor agents
	// start on, in Cursor's own names; "" goes back to Cursor's Auto and the
	// model's own default level.
	DefaultCursorModel  *string `json:"defaultCursorModel,omitempty"`
	DefaultCursorEffort *string `json:"defaultCursorEffort,omitempty"`
	// ResumeAfterLimit turns the automatic resume after a Claude usage limit
	// on or off, for every agent.
	ResumeAfterLimit *bool `json:"resumeAfterLimit,omitempty"`
	// ContinueAfterRestart turns "Continue agents after restarts" on or off.
	ContinueAfterRestart *bool `json:"continueAfterRestart,omitempty"`
	// CreditAITool turns "Credit the AI tool in commits and pull requests"
	// on or off.
	CreditAITool *bool `json:"creditAITool,omitempty"`
	// BuiltWithBadge turns the "Built with AgentBox" footer on agents' pull
	// requests on or off. Either way it counts as answering the offer.
	BuiltWithBadge *bool `json:"builtWithBadge,omitempty"`
	// DismissBuiltWithBadgeOffer closes the offer without turning the
	// footer on.
	DismissBuiltWithBadgeOffer *bool `json:"dismissBuiltWithBadgeOffer,omitempty"`
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
	// AgentGPU turns "GPU for agents" on or off.
	AgentGPU *bool `json:"agentGPU,omitempty"`
	// BlockFullSuite turns refusing agents' whole test suites on or off.
	BlockFullSuite *bool `json:"blockFullSuite,omitempty"`
	// ErrorReports turns automatic error reports on or off.
	ErrorReports *bool `json:"errorReports,omitempty"`
	// PRWatch turns the pull request watch on or off.
	PRWatch *bool `json:"prWatch,omitempty"`
	// AgentFreeze turns the experimental memory governor on or off.
	AgentFreeze *bool `json:"agentFreeze,omitempty"`
	// MediaRetention is one of the MediaRetention values.
	MediaRetention *string `json:"mediaRetention,omitempty"`
	// Language sets Settings.Language: a language tag, "" going back to
	// DefaultLanguage.
	Language *string `json:"language,omitempty"`
	// Timezone sets Settings.Timezone: an IANA name, which the app sends as
	// it starts.
	Timezone *string `json:"timezone,omitempty"`
	// AutoStopIdle turns "auto-stop idle agents" on or off.
	AutoStopIdle *bool `json:"autoStopIdle,omitempty"`
	// RemoveStopped turns "remove stopped agents' machines" on or off, and
	// RemoveStoppedDays is after how many days, from 1 to 365.
	RemoveStopped     *bool `json:"removeStopped,omitempty"`
	RemoveStoppedDays *int  `json:"removeStoppedDays,omitempty"`
	// DockerPruneOnStop turns freeing an agent's Docker space on stop on or
	// off.
	DockerPruneOnStop *bool `json:"dockerPruneOnStop,omitempty"`
	// IdleTimeSeconds is how long AutoStopIdle waits before stopping an idle
	// agent, at least 60.
	IdleTimeSeconds *int `json:"idleTimeSeconds,omitempty"`
	// IdleReclaim turns "reclaim idle agents' memory" on or off;
	// IdleFreezeSeconds and IdleStopSeconds set its waits, from 60 seconds
	// to a day.
	IdleReclaim       *bool `json:"idleReclaim,omitempty"`
	IdleFreezeSeconds *int  `json:"idleFreezeSeconds,omitempty"`
	IdleStopSeconds   *int  `json:"idleStopSeconds,omitempty"`
	// LeadRecheck turns "lead rechecks agents" on or off, and
	// LeadRecheckMinutes is how often, from 5 to 1440.
	LeadRecheck        *bool `json:"leadRecheck,omitempty"`
	LeadRecheckMinutes *int  `json:"leadRecheckMinutes,omitempty"`
	// LeadSleep turns "stop idle chats" on or off, and LeadSleepMinutes is
	// after how long, from 5 to 1440.
	LeadSleep        *bool `json:"leadSleep,omitempty"`
	LeadSleepMinutes *int  `json:"leadSleepMinutes,omitempty"`
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
	// LocalNodeModules turns "Keep agents' node_modules in the VM" on or off,
	// for agents made from now on.
	LocalNodeModules *bool `json:"localNodeModules,omitempty"`
	// ClaudeCodeAutoUpdate turns "Keep Claude Code up to date" on, which
	// checks at once, or off, which puts agents back on the pinned versions.
	ClaudeCodeAutoUpdate *bool `json:"claudeCodeAutoUpdate,omitempty"`
	// CheckClaudeCode checks for a newer Claude Code now, while it's on.
	CheckClaudeCode bool `json:"checkClaudeCode,omitempty"`
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
	// CodexAccount, OpenCodeAccounts (provider → account) and CursorAccount
	// are the accounts whose login it was given for its tool, when it runs
	// that tool.
	CodexAccount     string            `json:"codexAccount,omitempty"`
	OpenCodeAccounts map[string]string `json:"openCodeAccounts,omitempty"`
	CursorAccount    string            `json:"cursorAccount,omitempty"`
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
	// Outcome is how the agent's last turn left it, as the daemon judged it
	// when the turn ended (AgentOutcome*): it says what an agent whose chat
	// is ready is doing — asking, done, stopped on an error, idle. Empty
	// while nothing has been judged, as for an agent that hasn't had a turn.
	// A running chat is working whatever it says.
	Outcome string `json:"outcome,omitempty"`
	// OutcomeNote is what to show with Outcome: the question it asked, the
	// error it stopped on, why it isn't resuming.
	OutcomeNote string `json:"outcomeNote,omitempty"`
	// OutcomeUntil is when an agent with outcome "limited" resumes by itself.
	// Absent means AgentBox won't (the setting is off, or it has waited
	// enough times): "Resume now" is the way on.
	OutcomeUntil *time.Time `json:"outcomeUntil,omitempty"`
	// ChangesRequested is what the user asked it to change from the review
	// (RequestChangesRequest), while it works on that: set from the request
	// until its chat's turn ends.
	ChangesRequested string `json:"changesRequested,omitempty"`
	State            string `json:"state"` // running, stopped, paused, initializing, incomplete or missing; queued only for one an earlier release left queued, as the daemon starts it
	// WaitingForImage is set while an initializing agent waits for the base
	// image, which is being downloaded or built (ImageStatus says how far):
	// its worktree and branch are there, and its machine is made as soon as
	// the image is ready.
	WaitingForImage bool `json:"waitingForImage,omitempty"`
	// Memory is what the daemon holds of the agent's heavy commands for
	// memory: paused or waiting to start (package pressure). Absent when it
	// holds nothing.
	Memory *MemoryHold `json:"memory,omitempty"`
	// Reclaimed says idle reclaim has its memory (Settings.IdleReclaim):
	// "frozen" when what it left running is frozen and pushed out to swap,
	// "stopped" when its machine was stopped after it finished. Absent
	// otherwise.
	Reclaimed string    `json:"reclaimed,omitempty"`
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"createdAt"`
}

// ArchivedAgent is a past agent: one whose machine was removed, by the user or
// the lead's retire_agent, kept with its chat until the user deletes it.
type ArchivedAgent struct {
	Ref     string `json:"ref"`
	Project string `json:"project"`
	Name    string `json:"name"`
	Title   string `json:"title"`
	// Task is its first message, as it was sent.
	Task       string    `json:"task"`
	AI         string    `json:"ai"`
	Interface  string    `json:"interface"`
	Branch     string    `json:"branch"`
	BaseRef    string    `json:"baseRef"`
	BaseCommit string    `json:"baseCommit"`
	CreatedAt  time.Time `json:"createdAt"`
	ArchivedAt time.Time `json:"archivedAt"`
	// PR is its pull request as last read: when it was archived, and since
	// for one still open. Absent when none was found.
	PR *PullRequest `json:"pr,omitempty"`
	// Tokens and CostUSD are what its chat used, from the token ledger.
	Tokens  int64   `json:"tokens"`
	CostUSD float64 `json:"costUsd"`
}

// The pull request states GET /v1/archived-agents filters on (its pr
// parameter): PRStateNone is an agent with none.
const (
	PRStateOpen   = "open"
	PRStateMerged = "merged"
	PRStateClosed = "closed"
	PRStateNone   = "none"
)

// DeleteArchivedRequest deletes past agents in bulk, by ref (project/name).
type DeleteArchivedRequest struct {
	Refs []string `json:"refs"`
}

// DeleteArchivedResult is what a bulk delete did: Deleted by ref, and Failed
// with why for any it couldn't.
type DeleteArchivedResult struct {
	Deleted []string          `json:"deleted"`
	Failed  map[string]string `json:"failed,omitempty"`
}

// ContinueArchivedRequest starts a new agent from a past one's branch, given a
// short summary of its chat and then Message, when there is one.
type ContinueArchivedRequest struct {
	Message string `json:"message,omitempty"`
	// AI is the new agent's tool; empty is the past agent's.
	AI string `json:"ai,omitempty"`
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
	Task string `json:"task,omitempty"`
	// Issue, when set, is the number of one of the project's GitHub issues
	// the agent is made for: its task is the issue (title, body and comments)
	// followed by Task, when there is one, its pull request is to say
	// "Closes #N", and the issue shows the agent in the Issues view. Title
	// and Branch are made from the issue when left empty.
	Issue   int    `json:"issue,omitempty"`
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
	// StartAt, when set, is the commit or ref the agent's branch starts at
	// instead of From's tip; From stays the branch it is based on.
	StartAt string `json:"startAt,omitempty"`
	// ClaudeAccount overrides the project's Claude Code account for this agent.
	ClaudeAccount string `json:"claudeAccount,omitempty"`
	// CodexAccount, OpenCodeAccounts (provider → account) and CursorAccount
	// override the project's for an agent on that tool.
	CodexAccount     string            `json:"codexAccount,omitempty"`
	OpenCodeAccounts map[string]string `json:"openCodeAccounts,omitempty"`
	CursorAccount    string            `json:"cursorAccount,omitempty"`
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
	// Queue and Size are accepted for the callers that still send them,
	// and do nothing: every agent starts at once, and the VM's memory
	// pressure, not a size, decides when its heavy commands run.
	Queue *bool  `json:"queue,omitempty"`
	Size  string `json:"size,omitempty"`
	// Connectors limits which of the project's connectors the agent is
	// given, by name; absent gives it every one, and an empty list none. An
	// agent's own connectors, added to it later, aren't limited by it.
	Connectors *[]string `json:"connectors,omitempty"`
	// ToolNotice is the daemon's own, never sent: why an agent that named no
	// AI tool runs Claude Code rather than its lead's (Job.Notice).
	ToolNotice string `json:"-"`
}

// MemoryHold is what the daemon holds of an agent's heavy commands (test
// runs, builds) for memory.
type MemoryHold struct {
	// Paused is how many of them are paused (frozen) while the VM's memory
	// is under pressure, and Waiting how many wait to start until it eases.
	Paused  int `json:"paused,omitempty"`
	Waiting int `json:"waiting,omitempty"`
	// Machine is what Settings.AgentFreeze holds of the agent's whole
	// machine: "paused" (frozen for memory, it thaws by itself) or "waiting"
	// (to start until the VM has the memory to boot it); "" for neither.
	// Ahead is how many machines thaw or start before it.
	Machine string `json:"machine,omitempty"`
	Ahead   int    `json:"ahead,omitempty"`
	// Since is when the oldest of them was paused or began to wait.
	Since time.Time `json:"since"`
	// Why is what holds them now (pressure.Reason): "stalling" (tasks stall
	// on memory, Stalled percent of the last 10 seconds), "falling" (what is
	// available falls Rate bytes a second toward running out), "churning"
	// (memory pushed out to swap comes back Rate bytes a second), "low"
	// (little available with swap full), "easing" (it eased: paused
	// commands resume first, one at a time), or "" (their turn comes next).
	Why     string  `json:"why,omitempty"`
	Stalled float64 `json:"stalled,omitempty"`
	Rate    int64   `json:"rate,omitempty"`
	// Available and Total are the VM's memory, MemAvailable and MemTotal.
	Available int64 `json:"available,omitempty"`
	Total     int64 `json:"total,omitempty"`
	// SwapFull says the VM's swap is full, with pausing whole agents on:
	// what a paused machine or command holds stays in memory, so pausing it
	// stops it growing and frees nothing.
	SwapFull bool `json:"swapFull,omitempty"`
}

// PressureStatus is the VM's memory pressure and the heavy commands the
// daemon decides about from it: GET /v1/pressure.
type PressureStatus struct {
	// Some and Full are the VM's memory PSI, avg10: the share of the last 10
	// seconds, in percent, some task (Some) or every task (Full) stalled
	// waiting for memory.
	Some float64 `json:"some"`
	Full float64 `json:"full"`
	// Why is what holds runs back now, as in MemoryHold.Why.
	Why string `json:"why,omitempty"`
	// Runs are the heavy commands running, oldest first, then those
	// waiting, in the order they start.
	Runs []HeavyRun `json:"runs"`
}

// HeavyRun is one heavy command of an agent's.
type HeavyRun struct {
	Agent   string `json:"agent"` // its ref
	Key     string `json:"key"`
	Command string `json:"command,omitempty"`
	// State is "running", "paused" or "waiting".
	State string    `json:"state"`
	Since time.Time `json:"since"`
	// Placed says its processes are in a cgroup of their own, which is what
	// pausing it freezes; one that isn't is never paused.
	Placed bool `json:"placed,omitempty"`
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

// AgentAccountRequest moves a running agent to another account of its AI
// tool (the lead's switch_agent_account): Account for Claude Code, Codex or
// Cursor, "" for the project's; OpenCodeAccounts, provider → account, laid
// over the ones an OpenCode agent has.
type AgentAccountRequest struct {
	Account          string            `json:"account,omitempty"`
	OpenCodeAccounts map[string]string `json:"openCodeAccounts,omitempty"`
}

// AgentAccountSwitch is what moving an agent to another account did.
type AgentAccountSwitch struct {
	Agent string `json:"agent"`
	Tool  string `json:"tool"` // how to say it: "Claude Code"
	// Account is the one it runs on now, OpenCode's as "provider: account, …".
	Account string `json:"account"`
	// Note is what its chat said about the move, "" when no chat was running
	// (the next one starts on the new account).
	Note string `json:"note,omitempty"`
	// Resumes says its chat's session carries on on the new account; false
	// when the tool's adapter can't resume a session.
	Resumes bool `json:"resumes"`
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
	// AI and Notice are on the reply that starts a create job only: the AI
	// tool the agent runs, which a request naming none leaves to the
	// project's lead, and why it runs Claude Code instead when the lead's
	// tool can't run agents here.
	AI     string `json:"ai,omitempty"`
	Notice string `json:"notice,omitempty"`
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

// HostMemory is GET /v1/usage/host: the daemon's machine as its kernel sees
// it, /proc/meminfo's MemTotal and MemAvailable in bytes and its CPUs. It asks
// Incus nothing, so it answers at once however busy the machine is: the app's
// top bar reads it on Windows rather than have wsl.exe run a command in the
// distro, which can take WSL 30 seconds and fail under load.
type HostMemory struct {
	Total     int64 `json:"total"`
	Available int64 `json:"available"`
	CPUs      int   `json:"cpus"`
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

// StoppedMachine is an agent whose machine is stopped but still on disk,
// and what "remove stopped agents' machines" would do with it now.
type StoppedMachine struct {
	Ref     string `json:"ref"`
	Project string `json:"project"`
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Branch  string `json:"branch"`
	// StoppedSince is when its machine stopped, or, when AgentBox didn't
	// see that happen, when it was last seen doing anything.
	StoppedSince time.Time `json:"stoppedSince"`
	// Machine is the bytes its machine holds on disk; left out when Incus
	// can't say.
	Machine *int64 `json:"machine,omitempty"`
	// Remove says why it would be removed now; Keep, when it wouldn't, why
	// not. Exactly one is set, unless the safety checks weren't asked for.
	Remove string `json:"remove,omitempty"`
	Keep   string `json:"keep,omitempty"`
}

// StoppedMachines is every agent holding a stopped machine, for one project
// or all of them, with the setting's days the verdicts were made against.
type StoppedMachines struct {
	Days     int              `json:"days"`
	Machines []StoppedMachine `json:"machines"`
}

// RemoveStoppedRequest removes the machines of the agents listed, by ref,
// each only if it is still safe to on a fresh look.
type RemoveStoppedRequest struct {
	Agents []string `json:"agents"`
}

// RemoveStoppedResult is what RemoveStoppedRequest did: the agents removed,
// and the rest kept, each with why.
type RemoveStoppedResult struct {
	Removed []string         `json:"removed"`
	Kept    []StoppedMachine `json:"kept"`
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
	HostTotal  int64 `json:"hostTotal"`
	AgentsUsed int64 `json:"agentsUsed"`
	OtherUsed  int64 `json:"otherUsed"`
	SwapTotal  int64 `json:"swapTotal"`
	SwapUsed   int64 `json:"swapUsed"`
	AgentsSwap int64 `json:"agentsSwap"`
	// Used is the VM's used memory, which AgentsUsed, Leads, AgentBox,
	// Zram.RealBytes and Kernel add up to. FileCache is on top of it: the
	// agents' file cache, given back when memory runs short.
	Used      int64              `json:"used"`
	FileCache int64              `json:"fileCache"`
	Leads     int64              `json:"leads"`
	AgentBox  int64              `json:"agentbox"`
	Kernel    int64              `json:"kernel"`
	Zram      *ZramUsage         `json:"zram,omitempty"`
	Agents    []MemoryUsageAgent `json:"agents"`
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
	Claude   bool `json:"claude"` // at least one Claude Code account is stored
	Codex    bool `json:"codex"`
	OpenCode bool `json:"opencode"` // agentbox auth opencode stored a provider login
	// Cursor is true when agents are signed in to Cursor, with an API key or
	// Cursor's browser sign-in; CursorEmail is whose account it is, when
	// Cursor said.
	Cursor         bool            `json:"cursor"`
	CursorEmail    string          `json:"cursorEmail,omitempty"`
	ClaudeAccounts []ClaudeAccount `json:"claudeAccounts"`
	// CodexAccounts, OpenCodeAccounts and CursorAccounts are the stored logins
	// for the other tools; Codex, OpenCode and Cursor say there is a default
	// one agents can use.
	CodexAccounts    []ToolAccount `json:"codexAccounts"`
	OpenCodeAccounts []ToolAccount `json:"openCodeAccounts"`
	CursorAccounts   []ToolAccount `json:"cursorAccounts"`
	// GitHub is true when at least one GitHub account is stored.
	GitHub         bool            `json:"github"`
	GitHubAccounts []GitHubAccount `json:"githubAccounts"`
	// GitHubUser and GitHubError describe the default account: who its token
	// belongs to, or why it couldn't be checked.
	GitHubUser  string `json:"githubUser,omitempty"`
	GitHubError string `json:"githubError,omitempty"`
}

// ToolCredential is what an agent's AI tool really runs on, read by the daemon
// from the environment of the tool's running process, not from settings: a
// project secret such as ANTHROPIC_API_KEY that reached it would show here as
// the variable it runs on (internal/toolcreds). Values never leave the agent.
type ToolCredential struct {
	Agent string `json:"agent"` // project/agent
	Tool  string `json:"tool"`
	// Running is false when the tool's process isn't running: the rest is then empty.
	Running bool `json:"running"`
	// Variable is the environment variable it authenticates with
	// (CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_API_KEY, OPENAI_API_KEY…), or empty
	// when it has none and uses its own stored sign-in.
	Variable string `json:"variable,omitempty"`
	// Account is the Claude account whose token is in its environment.
	Account string `json:"account,omitempty"`
	// Others are the other credential, provider and endpoint variables it has.
	Others []string `json:"others,omitempty"`
	// Error is why it couldn't be read.
	Error string `json:"error,omitempty"`
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

// ToolAccount is one stored Codex, OpenCode or Cursor login agents can be
// given.
type ToolAccount struct {
	Tool string `json:"tool"` // codex, opencode or cursor
	// Provider is the OpenCode provider it signs in to ("anthropic"); empty
	// for Codex and Cursor.
	Provider string `json:"provider,omitempty"`
	Name     string `json:"name"`
	// Default is the account of its tool (and provider) agents get when
	// neither the agent nor its project names one.
	Default bool      `json:"default"`
	SavedAt time.Time `json:"savedAt"`
	// Kind is how it signs in: apikey, chatgpt (Codex), oauth or wellknown
	// (OpenCode's), empty when its file doesn't say.
	Kind string `json:"kind,omitempty"`
	// Email is whose account it is, when its file says.
	Email string `json:"email,omitempty"`
	// Expired is true for a Cursor key that has stopped working, which is no
	// sign-in agents can use until it is signed in again.
	Expired bool       `json:"expired,omitempty"`
	Expiry  *time.Time `json:"expiry,omitempty"`
}

// ToolAccountRequest stores a Codex or OpenCode login under an account, from
// an API key: Codex's from OpenAI, OpenCode's from Provider. A ChatGPT or
// OAuth sign-in is `agentbox auth codex|opencode --account <name>`.
type ToolAccountRequest struct {
	// Account names the login; empty means the account called "default".
	Account  string `json:"account,omitempty"`
	Provider string `json:"provider,omitempty"` // OpenCode's only
	APIKey   string `json:"apiKey"`
}

// RenameToolAccountRequest gives a stored Codex, OpenCode or Cursor account
// another name.
type RenameToolAccountRequest struct {
	Name string `json:"name"`
}

// RenamedToolAccount is what a rename carried over to the new name. The
// login is the same, so the agents holding it keep running.
type RenamedToolAccount struct {
	Tool     string   `json:"tool"`
	Provider string   `json:"provider,omitempty"`
	Old      string   `json:"old"`
	Name     string   `json:"name"`
	Projects []string `json:"projects"`
	Agents   []string `json:"agents"`
}

// CursorKeyRequest signs agents in to Cursor with an API key from Cursor's
// dashboard. The daemon asks Cursor whose it is before keeping it.
type CursorKeyRequest struct {
	APIKey string `json:"apiKey"`
	// Account names the login; empty means the account called "default".
	Account string `json:"account,omitempty"`
}

// CursorLoginRequest starts Cursor's browser sign-in for an account; empty
// means the account called "default".
type CursorLoginRequest struct {
	Account string `json:"account,omitempty"`
}

// CursorKeyResponse says whose Cursor account the key belongs to, when
// Cursor said.
type CursorKeyResponse struct {
	Email string `json:"email,omitempty"`
}

// The states of a Cursor browser sign-in (CursorLogin.State).
const (
	CursorLoginIdle     = "idle"     // none was started since the daemon started
	CursorLoginStarting = "starting" // installing what it needs, or waiting for Cursor's page
	CursorLoginWaiting  = "waiting"  // URL is the page to open and finish it in
	CursorLoginDone     = "done"
	CursorLoginFailed   = "failed"
)

// CursorLogin is the progress of Cursor's browser sign-in, which mints an
// API key named "AgentBox" on the account (POST /v1/auth/cursor/login, then
// GET the same path until it is done or failed).
type CursorLogin struct {
	State string `json:"state"`
	// Account is the account the sign-in is for.
	Account string `json:"account,omitempty"`
	URL     string `json:"url,omitempty"`
	Email   string `json:"email,omitempty"`
	Error   string `json:"error,omitempty"`
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
	// Fork is set when its head branch is in another repository, which no
	// other pull request can be stacked on.
	Fork bool `json:"fork,omitempty"`
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
	// Labels are the labels on it, with GitHub's colours.
	Labels []Label `json:"labels,omitempty"`
}

// Label is one of a repository's GitHub labels. Color is six hex digits, with
// no #, as GitHub gives it.
type Label struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description,omitempty"`
}

// ProjectLabels is every label of a project's repository, for the Pull
// requests tab's label picker.
type ProjectLabels struct {
	Labels []Label `json:"labels"`
}

// EditLabelsRequest puts labels on a pull request and takes others off, by
// name. A label it already has, or one it hasn't, is no error: two edits that
// cross each other both land.
type EditLabelsRequest struct {
	Add    []string `json:"add,omitempty"`
	Remove []string `json:"remove,omitempty"`
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
	GitHubAccount string `json:"githubAccount,omitempty"`
	// GitHubLogin is who that account is on GitHub, which is what the tab's
	// Mine filter matches authors against. Empty for an account stored before
	// AgentBox remembered logins.
	GitHubLogin  string        `json:"githubLogin,omitempty"`
	GitHubError  *GitHubError  `json:"githubError,omitempty"` // why they couldn't be read
	PullRequests []PullRequest `json:"pullRequests"`
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

// IssueLabel is the label that has a project picking an issue up by itself,
// when the project's PickUpIssues is on.
const IssueLabel = "agentbox"

// Issue is one of a project repository's open GitHub issues, with the agent
// it was handed to, if any.
type Issue struct {
	Number       int        `json:"number"`
	Title        string     `json:"title"`
	URL          string     `json:"url"`
	Author       string     `json:"author,omitempty"`
	AuthorAvatar string     `json:"authorAvatar,omitempty"`
	Labels       []Label    `json:"labels,omitempty"`
	Comments     int        `json:"comments,omitempty"`
	CreatedAt    *time.Time `json:"createdAt,omitempty"`
	UpdatedAt    *time.Time `json:"updatedAt,omitempty"`
	// Handed is set once the issue was handed to an agent.
	Handed *IssueHandoff `json:"handed,omitempty"`
}

// IssueHandoff is the agent an issue was handed to and what came of it.
type IssueHandoff struct {
	// Agent is the agent's name; empty while Job is still making it.
	Agent string `json:"agent,omitempty"`
	Job   string `json:"job,omitempty"`
	// Title is the agent's title, and Status its status, while it is there;
	// Retired says it has since been retired.
	AgentTitle string `json:"agentTitle,omitempty"`
	Status     string `json:"status,omitempty"`
	Retired    bool   `json:"retired,omitempty"`
	// Auto says the project picked the issue up by itself (PickUpIssues).
	Auto bool      `json:"auto,omitempty"`
	At   time.Time `json:"at"`
	// PullRequest is the pull request the agent opened, once one is seen.
	PullRequest *IssuePullRequest `json:"pullRequest,omitempty"`
}

// IssuePullRequest is the pull request an issue's agent opened.
type IssuePullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"` // open, closed or merged
}

// ProjectIssues is a project repository's open issues, as the Issues view and
// the lead's list_issues read them. The repository fields say the same as
// ProjectPullRequests' do.
type ProjectIssues struct {
	Project         string       `json:"project"`
	GitHub          string       `json:"github,omitempty"`
	GitHubAccount   string       `json:"githubAccount,omitempty"`
	GitHubError     *GitHubError `json:"githubError,omitempty"`
	NoOrigin        bool         `json:"noOrigin,omitempty"`
	NonGitHubRemote string       `json:"nonGitHubRemote,omitempty"`
	Issues          []Issue      `json:"issues"`
	// PickUpIssues is the project's setting, so the view can say whether
	// issues labelled Label are picked up by themselves.
	PickUpIssues bool       `json:"pickUpIssues"`
	Label        string     `json:"label"`
	FetchedAt    *time.Time `json:"fetchedAt,omitempty"`
}

// MergePullRequestRequest merges a pull request with the given method:
// "merge", "squash" or "rebase".
type MergePullRequestRequest struct {
	Method string `json:"method"`
	// Confirmed says the user confirmed this merge on the phone it came
	// from: a phone's merge without it is refused. The app needn't set it.
	Confirmed bool `json:"confirmed,omitempty"`
}

// PullRequestDetail is one pull request as the app's pull request view shows
// it: the list's fields, read fresh, and its description and check
// runs. Body is GitHub markdown; its pictures are read through the
// daemon's /pulls/image, since a private repository's need the token.
type PullRequestDetail struct {
	PullRequest
	Body      string      `json:"body"`
	CheckRuns []PullCheck `json:"checkRuns"`
	// ChangedFiles is how many files it changes, before they are listed.
	ChangedFiles int `json:"changedFiles"`
}

// PullCheck is one check run on a pull request's head. Status is queued,
// in_progress or completed; Conclusion, once completed, is GitHub's: success,
// failure, neutral, cancelled, skipped, timed_out or action_required.
type PullCheck struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
	URL        string `json:"url,omitempty"`
}

// PullRequestFiles is the files a pull request changes, without their diffs:
// each is read on its own, from PullRequestFileDiff. Truncated is set when
// GitHub had more than it lists.
type PullRequestFiles struct {
	Files     []PullFile `json:"files"`
	Truncated bool       `json:"truncated,omitempty"`
}

// PullFile is one file a pull request changes. Status is GitHub's: added,
// removed, modified, renamed, copied, changed or unchanged. HasDiff is false
// for a binary file and for one whose diff GitHub found too big to send.
type PullFile struct {
	Path         string `json:"path"`
	PreviousPath string `json:"previousPath,omitempty"`
	Status       string `json:"status"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	HasDiff      bool   `json:"hasDiff"`
}

// PullFileDiff is one file's unified diff, hunks only, without the file
// header.
type PullFileDiff struct {
	Path  string `json:"path"`
	Patch string `json:"patch"`
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
	// Outcome is how the turn left the agent, on finished (done or idle),
	// asking and failed: one of AgentOutcome*.
	Outcome string `json:"outcome,omitempty"`
	// ResumeAt is when the agent resumes by itself, on limited.
	ResumeAt *time.Time `json:"resumeAt,omitempty"`
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
	// AgentAsking is an agent that ended its turn on a question in its own
	// words, without `agentbox ask`: it waits for an answer, and isn't
	// finished. Summary is the question.
	AgentAsking = "asking"
	// AgentFailed is an agent whose turn stopped on an error, or was cut
	// short by a limit, in the middle of its work. Summary says what.
	AgentFailed = "failed"
	// AgentLimited is an agent whose turn the AI tool's usage limit cut short:
	// it is paused, not finished, and AgentBox carries it on when the limit
	// resets (ResumeAt). Summary says what the tool said.
	AgentLimited = "limited"
)

// How an agent's last turn left it (Agent.Outcome, AgentEvent.Outcome). A
// running turn is working, whatever the last one said; an agent waiting on
// background work it left is Agent.Background.
const (
	// AgentOutcomeDone finished with something to show: a pull request, or
	// changes on its branch.
	AgentOutcomeDone = "done"
	// AgentOutcomeIdle ended its turn with nothing to show and nothing
	// asked: a question answered, a check made, or work that didn't happen.
	AgentOutcomeIdle = "idle"
	// AgentOutcomeAsking ended its turn on a question for whoever drives it.
	AgentOutcomeAsking = "asking"
	// AgentOutcomeFailed stopped mid-work: an error, its AI tool exiting, a
	// limit or a refusal.
	AgentOutcomeFailed = "failed"
	// AgentOutcomeLimited is paused on the AI tool's usage limit, carried on
	// by AgentBox when it resets (Agent.OutcomeUntil). It isn't finished or
	// failed.
	AgentOutcomeLimited = "limited"
	// AgentOutcomeResuming had a turn AgentBox's restart cut short, which is
	// being carried on.
	AgentOutcomeResuming = "resuming"
)

// ReviewItem is an agent in "Ready to review": it finished its work, and the
// user hasn't looked at it since — opening it in the review marks it reviewed
// — or did, and it changed after.
// Newest first, by FinishedAt.
type ReviewItem struct {
	Project string `json:"project"`
	Agent   string `json:"agent"`
	Ref     string `json:"ref"`
	Title   string `json:"title,omitempty"`
	Branch  string `json:"branch,omitempty"`
	// BaseRef is the branch it stands on: another agent's, when it is
	// stacked on that one's work.
	BaseRef string `json:"baseRef,omitempty"`
	AI      string `json:"ai,omitempty"`
	// Summary is what the agent said when it finished, cut down the way its
	// finish event cuts it.
	Summary string `json:"summary,omitempty"`
	// Changes is what its branch changed when it finished.
	Changes *AgentChanges `json:"changes,omitempty"`
	// PR is its pull request, as the project's pull request cache and watch
	// have it now, or as it was when it finished when the cache has none.
	PR         *PullRequest `json:"pr,omitempty"`
	FinishedAt time.Time    `json:"finishedAt"`
	// Fingerprint is the work as it is now, which a verdict is recorded
	// against: when it changes, the agent comes back.
	Fingerprint string `json:"fingerprint"`
	// Verdict and ReviewedAt are set on the Reviewed list (?reviewed=1): what
	// the user decided, and when, about the work as it still is.
	Verdict    string     `json:"verdict,omitempty"`
	ReviewedAt *time.Time `json:"reviewedAt,omitempty"`
	// MachineKept is set on a reviewed agent whose machine the daemon did
	// not free, because it holds uncommitted or unpushed work.
	MachineKept bool `json:"machineKept,omitempty"`
}

// ReviewPage is GET /v1/review with ?limit= (and the same under a project),
// and ?cursor= for the page after Next: ready to review a page at a time, or
// with ?reviewed=1 the reviewed. Total is how many the list holds in all, so a
// count doesn't stop at the pages loaded. Without ?limit= the routes answer a
// plain list of every item.
type ReviewPage struct {
	Items []ReviewItem `json:"items"`
	Next  string       `json:"next,omitempty"`
	Total int          `json:"total"`
}

// ReviewRequest is POST /v1/agents/{project}/{agent}/review: the user's verdict
// (reviewed or dismissed) on the work they saw, by its Fingerprint.
type ReviewRequest struct {
	Verdict     string `json:"verdict"`
	Fingerprint string `json:"fingerprint"`
}

// WorkItem is one agent's work on the Work page, a live agent's or a past
// one's (its machine removed, kept in archived_agents): its review item, as
// "Ready to review" would show it, with what the page lists beside it.
type WorkItem struct {
	// Item is the agent as the review shows it. Its Fingerprint is set only
	// on a live agent that finished, the only work a verdict is recorded on;
	// a past agent's FinishedAt is when its machine was removed.
	Item ReviewItem `json:"item"`
	Task string     `json:"task,omitempty"`
	Past bool       `json:"past,omitempty"`
	// Status is WorkWorking, WorkReview, WorkReviewed or WorkDone.
	Status string `json:"status"`
	// Report is the newest report the agent filed when it finished.
	Report      *AgentReport `json:"report,omitempty"`
	Screenshots int          `json:"screenshots,omitempty"`
	Recordings  int          `json:"recordings,omitempty"`
	// At is what the list sorts by, newest first: when it finished, or for
	// one still at work, when it was created.
	At time.Time `json:"at"`
}

// What a WorkItem's Status can be: at work (or stopped short of finishing),
// finished and waiting for review, reviewed, or finished with nothing to
// review (its pull request merged or closed, or a past agent's).
const (
	WorkWorking  = "working"
	WorkReview   = "review"
	WorkReviewed = "reviewed"
	WorkDone     = "done"
)

// The filters GET /v1/work takes (?filter=): waiting for review, or by its
// pull request's state, or with none.
const (
	WorkFilterReview = "review"
	WorkFilterOpen   = "open"
	WorkFilterMerged = "merged"
	WorkFilterClosed = "closed"
	WorkFilterNone   = "none"
)

// WorkPage is a page of GET /v1/work, newest first, after ?cursor=. Total is
// how many the filter and the search keep; Counts is how many each filter
// keeps under the search, by filter, "" being all.
type WorkPage struct {
	Items  []WorkItem     `json:"items"`
	Next   string         `json:"next,omitempty"`
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
}

// The verdicts of a ReviewRequest, and ReviewChanges, which only
// RequestChanges records.
const (
	ReviewReviewed  = "reviewed"
	ReviewDismissed = "dismissed"
	ReviewChanges   = "changes"
)

// RequestChangesRequest is POST /v1/agents/{project}/{agent}/request-changes:
// what the user wants changed in the work they saw (by its Fingerprint),
// sent to the agent as tell_agent sends a message, and recorded as the
// verdict "changes" until the agent finishes again.
// Notes are the IDs of the draft ReviewNotes to send with it: the agent gets
// each as its file and lines, the code they quote and the note, after Text,
// which may then be empty.
type RequestChangesRequest struct {
	Text        string  `json:"text"`
	Fingerprint string  `json:"fingerprint,omitempty"`
	Notes       []int64 `json:"notes,omitempty"`
}

// ReviewNote is a note the user left on lines of an agent's diff in the
// review (GET/POST /v1/agents/{project}/{agent}/review-notes): the file, the
// side of the diff its lines are numbered on, ReviewSideNew for added and
// unchanged lines or ReviewSideOld for removed ones, the lines Start to End,
// the Code they quote as diff lines (each led by "+", "-" or " ") and Body,
// what the user wrote. Status is ReviewNoteDraft until Request changes sends
// it, then ReviewNotePending, or ReviewNoteResolved once the code it quotes
// has changed in the agent's worktree, or the user said so.
type ReviewNote struct {
	ID         int64      `json:"id"`
	Path       string     `json:"path"`
	Side       string     `json:"side"`
	Start      int        `json:"start"`
	End        int        `json:"end"`
	Code       string     `json:"code"`
	Body       string     `json:"body"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"createdAt"`
	SentAt     *time.Time `json:"sentAt,omitempty"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

// The sides a ReviewNote's lines are numbered on, and its statuses.
const (
	ReviewSideNew      = "new"
	ReviewSideOld      = "old"
	ReviewNoteDraft    = "draft"
	ReviewNotePending  = "pending"
	ReviewNoteResolved = "resolved"
)

// AddReviewNoteRequest is POST /v1/agents/{project}/{agent}/review-notes: a
// new draft note.
type AddReviewNoteRequest struct {
	Path  string `json:"path"`
	Side  string `json:"side"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Code  string `json:"code"`
	Body  string `json:"body"`
}

// EditReviewNoteRequest is PATCH /v1/agents/{project}/{agent}/review-notes/{id}:
// a draft's new Body, or a sent note marked resolved or not.
type EditReviewNoteRequest struct {
	Body     *string `json:"body,omitempty"`
	Resolved *bool   `json:"resolved,omitempty"`
}

// EventGroup says a group of agents given one task changed: it was made, one
// of its agents was made or finished, the lead recommended one, or it was
// decided. It carries the group's ID and project (GroupChange).
const EventGroup = "group"

// GroupChange is EventGroup's data.
type GroupChange struct {
	ID      string `json:"id"`
	Project string `json:"project"`
}

// EventReview says "Ready to review" changed: an agent finished, or a verdict
// was recorded. It carries the project.
const EventReview = "review"

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
	// Unchanged is a re-read that found what was already there. It is still
	// sent, so a client showing "refreshing" can stop.
	Unchanged bool `json:"unchanged,omitempty"`
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
	// Memory is what the daemon holds of the agent's heavy commands
	// (Agent.Memory).
	Memory *MemoryHold `json:"memory,omitempty"`
	// WaitingForImage is Agent.WaitingForImage.
	WaitingForImage bool `json:"waitingForImage,omitempty"`
	// Reclaimed is Agent.Reclaimed.
	Reclaimed string `json:"reclaimed,omitempty"`
}

// The ways idle reclaim holds an agent's memory (Agent.Reclaimed).
const (
	ReclaimedFrozen  = "frozen"
	ReclaimedStopped = "stopped"
)

// HeavyRequest asks, from inside an agent, to start a heavy command (a test
// run, a build): at once while the VM's memory is calm, else once it eases,
// in the order asked (package pressure). Asking again with the same key
// renews a started one, or keeps a waiting one's place.
type HeavyRequest struct {
	Key string `json:"key"`
	// Command is what it runs, for saying what waits or was paused.
	Command string `json:"command,omitempty"`
	// TTLSeconds is how long a command not in a cgroup of its own (one that
	// didn't join its run, HeavyJoin) counts as running without being asked
	// for again or ended: 15 minutes when 0.
	TTLSeconds int `json:"ttlSeconds,omitempty"`
	// WaitSeconds is how long to wait to start, at most 600; 0 waits that
	// long.
	WaitSeconds int `json:"waitSeconds,omitempty"`
}

// HeavyStart is the answer to a HeavyRequest.
type HeavyStart struct {
	Started bool `json:"started"`
	// Why is why it hasn't, a clause like "the VM's memory is under
	// pressure (some 34%) and 2 commands are ahead of it".
	Why string `json:"why,omitempty"`
}

// TestPolicy is what the in-agent API says about the agent's test runs
// (GET /v1/self/tests): whether a whole suite is refused, and the commit its
// branch started from, which what it changed is counted against.
type TestPolicy struct {
	BlockFullSuite bool   `json:"blockFullSuite"`
	Base           string `json:"base,omitempty"`
}

// HeavyJoin puts a process of the agent's, by its PID inside the agent, in
// the cgroup of a started heavy command, with everything it starts after.
type HeavyJoin struct {
	PID int `json:"pid"`
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

// UpdateRelease is what GET /v1/update/release answers: the update channel's
// latest release as GitHub lists it, and the files it offers, which the app
// picks its own package from to update itself in place. Assets is empty when
// GitHub couldn't be reached and the last check's find stands in.
type UpdateRelease struct {
	Version string         `json:"version"`
	URL     string         `json:"url"`
	Assets  []ReleaseAsset `json:"assets,omitempty"`
}

// ReleaseAsset is one file of a release: a package of the app, the
// command-line tool, or the SHA256SUMS (SHA256SUMS-mac) they're checked against.
type ReleaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
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

// DesktopUse is when one of a project's agents last used its desktop: its AI
// tool drove the display, or showed or recorded it (GET
// /v1/projects/{project}/desktops). The lead's chat shows a live view of the
// desktops in use, and only those.
type DesktopUse struct {
	Ref    string    `json:"ref"`
	UsedAt time.Time `json:"usedAt"`
}

type BrowserPage struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

type BrowserOpenRequest struct {
	URL string `json:"url"`
}

// ElementPickRequest starts the element picker on the page an agent's browser
// shows (POST .../browser/pick): Hint is what the overlay says over the page,
// in the app's language.
type ElementPickRequest struct {
	Hint string `json:"hint,omitempty"`
}

// ElementPickResult is what the picker ended with: the element clicked, or
// none when it was cancelled, with Escape or POST .../browser/pick/cancel.
type ElementPickResult struct {
	Element *ElementPick `json:"element,omitempty"`
}

// ElementPick is an element picked on a page in an agent's browser, to send
// to a chat with a message: where it is, what it is and how it looks.
type ElementPick struct {
	URL      string `json:"url"`
	Title    string `json:"title,omitempty"`
	Selector string `json:"selector"`
	Tag      string `json:"tag"`
	// HTML is its outer HTML, trimmed: long text and attributes cut, SVG paths
	// dropped, at most 3,000 characters.
	HTML   string         `json:"html"`
	Text   string         `json:"text,omitempty"`
	Styles []ElementStyle `json:"styles"` // computed, those not at their default
	// Box is where it is in the page's viewport (Viewport), in CSS pixels.
	Box      ElementBox  `json:"box"`
	Viewport ElementSize `json:"viewport"`
	// Image is the element cropped from a screenshot of the page, with a
	// little of what is around it.
	Image *ChatImageUpload `json:"image,omitempty"`
}

type ElementStyle struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ElementBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type ElementSize struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
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
	// Recorded is how long a trimmed recording took on the wall clock, in
	// seconds, and Cuts the stretches of it, in the same seconds, its video
	// leaves out: where the screen stood still. A note made during one is
	// placed in the video by taking out what was cut before it.
	Recorded float64    `json:"recorded,omitempty"`
	Cuts     []MediaCut `json:"cuts,omitempty"`
}

type MediaCut struct {
	From float64 `json:"from"`
	To   float64 `json:"to"`
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
	// NoTrim keeps every frame, where a recording otherwise plays a stretch
	// the screen stood still for as a moment of it.
	NoTrim bool `json:"noTrim,omitempty"`
	// WholeDisplay films the desktop as it is, where a display recording
	// otherwise makes the browser in front fullscreen first.
	WholeDisplay bool `json:"wholeDisplay,omitempty"`
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
	// Query narrows All to what the list's search keeps (?q=), so "Delete
	// all" takes what a search shows, including what isn't loaded yet.
	Query string `json:"query,omitempty"`
}

// MediaPage is one page of a Media view: GET /v1/media, a project's or an
// agent's media with ?limit=, newest first. Next is the cursor (?cursor=)
// for the page after it, empty on the last one. Without ?limit= those routes
// answer every item at once, as a plain list.
//
// Every route takes the same filters: ?project= (on /v1/media), ?agent= (a
// project's agent), ?kind= (a comma-separated list), ?only= (one kind of
// those), ?favorite=1, ?unseen=1 and ?q=, a search every item has to
// match each word (or "quoted phrase") of in its name, file, kind, type,
// agent, page URL or a note's text.
type MediaPage struct {
	Items []MediaItem `json:"items"`
	Next  string      `json:"next,omitempty"`
}

// MediaCounts is GET .../media/counts, with a list's own filters: the counts
// a Media view puts on its filters, from every item rather than the pages
// loaded. Total, Bytes, Kinds, Favorites and Unseen count what ?project=,
// ?agent= and ?kind= keep; Agents what ?project= and ?kind= keep, for
// choosing an agent; Projects what ?kind= keeps, for choosing a project.
// Matching is what every filter and the search keep: what the list shows
// once it's scrolled to its end, and what "Delete all" takes.
type MediaCounts struct {
	Total         int                 `json:"total"`
	Bytes         int64               `json:"bytes"`
	Kinds         map[string]int      `json:"kinds"`
	Favorites     int                 `json:"favorites"`
	Unseen        int                 `json:"unseen"`
	Agents        []MediaAgentCount   `json:"agents"`
	Projects      []MediaProjectCount `json:"projects"`
	Matching      int                 `json:"matching"`
	MatchingBytes int64               `json:"matchingBytes"`
}

// MediaAgentCount is one agent's items, newest agent first.
type MediaAgentCount struct {
	Agent string `json:"agent"` // project/agent
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
	Gone  bool   `json:"gone,omitempty"`
	Count int    `json:"count"`
	Bytes int64  `json:"bytes"`
}

// MediaProjectCount is one project's items, newest project first.
type MediaProjectCount struct {
	Project string `json:"project"`
	Count   int    `json:"count"`
}

// DeleteMediaResult is what a bulk delete took away. Bytes is what that freed
// on disk, which is why people delete media in the first place.
type DeleteMediaResult struct {
	Deleted int   `json:"deleted"`
	Bytes   int64 `json:"bytes"`
}

const EventMedia = "media"

// Notification is one thing the app tells the user needs them, across
// projects (GET /v1/notifications, and EventNotification as it happens): an
// agent asking something, or a credential or connector only they can give.
// What agents merely did, such as finishing, is not one: it is the review
// list's. Clicking one goes to the project chat where it is answered.
//
// A kept screenshot or recording is stored as a NotifyMedia notification too,
// as the Media views' record of what's unseen; it is announced on
// EventNotification for them and left out of the list.
type Notification struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"` // NotifyQuestion, or NotifyMedia on the event
	Project string `json:"project"`
	Agent   string `json:"agent"` // the agent's name
	Ref     string `json:"ref"`
	Title   string `json:"title,omitempty"` // what the agent was called at the time
	// Text is what to show under the headline: the question, or the media
	// item's name.
	Text  string     `json:"text,omitempty"`
	Media *MediaItem `json:"media,omitempty"` // on NotifyMedia
	// Question is the question's ID, on NotifyQuestion.
	Question string    `json:"question,omitempty"`
	At       time.Time `json:"at"`
	Seen     bool      `json:"seen,omitempty"`
}

// The kinds of Notification.
const (
	NotifyQuestion = "question"
	NotifyMedia    = "media"
)

// NotificationPage is GET /v1/notifications with ?limit=, and ?cursor= for
// the page after Next: the inbox's questions a page at a time, newest first.
// ?unseen=1 keeps those not seen yet. Total is how many the filter keeps in
// all, so a badge doesn't count only the pages loaded. Without ?limit= the
// route answers every one as a plain list.
type NotificationPage struct {
	Items []Notification `json:"items"`
	Next  string         `json:"next,omitempty"`
	Total int            `json:"total"`
}

// SeeNotificationsRequest marks notifications seen: those listed by ID, those
// about the listed media items, or all of them.
type SeeNotificationsRequest struct {
	IDs   []string `json:"ids,omitempty"`
	Media []string `json:"media,omitempty"`
	All   bool     `json:"all,omitempty"`
	// Kind is every notification of that kind, for the inbox's "Mark all
	// read", which can't list questions it hasn't loaded.
	Kind string `json:"kind,omitempty"`
	// AllMedia is every notification about a media item, for the Media
	// view's "Mark seen", which can't list items it hasn't loaded.
	AllMedia bool `json:"allMedia,omitempty"`
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
	Status   string `json:"status"` // ok, missing, outdated, optional, warn, updating or building
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
	// Local builds the image here even when a published one could be
	// downloaded instead.
	Local bool `json:"local,omitempty"`
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
	// SetupBuilding is the base image being built for the first time
	// (SetupCheck.Job). It doesn't block the wizard either: what needs the
	// image waits for the build.
	SetupBuilding = "building"
)

// ImageStatus is the base image as the top bar shows it: whether agents can
// be made from it, and the build started last, which runs in the background.
type ImageStatus struct {
	Ready    bool   `json:"ready"`
	Snapshot string `json:"snapshot"`
	// Build is idle, building or failed: failed until a build succeeds.
	Build string `json:"build"`
	// Job is that build's job, whose log the app shows.
	Job string `json:"job,omitempty"`
	// Step is the build's latest step, while it runs.
	Step string `json:"step,omitempty"`
	// Progress is how far that step has got, when it says ("40% of 1800 MB"
	// while downloading).
	Progress string `json:"progress,omitempty"`
	// Error is why it failed.
	Error string `json:"error,omitempty"`
	// Waiting counts the jobs, agents being made among them, that start once
	// the build is done.
	Waiting int `json:"waiting"`
}

const (
	ImageBuildIdle   = "idle"
	ImageBuilding    = "building"
	ImageBuildFailed = "failed"
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
	FeatureAgentCreateCursor   = "agent.create.cursor"
	FeatureAgentCreateByLead   = "agent.create.by_lead"
	FeatureAgentDestroy        = "agent.destroy"
	FeatureAgentRetire         = "agent.retire"
	FeatureAgentFork           = "agent.fork"
	FeatureGroupFanOut         = "group.fan_out"
	FeatureGroupKeep           = "group.keep"
	FeatureAgentSnapshot       = "agent.snapshot"
	FeatureAgentRestore        = "agent.restore"
	FeatureAgentRollback       = "agent.rollback"
	FeatureAgentForkTurn       = "agent.fork.turn"
	FeatureAgentTurn           = "agent.turn"
	FeatureLeadTurnClaude      = "lead.turn.claude"
	FeatureLeadTurnCodex       = "lead.turn.codex"
	FeatureLeadTurnOpenCode    = "lead.turn.opencode"
	FeatureLeadTurnCursor      = "lead.turn.cursor"
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
	FeaturePullLabels          = "pr.labels"
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
	FeatureChatFind            = "chat.find"   // a search inside a chat, counted once per opening of its find bar
	FeatureSearchOpen          = "search.open" // the search palette, AgentBox-wide
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
	// Health is the health events waiting, posted to EventsURL in a body
	// of their own; "" when none waits.
	Health string `json:"health"`
	// Setup is the same for the setup funnel's events.
	Setup string `json:"setup"`
}

// SetupFunnelReport is POST /v1/usage-events/setup: the steps of Setup the
// front end took on the user's machine before the daemon was up to keep
// them, the KVM check and the VM's making, handed over once it is. The
// daemon keeps them as setup events like its own, when the usage stats are
// on, and only their class of error leaves it.
type SetupFunnelReport struct {
	// Began is when the first of them began, in Unix seconds.
	Began int64             `json:"began"`
	Steps []SetupFunnelStep `json:"steps"`
}

// SetupFunnelStep is one of a SetupFunnelReport's steps.
type SetupFunnelStep struct {
	// Step is kvm_check or vm_ready.
	Step string `json:"step"`
	// Outcome is ok or failed, and for kvm_check missing or no_permission.
	Outcome string `json:"outcome"`
	// At is when it ended, in Unix seconds, and DurationS how long it took.
	At        int64 `json:"at"`
	DurationS int64 `json:"durationS"`
	// Error is a failed step's error, which the daemon only classifies.
	Error string `json:"error,omitempty"`
}

// AppFeatures are the keys the app may count through the API.
var AppFeatures = []string{
	FeatureDesktopOpen, FeatureTerminalOpen, FeatureAndroidOpen, FeatureAgentMediaView, FeatureProjectMediaView,
	FeaturePullList, FeatureMemoryView, FeatureTokensView,
	FeatureSettingsEnvironment, FeatureSettingsAccounts, FeatureSettingsLead, FeatureSettingsAgents,
	FeatureSettingsGeneral, FeatureSettingsModels, FeatureSettingsResources, FeatureSettingsProject, FeatureSettingsSearch, FeatureSearchOpen,
	FeatureChatFind, FeatureMenuOpenChat, FeatureMenuOpenTerminal, FeatureMenuInfo, FeatureMenuLifecycle, FeatureMenuRetire,
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

// TerminalTail is the end of one agent's terminal, for the thumbnail in its
// row of the agent list: the tmux window read (its AI tool's when it is used
// from the terminal, else the shell), the pane's foreground command, whether
// that is something other than a shell at its prompt, and the pane's last
// few lines of text, oldest first.
type TerminalTail struct {
	Ref     string   `json:"ref"`
	Window  string   `json:"window"`
	Command string   `json:"command,omitempty"`
	Busy    bool     `json:"busy"`
	Lines   []string `json:"lines"`
}

// TerminalTails answers GET /v1/projects/{project}/terminals: one tail per
// agent asked for that could be read; an agent that isn't running is left out.
type TerminalTails struct {
	Agents []TerminalTail `json:"agents"`
}

// VMGPU is the GPU AgentBox's VM has to give agents (Settings.VMGPU).
type VMGPU struct {
	// Kind is "dxg" (WSL's /dev/dxg), "render" (a DRM render node), "host"
	// (the Linux host's GPU, which the VM gets when it next starts with
	// "GPU for agents" on, and agents nothing until then), or "" when the
	// VM has none.
	Kind string `json:"kind"`
	// Node is the device: /dev/dxg or /dev/dri/renderD128, the host's for
	// "host".
	Node string `json:"node"`
	// Reason says why the VM has none, when Kind is "", or why readying it
	// failed (VMGPUSetupFailed); and what's still to do, such as restarting
	// the VM, when Kind is "host".
	Reason string `json:"reason"`
	// Setup is how far readying the GPU has got when the VM's can be readied
	// but isn't ready, as on a Mac's krunkit VM: one of the VMGPUSetup values.
	// "" when there's nothing to ready, Kind's GPU being there or there being
	// none to have. With Kind's GPU there and "GPU for agents" on, a krunkit
	// VM building a newer driver says VMGPUSetupRunning, then
	// VMGPUSetupFailed if it failed, agents keeping the one they have.
	Setup string `json:"setup"`
}

// VMGPU.Setup's values.
const (
	// VMGPUSetupAvailable: turning "GPU for agents" on readies the GPU (on a
	// Mac, Debian's generic kernel and a Venus driver built in the VM).
	VMGPUSetupAvailable = "available"
	// VMGPUSetupRunning: it's being readied, which takes minutes.
	VMGPUSetupRunning = "running"
	// VMGPUSetupRestart: ready once the VM restarts on its new kernel.
	VMGPUSetupRestart = "restart"
	// VMGPUSetupFailed: readying it failed, Reason says why. Turning "GPU for
	// agents" off and on tries again.
	VMGPUSetupFailed = "failed"
)

// GPUReport is GET .../browser/gpu: what an agent's desktop renders on, as
// its own glxinfo and vulkaninfo see it.
type GPUReport struct {
	// Device is the GPU device in the agent (/dev/dxg, a render node), ""
	// when it has none.
	Device string `json:"device"`
	// Display says whether the desktop answered: the renderer is empty when
	// it didn't.
	Display bool `json:"display"`
	// Renderer is OpenGL's renderer string on the desktop, and Accelerated
	// whether it is a GPU's rather than Mesa's software one (llvmpipe).
	Renderer    string `json:"renderer"`
	Accelerated bool   `json:"accelerated"`
	// Vulkan are the Vulkan devices the agent sees.
	Vulkan []VulkanDevice `json:"vulkan"`
	// Error is what went wrong asking, when something did.
	Error string `json:"error,omitempty"`
}

// VulkanDevice is one Vulkan physical device: its name and type
// ("DISCRETE_GPU", "INTEGRATED_GPU", "CPU", ...).
type VulkanDevice struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
