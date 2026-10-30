package image

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// base-packages.txt is split on whitespace by three Dockerfiles (grep and
// xargs) and read line by line by system.sh, so a line holding two names would
// install one of them oddly and a comma would be a package nobody has. One name
// per line, comments and blank lines skipped, is the contract all four rely on.
func TestBasePackagesAreOneNamePerLine(t *testing.T) {
	t.Parallel()
	names := 0
	for n, line := range strings.Split(string(basePackages), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.ContainsAny(line, " \t,") {
			t.Errorf("base-packages.txt line %d holds more than one name: %q", n+1, line)
		}
		names++
	}
	// A truncated file would leave every image without the commands its tools
	// call, which is the failure this file exists to stop.
	if names < 10 {
		t.Errorf("base-packages.txt names %d packages, want the full list", names)
	}
}

// The list is only one list while every runtime reads it. This is the test that
// would have caught the daemon image coming adrift: it was built on its own
// three-package list and had no curl, so a Claude Code login on it failed after
// the download had been announced.
func TestEveryRuntimeInstallsTheBasePackages(t *testing.T) {
	t.Parallel()
	// The pod image, the desktop image the machines runtime builds, and the
	// daemon container; the Incus image reads it through system.sh, below.
	for _, path := range []string{
		"../agentimage/Dockerfile",
		"../machines/image/Dockerfile",
		"../../packaging/daemon/Dockerfile",
	} {
		data, err := os.ReadFile(filepath.FromSlash(path))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if !strings.Contains(string(data), "base-packages.txt") {
			t.Errorf("%s installs packages of its own instead of base-packages.txt", path)
		}
	}
	if !strings.Contains(string(systemScript), "/root/base-packages.txt") {
		t.Error("system.sh doesn't install the base packages from /root/base-packages.txt")
	}
}
