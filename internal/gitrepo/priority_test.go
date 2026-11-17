package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestLowPriorityWrapsGit(t *testing.T) {
	nice, ionice := lowPriorityTools()
	name, args := lowPriority("git", []string{"-C", "/w", "add", "--all"})
	want := []string{"-C", "/w", "add", "--all"}
	wantName := "git"
	if ionice != "" {
		want = append([]string{"-c", "2", "-n", "7", "git"}, want...)
		wantName = ionice
	}
	if nice != "" {
		want = append([]string{"-n", "19", wantName}, want...)
		wantName = nice
	}
	if name != wantName || !slices.Equal(args, want) {
		t.Errorf("lowPriority = %s %v, want %s %v", name, args, wantName, want)
	}
}

func TestMeasureWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "base"}} {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	base, err := run(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if stat, dirty, err := MeasureWorktree(dir, base); err != nil || stat != "" || dirty {
		t.Errorf("a clean worktree: %q, %v, %v", stat, dirty, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stat, dirty, err := MeasureWorktree(dir, base)
	if err != nil || !dirty || stat == "" {
		t.Errorf("an untracked file: %q, %v, %v", stat, dirty, err)
	}
}
