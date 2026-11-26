package cli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"agentbox/internal/agent"
)

// What a heavy command's worker counts need of the machine it runs in when
// the daemon counted memory (api.HeavyWorkers.Memory): whether a
// gradle.properties sets Gradle's count already, and whether its tsc is
// TypeScript 7's, the first to take --checkers.

// memoryKnobs is env and argv with the worker counts for n workers set the
// way a daemon that counted memory asks (agent.MemoryWorkerEnv,
// agent.WorkerArgs), in dir.
func memoryKnobs(env, argv []string, path, dir string, n int) ([]string, []string) {
	env = agent.MemoryWorkerEnv(env, n, gradleSetsWorkers(dir, envValue(env, "GRADLE_USER_HOME"), envValue(env, "HOME")))
	tsc7 := false
	if agent.ArgsTool(argv) == "tsc" {
		tsc7 = typeScriptMajor(path, dir) >= 7
	}
	return env, agent.WorkerArgs(argv, n, runtime.NumCPU(), tsc7)
}

func envValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == key {
			return v
		}
	}
	return ""
}

// gradleWorkersKey is the gradle.properties key for Gradle's worker count.
const gradleWorkersKey = "org.gradle.workers.max"

// gradleSetsWorkers reports whether a gradle.properties Gradle reads sets
// its worker count: in dir or a directory above it (the build's root), or in
// Gradle's user home (gradleHome, else ~/.gradle).
func gradleSetsWorkers(dir, gradleHome, home string) bool {
	if gradleHome == "" && home != "" {
		gradleHome = filepath.Join(home, ".gradle")
	}
	if gradleHome != "" && propertiesSet(filepath.Join(gradleHome, "gradle.properties"), gradleWorkersKey) {
		return true
	}
	for d := dir; d != ""; {
		if propertiesSet(filepath.Join(d, "gradle.properties"), gradleWorkersKey) {
			return true
		}
		up := filepath.Dir(d)
		if up == d {
			break
		}
		d = up
	}
	return false
}

// propertiesSet reports whether a .properties file sets key.
func propertiesSet(path, key string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		k, _, ok := strings.Cut(line, "=")
		if !ok {
			k, _, ok = strings.Cut(line, ":")
		}
		if ok && strings.TrimSpace(k) == key {
			return true
		}
	}
	return false
}

// typeScriptMajor is the major version of the TypeScript whose tsc a command
// runs: the package path (tsc itself, past its links) is in, else the one
// dir's node_modules or a directory's above it holds, which npx and its kind
// run. 0 when neither says.
func typeScriptMajor(path, dir string) int {
	if filepath.Base(path) == "tsc" {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			for d := filepath.Dir(real); ; {
				if v, ok := packageVersion(filepath.Join(d, "package.json"), "typescript"); ok {
					return v
				}
				up := filepath.Dir(d)
				if up == d || filepath.Base(d) == "node_modules" {
					break
				}
				d = up
			}
		}
	}
	for d := dir; d != ""; {
		if v, ok := packageVersion(filepath.Join(d, "node_modules", "typescript", "package.json"), "typescript"); ok {
			return v
		}
		up := filepath.Dir(d)
		if up == d {
			break
		}
		d = up
	}
	return 0
}

// packageVersion is the major version in a package.json, if it is name's.
func packageVersion(path, name string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &pkg) != nil || pkg.Name != name {
		return 0, false
	}
	major, _, _ := strings.Cut(pkg.Version, ".")
	n, err := strconv.Atoi(major)
	return n, err == nil
}
