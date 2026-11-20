package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureIsPinned(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	commit, err := writeFixture(filepath.Join(t.TempDir(), "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if commit != fixtureCommit {
		t.Fatalf("the fixture is commit %s now, not the pinned %s: if the change was meant, set fixtureCommit to it (results from before aren't comparable with after)", commit, fixtureCommit)
	}
}

func TestEnsureFixture(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := filepath.Join(t.TempDir(), "fixture")
	if _, err := ensureFixture(dir); err != nil {
		t.Fatal(err)
	}
	// A run left a change behind: it is written again.
	if err := os.WriteFile(filepath.Join(dir, "stats", "stats.go"), []byte("package stats\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureFixture(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "stats", "stats.go")); !strings.Contains(string(b), "func Median") {
		t.Error("the changed fixture wasn't written again")
	}
	// Anything else in the way is left alone.
	other := filepath.Join(t.TempDir(), "mine")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureFixture(other); err == nil || !strings.Contains(err.Error(), "in the way") {
		t.Errorf("ensureFixture over someone's directory: %v", err)
	}
}

// The fixture's Go code: the stats test fails on the seeded bug and passes
// with it fixed, and the rest builds and passes. Only a few packages, to keep
// it quick; check.sh builds them all.
func TestFixtureCode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the fixture")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go")
	}
	dir := filepath.Join(t.TempDir(), "fixture")
	if _, err := writeFixture(dir); err != nil {
		t.Fatal(err)
	}
	gotest := func(pkgs ...string) (string, error) {
		cmd := exec.Command("go", append([]string{"test", "-count=1"}, pkgs...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := gotest("./strutil", "./gen/p00", "./cmd/tool0"); err != nil {
		t.Fatalf("the fixture doesn't pass where it should: %v\n%s", err, out)
	}
	out, err := gotest("./stats")
	if err == nil || !strings.Contains(out, "Median([4 1 3 2]) = 3, want 2.5") {
		t.Fatalf("the stats test should fail on the seeded bug: %v\n%s", err, out)
	}
	p := filepath.Join(dir, "stats", "stats.go")
	b, _ := os.ReadFile(p)
	fixed := strings.Replace(string(b), "(s[m-1] + s[m+1]) / 2", "(s[m-1] + s[m]) / 2", 1)
	if fixed == string(b) {
		t.Fatal("the seeded bug isn't where the test expects it")
	}
	if err := os.WriteFile(p, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := gotest("./stats"); err != nil {
		t.Fatalf("the fix doesn't pass: %v\n%s", err, out)
	}
}
