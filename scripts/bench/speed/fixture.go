package main

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// fixtureCommit is the one commit of the fixture repository writeFixture
// makes. Every file and the commit's author, date and message are fixed, so
// the hash is the same on every machine; a change to the fixture changes it,
// and TestFixtureIsPinned says to what. Results are only comparable between
// runs on the same commit, which is why the report carries it.
const fixtureCommit = "eea4fe55fbd2600a18e2b2a77238502278d0c75b"

// The generated part of the fixture: packages gen/p00… with genFuncs
// functions each, and genTools programs under cmd/ that link them. Tuned so
// that a cold ./check.sh takes about a minute of CPU, most of it spread over
// as many cores as the machine gives it: compiling, linking a test binary per
// package and running the tests.
const (
	genPackages = 48
	genFuncs    = 96
	genRounds   = 1800
	genTools    = 4
)

//go:embed all:testdata/fixture
var fixtureFiles embed.FS

// gitEnv isolates git from the user's own configuration, whose hooks,
// templates or attributes could change what is committed and so the hash.
func gitEnv(extra ...string) []string {
	env := append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	return append(env, extra...)
}

func git(dir string, extra []string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=" + os.DevNull}, args...)...)
	cmd.Dir = dir
	cmd.Env = gitEnv(extra...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// fixtureHead is the commit dir's repository is on, or "" when dir isn't one.
func fixtureHead(dir string) string {
	head, err := git(dir, nil, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return head
}

// ensureFixture makes sure dir holds the pinned fixture repository, writing
// it when it isn't there or isn't on the pinned commit, and returns the
// commit. It only replaces a directory that holds an earlier fixture, never
// anything else.
func ensureFixture(dir string) (string, error) {
	if head := fixtureHead(dir); head == fixtureCommit {
		if status, err := git(dir, nil, "status", "--porcelain"); err == nil && status == "" {
			return head, nil
		}
	}
	if _, err := os.Stat(dir); err == nil {
		if _, err := os.Stat(filepath.Join(dir, "bench", "lib.sh")); err != nil {
			return "", fmt.Errorf("%s is in the way: it isn't a fixture this benchmark wrote, so it is left alone", dir)
		}
		if err := os.RemoveAll(dir); err != nil {
			return "", err
		}
	}
	commit, err := writeFixture(dir)
	if err != nil {
		return "", err
	}
	if commit != fixtureCommit {
		return commit, fmt.Errorf("the fixture came out as commit %s, not the pinned %s: its files changed (update fixtureCommit if that was meant), or this git commits differently", commit, fixtureCommit)
	}
	return commit, nil
}

// writeFixture writes the fixture repository into dir, which must not exist,
// and commits it.
func writeFixture(dir string) (string, error) {
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("%s exists already", dir)
	}
	files := map[string]string{}
	err := fs.WalkDir(fixtureFiles, "testdata/fixture", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fixtureFiles.ReadFile(p)
		files[strings.TrimPrefix(p, "testdata/fixture/")] = string(b)
		return err
	})
	if err != nil {
		return "", err
	}
	for name, body := range generatedFiles() {
		if _, ok := files[name]; ok {
			return "", fmt.Errorf("generated %s clashes with an embedded file", name)
		}
		files[name] = body
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		mode := os.FileMode(0o644)
		if path.Ext(name) == ".sh" {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			return "", err
		}
		if err := os.Chmod(p, mode); err != nil { // past the umask
			return "", err
		}
	}
	id := []string{
		"GIT_AUTHOR_NAME=AgentBox speed benchmark", "GIT_AUTHOR_EMAIL=bench@agentbox.invalid",
		"GIT_AUTHOR_DATE=2026-11-20T00:00:00Z",
		"GIT_COMMITTER_NAME=AgentBox speed benchmark", "GIT_COMMITTER_EMAIL=bench@agentbox.invalid",
		"GIT_COMMITTER_DATE=2026-11-20T00:00:00Z",
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"commit", "-q", "--no-gpg-sign", "-m", "benchfix: the speed benchmark's fixture"},
	} {
		if _, err := git(dir, id, args...); err != nil {
			return "", err
		}
	}
	head := fixtureHead(dir)
	if head == "" {
		return "", errors.New("the fixture's commit didn't take")
	}
	return head, nil
}

// generatedFiles is the generated part of the fixture, by path.
func generatedFiles() map[string]string {
	files := map[string]string{
		"go.mod":     "module benchfix\n\ngo 1.22\n",
		".gitignore": "/.bench/\n/out/\n",
	}
	for p := range genPackages {
		name := fmt.Sprintf("p%02d", p)
		files["gen/"+name+"/"+name+".go"] = genPackage(p)
		files["gen/"+name+"/"+name+"_test.go"] = genTest(p)
	}
	per := genPackages / genTools
	for t := range genTools {
		var b strings.Builder
		fmt.Fprintf(&b, "// Code generated for AgentBox's speed benchmark. DO NOT EDIT.\n\n")
		fmt.Fprintf(&b, "// Command tool%d prints what the generated packages it links make of a few numbers.\npackage main\n\nimport (\n\t\"fmt\"\n\n", t)
		for p := t * per; p < (t+1)*per; p++ {
			fmt.Fprintf(&b, "\t\"benchfix/gen/p%02d\"\n", p)
		}
		fmt.Fprintf(&b, ")\n\nfunc main() {\n\txs := []uint64{1, 2, 3, 5, 8, 13}\n\tvar h uint64\n")
		for p := t * per; p < (t+1)*per; p++ {
			fmt.Fprintf(&b, "\th = p%02d.All(h, xs)\n", p)
		}
		fmt.Fprintf(&b, "\tfmt.Printf(\"%%x\\n\", h)\n}\n")
		files[fmt.Sprintf("cmd/tool%d/main.go", t)] = b.String()
	}
	return files
}

// genConst is a constant for function f of package p, different for each.
func genConst(p, f, k int) uint64 {
	x := uint64(p*1_000_003+f*7_919+k*104_729) * 0x9e3779b97f4a7c15
	return x ^ x>>31
}

func genPackage(p int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated for AgentBox's speed benchmark. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "// Package p%02d mixes numbers through generated functions, for the build to\n// chew on.\npackage p%02d\n\nimport \"benchfix/stats\"\n\n", p, p)
	for f := range genFuncs {
		fmt.Fprintf(&b, "// F%03d mixes xs into h.\nfunc F%03d(h uint64, xs []uint64) uint64 {\n", f, f)
		fmt.Fprintf(&b, "\tfor i, x := range xs {\n\t\tswitch (x ^ uint64(i)) %% 5 {\n")
		fmt.Fprintf(&b, "\t\tcase 0:\n\t\t\th = h*0x100000001b3 + x + %#x\n", genConst(p, f, 0))
		fmt.Fprintf(&b, "\t\tcase 1:\n\t\t\th ^= (x<<13 | x>>51) + %#x\n", genConst(p, f, 1))
		fmt.Fprintf(&b, "\t\tcase 2:\n\t\t\th = (h + x*%#x) ^ (h >> 29)\n", genConst(p, f, 2)|1)
		fmt.Fprintf(&b, "\t\tcase 3:\n\t\t\th = (h<<7 | h>>57) ^ %#x\n", genConst(p, f, 3))
		fmt.Fprintf(&b, "\t\tdefault:\n\t\t\th -= x ^ %#x\n\t\t}\n\t}\n\treturn h\n}\n\n", genConst(p, f, 4))
	}
	fmt.Fprintf(&b, "// All runs every function of the package over xs, starting from h.\nfunc All(h uint64, xs []uint64) uint64 {\n")
	for f := range genFuncs {
		fmt.Fprintf(&b, "\th = F%03d(h, xs)\n", f)
	}
	fmt.Fprintf(&b, "\treturn h\n}\n\n")
	fmt.Fprintf(&b, "// Scale is the mean of xs, each taken modulo 1000.\nfunc Scale(xs []uint64) float64 {\n\tf := make([]float64, len(xs))\n\tfor i, x := range xs {\n\t\tf[i] = float64(x %% 1000)\n\t}\n\treturn stats.Mean(f)\n}\n")
	return b.String()
}

func genTest(p int) string {
	return fmt.Sprintf(`// Code generated for AgentBox's speed benchmark. DO NOT EDIT.

package p%02d

import "testing"

func TestAll(t *testing.T) {
	xs := make([]uint64, 1024)
	for i := range xs {
		xs[i] = uint64(i) * 0x9e3779b97f4a7c15
	}
	var a, b uint64
	for range %d {
		a = All(a, xs)
	}
	for range %d {
		b = All(b, xs)
	}
	if a != b {
		t.Fatalf("All isn't deterministic: %%x, then %%x", a, b)
	}
	if s := Scale(xs); s < 0 || s >= 1000 {
		t.Fatalf("Scale = %%v, want it in [0, 1000)", s)
	}
}
`, p, genRounds/2, genRounds/2)
}
