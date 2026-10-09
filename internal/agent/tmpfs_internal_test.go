package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runTmpScript runs tmpScript against an fstab holding fstab, with /t mounted
// or not, and gives back the fstab after it and the mount commands it ran.
func runTmpScript(t *testing.T, fstab string, mounted bool) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fstab")
	if err := os.WriteFile(path, []byte(fstab), 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "mounts")
	status := "1"
	if mounted {
		status = "0"
	}
	stubs := map[string]string{
		"mountpoint": "exit " + status,
		"mount":      `echo "$*" >>` + log,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", "-c", tmpScript(path))
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tmpScript: %v: %s", err, out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mounts, _ := os.ReadFile(log)
	return string(after), strings.TrimSpace(string(mounts))
}

func TestTmpScriptCapsTheBaseImagesTmpfs(t *testing.T) {
	t.Parallel()
	const other = "UUID=x / ext4 defaults 0 1\n"
	capped := "tmpfs /t tmpfs mode=1777,nosuid,nodev,size=" + TmpSize + " 0 0\n"

	// The line system.sh writes, on a running agent.
	fstab, mounts := runTmpScript(t, other+"tmpfs /t tmpfs mode=1777,nosuid,nodev 0 0\n", true)
	if fstab != other+capped {
		t.Errorf("fstab =\n%s", fstab)
	}
	if want := "-o remount,size=" + TmpSize + " /t"; mounts != want {
		t.Errorf("mounts = %q, want %q", mounts, want)
	}

	// Already capped, and not mounted: nothing to rewrite or remount.
	fstab, mounts = runTmpScript(t, other+capped, false)
	if fstab != other+capped || mounts != "" {
		t.Errorf("fstab =\n%s\nmounts = %q", fstab, mounts)
	}

	// A base from before /t: no tmpfs to cap, and none made.
	fstab, mounts = runTmpScript(t, other, true)
	if fstab != other || mounts != "" {
		t.Errorf("fstab =\n%s\nmounts = %q", fstab, mounts)
	}
}
