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
	env := func() string {
		t.Helper()
		out, err := exec.Command("sh", "-c", inSandbox(m.packageCacheEnv(), dir)+"env").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	if got := env(); strings.Contains(got, "npm_config_cache") {
		t.Errorf("caches named with no directory:\n%s", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := env()
	for _, want := range []string{
		"npm_config_cache=" + dir + "/cache/npm\n",
		"npm_config_logs_dir=" + dir + "/home/.npm/_logs\n",
		"pnpm_config_store_dir=" + dir + "/cache/pnpm/store\n",
		"GOMODCACHE=" + dir + "/cache/go/mod\n",
		"GOCACHE=" + dir + "/cache/go/build\n",
		"PIP_CACHE_DIR=" + dir + "/cache/pip\n",
		"UV_CACHE_DIR=" + dir + "/cache/uv\n",
		"PLAYWRIGHT_BROWSERS_PATH=" + dir + "/cache/ms-playwright\n",
		"PLAYWRIGHT_SKIP_BROWSER_GC=1\n",
		"COREPACK_HOME=" + dir + "/cache/corepack\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in the env", want)
		}
	}
	// Yarn 1 would take YARN_GLOBAL_FOLDER for where `yarn global` installs.
	if strings.Contains(got, "YARN_") {
		t.Error("a Yarn variable is set")
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
		if out, err := exec.Command("sh", "-c", script).CombinedOutput(); err != nil {
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
