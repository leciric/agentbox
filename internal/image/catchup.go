package image

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"agentbox/internal/incus"
)

//go:embed system.sh
var systemScript []byte

// Change is one image version and what it changed in the machine, in words for
// the person looking at a project base that doesn't have it yet.
type Change struct {
	Version string
	What    string
}

// Changes are what each image version changed, oldest first. Bumping Version
// means adding its line here: TestChangesEndAtVersion fails until it's there.
// It is what a project base's card lists under "what changed", since a base
// saved before a version knows its number and nothing else.
var Changes = []Change{
	{"2026.09.25.1", "bsdtar (libarchive-tools), and temporary files on a tmpfs at /t (TMPDIR)"},
	{"2026.09.25.2", "the DejaVu fonts the agent dock's key captions are drawn in"},
	{"2026.09.26.1", "Incus, for images built with it, so projects can turn nesting on"},
	{"2026.09.27.1", "Mesa's DRI, VA-API and Vulkan drivers, and vainfo, for GPU for agents"},
	{"2026.10.02.1", "/etc/agentbox-image, a marker that tells the agentbox binary it's on an agent's machine"},
}

// OlderVersion reports whether image version a comes before b. Versions are
// dates with a counter, "2026.09.27.1", compared field by field as numbers, so
// "2026.09.27.10" comes after "2026.09.27.9". An empty version, a machine from
// before versions were recorded, comes before every other.
func OlderVersion(a, b string) bool {
	if a == "" || b == "" {
		return a == "" && b != ""
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, errx := strconv.Atoi(as[i])
		y, erry := strconv.Atoi(bs[i])
		if errx != nil || erry != nil {
			if as[i] != bs[i] {
				return as[i] < bs[i]
			}
			continue
		}
		if x != y {
			return x < y
		}
	}
	return len(as) < len(bs)
}

// Behind is how far a machine copied from the base image some time ago — a
// project base — is behind what this AgentBox puts in the image now.
type Behind struct {
	// From is the image version the machine descends from, "" when it
	// doesn't record one. Image is whether that is older than Version, or
	// unknown; Changes are what the versions since changed, empty when From
	// is unknown.
	From    string
	Image   bool
	Changes []Change
	// Components are the optional parts the image has and the machine was
	// made without, for a person to read: "Incus", "Codex".
	Components []string
	// Install are the pinned agent tools the machine doesn't have at their
	// pinned version; Remove are the ones it has that tools.txt no longer pins
	// at all. ToolsUnknown says the machine doesn't record its tools, so
	// Install is every one of them, which mise skips where it already has
	// that version.
	Install, Remove []Tool
	ToolsUnknown    bool

	had        []string   // the machine's tool specs
	tools      []Tool     // every tool the machine will have
	components Components // what the machine will have
}

// Any reports whether the machine is behind at all.
func (b Behind) Any() bool {
	return b.Image || len(b.Components) > 0 || len(b.Install) > 0 || len(b.Remove) > 0
}

// ToolChange is one tool that moves: From is the version the machine has, ""
// for a tool it hasn't got (or doesn't record); To is the version it moves to,
// "" for one that goes.
type ToolChange struct {
	Name, From, To string
}

// ToolChanges are Install and Remove, as moves from one version to another.
func (b Behind) ToolChanges() []ToolChange {
	var out []ToolChange
	for _, t := range b.Install {
		c := ToolChange{Name: t.Name(), To: versionOf(t.Spec)}
		for _, spec := range b.had {
			if toolName(spec) == c.Name {
				c.From = versionOf(spec)
			}
		}
		out = append(out, c)
	}
	for _, t := range b.Remove {
		out = append(out, ToolChange{Name: t.Name(), From: versionOf(t.Spec)})
	}
	return out
}

func versionOf(spec string) string {
	return strings.TrimPrefix(spec, toolName(spec)+"@")
}

// BehindImage works out how far a machine that recorded machine is behind
// this AgentBox's image, whose base is built with image. The components wanted
// are the ones the base image has, since that is what a new agent from it would
// get; the tools are tools.txt's for them, which is what the daemon moves the
// base image's own tools to.
func BehindImage(machine, image Installed) Behind {
	b := Behind{From: machine.Version, had: machine.Tools}
	if OlderVersion(machine.Version, Version) {
		b.Image = true
		if machine.Version != "" {
			for _, c := range Changes {
				if OlderVersion(machine.Version, c.Version) {
					b.Changes = append(b.Changes, c)
				}
			}
		}
	}

	have, want := machine.Components, image.Components
	for _, c := range []struct {
		name       string
		have, want bool
	}{
		{"the Android tools", have.Android, want.Android},
		{"Codex", have.Codex, want.Codex},
		{"OpenCode", have.OpenCode, want.OpenCode},
		{"Incus", have.Incus, want.Incus},
	} {
		if c.want && !c.have {
			b.Components = append(b.Components, c.name)
		}
	}
	// A component the image dropped stays on the machine: the project may use
	// it, and nothing about a missing one is broken. AgentBox's development
	// caches aren't caught up either: they are caches, and a project base has
	// its own.
	b.components = Components{
		Android:   have.Android || want.Android,
		Codex:     have.Codex || want.Codex,
		OpenCode:  have.OpenCode || want.OpenCode,
		DevCaches: have.DevCaches,
		Incus:     have.Incus || want.Incus,
	}

	b.tools = ToolsFor(b.components)
	pinned := map[string]bool{}
	for _, t := range Tools {
		pinned[t.Name()] = true
	}
	if machine.Tools == nil {
		b.ToolsUnknown = true
		b.Install = b.tools
		return b
	}
	for _, t := range b.tools {
		if !slices.Contains(machine.Tools, t.Spec) {
			b.Install = append(b.Install, t)
		}
	}
	for _, spec := range machine.Tools {
		if !pinned[toolName(spec)] {
			b.Remove = append(b.Remove, Tool{Spec: spec})
		}
	}
	return b
}

// record are the configuration keys that say what a machine that caught up
// descends from: this image version, its components, and its tools.
func (b Behind) record() []string {
	return append([]string{
		versionKey + "=" + Version,
		androidKey + "=" + envFlag(b.components.Android),
		codexKey + "=" + envFlag(b.components.Codex),
		opencodeKey + "=" + envFlag(b.components.OpenCode),
		devCachesKey + "=" + envFlag(b.components.DevCaches),
		incusKey + "=" + envFlag(b.components.Incus)},
		recordTools(b.tools)...)
}

// CatchUp brings a running machine copied from a project base up to this
// AgentBox's image, as far as is safe on a machine an agent has already used:
// system.sh, which is written to run again, when the image moved on or has a
// component the machine lacks; then the agent tools that moved, installed and
// removed with mise the way UpdateTools does it; then a check of every tool.
// provision.sh isn't run again: it makes the user and writes the agent tools'
// own settings, which the project may have changed since.
//
// Once everything passed, it records on the instance what the machine now
// descends from, so a base saved from it says so. A failure records nothing:
// the base saved from it still shows as behind.
func CatchUp(ctx context.Context, inc incus.Client, instance string, u User, b Behind, log io.Writer) error {
	if !b.Any() {
		return nil
	}
	step := stepper(log)
	system := b.Image || len(b.Components) > 0
	type file struct {
		path    string
		content []byte
		mode    os.FileMode
	}
	files := []file{
		{"/root/tools.sh", toolsScript, 0o700},
		{"/root/tools.list", toolsList(b.Install), 0o600},
		{"/root/tools.remove", toolsList(b.Remove), 0o600},
		{"/root/tools.current", toolsList(b.tools), 0o600},
	}
	if system {
		files = append([]file{{"/root/system.sh", systemScript, 0o700}}, files...)
	}
	// The machine is saved as the project's base afterwards: nothing of this
	// may be left in it, whatever happened.
	defer func() {
		rm := []string{"rm", "-f"}
		for _, f := range files {
			rm = append(rm, f.path)
		}
		_, _ = inc.Exec(context.WithoutCancel(ctx), instance, rm...)
	}()
	for _, f := range files {
		if err := inc.WriteFile(ctx, instance, f.path, f.content, 0, 0, f.mode); err != nil {
			return err
		}
	}
	run := func(what string, env []string, args ...string) error {
		cmd := []string{"exec", instance, "-T"}
		for _, e := range env {
			cmd = append(cmd, "--env", e)
		}
		c := inc.Command(ctx, append(append(cmd, "--"), args...)...)
		c.Stdout, c.Stderr = log, log
		if err := c.Run(); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		return nil
	}

	if system {
		step("Installing what the base image has now: Debian's packages, Docker, the browser, Mesa, mise and the machine's settings")
		if err := run("system.sh", b.components.Env(), "/root/system.sh", u.Name); err != nil {
			return err
		}
	}
	if len(b.Remove) > 0 {
		step("Removing %s", strings.Join(specsOf(b.Remove), ", "))
		if err := run("tools.sh remove", nil, "/root/tools.sh", "remove", u.Name, "/root/tools.remove"); err != nil {
			return err
		}
	}
	if len(b.Install) > 0 {
		step("Installing %s", strings.Join(specsOf(b.Install), ", "))
		if err := run("tools.sh install", nil, "/root/tools.sh", "install", u.Name, "/root/tools.list"); err != nil {
			return err
		}
	}
	step("Checking every agent tool")
	if err := run("tools.sh verify", nil, "/root/tools.sh", "verify", u.Name, "/root/tools.current"); err != nil {
		return err
	}
	return inc.SetConfig(ctx, instance, b.record()...)
}
