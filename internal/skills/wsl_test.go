package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeMnt stands a temporary folder in for WSL's /mnt, with C:\Users\Ana in
// it, and returns Ana's home there.
func fakeMnt(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	old := mnt
	mnt = root
	t.Cleanup(func() { mnt = old })
	home := filepath.Join(root, "c", "Users", "Ana")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverReadsTheWindowsHomeFromWSL(t *testing.T) {
	win := fakeMnt(t)
	linux := t.TempDir()

	// npx skills add on Windows: the skill in ~/.agents/skills, and a
	// junction to it from ~/.claude/skills whose target, if WSL doesn't
	// translate it, is a Windows path. Written there, with CRLF, and every file
	// 0777 the way drvfs shows them.
	skill := filepath.Join(win, ".agents", "skills", "win-skill")
	write(t, filepath.Join(skill, "SKILL.md"), "---\r\nname: win-skill\r\ndescription: >\r\n  From Windows,\r\n  with CRLF.\r\n---\r\nDo it.\r\n")
	write(t, filepath.Join(skill, "scripts", "run.sh"), "#!/bin/sh\r\necho hi\r\n")
	write(t, filepath.Join(skill, "notes.md"), "notes")
	for _, f := range []string{"SKILL.md", "scripts/run.sh", "notes.md"} {
		if err := os.Chmod(filepath.Join(skill, f), 0o777); err != nil {
			t.Fatal(err)
		}
	}
	symlink(t, `C:\Users\Ana\.agents\skills\win-skill`, filepath.Join(win, ".claude", "skills", "win-skill"))
	// And where it couldn't link, a copy: listed once too.
	write(t, filepath.Join(win, ".agents", "skills", "copied", "SKILL.md"), skillMD("copied", "Copied."))
	write(t, filepath.Join(win, ".claude", "skills", "copied", "SKILL.md"), skillMD("copied", "Copied."))
	// Claude Code on Windows keeps its plugins' Windows paths.
	write(t, filepath.Join(win, ".claude", "plugins", "cache", "mkt", "tk", "1.0.0", "skills", "plugged", "SKILL.md"), skillMD("plugged", "Plugged."))
	write(t, filepath.Join(win, ".claude", "plugins", "installed_plugins.json"),
		fmt.Sprintf(`{"version":2,"plugins":{"tk@mkt":[{"installPath":%q}]}}`, `C:\Users\Ana\.claude\plugins\cache\mkt\tk\1.0.0`))
	// The same skill in both homes is one; a different one of a name isn't.
	write(t, filepath.Join(linux, ".claude", "skills", "copied", "SKILL.md"), skillMD("copied", "Copied."))
	write(t, filepath.Join(linux, ".claude", "skills", "win-skill", "SKILL.md"), skillMD("win-skill", "Linux's own."))
	// A home linked to the Windows one's folder is read once.
	symlink(t, filepath.Join(win, ".agents", "skills"), filepath.Join(linux, ".agents", "skills"))

	var got []string
	var winSkill Candidate
	for _, c := range Discover([]string{win, linux}) {
		got = append(got, c.Origin+":"+c.Name+":"+c.Problem)
		if c.Name == "win-skill" && winSkill.Dir == "" {
			winSkill = c
		}
	}
	want := "claude:copied:,claude:win-skill:,claude-plugin:plugged:,claude:win-skill:"
	if strings.Join(got, ",") != want {
		t.Fatalf("Discover() =\n%s\nwant\n%s", strings.Join(got, ","), want)
	}
	if winSkill.Description != "From Windows, with CRLF." || winSkill.Dir != filepath.Join(win, ".claude", "skills", "win-skill") {
		t.Errorf("the junction's skill = %+v", winSkill)
	}
	modes := map[string]uint32{}
	for _, f := range winSkill.Files {
		modes[f.Path] = f.Mode
	}
	if modes["SKILL.md"] != 0o644 || modes["notes.md"] != 0o644 || modes["scripts/run.sh"] != 0o755 {
		t.Errorf("modes = %v, want 0644 but for the script", modes)
	}
	files, meta, err := winSkill.Prepare("win-skill")
	if err != nil || meta.Description != "From Windows, with CRLF." || !strings.HasPrefix(string(files[0].Content), "---\r\nname: win-skill\r\ndescription") {
		t.Errorf("Prepare() = %q, %+v, %v", files[0].Content, meta, err)
	}
}

func TestFileModes(t *testing.T) {
	for _, tc := range []struct {
		perm    os.FileMode
		content string
		want    uint32
	}{
		{0o644, "x", 0o644},
		{0o666, "x", 0o644},
		{0o755, "x", 0o755},
		{0o700, "x", 0o755},
		{0o777, "x", 0o644},
		{0o777, "#!/usr/bin/env python3\n", 0o755},
	} {
		if got := fileMode(tc.perm, []byte(tc.content)); got != tc.want {
			t.Errorf("fileMode(%o, %q) = %o, want %o", tc.perm, tc.content, got, tc.want)
		}
	}
}

func TestScanTakesWindowsPathsInWSL(t *testing.T) {
	win := fakeMnt(t)
	write(t, filepath.Join(win, "skills", "pdf", "SKILL.md"), skillMD("pdf", "PDFs."))
	old := windowsPaths
	t.Cleanup(func() { windowsPaths = old })
	t.Setenv("WSL_DISTRO_NAME", "AgentBox")

	windowsPaths = func() bool { return true }
	for _, src := range []string{`C:\Users\Ana\skills`, `"C:\Users\Ana\skills\pdf"`, `c:/Users/Ana/skills/pdf/SKILL.md`} {
		found, err := Scan(context.Background(), src, nil)
		if err != nil || len(found) != 1 || found[0].Name != "pdf" {
			t.Errorf("Scan(%s) = %+v, %v", src, found, err)
		}
	}
	if _, err := Scan(context.Background(), `\\wsl.localhost\Ubuntu\home\ana\skills`, nil); err == nil || !strings.Contains(err.Error(), "WSL can see") {
		t.Errorf("another distro's folder: %v", err)
	}

	windowsPaths = func() bool { return false }
	if _, err := Scan(context.Background(), `C:\Users\Ana\skills`, nil); err == nil || !strings.Contains(err.Error(), "isn't a full path") {
		t.Errorf("a Windows path outside WSL: %v", err)
	}
}

func TestParseAndWithNameKeepCRLF(t *testing.T) {
	content := "\ufeff---\r\ndescription: |\r\n  One.\r\n  Two.\r\nuser-invocable: false\r\n---\r\n# Body\r\n"
	meta, body, err := Parse(content)
	if err != nil || meta.Description != "One.\nTwo." || meta.UserInvocable || body != "# Body\r\n" {
		t.Errorf("Parse() = %+v, %q, %v", meta, body, err)
	}
	if got := WithName(content, "x"); got != "---\r\nname: x\r\ndescription: |\r\n  One.\r\n  Two.\r\nuser-invocable: false\r\n---\r\n# Body\r\n" {
		t.Errorf("WithName(no name) = %q", got)
	}
	if got := WithName("---\r\nname: old\r\ndescription: d\r\n---\r\n", "new"); got != "---\r\nname: new\r\ndescription: d\r\n---\r\n" {
		t.Errorf("WithName(a name) = %q", got)
	}
}
