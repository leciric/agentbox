package skills

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/state"
)

func TestParseReadsTheFrontmatterSkillsUse(t *testing.T) {
	meta, body, err := Parse("---\nname: pdf\ndescription: >\n  Fill PDF forms,\n  and read them.\nuser-invocable: false\nmetadata:\n  surfaces: cli\nlicense: 'Apache 2.0'\n---\n# PDF\n")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "pdf" || meta.Description != "Fill PDF forms, and read them." || meta.UserInvocable || meta.Fields["license"] != "Apache 2.0" || meta.Fields["metadata"] != "surfaces: cli" {
		t.Errorf("Parse() = %+v", meta)
	}
	if body != "# PDF\n" {
		t.Errorf("body = %q", body)
	}
	if meta, body, _ := Parse("Just instructions."); meta.Name != "" || !meta.UserInvocable || body != "Just instructions." {
		t.Errorf("Parse(no frontmatter) = %+v, %q", meta, body)
	}
	if _, _, err := Parse("---\nname: x\n"); err == nil {
		t.Error("an unclosed frontmatter parsed")
	}
	if meta, _, _ := Parse("---\r\nname: x\r\ndescription: \"Say \\\"hi\\\"\"\r\n---\r\n"); meta.Description != `Say "hi"` {
		t.Errorf("CRLF and a quoted description: %+v", meta)
	}
}

func TestNamesAreTheSpecs(t *testing.T) {
	for _, n := range []string{"pdf", "review-pr", "a1"} {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v", n, err)
		}
	}
	for _, n := range []string{"", "PDF", "-x", "x-", "a--b", "a_b", "a/b", strings.Repeat("a", 65)} {
		if ValidateName(n) == nil {
			t.Errorf("ValidateName(%q) passed", n)
		}
	}
	for in, want := range map[string]string{"My_Skill": "my-skill", "  ": "", "plugin:skill": "plugin-skill", "a--b": "a-b"} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestWithNameMakesTheFolderAndTheFrontmatterAgree(t *testing.T) {
	for in, want := range map[string]string{
		"---\nname: old\ndescription: d\n---\nbody": "---\nname: new\ndescription: d\n---\nbody",
		"---\ndescription: d\n---\nbody":            "---\nname: new\ndescription: d\n---\nbody",
		"body":                                      "---\nname: new\ndescription: \n---\n\nbody",
	} {
		if got := WithName(in, "new"); got != want {
			t.Errorf("WithName(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestCheck(t *testing.T) {
	good := []state.SkillFile{{Path: "SKILL.md", Content: []byte("---\nname: x\ndescription: d\n---\n")}}
	if _, err := Check("x", good); err != nil {
		t.Error(err)
	}
	for name, files := range map[string][]state.SkillFile{
		"no SKILL.md":    {{Path: "README.md"}},
		"no description": {{Path: "SKILL.md", Content: []byte("---\nname: x\n---\n")}},
		"escaping path":  {good[0], {Path: "../evil"}},
		"absolute path":  {good[0], {Path: "/etc/passwd"}},
		"too large":      {good[0], {Path: "big", Content: make([]byte, MaxFileBytes+1)}},
	} {
		if _, err := Check("x", files); err == nil {
			t.Errorf("%s: Check passed", name)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillMD(name, desc string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\nDo it.\n", name, desc)
}

func TestCollectFindsEverySkillOfAFolder(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "skills", "pdf", "SKILL.md"), skillMD("pdf", "PDFs."))
	write(t, filepath.Join(root, "skills", "pdf", "scripts", "fill.py"), "print()")
	// A skill's own subfolders aren't more skills.
	write(t, filepath.Join(root, "skills", "pdf", "nested", "SKILL.md"), skillMD("nested", "no"))
	write(t, filepath.Join(root, "skills", "Bad_Name", "SKILL.md"), "---\ndescription: d\n---\n")
	write(t, filepath.Join(root, "skills", "nodesc", "SKILL.md"), "---\nname: nodesc\n---\n")
	write(t, filepath.Join(root, "node_modules", "x", "SKILL.md"), skillMD("x", "no"))
	if err := os.Symlink("/etc/hostname", filepath.Join(root, "skills", "pdf", "link")); err != nil {
		t.Fatal(err)
	}

	found, err := Collect(root, "folder", "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range found {
		names = append(names, c.Name+"|"+c.Problem)
	}
	if got := strings.Join(names, ","); got != "bad-name|,nodesc|its SKILL.md has no description,pdf|" {
		t.Fatalf("Collect() = %s", got)
	}
	pdf := found[2]
	if len(pdf.Files) != 3 { // SKILL.md, scripts/fill.py, nested/SKILL.md; not the symlink
		t.Errorf("pdf's files = %+v", pdf.Files)
	}
	files, meta, err := found[0].Prepare("bad-name")
	if err != nil || meta.Name != "bad-name" || !bytes.Contains(files[0].Content, []byte("name: bad-name")) {
		t.Errorf("Prepare() = %v, %+v, %v", files, meta, err)
	}
	if _, _, err := found[1].Prepare("nodesc"); err == nil {
		t.Error("a candidate with a problem was prepared")
	}

	// One skill's folder, or its SKILL.md, is that skill.
	for _, p := range []string{filepath.Join(root, "skills", "pdf"), filepath.Join(root, "skills", "pdf", "SKILL.md")} {
		if found, err := Collect(p, "folder", ""); err != nil || len(found) != 1 || found[0].Name != "pdf" {
			t.Errorf("Collect(%s) = %+v, %v", p, found, err)
		}
	}
}

func TestDiscoverReadsTheAIToolsOwnSkills(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude", "skills", "mine", "SKILL.md"), skillMD("mine", "Mine."))
	write(t, filepath.Join(home, ".codex", "skills", ".system", "bundled", "SKILL.md"), skillMD("bundled", "Codex's own."))
	write(t, filepath.Join(home, ".codex", "skills", "codexy", "SKILL.md"), skillMD("codexy", "Codex."))
	write(t, filepath.Join(home, ".config", "opencode", "skills", "oc", "SKILL.md"), skillMD("oc", "OpenCode."))
	write(t, filepath.Join(home, ".cursor", "skills", "cur", "SKILL.md"), skillMD("cur", "Cursor."))
	// The same skill linked into ~/.agents/skills is listed once.
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".claude", "skills", "mine"), filepath.Join(home, ".agents", "skills", "mine")); err != nil {
		t.Fatal(err)
	}
	install := filepath.Join(home, ".claude", "plugins", "cache", "mkt", "toolkit", "1.0.0")
	write(t, filepath.Join(install, "skills", "plugged", "SKILL.md"), skillMD("plugged", "From a plugin."))
	write(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		fmt.Sprintf(`{"version":2,"plugins":{"toolkit@mkt":[{"scope":"user","installPath":%q}]}}`, install))

	var got []string
	for _, c := range Discover([]string{home, home}) {
		got = append(got, c.Origin+":"+c.Name+":"+c.Plugin+":"+c.Source)
	}
	want := "claude:mine::claude:mine,codex:codexy::codex:codexy,opencode:oc::opencode:oc,cursor:cur::cursor:cur,claude-plugin:plugged:toolkit:claude-plugin:toolkit/plugged"
	if strings.Join(got, ",") != want {
		t.Errorf("Discover() =\n%s\nwant\n%s", strings.Join(got, ","), want)
	}
}

func TestGitSourceUnderstandsGitHubLinks(t *testing.T) {
	for in, want := range map[string][3]string{
		"https://github.com/anthropics/skills":                      {"https://github.com/anthropics/skills", "", ""},
		"https://github.com/anthropics/skills.git":                  {"https://github.com/anthropics/skills", "", ""},
		"https://github.com/anthropics/skills/tree/main/skills/pdf": {"https://github.com/anthropics/skills", "main", "skills/pdf"},
		"https://github.com/anthropics/skills/blob/v1/pdf/SKILL.md": {"https://github.com/anthropics/skills", "v1", "pdf"},
		"github.com/o/r#skills/deploy":                              {"https://github.com/o/r", "", "skills/deploy"},
		"git@gitlab.com:team/skills.git#review":                     {"git@gitlab.com:team/skills.git", "", "review"},
	} {
		repo, ref, sub := gitSource(in)
		if [3]string{repo, ref, sub} != want {
			t.Errorf("gitSource(%q) = %q, %q, %q; want %q", in, repo, ref, sub, want)
		}
	}
}

func TestScanClonesAGitRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	write(t, filepath.Join(repo, "skills", "one", "SKILL.md"), skillMD("one", "One."))
	write(t, filepath.Join(repo, "skills", "two", "SKILL.md"), skillMD("two", "Two."))
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "skills"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	found, err := Scan(context.Background(), "file://"+repo, nil)
	if err == nil {
		t.Fatalf("a file:// URL isn't a git source here, got %+v", found)
	}
	// Clone takes any URL git does; Scan's IsGitURL is the gate.
	dir, cleanup, err := Clone(context.Background(), "file://"+repo+"#skills/two")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	got, err := Collect(dir, "git", "")
	if err != nil || len(got) != 1 || got[0].Name != "two" {
		t.Errorf("Collect(clone#skills/two) = %+v, %v", got, err)
	}
}

func installed(n string) Installed {
	return Installed{Name: n, Files: []state.SkillFile{
		{Path: "SKILL.md", Mode: 0o644, Content: []byte(skillMD(n, "d"))},
		{Path: "scripts/run.sh", Mode: 0o755, Content: []byte("echo")},
	}}
}

// checkInstall runs one install twice, the second with a skill fewer, and
// checks that the skill dropped leaves and the agent's own skill stays.
func checkInstall(t *testing.T, home string, install func([]Installed) error) {
	t.Helper()
	write(t, filepath.Join(home, ".claude", "skills", "own", "SKILL.md"), skillMD("own", "The agent's own."))
	if err := install([]Installed{installed("one"), installed("two")}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range InstallDirs {
		for _, n := range []string{"one", "two"} {
			if _, err := os.Stat(filepath.Join(home, dir, n, "scripts", "run.sh")); err != nil {
				t.Errorf("%s/%s wasn't installed: %v", dir, n, err)
			}
		}
	}
	if info, err := os.Stat(filepath.Join(home, ".claude", "skills", "one", "scripts", "run.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("run.sh lost its mode: %v %v", info, err)
	}
	if err := install([]Installed{installed("two")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "one")); err == nil {
		t.Error("a skill no longer given stayed")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "own", "SKILL.md")); err != nil {
		t.Error("the agent's own skill was removed")
	}
	if data, _ := os.ReadFile(filepath.Join(home, filepath.FromSlash(Manifest))); string(data) != "two\n" {
		t.Errorf("manifest = %q", data)
	}
	if err := install(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "two")); err == nil {
		t.Error("an install of nothing left a skill")
	}
}

func TestInstallLocal(t *testing.T) {
	home := t.TempDir()
	checkInstall(t, home, func(list []Installed) error { return InstallLocal(home, list) })
}

// TestInstallScript runs what goes into an agent, on this machine.
func TestInstallScript(t *testing.T) {
	home := t.TempDir()
	owner := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	checkInstall(t, home, func(list []Installed) error {
		archive, err := Tar(list)
		if err != nil {
			return err
		}
		cmd := exec.Command("sh", "-c", InstallScript, "sh", home, owner)
		cmd.Stdin = bytes.NewReader(archive)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%v: %s", err, out)
		}
		return nil
	})
}
