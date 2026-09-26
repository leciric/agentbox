package incus

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func checkFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("%s holds %q, want %q", path, got, content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != mode {
		t.Errorf("%s has mode %04o, want %04o", path, info.Mode().Perm(), mode)
	}
}

func TestAPIPullFile(t *testing.T) {
	startFakeAPI(t)
	ctx := context.Background()
	inside, host := t.TempDir(), t.TempDir()
	writeFile(t, inside+"/run.sh", "echo hi\n", 0o750)
	c := Client{}

	if err := c.PullFile(ctx, "agent-01", inside+"/run.sh", host+"/copy.sh"); err != nil {
		t.Fatal(err)
	}
	checkFile(t, host+"/copy.sh", "echo hi\n", 0o750)

	// A target that is a directory gets the file inside it.
	if err := c.PullFile(ctx, "agent-01", inside+"/run.sh", host); err != nil {
		t.Fatal(err)
	}
	checkFile(t, host+"/run.sh", "echo hi\n", 0o750)

	err := c.PullFile(ctx, "agent-01", inside+"/missing", host+"/x")
	if want := "incus file pull agent-01" + inside + "/missing " + host + "/x: "; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("pulling a missing file: %v, want it to start %q", err, want)
	}
	if err := c.PullFile(ctx, "agent-02", inside+"/run.sh", host); err == nil {
		t.Error("pulling from a missing instance succeeded")
	}
}

func TestAPIPullDir(t *testing.T) {
	startFakeAPI(t)
	ctx := context.Background()
	inside, host := t.TempDir(), t.TempDir()
	writeFile(t, inside+"/state/a.txt", "a", 0o644)
	writeFile(t, inside+"/state/sub/b.txt", "b", 0o600)
	if err := os.Symlink("a.txt", inside+"/state/link"); err != nil {
		t.Fatal(err)
	}
	c := Client{}

	// Into a directory that exists, the tree lands under its own name.
	if err := c.PullDir(ctx, "agent-01", inside+"/state", host); err != nil {
		t.Fatal(err)
	}
	checkFile(t, host+"/state/a.txt", "a", 0o644)
	checkFile(t, host+"/state/sub/b.txt", "b", 0o600)
	if link, err := os.Readlink(host + "/state/link"); err != nil || link != "a.txt" {
		t.Errorf("link pulled as %q, %v; want a symlink to a.txt", link, err)
	}

	// Into one that doesn't, the tree becomes it.
	if err := c.PullDir(ctx, "agent-01", inside+"/state", host+"/fresh"); err != nil {
		t.Fatal(err)
	}
	checkFile(t, host+"/fresh/sub/b.txt", "b", 0o600)

	err := c.PullDir(ctx, "agent-01", inside+"/missing", host)
	if want := "incus file pull -r agent-01" + inside + "/missing " + host + ": "; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("pulling a missing directory: %v, want it to start %q", err, want)
	}
}

func TestAPIPushFile(t *testing.T) {
	startFakeAPI(t)
	ctx := context.Background()
	inside, host := t.TempDir(), t.TempDir()
	writeFile(t, host+"/tool", "#!/bin/sh\n", 0o600)
	c := Client{}

	if err := c.PushFile(ctx, host+"/tool", "agent-01", inside+"/bin-tool", 0o755); err != nil {
		t.Fatal(err)
	}
	checkFile(t, inside+"/bin-tool", "#!/bin/sh\n", 0o755)

	// A file that was there is overwritten, and gets the mode asked for.
	writeFile(t, host+"/tool", "new\n", 0o600)
	if err := c.PushFile(ctx, host+"/tool", "agent-01", inside+"/bin-tool", 0o700); err != nil {
		t.Fatal(err)
	}
	checkFile(t, inside+"/bin-tool", "new\n", 0o700)

	// A target that is a directory gets the file inside it.
	if err := c.PushFile(ctx, host+"/tool", "agent-01", inside, 0o644); err != nil {
		t.Fatal(err)
	}
	checkFile(t, inside+"/tool", "new\n", 0o644)

	err := c.PushFile(ctx, host+"/tool", "agent-01", inside+"/no/such/dir/tool", 0o644)
	if want := "incus file push " + host + "/tool agent-01" + inside + "/no/such/dir/tool --mode 0644: Failed to open target file"; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("pushing into a missing directory: %v, want it to start %q", err, want)
	}
	err = c.PushFile(ctx, host+"/missing", "agent-01", inside+"/x", 0o644)
	if want := "Failed to open source file"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("pushing a missing file: %v, want %q", err, want)
	}
}

func TestAPIPing(t *testing.T) {
	startFakeAPI(t)
	ctx := context.Background()

	// The command must be there too, since terminals still run it.
	t.Setenv("PATH", t.TempDir())
	if err := (Client{}).Ping(ctx); err == nil || !strings.HasPrefix(err.Error(), "incus query /1.0: ") {
		t.Errorf("Ping with no incus command: %v", err)
	}
	bin := t.TempDir()
	writeFile(t, bin+"/incus", "#!/bin/sh\n", 0o755)
	t.Setenv("PATH", bin)
	if err := (Client{}).Ping(ctx); err != nil {
		t.Errorf("Ping: %v", err)
	}
}
