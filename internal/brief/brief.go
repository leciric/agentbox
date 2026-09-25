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

var (
	tmpl     = template.Must(template.New("brief").Parse(source))
	leadTmpl = template.Must(template.New("lead").Parse(leadSource))
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
	// Autonomy is how much it may do without being asked: "ask" or "on".
	Autonomy string
	// AgentModel is what the project says the agents it creates run on: "" to
	// follow the model new agents start on, a model name they all get, or
	// state.AgentModelAuto, which asks the lead to choose one per task.
	AgentModel string
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

func render(t *template.Template, d any) (string, error) {
	var b strings.Builder
	if err := t.Execute(&b, d); err != nil {
		return "", err
	}
	return b.String(), nil
}
