package chv

import (
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// With systemd's user manager, the supervisor starts in a scope of its own
// beside the user's apps, at a lower weight; without it, as it was.
func TestSupervisorArgv(t *testing.T) {
	saved := systemdScope
	t.Cleanup(func() { systemdScope = saved })

	systemdScope = func() bool { return false }
	if got := supervisorArgv("/bin/agentbox", "agentbox"); !slices.Equal(got, []string{"/bin/agentbox", "vm", "run"}) {
		t.Errorf("without systemd: %q", got)
	}

	systemdScope = func() bool { return true }
	got := supervisorArgv("/bin/agentbox", "my vm/1")
	line := strings.Join(got, " ")
	for _, want := range []string{
		"systemd-run --user --scope ", "--slice=app.slice", "--unit=agentbox-vm-my-vm-1-",
		"-p CPUWeight=20", "-p IOWeight=20", " -- /bin/agentbox vm run",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("argv %q lacks %q", line, want)
		}
	}
	if got[0] != "systemd-run" || !slices.Equal(got[len(got)-3:], []string{"/bin/agentbox", "vm", "run"}) {
		t.Errorf("argv %q", got)
	}
}

// lowerPriority leaves every thread of the process at nice 10 and the lowest
// best-effort I/O priority. Run in a child, so the tests here keep theirs.
func TestLowerPriority(t *testing.T) {
	if os.Getenv("AGENTBOX_TEST_LOWER_PRIORITY") == "1" {
		if failed := lowerPriority(); len(failed) > 0 {
			t.Fatalf("lowerPriority: %v", failed)
		}
		// A thread made after it inherits it too.
		done := make(chan struct{})
		go func() { defer close(done); _ = exec.Command("true").Run() }()
		<-done
		tasks, _ := os.ReadDir("/proc/self/task")
		for _, task := range tasks {
			tid, _ := strconv.Atoi(task.Name())
			prio, err := unix.Getpriority(unix.PRIO_PROCESS, tid)
			if err != nil {
				continue // ended meanwhile
			}
			if nice := 20 - prio; nice != vmNice {
				t.Errorf("thread %d at nice %d", tid, nice)
			}
			io, _, errno := unix.Syscall(unix.SYS_IOPRIO_GET, ioprioWhoProcess, uintptr(tid), 0)
			if errno == 0 && io != ioprioBestEffortLowest {
				t.Errorf("thread %d at I/O priority %#x", tid, io)
			}
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLowerPriority$", "-test.count=1")
	cmd.Env = append(os.Environ(), "AGENTBOX_TEST_LOWER_PRIORITY=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestUnitSafe(t *testing.T) {
	if got := unitSafe("agent_box.2/x y"); got != "agent_box-2-x-y" {
		t.Errorf("unitSafe = %q", got)
	}
}

func TestActiveScheduler(t *testing.T) {
	for in, want := range map[string]string{
		"[none] mq-deadline kyber bfq": "none",
		"mq-deadline kyber [bfq] none": "bfq",
		"none":                         "none",
	} {
		if got := activeScheduler(in); got != want {
			t.Errorf("activeScheduler(%q) = %q, want %q", in, got, want)
		}
	}
}

// The disk's scheduler is found by the file's device, on the disk itself or,
// for a partition, the disk above it; "none" is a gap.
func TestDiskScheduler(t *testing.T) {
	file := t.TempDir() + "/root.raw"
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var st unix.Stat_t
	if err := unix.Stat(file, &st); err != nil {
		t.Fatal(err)
	}
	saved := sysRoot
	t.Cleanup(func() { sysRoot = saved })
	sysRoot = t.TempDir()
	if got := diskScheduler(file); got != "" {
		t.Fatalf("nothing in /sys: %q", got)
	}
	// /sys/dev/block/M:m links to the partition, whose disk holds the queue.
	disk := sysRoot + "/devices/nvme0n1"
	if err := os.MkdirAll(disk+"/nvme0n1p2", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(disk+"/queue", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(disk+"/queue/scheduler", []byte("[none] mq-deadline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sysRoot+"/dev/block", 0o755); err != nil {
		t.Fatal(err)
	}
	dev := strconv.Itoa(int(unix.Major(st.Dev))) + ":" + strconv.Itoa(int(unix.Minor(st.Dev)))
	if err := os.Symlink(disk+"/nvme0n1p2", sysRoot+"/dev/block/"+dev); err != nil {
		t.Fatal(err)
	}
	if got := diskScheduler(file); got != "none" {
		t.Fatalf("partition of an NVMe disk: %q", got)
	}
	gaps := strings.Join(priorityGaps(file), "\n")
	if !strings.Contains(gaps, "scheduler is none") {
		t.Fatalf("gaps %q lack the scheduler", gaps)
	}
}
