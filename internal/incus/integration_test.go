//go:build integration

package incus_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/incus"
)

// These tests need Incus, scripts/host-setup.sh and a built base image. They
// make each change twice, once through the incus command and once through the
// API, on two copies of the base, and check both come out the same, errors
// included:
//
//	agentbox image build && go test -tags integration ./internal/incus

const base = "agentbox-base"

type side struct {
	name string
	c    incus.Client
	inst string
}

func sides(t *testing.T) []side {
	t.Helper()
	ctx := context.Background()
	api := incus.Client{}
	if err := api.Ping(ctx); err != nil {
		t.Skipf("no Incus: %v", err)
	}
	if ok, err := api.HasSnapshot(ctx, base, "ready"); err != nil || !ok {
		t.Skipf("no base image (%v): run agentbox image build first", err)
	}
	both := []side{
		{"cli", incus.Client{Bin: "incus"}, "ab-it-cli"},
		{"api", api, "ab-it-api"},
	}
	for _, s := range both {
		_ = api.Delete(ctx, s.inst)
		t.Cleanup(func() { _ = api.Delete(context.Background(), s.inst) })
		start := time.Now()
		if err := s.c.Copy(ctx, base+"/ready", s.inst); err != nil {
			t.Fatalf("%s: copy: %v", s.name, err)
		}
		t.Logf("%s: copy took %s", s.name, time.Since(start).Round(time.Millisecond))
	}
	return both
}

// same checks that each side got the same result, with its own instance's
// name taken out.
func same(t *testing.T, what string, both []side, got func(side) string) {
	t.Helper()
	var first string
	for i, s := range both {
		v := strings.ReplaceAll(got(s), s.inst, "INST")
		if s.c.Bin != "" {
			v = olderWording.Replace(v)
		}
		if i == 0 {
			first = v
			continue
		}
		if v != first {
			t.Errorf("%s:\n  %s: %q\n  %s: %q", what, both[0].name, first, s.name, v)
		}
	}
}

// The client follows Incus 6.23's command, whose messages the command in
// Debian's Incus 6.0 LTS words a little differently. Its words are put into
// 6.23's before the two sides are compared. Incus 7's command adds a second
// line to a missing device's error, which is about devices from profiles and
// so wrong for it; the client leaves it out.
var olderWording = strings.NewReplacer(
	"INST never: Device doesn't exist", "INST never: Device “never” doesn't exist",
	"\nDevice from profile(s) cannot be removed from individual instance. Override device “never” or modify profile instead", "",
	`Failed checking instance exists "local:ab-it-missing"`, "Failed checking instance ab-it-missing exists",
	"INST/no/such/dir/f --mode 0755: file does not exist", `INST/no/such/dir/f --mode 0755: Failed to open target file "/no/such/dir/f": file does not exist`,
)

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func TestClientsAgree(t *testing.T) {
	ctx := context.Background()
	both := sides(t)
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}

	same(t, "copied config", both, func(s side) string {
		d, err := s.c.Details(ctx, s.inst)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for k, v := range d.Config {
			if !strings.HasPrefix(k, "volatile.") || k == "volatile.base_image" {
				keys = append(keys, k+"="+v)
			}
		}
		for k, dev := range d.Devices {
			keys = append(keys, "device "+k+"="+dev["type"])
		}
		return strings.Join(slices.Sorted(slices.Values(keys)), "\n")
	})

	for _, s := range both {
		if err := s.c.Start(ctx, s.inst); err != nil {
			t.Fatalf("%s: start: %v", s.name, err)
		}
		if _, err := s.c.WaitReady(ctx, s.inst, 2*time.Minute); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
	}

	run := func(what string, f func(s side) (string, error)) {
		t.Helper()
		same(t, what, both, func(s side) string {
			out, err := f(s)
			return out + " | " + errText(err)
		})
	}
	run("exec", func(s side) (string, error) {
		return s.c.Exec(ctx, s.inst, "sh", "-c", "echo out; echo err >&2")
	})
	run("exec that fails with stderr", func(s side) (string, error) {
		return s.c.Exec(ctx, s.inst, "sh", "-c", "echo partial; echo oops >&2; exit 3")
	})
	run("exec that fails silently", func(s side) (string, error) { return s.c.Exec(ctx, s.inst, "false") })
	run("exec of a missing command", func(s side) (string, error) { return s.c.Exec(ctx, s.inst, "/nonexistent") })
	run("exec of a missing instance", func(s side) (string, error) { return s.c.Exec(ctx, "ab-it-missing", "true") })
	run("exec with stdin", func(s side) (string, error) {
		return s.c.ExecInput(ctx, strings.NewReader("hello\n"), s.inst, "cat")
	})
	run("user exec", func(s side) (string, error) {
		var stdout, stderr bytes.Buffer
		err := s.c.UserExec(ctx, s.inst, u.Username, "echo $USER; pwd; echo e >&2; exit 4", nil, &stdout, &stderr)
		code, ok := incus.ExitCode(err)
		return stdout.String() + stderr.String(), errors.New(strings.Repeat("x", code) + map[bool]string{true: " exit"}[ok])
	})
	run("user exec, stdin and the same buffer for both", func(s side) (string, error) {
		var out bytes.Buffer
		err := s.c.UserExec(ctx, s.inst, u.Username, "tr a-z A-Z; echo done >&2", strings.NewReader("quiet\n"), &out, &out)
		// Both reach the buffer, in whichever order their streams deliver them.
		return strings.Join(slices.Sorted(slices.Values(strings.Fields(out.String()))), " "), err
	})
	run("user exec in a missing instance", func(s side) (string, error) {
		var out bytes.Buffer
		err := s.c.UserExec(ctx, "ab-it-missing", u.Username, "true", nil, &out, &out)
		code, _ := incus.ExitCode(err)
		return strings.TrimSpace(out.String()), errors.New(strings.Repeat("x", code))
	})
	run("user exec past its deadline", func(s side) (string, error) {
		short, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		start := time.Now()
		err := s.c.UserExec(short, s.inst, u.Username, "sleep 10", nil, nil, nil)
		if time.Since(start) > 5*time.Second {
			return "", errors.New("didn't return at its deadline")
		}
		return "", errors.New(map[bool]string{true: "stopped"}[err != nil])
	})

	run("write file", func(s side) (string, error) {
		if err := s.c.WriteFile(ctx, s.inst, "/root/deep/dir/f", []byte("content"), 1000, 1000, 0o640); err != nil {
			return "", err
		}
		return s.c.Exec(ctx, s.inst, "sh", "-c", "stat -c '%a %u %g' /root/deep/dir/f; cat /root/deep/dir/f")
	})

	local := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(local, []byte("#!/bin/sh\necho pushed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("push file", func(s side) (string, error) {
		if err := s.c.PushFile(ctx, local, s.inst, "/usr/local/bin/pushed", 0o755); err != nil {
			return "", err
		}
		return s.c.Exec(ctx, s.inst, "sh", "-c", "stat -c '%a %u %g' /usr/local/bin/pushed; pushed")
	})
	run("push to a missing directory", func(s side) (string, error) {
		return "", s.c.PushFile(ctx, local, s.inst, "/no/such/dir/f", 0o755)
	})

	tree := `set -e; mkdir -p /root/tree/sub; echo a >/root/tree/a; chmod 600 /root/tree/a
echo b >/root/tree/sub/b; chmod 755 /root/tree/sub/b; ln -s a /root/tree/link; ln -sf tree/a /root/single`
	run("pull file", func(s side) (string, error) {
		if _, err := s.c.Exec(ctx, s.inst, "sh", "-c", tree); err != nil {
			return "", err
		}
		dir := t.TempDir()
		if err := s.c.PullFile(ctx, s.inst, "/root/tree/a", filepath.Join(dir, "got")); err != nil {
			return "", err
		}
		if err := s.c.PullFile(ctx, s.inst, "/root/tree/sub/b", dir); err != nil { // into a directory
			return "", err
		}
		return listTree(t, dir), nil
	})
	run("pull dir", func(s side) (string, error) {
		dir := t.TempDir()
		if err := s.c.PullDir(ctx, s.inst, "/root/tree", dir); err != nil {
			return "", err
		}
		return listTree(t, dir), nil
	})
	// Two pulls where the client does what 6.23's command does, and 6.0's
	// did something else: a symlink is pulled as the file it points to,
	// where 6.0 put the symlink itself in its working directory, and a
	// directory pulled to a target that doesn't exist yet becomes that
	// target, where 6.0 made it and put the directory inside.
	api := both[1]
	dir := t.TempDir()
	if err := api.c.PullFile(ctx, api.inst, "/root/single", filepath.Join(dir, "got")); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, "fresh")
	if err := api.c.PullDir(ctx, api.inst, "/root/tree", fresh); err != nil {
		t.Fatal(err)
	}
	if got, want := listTree(t, dir), "fresh drwxr-xr-x\nfresh/a -rw------- a\nfresh/link Lrwxrwxrwx -> a\nfresh/sub drwxr-xr-x\nfresh/sub/b -rwxr-xr-x b\ngot -rw------- a"; got != want {
		t.Errorf("api pulls:\n%s\nwant\n%s", got, want)
	}
	run("pull a missing file", func(s side) (string, error) {
		dir := t.TempDir()
		err := s.c.PullFile(ctx, s.inst, "/no/such/file", filepath.Join(dir, "f"))
		if err != nil {
			err = errors.New(strings.ReplaceAll(err.Error(), dir, "DIR"))
		}
		return "", err
	})

	run("config", func(s side) (string, error) {
		if err := s.c.SetConfig(ctx, s.inst, "user.a=1", "user.b=two words"); err != nil {
			return "", err
		}
		if err := s.c.UnsetConfig(ctx, s.inst, "user.a"); err != nil {
			return "", err
		}
		config, err := s.c.Config(ctx, s.inst)
		return config["user.a"] + "," + config["user.b"], err
	})
	run("unset a missing key", func(s side) (string, error) { return "", s.c.UnsetConfig(ctx, s.inst, "user.never") })
	run("set a bad key", func(s side) (string, error) { return "", s.c.SetConfig(ctx, s.inst, "limits.cpu=lots") })
	run("devices", func(s side) (string, error) {
		if err := s.c.AddDevice(ctx, s.inst, "extra", "disk", "source=/tmp", "path=/mnt/extra"); err != nil {
			return "", err
		}
		devices, err := s.c.Devices(ctx, s.inst)
		if err != nil {
			return "", err
		}
		return devices["extra"]["type"] + " " + devices["extra"]["source"] + " " + devices["extra"]["path"],
			s.c.RemoveDevice(ctx, s.inst, "extra")
	})
	run("add a device twice", func(s side) (string, error) {
		_ = s.c.AddDevice(ctx, s.inst, "twice", "disk", "source=/tmp", "path=/mnt/twice")
		defer func() { _ = s.c.RemoveDevice(ctx, s.inst, "twice") }()
		return "", s.c.AddDevice(ctx, s.inst, "twice", "disk", "source=/tmp", "path=/mnt/twice")
	})
	run("remove a missing device", func(s side) (string, error) { return "", s.c.RemoveDevice(ctx, s.inst, "never") })

	status := func(s side) string {
		inst, err := s.c.Instance(ctx, s.inst)
		if err != nil {
			return err.Error()
		}
		return inst.Status
	}
	run("pause and resume", func(s side) (string, error) {
		if err := s.c.Pause(ctx, s.inst); err != nil {
			return "", err
		}
		paused := status(s)
		if err := s.c.Resume(ctx, s.inst); err != nil {
			return "", err
		}
		return paused + " " + status(s), nil
	})
	// Whether the command points at the log for these depends on whether
	// Incus had already failed the operation when it first looked at it, so
	// the pointer is left out of the comparison.
	noHint := func(err error) error {
		if err == nil {
			return nil
		}
		msg, _, _ := strings.Cut(err.Error(), "\nTry `incus info --show-log")
		return errors.New(msg)
	}
	run("start a running instance", func(s side) (string, error) { return "", noHint(s.c.Start(ctx, s.inst)) })
	run("snapshots", func(s side) (string, error) {
		if err := s.c.CreateSnapshot(ctx, s.inst, "s1"); err != nil {
			return "", err
		}
		if _, err := s.c.Exec(ctx, s.inst, "touch", "/root/after"); err != nil {
			return "", err
		}
		if err := s.c.StopWithin(ctx, s.inst, 30*time.Second); err != nil {
			return "", err
		}
		stopped := status(s)
		if err := s.c.RestoreSnapshot(ctx, s.inst, "s1"); err != nil {
			return "", err
		}
		has, _ := s.c.HasSnapshot(ctx, s.inst, "s1")
		snaps, err := s.c.Snapshots(ctx, s.inst)
		if err != nil {
			return "", err
		}
		if err := s.c.DeleteSnapshot(ctx, s.inst, "s1"); err != nil {
			return "", err
		}
		missing := s.c.DeleteSnapshot(ctx, s.inst, "s1")
		return stopped + " " + map[bool]string{true: "has"}[has] + " " + snaps[len(snaps)-1].Name, missing
	})
	run("rename", func(s side) (string, error) {
		if err := s.c.Rename(ctx, s.inst, s.inst+"-r"); err != nil {
			return "", err
		}
		_, gone := s.c.Instance(ctx, s.inst)
		if err := s.c.Rename(ctx, s.inst+"-r", s.inst); err != nil {
			return "", err
		}
		return errText(gone), nil
	})
	run("stop a stopped instance", func(s side) (string, error) { return "", noHint(s.c.Stop(ctx, s.inst)) })
	run("start, then stop it by force", func(s side) (string, error) {
		if err := s.c.Start(ctx, s.inst); err != nil {
			return "", err
		}
		if err := s.c.ForceStop(ctx, s.inst); err != nil {
			return "", err
		}
		return status(s), nil
	})
	run("start a missing instance", func(s side) (string, error) { return "", s.c.Start(ctx, "ab-it-missing") })
	run("delete a missing instance", func(s side) (string, error) { return "", s.c.Delete(ctx, "ab-it-missing") })
	run("copy onto an existing instance", func(s side) (string, error) { return "", s.c.Copy(ctx, base+"/ready", s.inst) })
	run("details of a missing instance", func(s side) (string, error) {
		_, err := s.c.Details(ctx, "ab-it-missing")
		return map[bool]string{true: "ErrNotFound"}[errors.Is(err, incus.ErrNotFound)], err
	})
	run("delete a running instance", func(s side) (string, error) {
		if err := s.c.Start(ctx, s.inst); err != nil {
			return "", err
		}
		err := s.c.Delete(ctx, s.inst)
		_, gone := s.c.Instance(ctx, s.inst)
		return errText(gone), err
	})
}

// TestClientSpeed times the calls the daemon makes most, each way.
func TestClientSpeed(t *testing.T) {
	ctx := context.Background()
	both := sides(t)[:2]
	for _, s := range both {
		if err := s.c.Start(ctx, s.inst); err != nil {
			t.Fatal(err)
		}
		if _, err := s.c.WaitReady(ctx, s.inst, 2*time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	const n = 20
	for _, call := range []struct {
		name string
		do   func(side) error
	}{
		{"Instances", func(s side) error { _, err := s.c.Instances(ctx); return err }},
		{"Details", func(s side) error { _, err := s.c.Details(ctx, s.inst); return err }},
		{"Exec true", func(s side) error { _, err := s.c.Exec(ctx, s.inst, "true"); return err }},
		{"SetConfig", func(s side) error { return s.c.SetConfig(ctx, s.inst, "user.speed=1") }},
	} {
		var took []string
		for _, s := range both {
			start := time.Now()
			for range n {
				if err := call.do(s); err != nil {
					t.Fatalf("%s %s: %v", s.name, call.name, err)
				}
			}
			took = append(took, s.name+" "+(time.Since(start)/n).Round(10*time.Microsecond).String())
		}
		t.Logf("%-10s %s", call.name, strings.Join(took, ", "))
	}
}

func listTree(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || p == dir {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		line := rel + " " + info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			line += " -> " + target
		case info.Mode().IsRegular():
			data, _ := os.ReadFile(p)
			line += " " + strings.TrimSpace(string(data))
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}
