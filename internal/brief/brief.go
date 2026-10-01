// Package brief renders the instructions an AI tool receives about its
// AgentBox environment. AgentBox doesn't set projects up itself: the brief
// tells the agent what the machine offers and leaves the setup to the agent.
package brief

import (
	_ "embed"
	"strconv"
	"strings"
	"text/template"
)

//go:embed brief.md.tmpl
var source string

//go:embed lead.md.tmpl
var leadSource string

//go:embed home.md.tmpl
var homeSource string

var (
	tmpl     = template.Must(template.New("brief").Parse(source))
	leadTmpl = template.Must(template.New("lead").Parse(leadSource))
	homeTmpl = template.Must(template.New("home").Parse(homeSource))
)

type Data struct {
	Project  string
	Agent    string
	Title    string // optional
	Worktree string
	Branch   string
	BaseRef  string
	IP       string // empty in previews, before the agent has an address
	EnvFiles []string
	// Secrets are the names of the secrets this agent has as environment
	// variables (agentbox secrets). Values never come near the brief.
	Secrets []string
	Android bool // the project builds an Android app
	GitHub  bool // AgentBox shares a GitHub token with agents
	// Nesting is whether the project turned nesting on: the agent runs a real
	// Incus daemon of its own, inside its own container.
	Nesting bool
	// AgentPRs is whether the project lets its agents push their own branch
	// and open their own pull request. Off, the brief tells the agent not to
	// push and leaves both to the user and the lead.
	AgentPRs bool
	// VM is set when AgentBox runs in a Linux VM on another OS: WSL 2 on
	// Windows (package hostwsl, D94), or the VM it makes on a Mac (package
	// hostvm, D92). The agent's address is then inside that VM, which the
	// user's computer can't reach: only the preview proxy, which the VM
	// forwards to it, is.
	VM bool
	// Host names the user's computer when VM is set: "Windows" or "a Mac"
	// (hostos.Name).
	Host string
	// Notes are the project's notes: what every agent of it should know,
	// written by the user and by the project's lead (package notes). Empty
	// when the project has none, and then the brief has no such section.
	Notes string
	// Knowledge is this agent's slice of what the project remembers, built
	// from its memory against the agent's own task
	// (D75). It is a summary and
	// the brief says so: the agent goes deeper with search_memory. Empty until
	// the project has remembered something, and then there is no such section.
	Knowledge string
	// CompactWindow is this installation's configured Claude compact window,
	// in tokens (state.ClaudeCompactWindow, D83). 0 is a real setting, not a
	// missing one: it means the chat compacts at the model's own context
	// window rather than a configured one.
	CompactWindow int64
}

// CompactWindowText is how the brief names the compact window in prose:
// comma-grouped ("200,000 tokens") when the installation sets one, or the
// model's own window when it doesn't (CompactWindow == 0).
func (d Data) CompactWindowText() string {
	if d.CompactWindow == 0 {
		return "your model's whole context window"
	}
	return groupThousands(d.CompactWindow) + " tokens"
}

func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func Render(d Data) (string, error) {
	d.Notes = strings.TrimSpace(d.Notes)
	d.Knowledge = strings.TrimSpace(d.Knowledge)
	return render(tmpl, d)
}

// LeadData describes a project's lead: the chat that directs the project's
// agents from the host, without a machine or a shell of its own.
type LeadData struct {
	Project  string
	Root     string // the project's main checkout
	Worktree string // the lead's detached worktree
	BaseRef  string // the branch it stands on
	// CanSpawn is true once the lead has the tools to create and follow this
	// project's agents itself.
	CanSpawn bool
	// AgentPRs is whether the project's agents push their own branch and open
	// their own pull request, so the lead doesn't do it for them and retires
	// one once its PR is open.
	AgentPRs bool
	// Recheck is whether the daemon wakes the lead now and then with a
	// status of its running agents and its queue ("lead rechecks agents"),
	// which it may act on by retiring finished agents.
	Recheck bool
	// Queue is whether the agent queue is on, so create_agent's queue flag
	// means something.
	Queue bool
	// Autonomy is how much it may do without being asked: "ask" or "on".
	Autonomy string
	// AgentModel is what the project says the agents it creates run on: "" to
	// follow the model new agents start on, a model name they all get, or
	// state.AgentModelAuto, which asks the lead to choose one per task.
	AgentModel string
	// AgentDefaultModel and AgentDefaultWindow are what a Claude Code agent
	// the lead creates starts on when it passes neither — Settings → Models,
	// or the model the project names — with the window written the way
	// create_agent takes it ("200k", "1m"). EnforceAgentDefaults says they are
	// the only model and window it may use; otherwise they are the most it
	// may (agent.CheckLeadChoice refuses anything else either way).
	AgentDefaultModel    string
	AgentDefaultWindow   string
	EnforceAgentDefaults bool
	// ModelMenu is the models Claude Code last advertised for this account,
	// plus AgentBox's own small pinned list (D69), so the lead choosing one
	// names a model worth naming. Never empty for a Claude lead: the pinned
	// list alone is enough before any chat has started.
	ModelMenu []string
	// OpenCodeMenu is the models OpenCode named for this installation's own
	// OpenCode login, as its "provider/model" ids. It is empty unless agents
	// can actually run OpenCode — the base image has it and there is a login —
	// so a brief that lists models is a brief whose lead can really create
	// OpenCode agents.
	OpenCodeMenu []string
	// ClaudeAccounts are the names of the Claude Code accounts this project may
	// use: the machine's, less any its allow-list leaves out. The brief says
	// nothing about spreading agents across them unless there is more than one
	// to spread across (D88).
	ClaudeAccounts []string
	// VM is set when AgentBox runs in a Linux VM on another OS, and Host
	// names that OS as hostos.Name does ("a Mac", "Windows"). The lead's shell
	// is then the VM's, not the user's computer itself: on a Mac it reaches
	// the Mac's home through Lima's mount (D92).
	VM   bool
	Host string
	// Notes are the project's notes, the same ones its agents are given.
	Notes string
	// PRWatch is whether AgentBox watches this project's agents' pull
	// requests and tells an agent when its own breaks. When it's off, the
	// lead is asked to suggest turning it on the next time it's asked to fix
	// CI or a conflict.
	PRWatch bool
	// Recap is where this project's chat had got to, rendered from its
	// memory: it is what a session started after a rollover reads in place of
	// the conversation it can no longer see (D73). Empty until the first
	// compaction — and empty is the ordinary case, not a fault.
	Recap string
}

func RenderLead(d LeadData) (string, error) {
	d.Notes = strings.TrimSpace(d.Notes)
	d.Recap = strings.TrimSpace(d.Recap)
	return render(leadTmpl, d)
}

// HomeData is what the Home chat's brief is rendered from: the user's main
// chat, across every project and tied to none.
type HomeData struct {
	Dir      string // its working directory, ~/.agentbox
	Projects []HomeProject
	// VM and Host are LeadData's: where the chat's shell is.
	VM   bool
	Host string
}

// HomeProject is one project the Home chat is told about.
type HomeProject struct {
	Name string
	Root string
}

func RenderHome(d HomeData) (string, error) {
	return render(homeTmpl, d)
}

func render(t *template.Template, d any) (string, error) {
	var b strings.Builder
	if err := t.Execute(&b, d); err != nil {
		return "", err
	}
	return b.String(), nil
}
