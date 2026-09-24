package image

import (
	"strconv"
	"strings"
)

// The optional components a download belongs to. A download with no option is
// in every build.
const (
	OptionAndroid  = "android"
	OptionCodex    = "codex"
	OptionOpenCode = "opencode"
)

// Download is one thing a base image build fetches over the network. The app's
// Setup page and `agentbox image build` show this list, so that the few
// minutes the build takes are a list of things rather than a spinner.
type Download struct {
	// Name is what it is called, like "Chromium, VNC and ffmpeg".
	Name string
	// Purpose is one sentence on why an agent has it.
	Purpose string
	// MB is roughly how much it downloads, in megabytes. These were measured
	// on 2026-09-18, on Debian 13 amd64, by reading a container's own
	// interface counters around each step of provision.sh. Debian's mirrors
	// and the registries move, so they say what to expect rather than what
	// you will get exactly.
	MB int
	// Option is the image option that fetches it: "" for always, otherwise
	// OptionAndroid, OptionCodex or OptionOpenCode.
	Option string
}

// DownloadsHint is the one line that goes with the list wherever it's shown.
const DownloadsHint = "Debian's packages come from your closest mirror, so the time this takes depends on your connection"

// Downloads is everything a base image build can fetch, in the order the build
// fetches it: about 1.1 GB with no option, 1.5 GB with all of them. These are
// compressed download sizes, and rather less than what ends up on disk.
var Downloads = []Download{
	{
		Name:    "Debian 13",
		Purpose: "The Linux an agent runs, as Incus' Debian 13 container image.",
		MB:      105,
	},
	{
		Name:    "Base packages",
		Purpose: "git, tmux, build-essential, Python, jq, ripgrep and the rest of a usable shell.",
		MB:      143,
	},
	{
		Name:    "Docker Engine",
		Purpose: "Engine, CLI, containerd, Compose and Buildx, so an agent can run databases and services.",
		MB:      109,
	},
	{
		Name:    "Chromium, VNC and ffmpeg",
		Purpose: "The agent's own browser on a virtual display, and the screenshots and recordings it keeps.",
		MB:      276,
	},
	{
		Name:    "scrcpy",
		Purpose: "Mirrors the screen of an agent's Android emulator into its display.",
		MB:      18,
		Option:  OptionAndroid,
	},
	{
		Name:    "mise",
		Purpose: "Installs the pinned tools below, and whatever versions a project's own mise.toml asks for.",
		MB:      44,
	},
	{
		Name:    "Go",
		Purpose: "The Go toolchain, pinned so every agent builds with the same one.",
		MB:      72,
	},
	{
		Name:    "Node.js and pnpm",
		Purpose: "The Node.js LTS runtime and pnpm, which also run the tools below.",
		MB:      81,
	},
	{
		Name:    "Claude Code",
		Purpose: "The AI tool agents run by default.",
		MB:      104,
	},
	{
		Name:    "Claude Code ACP adapter",
		Purpose: "Drives Claude Code for the app's Chat tab.",
		MB:      108,
	},
	{
		Name:    "GitHub CLI",
		Purpose: "gh, for an agent to read pull requests, issues and checks.",
		MB:      16,
	},
	{
		Name:    "Playwright MCP server",
		Purpose: "Lets an agent's AI tool drive the browser you watch it in.",
		MB:      12,
	},
	{
		Name:    "Codex CLI",
		Purpose: "OpenAI's Codex, for agents created with --ai codex.",
		MB:      141,
		Option:  OptionCodex,
	},
	{
		Name:    "Codex ACP adapter",
		Purpose: "Drives Codex for the app's Chat tab.",
		MB:      132,
		Option:  OptionCodex,
	},
	{
		Name:    "OpenCode",
		Purpose: "The open-source agent, for agents created with --ai opencode. It is its own ACP adapter.",
		MB:      120,
		Option:  OptionOpenCode,
	},
}

// on reports whether opts asks for this option.
func (c Components) on(option string) bool {
	switch option {
	case "":
		return true
	case OptionAndroid:
		return c.Android
	case OptionCodex:
		return c.Codex
	case OptionOpenCode:
		return c.OpenCode
	}
	return false
}

// DownloadsFor is what a local build with these components fetches.
func DownloadsFor(c Components) []Download {
	var want []Download
	for _, d := range Downloads {
		if c.on(d.Option) {
			want = append(want, d)
		}
	}
	return want
}

// TotalMB adds up a list of downloads.
func TotalMB(downloads []Download) int {
	total := 0
	for _, d := range downloads {
		total += d.MB
	}
	return total
}

// DownloadTable renders the list as plain text, for `agentbox image build`.
func DownloadTable(downloads []Download) string {
	var b strings.Builder
	for _, d := range downloads {
		b.WriteString(pad(d.Name, 26))
		b.WriteString(pad(size(d.MB), 8))
		b.WriteString(d.Purpose)
		if d.Option != "" {
			b.WriteString(" (--" + d.Option + ")")
		}
		b.WriteString("\n")
	}
	b.WriteString(pad("Total", 26) + size(TotalMB(downloads)) + "\n")
	return b.String()
}

// size reads a download as a person would, rounded to a tenth of a gigabyte.
func size(mb int) string {
	if mb < 1000 {
		return strconv.Itoa(mb) + " MB"
	}
	tenths := (mb + 50) / 100
	return strconv.Itoa(tenths/10) + "." + strconv.Itoa(tenths%10) + " GB"
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s + " "
	}
	return s + strings.Repeat(" ", width-len(s))
}
