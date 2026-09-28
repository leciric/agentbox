//go:build chvintegration

package chv

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"testing"
	"time"
)

// TestMakeVMFiles fetches the tools and makes the disks and seed for a real
// VM, the way `agentbox vm init` does, into $CHV_TEST_ROOT (a Layout named
// img-test), for booting by hand:
//
//	CHV_TEST_ROOT=$HOME/chvtest go test -tags chvintegration -run TestMakeVMFiles -v ./internal/hostvm/chv
func TestMakeVMFiles(t *testing.T) {
	root := os.Getenv("CHV_TEST_ROOT")
	if root == "" {
		t.Skip("CHV_TEST_ROOT isn't set")
	}
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	c := Config{Name: "img-test", CPUs: 4, MemoryMin: DefaultMemoryMin, MemoryCap: 20 * GiB, Disk: DefaultDisk,
		User: u.Username, UID: uid, GID: gid, Home: u.HomeDir, GuestHome: u.HomeDir + ".linux", Created: time.Now()}
	l := Layout{Root: root, Name: c.Name}
	ctx := context.Background()
	start := time.Now()
	if err := EnsureTools(ctx, l, testLog{t}); err != nil {
		t.Fatal(err)
	}
	t.Logf("tools: %s", time.Since(start))
	start = time.Now()
	if err := MakeDisks(ctx, c, l, testLog{t}); err != nil {
		t.Fatal(err)
	}
	t.Logf("disks: %s", time.Since(start))
}

// TestWaitProvisionedReal waits for the VM booted from TestMakeVMFiles's
// files, over ssh on $CHV_TEST_SSH_PORT (passt's -t, for testing only).
func TestWaitProvisionedReal(t *testing.T) {
	root, port := os.Getenv("CHV_TEST_ROOT"), os.Getenv("CHV_TEST_SSH_PORT")
	if root == "" || port == "" {
		t.Skip("CHV_TEST_ROOT or CHV_TEST_SSH_PORT isn't set")
	}
	u, _ := user.Current()
	l := Layout{Root: root, Name: "img-test"}
	run := func(ctx context.Context, argv ...string) ([]byte, error) {
		args := append([]string{"-q", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
			"-o", "ConnectTimeout=2", "-i", l.Key(), "-p", port, u.Username + "@127.0.0.1", "--"}, argv...)
		return exec.CommandContext(ctx, "ssh", args...).Output()
	}
	if err := waitCloudInit(context.Background(), run, testLog{t}, 2*time.Second); err != nil {
		t.Fatal(err)
	}
}

type testLog struct{ t *testing.T }

func (l testLog) Write(b []byte) (int, error) { l.t.Log(string(b)); return len(b), nil }
