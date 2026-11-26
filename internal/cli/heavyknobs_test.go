package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agentbox/internal/api"
)

func writeTestFile(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

// A daemon that counted memory has the command start with the worker
// settings only that sets: Gradle's in GRADLE_OPTS, turbo's at 2 at least,
// and jest's on its command line; one that didn't, with exactly what it
// always had.
func TestRunHeavyWithMemoryCounted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	// A jest that says what it was run with.
	jest := filepath.Join(dir, "bin", "jest")
	writeTestFile(t, jest, "#!/bin/sh\necho \"$GRADLE_OPTS|$TURBO_CONCURRENCY|$GOMAXPROCS|$*\" > "+out+"\n", 0o755)
	run := func(count *api.HeavyWorkers) string {
		t.Helper()
		f := &fakeHeavy{start: true, workers: 1, count: count}
		var stderr bytes.Buffer
		if code, err := runHeavy(context.Background(), f, []string{jest, "--ci"}, &stderr); code != 0 || err != nil {
			t.Fatalf("exit %d, %v: %s", code, err, stderr.String())
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	maxWorkers := ""
	if runtime.NumCPU() > 2 { // jest starts a worker per core but one
		maxWorkers = " --maxWorkers=1"
	}
	if got, want := run(&api.HeavyWorkers{Workers: 1, Memory: true, Limit: "memory"}), "-Dorg.gradle.workers.max=1|2|1|--ci"+maxWorkers; got != want {
		t.Errorf("memory counted: %q, want %q", got, want)
	}
	if got, want := run(&api.HeavyWorkers{Workers: 1, Limit: "cpu"}), "|1|1|--ci"; got != want {
		t.Errorf("memory not counted: %q, want %q", got, want)
	}
	if got, want := run(nil), "|1|1|--ci"; got != want {
		t.Errorf("a daemon that doesn't say: %q, want %q", got, want)
	}
}

// A gradle.properties in the build or Gradle's home that sets the worker
// count keeps it.
func TestGradleSetsWorkers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	build, home := filepath.Join(root, "app"), filepath.Join(root, "home")
	sub := filepath.Join(build, "module", "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if gradleSetsWorkers(sub, "", home) {
		t.Error("nothing sets it")
	}
	writeTestFile(t, filepath.Join(build, "gradle.properties"), "# org.gradle.workers.max=2\norg.gradle.jvmargs=-Xmx2g\n", 0o644)
	if gradleSetsWorkers(sub, "", home) {
		t.Error("a comment sets it")
	}
	writeTestFile(t, filepath.Join(home, ".gradle", "gradle.properties"), "org.gradle.workers.max : 3\n", 0o644)
	if !gradleSetsWorkers(sub, "", home) {
		t.Error("~/.gradle/gradle.properties sets it")
	}
	if gradleSetsWorkers(sub, filepath.Join(root, "elsewhere"), home) {
		t.Error("GRADLE_USER_HOME is elsewhere: ~/.gradle isn't read")
	}
	writeTestFile(t, filepath.Join(build, "gradle.properties"), "org.gradle.workers.max=2\n", 0o644)
	if !gradleSetsWorkers(sub, filepath.Join(root, "elsewhere"), home) {
		t.Error("the build's gradle.properties sets it")
	}
}

// The tsc a command runs is TypeScript 7's when the package it is in says
// so, or, run through npx, the one the project's node_modules holds.
func TestTypeScriptMajor(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	project := filepath.Join(root, "web")
	pkg := filepath.Join(project, "node_modules", "typescript")
	writeTestFile(t, filepath.Join(pkg, "package.json"), `{"name":"typescript","version":"7.0.2"}`, 0o644)
	writeTestFile(t, filepath.Join(pkg, "bin", "tsc"), "#!/bin/sh\n", 0o755)
	bin := filepath.Join(project, "node_modules", ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../typescript/bin/tsc", filepath.Join(bin, "tsc")); err != nil {
		t.Fatal(err)
	}
	if got := typeScriptMajor(filepath.Join(bin, "tsc"), root); got != 7 {
		t.Errorf("tsc through its link: %d, want 7", got)
	}
	if got := typeScriptMajor("/usr/bin/npx", filepath.Join(project, "src")); got != 7 {
		t.Errorf("npx tsc in the project: %d, want 7", got)
	}
	old := filepath.Join(root, "old")
	writeTestFile(t, filepath.Join(old, "node_modules", "typescript", "package.json"), `{"name":"typescript","version":"5.9.3"}`, 0o644)
	if got := typeScriptMajor("/usr/bin/npx", old); got != 5 {
		t.Errorf("TypeScript 5: %d", got)
	}
	if got := typeScriptMajor("/usr/bin/npx", t.TempDir()); got != 0 {
		t.Errorf("no TypeScript: %d", got)
	}
}
