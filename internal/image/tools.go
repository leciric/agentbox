package image

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"agentbox/internal/incus"
)

//go:embed tools.txt
var toolsManifest string

//go:embed tools.sh
var toolsScript []byte

// Tool is one agent tool the base image pins, from tools.txt.
type Tool struct {
	// Spec is mise's name for it with its version, like "claude@2.1.280" or
	// "npm:@playwright/mcp@0.0.79".
	Spec string
	// Option is the component it belongs to: "" for every image, otherwise
	// OptionCodex or OptionOpenCode.
	Option string
	// Check is a shell command, run as the user, that proves it works.
	Check string
}

// Name is the tool without its version: "npm:@playwright/mcp".
func (t Tool) Name() string { return toolName(t.Spec) }

func toolName(spec string) string {
	if i := strings.LastIndex(spec, "@"); i > 0 {
		return spec[:i]
	}
	return spec
}

// Tools is every tool tools.txt pins, in its order.
var Tools = mustParseTools(toolsManifest)

func mustParseTools(manifest string) []Tool {
	tools, err := parseTools(manifest)
	if err != nil {
		panic("internal/image/tools.txt: " + err.Error())
	}
	return tools
}

func parseTools(manifest string) ([]Tool, error) {
	var tools []Tool
	seen := map[string]bool{}
	for n, line := range strings.Split(manifest, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, fmt.Errorf("line %d: want a component, a tool and a check, got %q", n+1, line)
		}
		t := Tool{Spec: fields[1], Check: strings.Join(fields[2:], " ")}
		switch fields[0] {
		case "-":
		case OptionCodex, OptionOpenCode:
			t.Option = fields[0]
		default:
			return nil, fmt.Errorf("line %d: %q is no component a tool can belong to", n+1, fields[0])
		}
		if name := t.Name(); name == t.Spec || strings.HasSuffix(t.Spec, "@") || strings.HasSuffix(t.Spec, "@latest") {
			return nil, fmt.Errorf("line %d: %s isn't pinned to a version", n+1, t.Spec)
		}
		if seen[t.Name()] {
			return nil, fmt.Errorf("line %d: %s is pinned twice", n+1, t.Name())
		}
		seen[t.Name()] = true
		tools = append(tools, t)
	}
	return tools, nil
}

// ToolsFor is the tools an image with these components has.
func ToolsFor(c Components) []Tool {
	var out []Tool
	for _, t := range Tools {
		if t.Option == "" || t.Option == OptionCodex && c.Codex || t.Option == OptionOpenCode && c.OpenCode {
			out = append(out, t)
		}
	}
	return out
}

// Pin is the pinned spec of the tool mise calls name, like
// "npm:@agentclientprotocol/claude-agent-acp@0.81.0". It panics for a tool
// tools.txt doesn't pin, which only a change to AgentBox itself can cause.
func Pin(name string) string {
	for _, t := range Tools {
		if t.Name() == name {
			return t.Spec
		}
	}
	panic("internal/image/tools.txt pins no " + name)
}

// ToolsVersion names a set of tools: the same tools at the same versions have
// the same one, whatever their order. It is worked out rather than written
// down, so moving a tool on can't forget to bump it; the checks don't count,
// since changing one installs nothing.
func ToolsVersion(tools []Tool) string {
	specs := specsOf(tools)
	slices.Sort(specs)
	sum := sha256.Sum256([]byte(strings.Join(specs, "\n")))
	return hex.EncodeToString(sum[:])[:12]
}

func specsOf(tools []Tool) []string {
	specs := make([]string, 0, len(tools))
	for _, t := range tools {
		specs = append(specs, t.Spec)
	}
	return specs
}

// Action is what the base image on this machine needs.
type Action int

const (
	// UpToDate: nothing.
	UpToDate Action = iota
	// NeedsBuild: there is no base image, and only a build makes one.
	NeedsBuild
	// NeedsRebuild: it was provisioned by another image version, or with other
	// components, and only a rebuild, which you start, fixes that.
	NeedsRebuild
	// NeedsTools: only the pinned tools moved on, which UpdateTools does in
	// place, on its own.
	NeedsTools
)

// Plan is what to do about the base image, and for NeedsTools, what to change.
type Plan struct {
	Action Action
	// Install are the tools to install: new ones, and new versions of ones
	// already there. Remove are tools the image has that are no longer pinned.
	Install, Remove []Tool
	// Tools are every tool the image will have after it, and ToolsVersion
	// their version.
	Tools        []Tool
	ToolsVersion string
}

// PlanFor decides what the base image needs, from whether it is built, what
// it was built with, and the components wanted for it now.
func PlanFor(built bool, installed Installed, wanted Components) Plan {
	switch {
	case !built:
		return Plan{Action: NeedsBuild}
	case installed.Version != Version, installed.Components != wanted:
		return Plan{Action: NeedsRebuild}
	}
	want := ToolsFor(installed.Components)
	plan := Plan{Action: NeedsTools, Tools: want, ToolsVersion: ToolsVersion(want)}
	if installed.ToolsVersion == plan.ToolsVersion {
		plan.Action = UpToDate
		return plan
	}
	if installed.ToolsVersion == "" || installed.Tools == nil {
		// Built before the tools were recorded, by this same image version:
		// what it has is unknown, so everything is installed again, which mise
		// skips for a version it already has.
		plan.Install = want
		return plan
	}
	wantNames := map[string]bool{}
	for _, t := range want {
		wantNames[t.Name()] = true
		if !slices.Contains(installed.Tools, t.Spec) {
			plan.Install = append(plan.Install, t)
		}
	}
	for _, spec := range installed.Tools {
		if !wantNames[toolName(spec)] {
			plan.Remove = append(plan.Remove, Tool{Spec: spec})
		}
	}
	return plan
}

// toolsList renders tools as tools.sh reads them: the spec, a tab, the check.
func toolsList(tools []Tool) []byte {
	var b strings.Builder
	for _, t := range tools {
		b.WriteString(t.Spec + "\t" + t.Check + "\n")
	}
	return []byte(b.String())
}

// recordTools are the config keys that say which tools an image has.
func recordTools(tools []Tool) []string {
	return []string{
		toolsVersionKey + "=" + ToolsVersion(tools),
		toolsKey + "=" + strings.Join(specsOf(tools), " "),
	}
}

// UpdateTools moves the base image's tools on in place, as plan says, rather
// than building it again: it starts a copy of the base, installs only what
// changed with mise as the user, checks every tool, and swaps the copy in as
// the base with the new tools version recorded. The base stays as it was, and
// agents keep being made from it, until the swap; a failure deletes the copy
// and leaves it.
func UpdateTools(ctx context.Context, inc incus.Client, u User, plan Plan, log io.Writer) error {
	if plan.Action != NeedsTools {
		return fmt.Errorf("the base image needs no tool update")
	}
	step := stepper(log)
	// The copy starts with the profile as this AgentBox makes it, as a build would.
	if err := EnsureProfile(ctx, inc); err != nil {
		return err
	}
	next := Base + "-next"
	if err := remove(ctx, inc, next); err != nil {
		return err
	}
	fail := func(err error) error {
		_, _ = inc.Run(context.WithoutCancel(ctx), "delete", "--force", next)
		return fmt.Errorf("updating the agent tools in place: %w", err)
	}

	step("Copying %s to %s", SnapshotRef(), next)
	if err := run(ctx, inc, []string{"copy", SnapshotRef(), next}, []string{"start", next}); err != nil {
		return fail(err)
	}
	if _, err := inc.WaitReady(ctx, next, readyTimeout); err != nil {
		return fail(err)
	}
	for _, f := range []struct {
		path    string
		content []byte
		mode    os.FileMode
	}{
		{"/root/tools.sh", toolsScript, 0o700},
		{"/root/tools.list", toolsList(plan.Install), 0o600},
		{"/root/tools.remove", toolsList(plan.Remove), 0o600},
		{"/root/tools.current", toolsList(plan.Tools), 0o600},
	} {
		if err := inc.WriteFile(ctx, next, f.path, f.content, 0, 0, f.mode); err != nil {
			return fail(err)
		}
	}
	tools := func(mode, list string) error {
		cmd := inc.Command(ctx, "exec", next, "-T", "--", "/root/tools.sh", mode, u.Name, list)
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("tools.sh %s: %w", mode, err)
		}
		return nil
	}
	if len(plan.Remove) > 0 {
		step("Removing %s", strings.Join(specsOf(plan.Remove), ", "))
		if err := tools("remove", "/root/tools.remove"); err != nil {
			return fail(err)
		}
	}
	if len(plan.Install) > 0 {
		step("Installing %s", strings.Join(specsOf(plan.Install), ", "))
		if err := tools("install", "/root/tools.list"); err != nil {
			return fail(err)
		}
	}
	step("Checking every tool")
	if err := tools("verify", "/root/tools.current"); err != nil {
		return fail(err)
	}
	if err := run(ctx, inc,
		[]string{"exec", next, "--", "sh", "-c", "rm -f /root/tools.sh /root/tools.list /root/tools.remove /root/tools.current && " + scrubMachineID},
		[]string{"stop", next},
		append([]string{"config", "set", next}, recordTools(plan.Tools)...),
		[]string{"snapshot", "create", next, Snapshot},
	); err != nil {
		return fail(err)
	}
	return swapIn(ctx, inc, next, log)
}
