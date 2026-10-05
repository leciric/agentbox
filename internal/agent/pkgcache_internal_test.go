package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/image"
)

// inSandbox is a script with the agent's paths moved under dir, so it runs
// here as it would in the agent.
func inSandbox(script, dir string) string {
	return strings.NewReplacer(PackageCachePath, filepath.Join(dir, "cache"), "/home/dev", filepath.Join(dir, "home")).Replace(script)
}

func TestPackageCacheEnvNamesTheCachesOnlyWhenTheyreThere(t *testing.T) {
	t.Parallel()
	m := &Manager{User: image.User{Name: "dev", UID: os.Getuid(), GID: os.Getgid()}}
	dir := t.TempDir()
	// The shell gets a controlled environment, never the test's own: an agent
	// has npm_config_*, pnpm_config_* and BASH_ENV set, which would leak in.
	env := func() map[string]string {
		t.Helper()
		cmd := exec.Command("sh", "-c", inSandbox(m.packageCacheEnv(), dir)+"env")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(dir, "home")}
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		vars := map[string]string{}
		for _, line := range strings.Split(string(out), "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				vars[k] = v
			}
		}
		return vars
	}
	if got := env(); got["npm_config_cache"] != "" {
		t.Errorf("caches named with no directory: npm_config_cache=%q", got["npm_config_cache"])
	}
	if err := os.Mkdir(filepath.Join(dir, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := env()
	for k, want := range map[string]string{
		"npm_config_cache":           dir + "/cache/npm",
		"npm_config_logs_dir":        dir + "/home/.npm/_logs",
		"pnpm_config_store_dir":      dir + "/cache/pnpm/store",
		"GOMODCACHE":                 dir + "/cache/go/mod",
		"GOCACHE":                    dir + "/cache/go/build",
		"PIP_CACHE_DIR":              dir + "/cache/pip",
		"UV_CACHE_DIR":               dir + "/cache/uv",
		"PLAYWRIGHT_BROWSERS_PATH":   dir + "/cache/ms-playwright",
		"PLAYWRIGHT_SKIP_BROWSER_GC": "1",
		"COREPACK_HOME":              dir + "/cache/corepack",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	// Yarn 1 would take YARN_GLOBAL_FOLDER for where `yarn global` installs.
	for k := range got {
		if strings.HasPrefix(k, "YARN_") {
			t.Errorf("a Yarn variable is set: %s", k)
		}
	}
}

func TestPackageCacheSetupPointsPnpmAndYarnAtTheCachesOnce(t *testing.T) {
	t.Parallel()
	m := &Manager{User: image.User{Name: "dev", UID: os.Getuid(), GID: os.Getgid()}}
	dir := t.TempDir()
	yarnrc := filepath.Join(dir, "home", ".yarnrc.yml")
	if err := os.MkdirAll(filepath.Dir(yarnrc), 0o755); err != nil {
		t.Fatal(err)
	}
	// A setting of the user's own is kept, and wins.
	if err := os.WriteFile(yarnrc, []byte("globalFolder: /mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := inSandbox(m.packageCacheSetup(), dir)
	for range 2 {
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(dir, "home")}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	rc, err := os.ReadFile(filepath.Join(dir, "home", ".config", "pnpm", "rc"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "store-dir=" + dir + "/cache/pnpm/store\ncache-dir=" + dir + "/cache/pnpm/cache\n"; string(rc) != want {
		t.Errorf("pnpm's rc = %q, want %q", rc, want)
	}
	if got, _ := os.ReadFile(yarnrc); string(got) != "globalFolder: /mine\n" {
		t.Errorf(".yarnrc.yml = %q", got)
	}
	if info, err := os.Stat(filepath.Join(dir, "cache")); err != nil || !info.IsDir() {
		t.Errorf("the caches' directory wasn't made: %v", err)
	}
}
