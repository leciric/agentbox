package incus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeIncus stands in for the incus binary with a shell script, the same
// pattern internal/agent's tests use.
func fakeIncus(t *testing.T, script string) Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "incus")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Client{Bin: path}
}

func TestPathDefaultsToTheBareCommand(t *testing.T) {
	if got := (Client{}).Path(); got != "incus" {
		t.Errorf("Path() = %q, want %q", got, "incus")
	}
	if got := (Client{Bin: "/opt/incus"}).Path(); got != "/opt/incus" {
		t.Errorf("Path() = %q, want %q", got, "/opt/incus")
	}
}

func TestRunReturnsStdoutOnSuccess(t *testing.T) {
	c := fakeIncus(t, `echo "hello $2"`)
	out, err := c.Run(context.Background(), "list", "world")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out != "hello world\n" {
		t.Errorf("Run() = %q, want %q", out, "hello world\n")
	}
}

func TestRunStripsIncusErrorPrefixFromStderr(t *testing.T) {
	c := fakeIncus(t, `echo "Error: instance is already running" >&2; exit 1`)
	_, err := c.Run(context.Background(), "start", "agent-01")
	if err == nil {
		t.Fatal("Run() error = nil, want an error")
	}
	if got := err.Error(); !strings.Contains(got, "incus start agent-01: instance is already running") {
		t.Errorf("Run() error = %q, want the incus command and the trimmed message", got)
	}
	if strings.Contains(err.Error(), "Error:") {
		t.Errorf("Run() error = %q, still carries the Error: prefix", err)
	}
}

func TestRunKeepsTheUnderlyingErrorWhenStderrIsEmpty(t *testing.T) {
	// A binary that isn't there at all: exec itself fails, with nothing on
	// stderr to explain why, so the caller needs the raw error to tell that
	// incus isn't installed.
	c := Client{Bin: filepath.Join(t.TempDir(), "not-there")}
	_, err := c.Run(context.Background(), "list")
	if err == nil {
		t.Fatal("Run() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "incus list:") {
		t.Errorf("Run() error = %q, want it to name the command", err)
	}
}

func TestRunInputFeedsStdinToTheCommand(t *testing.T) {
	c := fakeIncus(t, `cat`)
	out, err := c.RunInput(context.Background(), strings.NewReader("piped content"), "exec")
	if err != nil {
		t.Fatalf("RunInput() error = %v", err)
	}
	if out != "piped content" {
		t.Errorf("RunInput() = %q, want %q", out, "piped content")
	}
}

func TestInstancesParsesTheListingIncludingConfig(t *testing.T) {
	c := fakeIncus(t, `[ "$1" = list ] && cat <<'JSON'
[
  {
    "name": "agent-01",
    "status": "Running",
    "config": {"user.foo": "bar"},
    "expanded_config": {"user.foo": "bar", "limits.cpu": "2"},
    "state": {
      "network": {"eth0": {"addresses": [{"family": "inet6", "address": "fe80::1"}, {"family": "inet", "address": "10.0.0.5"}]}},
      "cpu": {"usage": 42},
      "memory": {"usage": 1024},
      "processes": 3
    }
  },
  {"name": "agent-02", "status": "Stopped"}
]
JSON`)
	instances, err := c.Instances(context.Background())
	if err != nil {
		t.Fatalf("Instances() error = %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("Instances() = %d instances, want 2", len(instances))
	}
	if instances[0].Name != "agent-01" || instances[0].Status != "Running" {
		t.Errorf("Instances()[0] = %+v", instances[0])
	}
	if got := instances[0].Config["user.foo"]; got != "bar" {
		t.Errorf("Instances()[0].Config[user.foo] = %q, want bar", got)
	}
	if got := instances[0].ExpandedConfig["limits.cpu"]; got != "2" {
		t.Errorf("Instances()[0].ExpandedConfig[limits.cpu] = %q, want 2", got)
	}
	if got := instances[0].IPv4(); got != "10.0.0.5" {
		t.Errorf("Instances()[0].IPv4() = %q, want 10.0.0.5 (skipping the inet6 address)", got)
	}
	if instances[1].State != nil {
		t.Errorf("Instances()[1].State = %+v, want nil for an instance with no state block", instances[1].State)
	}
	if got := instances[1].IPv4(); got != "" {
		t.Errorf("Instances()[1].IPv4() = %q, want \"\" with no state", got)
	}
}

func TestInstancesFailsOnMalformedJSON(t *testing.T) {
	c := fakeIncus(t, `echo 'not json'`)
	if _, err := c.Instances(context.Background()); err == nil {
		t.Error("Instances() error = nil, want a parse error for malformed JSON")
	}
}

func TestInstancesPropagatesTheUnderlyingCommandError(t *testing.T) {
	c := fakeIncus(t, `echo "Error: daemon not reachable" >&2; exit 1`)
	if _, err := c.Instances(context.Background()); err == nil || !strings.Contains(err.Error(), "daemon not reachable") {
		t.Errorf("Instances() error = %v, want it to carry incus' own message", err)
	}
}

func TestInstanceFindsItsNameAmongTheListing(t *testing.T) {
	c := fakeIncus(t, `echo '[{"name": "agent-01", "status": "Running"}, {"name": "agent-02", "status": "Stopped"}]'`)
	inst, err := c.Instance(context.Background(), "agent-02")
	if err != nil {
		t.Fatalf("Instance() error = %v", err)
	}
	if inst.Name != "agent-02" || inst.Status != "Stopped" {
		t.Errorf("Instance() = %+v", inst)
	}
}

func TestInstanceReturnsErrNotFoundWhenAbsentFromTheListing(t *testing.T) {
	c := fakeIncus(t, `echo '[{"name": "agent-01", "status": "Running"}]'`)
	_, err := c.Instance(context.Background(), "agent-99")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Instance() error = %v, want ErrNotFound", err)
	}
}

func TestIPv4ReturnsEmptyWithNoEth0(t *testing.T) {
	inst := Instance{State: &InstanceState{}}
	if got := inst.IPv4(); got != "" {
		t.Errorf("IPv4() = %q, want \"\" with no eth0 entry", got)
	}
}

func TestHasSnapshotReportsWhetherItsPathIsListed(t *testing.T) {
	c := fakeIncus(t, `echo '["/1.0/instances/agent-01/snapshots/ready", "/1.0/instances/agent-01/snapshots/other"]'`)
	ok, err := c.HasSnapshot(context.Background(), "agent-01", "ready")
	if err != nil || !ok {
		t.Errorf("HasSnapshot(ready) = %v, %v, want true, nil", ok, err)
	}
	ok, err = c.HasSnapshot(context.Background(), "agent-01", "missing")
	if err != nil || ok {
		t.Errorf("HasSnapshot(missing) = %v, %v, want false, nil", ok, err)
	}
}

func TestHasSnapshotTreatsNotFoundAsFalseNotAnError(t *testing.T) {
	c := fakeIncus(t, `echo "Error: not found" >&2; exit 1`)
	ok, err := c.HasSnapshot(context.Background(), "gone", "ready")
	if err != nil {
		t.Errorf("HasSnapshot() error = %v, want nil when the instance is simply not found", err)
	}
	if ok {
		t.Error("HasSnapshot() = true, want false when the instance is not found")
	}
}

func TestHasSnapshotPropagatesOtherErrors(t *testing.T) {
	c := fakeIncus(t, `echo "Error: daemon not reachable" >&2; exit 1`)
	if _, err := c.HasSnapshot(context.Background(), "agent-01", "ready"); err == nil {
		t.Error("HasSnapshot() error = nil, want the underlying error surfaced")
	}
}

func TestHasSnapshotFailsOnMalformedJSON(t *testing.T) {
	c := fakeIncus(t, `echo 'not json'`)
	if _, err := c.HasSnapshot(context.Background(), "agent-01", "ready"); err == nil {
		t.Error("HasSnapshot() error = nil, want a parse error for malformed JSON")
	}
}

func TestSnapshotsSortsOldestFirst(t *testing.T) {
	c := fakeIncus(t, `cat <<'JSON'
[
  {"name": "second", "created_at": "2024-02-01T00:00:00Z"},
  {"name": "first", "created_at": "2024-01-01T00:00:00Z"},
  {"name": "third", "created_at": "2024-03-01T00:00:00Z"}
]
JSON`)
	snapshots, err := c.Snapshots(context.Background(), "agent-01")
	if err != nil {
		t.Fatalf("Snapshots() error = %v", err)
	}
	var names []string
	for _, s := range snapshots {
		names = append(names, s.Name)
	}
	want := []string{"first", "second", "third"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("Snapshots() order = %v, want %v", names, want)
	}
}

func TestSnapshotsFailsOnMalformedJSON(t *testing.T) {
	c := fakeIncus(t, `echo 'not json'`)
	if _, err := c.Snapshots(context.Background(), "agent-01"); err == nil {
		t.Error("Snapshots() error = nil, want a parse error for malformed JSON")
	}
}

func TestDetailsParsesConfigAndDevices(t *testing.T) {
	c := fakeIncus(t, `cat <<'JSON'
{
  "config": {"limits.cpu": "2"},
  "expanded_config": {"limits.cpu": "2", "limits.memory": "2GiB"},
  "devices": {"root": {"pool": "default", "size": "20GiB"}}
}
JSON`)
	d, err := c.Details(context.Background(), "agent-01")
	if err != nil {
		t.Fatalf("Details() error = %v", err)
	}
	if got := d.Config["limits.cpu"]; got != "2" {
		t.Errorf("Details().Config[limits.cpu] = %q, want 2", got)
	}
	if got := d.ExpandedConfig["limits.memory"]; got != "2GiB" {
		t.Errorf("Details().ExpandedConfig[limits.memory] = %q, want 2GiB", got)
	}
	if got := d.Devices["root"]["pool"]; got != "default" {
		t.Errorf("Details().Devices[root][pool] = %q, want default", got)
	}
}

func TestDetailsReturnsErrNotFoundForAMissingInstance(t *testing.T) {
	c := fakeIncus(t, `echo "Error: not found" >&2; exit 1`)
	_, err := c.Details(context.Background(), "gone")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Details() error = %v, want ErrNotFound", err)
	}
}

func TestDetailsPropagatesOtherErrors(t *testing.T) {
	c := fakeIncus(t, `echo "Error: daemon not reachable" >&2; exit 1`)
	if _, err := c.Details(context.Background(), "agent-01"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("Details() error = %v, want the underlying error, not ErrNotFound", err)
	}
}

func TestDetailsFailsOnMalformedJSON(t *testing.T) {
	c := fakeIncus(t, `echo 'not json'`)
	if _, err := c.Details(context.Background(), "agent-01"); err == nil {
		t.Error("Details() error = nil, want a parse error for malformed JSON")
	}
}

func TestConfigAndDevicesAreProjectionsOfDetails(t *testing.T) {
	c := fakeIncus(t, `echo '{"config": {"a": "1"}, "devices": {"root": {"pool": "default"}}}'`)
	cfg, err := c.Config(context.Background(), "agent-01")
	if err != nil || cfg["a"] != "1" {
		t.Errorf("Config() = %v, %v, want {a: 1}, nil", cfg, err)
	}
	devices, err := c.Devices(context.Background(), "agent-01")
	if err != nil || devices["root"]["pool"] != "default" {
		t.Errorf("Devices() = %v, %v, want {root: {pool: default}}, nil", devices, err)
	}
}

func TestPoolSpaceParsesUsedAndTotal(t *testing.T) {
	c := fakeIncus(t, `echo '{"Space": {"Used": 1000, "Total": 5000}}'`)
	used, total, err := c.PoolSpace(context.Background(), "default")
	if err != nil {
		t.Fatalf("PoolSpace() error = %v", err)
	}
	if used != 1000 || total != 5000 {
		t.Errorf("PoolSpace() = %d, %d, want 1000, 5000", used, total)
	}
}

func TestPoolSpaceFailsOnMalformedJSON(t *testing.T) {
	c := fakeIncus(t, `echo 'not json'`)
	if _, _, err := c.PoolSpace(context.Background(), "default"); err == nil {
		t.Error("PoolSpace() error = nil, want a parse error for malformed JSON")
	}
}

func TestPoolSpacePropagatesTheCommandError(t *testing.T) {
	c := fakeIncus(t, `echo "Error: pool not found" >&2; exit 1`)
	if _, _, err := c.PoolSpace(context.Background(), "gone"); err == nil || !strings.Contains(err.Error(), "pool not found") {
		t.Errorf("PoolSpace() error = %v, want it to carry incus' own message", err)
	}
}

func TestVolumeUsageParsesUsedBytes(t *testing.T) {
	c := fakeIncus(t, `echo '{"usage": {"used": 123456}}'`)
	used, err := c.VolumeUsage(context.Background(), "default", "agent-01")
	if err != nil {
		t.Fatalf("VolumeUsage() error = %v", err)
	}
	if used != 123456 {
		t.Errorf("VolumeUsage() = %d, want 123456", used)
	}
}

func TestVolumeUsageFailsOnMalformedJSON(t *testing.T) {
	c := fakeIncus(t, `echo 'not json'`)
	if _, err := c.VolumeUsage(context.Background(), "default", "agent-01"); err == nil {
		t.Error("VolumeUsage() error = nil, want a parse error for malformed JSON")
	}
}

func TestVolumeUsagePropagatesTheCommandError(t *testing.T) {
	c := fakeIncus(t, `echo "Error: volume not found" >&2; exit 1`)
	if _, err := c.VolumeUsage(context.Background(), "default", "gone"); err == nil || !strings.Contains(err.Error(), "volume not found") {
		t.Errorf("VolumeUsage() error = %v, want it to carry incus' own message", err)
	}
}

func TestWaitReadyReturnsAsSoonAsTheInstanceHasAnAddress(t *testing.T) {
	c := fakeIncus(t, `case "$1" in
  exec) exit 0 ;;
  list) echo '[{"name": "agent-01", "status": "Running", "state": {"network": {"eth0": {"addresses": [{"family": "inet", "address": "10.0.0.9"}]}}}}]' ;;
esac`)
	inst, err := c.WaitReady(context.Background(), "agent-01", time.Second)
	if err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}
	if inst.IPv4() != "10.0.0.9" {
		t.Errorf("WaitReady() instance IPv4 = %q, want 10.0.0.9", inst.IPv4())
	}
}

func TestWaitReadyTimesOutWithoutAnAddress(t *testing.T) {
	c := fakeIncus(t, `case "$1" in
  exec) exit 0 ;;
  list) echo '[{"name": "agent-01", "status": "Running"}]' ;;
esac`)
	_, err := c.WaitReady(context.Background(), "agent-01", 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not get an IPv4 address") {
		t.Errorf("WaitReady() error = %v, want a timeout naming the instance", err)
	}
}

func TestWaitReadyStopsWhenTheContextIsCancelled(t *testing.T) {
	c := fakeIncus(t, `case "$1" in
  exec) exit 0 ;;
  list) echo '[{"name": "agent-01", "status": "Running"}]' ;;
esac`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.WaitReady(ctx, "agent-01", time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("WaitReady() error = %v, want context.Canceled", err)
	}
}

func TestUserExecRunsAsALoginShellOfTheGivenUser(t *testing.T) {
	c := fakeIncus(t, `printf '%s\n' "$@" > "$CAPTURE"`)
	capture := filepath.Join(t.TempDir(), "args")
	t.Setenv("CAPTURE", capture)
	if err := c.UserExec(context.Background(), "agent-01", "dev", "echo hi", nil, nil, nil); err != nil {
		t.Fatalf("UserExec() error = %v", err)
	}
	got, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	want := "exec\nagent-01\n--\nrunuser\n-l\ndev\n-c\necho hi\n"
	if string(got) != want {
		t.Errorf("UserExec() ran %q, want %q", got, want)
	}
}

func TestWriteFileSendsContentThroughStdinNotTheCommandLine(t *testing.T) {
	c := fakeIncus(t, `cat > "$CAPTURE"`)
	capture := filepath.Join(t.TempDir(), "content")
	t.Setenv("CAPTURE", capture)
	if err := c.WriteFile(context.Background(), "agent-01", "/home/dev/.bashrc", []byte("export FOO=1\n"), 1000, 1000, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "export FOO=1\n" {
		t.Errorf("WriteFile() piped %q, want the content on stdin", got)
	}
}

func TestWriteFilePropagatesFailure(t *testing.T) {
	c := fakeIncus(t, `exit 1`)
	if err := c.WriteFile(context.Background(), "agent-01", "/tmp/x", []byte("x"), 0, 0, 0o600); err == nil {
		t.Error("WriteFile() error = nil, want the underlying command's failure surfaced")
	}
}

func TestCommandBuildsAnUnstartedCommandWithTheGivenArgs(t *testing.T) {
	c := Client{Bin: "/opt/incus"}
	cmd := c.Command(context.Background(), "list", "--format", "json")
	if cmd.Path != "/opt/incus" {
		t.Errorf("Command().Path = %q, want /opt/incus", cmd.Path)
	}
	if got := strings.Join(cmd.Args, " "); got != "/opt/incus list --format json" {
		t.Errorf("Command().Args = %q", got)
	}
}
