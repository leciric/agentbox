package image

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The manifest the image ships with parses, pins everything, and has what
// the rest of AgentBox asks it for.
func TestToolsManifest(t *testing.T) {
	if len(Tools) == 0 {
		t.Fatal("tools.txt pins no tools")
	}
	for _, name := range []string{"go", "node", "pnpm", "claude", "gh", "npm:@playwright/mcp",
		"npm:@agentclientprotocol/claude-agent-acp", "codex", "npm:@agentclientprotocol/codex-acp", "npm:opencode-ai"} {
		if spec := Pin(name); !strings.HasPrefix(spec, name+"@") {
			t.Errorf("Pin(%q) = %q", name, spec)
		}
	}
	names := func(tools []Tool) []string {
		var out []string
		for _, t := range tools {
			out = append(out, t.Name())
		}
		return out
	}
	plain := names(ToolsFor(Components{}))
	if slices.Contains(plain, "codex") || slices.Contains(plain, "npm:opencode-ai") || !slices.Contains(plain, "claude") {
		t.Errorf("an image with no components has %v", plain)
	}
	// Android and the dev caches aren't tools mise installs, so they change nothing here.
	if got := names(ToolsFor(Components{Android: true, DevCaches: true})); !slices.Equal(got, plain) {
		t.Errorf("Android and the dev caches changed the tools: %v", got)
	}
	all := names(ToolsFor(Components{Codex: true, OpenCode: true}))
	for _, want := range []string{"codex", "npm:@agentclientprotocol/codex-acp", "npm:opencode-ai"} {
		if !slices.Contains(all, want) {
			t.Errorf("an image with Codex and OpenCode has no %s: %v", want, all)
		}
	}
	// Nothing about the tools may be left in provision.sh, or bumping one
	// there would slip past the tools version.
	for _, pinned := range []string{"CLAUDE_VERSION", "CODEX_VERSION", "claude@", "mise use"} {
		if strings.Contains(string(provision), pinned) {
			t.Errorf("provision.sh still has %q: the tools are pinned in tools.txt", pinned)
		}
	}
}

func TestParseToolsRefuses(t *testing.T) {
	for _, bad := range []string{
		"- claude@2.1.0",                   // no check
		"- claude claude --version",        // no version
		"- claude@ claude --version",       // an empty one
		"- claude@latest claude --version", // not a pin
		"android scrcpy@4 scrcpy --version",
		"- claude@1 claude --version\n- claude@2 claude --version",
	} {
		if _, err := parseTools(bad); err == nil {
			t.Errorf("parseTools(%q) took it", bad)
		}
	}
	tools, err := parseTools("# a comment\n\ncodex   npm:@openai/codex@0.1.0   codex --version && true\n")
	if err != nil {
		t.Fatal(err)
	}
	want := Tool{Spec: "npm:@openai/codex@0.1.0", Option: OptionCodex, Check: "codex --version && true"}
	if len(tools) != 1 || tools[0] != want || tools[0].Name() != "npm:@openai/codex" {
		t.Errorf("parseTools = %+v", tools)
	}
}

func TestToolsVersion(t *testing.T) {
	a := []Tool{{Spec: "go@1.0"}, {Spec: "claude@2.0", Check: "claude --version"}}
	if ToolsVersion(a) != ToolsVersion([]Tool{a[1], a[0]}) {
		t.Error("the order of the tools changed the version")
	}
	if ToolsVersion(a) != ToolsVersion([]Tool{{Spec: "go@1.0"}, {Spec: "claude@2.0", Check: "true"}}) {
		t.Error("a check changed the version, though it installs nothing")
	}
	if ToolsVersion(a) == ToolsVersion([]Tool{{Spec: "go@1.0"}, {Spec: "claude@2.1"}}) {
		t.Error("moving a tool on kept the version")
	}
	if ToolsVersion(a) == ToolsVersion(a[:1]) {
		t.Error("dropping a tool kept the version")
	}
}

func TestPlanFor(t *testing.T) {
	current := ToolsFor(Components{})
	specs := specsOf(current)
	upToDate := Installed{Version: Version, ToolsVersion: ToolsVersion(current), Tools: specs}
	// claude moved on since the image was built, and something it had is no longer pinned.
	var older []string
	for _, s := range specs {
		if toolName(s) == "claude" {
			s = "claude@0.0.1"
		}
		older = append(older, s)
	}
	older = append(older, "npm:retired@1.0")

	for _, c := range []struct {
		name      string
		built     bool
		installed Installed
		wanted    Components
		action    Action
		install   []string
		remove    []string
	}{
		{name: "no image", action: NeedsBuild},
		{name: "up to date", built: true, installed: upToDate, action: UpToDate},
		{name: "an older image version", built: true, installed: Installed{Version: "2020.01.01.1", ToolsVersion: upToDate.ToolsVersion, Tools: specs}, action: NeedsRebuild},
		{name: "no version at all", built: true, action: NeedsRebuild},
		{name: "a component turned on", built: true, installed: upToDate, wanted: Components{Codex: true}, action: NeedsRebuild},
		{
			name: "tools moved on", built: true, action: NeedsTools,
			installed: Installed{Version: Version, ToolsVersion: "0123456789ab", Tools: older},
			install:   []string{Pin("claude")}, remove: []string{"npm:retired@1.0"},
		},
		{
			// An image of this version from before the tools were recorded has
			// them all put in again, which mise skips where it has them.
			name: "tools never recorded", built: true, action: NeedsTools,
			installed: Installed{Version: Version},
			install:   specs,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			plan := PlanFor(c.built, c.installed, c.wanted)
			if plan.Action != c.action {
				t.Fatalf("action = %v, want %v", plan.Action, c.action)
			}
			if got := specsOf(plan.Install); !slices.Equal(got, c.install) && len(got)+len(c.install) > 0 {
				t.Errorf("install = %v, want %v", got, c.install)
			}
			if got := specsOf(plan.Remove); !slices.Equal(got, c.remove) && len(got)+len(c.remove) > 0 {
				t.Errorf("remove = %v, want %v", got, c.remove)
			}
			if c.action == NeedsTools && plan.ToolsVersion != upToDate.ToolsVersion {
				t.Errorf("the update would record tools version %s, want %s", plan.ToolsVersion, upToDate.ToolsVersion)
			}
		})
	}

	// The tools an update puts in are the image's own components', not the
	// ones someone just turned on: those ask for a rebuild above.
	codex := ToolsFor(Components{Codex: true})
	plan := PlanFor(true, Installed{Version: Version, Components: Components{Codex: true}, ToolsVersion: "old", Tools: specsOf(codex)[:1]}, Components{Codex: true})
	if plan.Action != NeedsTools || !slices.Contains(specsOf(plan.Install), Pin("codex")) || ToolsVersion(plan.Tools) != ToolsVersion(codex) {
		t.Errorf("an image with Codex plans %+v", plan)
	}
}

// tools.sh runs against a fake runuser and mise, which log what they're asked.
func TestToolsScript(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	script := filepath.Join(dir, "tools.sh")
	for name, content := range map[string]string{
		// runuser -l <user> -c <command>
		"runuser":  "#!/bin/sh\necho \"as $2: $4\" >>" + log + "\nexec sh -c \"$4\"\n",
		"mise":     "#!/bin/sh\necho \"mise $*\" >>" + log + "\n",
		"tools.sh": string(toolsScript),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	list := filepath.Join(dir, "list")
	_ = os.WriteFile(list, toolsList([]Tool{{Spec: "claude@2.1.0", Check: "echo claude works"}, {Spec: "npm:@x/y@1.0", Check: "true"}}), 0o644)
	run := func(mode string) (string, error) {
		_ = os.Remove(log)
		cmd := exec.Command(bash, script, mode, "dev", list)
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		b, _ := os.ReadFile(log)
		return string(b) + string(out), err
	}

	out, err := run("install")
	if err != nil || !strings.Contains(out, "as dev: export MISE_YES=1 && mise use -g claude@2.1.0 npm:@x/y@1.0") {
		t.Errorf("install: %v\n%s", err, out)
	}
	out, err = run("remove")
	if err != nil || !strings.Contains(out, "mise unuse -g claude npm:@x/y") {
		t.Errorf("remove: %v\n%s", err, out)
	}
	out, err = run("verify")
	if err != nil || !strings.Contains(out, "claude works") {
		t.Errorf("verify: %v\n%s", err, out)
	}
	_ = os.WriteFile(list, toolsList([]Tool{{Spec: "claude@2.1.0", Check: "false"}}), 0o644)
	if out, err := run("verify"); err == nil || !strings.Contains(out, "claude@2.1.0 doesn't work") {
		t.Errorf("a failing check passed: %v\n%s", err, out)
	}
}
