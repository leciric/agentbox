package chv

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/hostos"
)

func testSupervisor(t *testing.T, ch *fakeCH) *supervisor {
	l := testLayout(t)
	if err := os.MkdirAll(l.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	// Sparse disks: a size, and next to nothing on disk.
	for _, disk := range []string{l.RootDisk(), l.PoolDisk()} {
		f, err := os.Create(disk)
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Truncate(GiB)
		_, _ = f.WriteAt(make([]byte, 4096), 0)
		_ = f.Close()
	}
	c := Config{Name: "agentbox", CPUs: 4, MemoryMin: 4 * GiB, MemoryCap: 12 * GiB}
	s := &supervisor{
		c: c, l: l,
		m:      &chMachine{ch: newCHClient(ch.serve(t))},
		policy: memPolicy{Min: c.MemoryMin, Cap: c.MemoryCap},
		stopc:  make(chan api.VMStopRequest, 1),
		state:  api.VMRunning,
		since:  time.Now(),
	}
	s.mem.Requested = c.MemoryMin
	return s
}

func call(t *testing.T, h http.Handler, method, path, body string) (int, api.VMStatus, api.Error) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	var st api.VMStatus
	var e api.Error
	if w.Code/100 == 2 {
		if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	} else if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error == "" {
		t.Fatalf("%s %s: error body %q isn't an api.Error", method, path, w.Body)
	}
	return w.Code, st, e
}

func TestVMSocketStatus(t *testing.T) {
	s := testSupervisor(t, &fakeCH{})
	s.sample = &memSample{Total: 6 * GiB, Available: 4 * GiB}
	s.mem.Requested = 6 * GiB
	code, st, _ := call(t, s.handler(), "GET", "/v1/vm", "")
	if code != http.StatusOK {
		t.Fatalf("GET /v1/vm: %d", code)
	}
	if st.Mode != api.ModeVM || st.Driver != api.VMDriverCloudHypervisor || st.Name != "agentbox" || st.State != api.VMRunning || st.CPUs != 4 {
		t.Errorf("status = %+v", st)
	}
	if st.Memory.Min != 4*GiB || st.Memory.Cap != 12*GiB || st.Memory.Granted != 6*GiB || st.Memory.Used != 2*GiB {
		t.Errorf("memory = %+v", st.Memory)
	}
	if st.Disk.Size != 2*GiB || st.Disk.Allocated <= 0 || st.Disk.Allocated >= GiB {
		t.Errorf("disk = %+v, want 2 GiB, sparse", st.Disk)
	}
	if st.Disk.Pool.Size+st.Disk.Root.Size != st.Disk.Size || st.Disk.Pool.Allocated+st.Disk.Root.Allocated != st.Disk.Allocated {
		t.Errorf("disk = %+v, want its totals to be its images'", st.Disk)
	}
}

func TestVMSocketPauseResume(t *testing.T) {
	ch := &fakeCH{}
	s := testSupervisor(t, ch)
	h := s.handler()
	if code, st, _ := call(t, h, "POST", "/v1/vm/pause", ""); code != http.StatusOK || st.State != api.VMPaused {
		t.Fatalf("pause: %d %s", code, st.State)
	}
	// Pausing a paused VM does nothing.
	if code, _, _ := call(t, h, "POST", "/v1/vm/pause", ""); code != http.StatusOK {
		t.Fatalf("pause again: %d", code)
	}
	// Resumed, it's starting until its daemon has answered.
	if code, st, _ := call(t, h, "POST", "/v1/vm/resume", ""); code != http.StatusOK || st.State != api.VMStarting {
		t.Fatalf("resume: %d %s", code, st.State)
	}
	if got := ch.called(); !slices.Equal(got, []string{"PUT vm.pause", "PUT vm.resume"}) {
		t.Errorf("Cloud Hypervisor was asked %q", got)
	}
	s.daemonReady = true
	s.state = api.VMPaused
	if _, st, _ := call(t, h, "POST", "/v1/vm/resume", ""); st.State != api.VMRunning {
		t.Errorf("resumed with its daemon up: %s", st.State)
	}
}

func TestVMSocketPauseFails(t *testing.T) {
	s := testSupervisor(t, &fakeCH{fail: "vm.pause"})
	code, _, e := call(t, s.handler(), "POST", "/v1/vm/pause", "")
	if code != http.StatusInternalServerError || !strings.Contains(e.Error, "InvalidStateTransition") {
		t.Errorf("pause: %d %q", code, e.Error)
	}
	if s.getState() != api.VMRunning {
		t.Errorf("state = %s after a failed pause", s.getState())
	}
}

func TestVMSocketStop(t *testing.T) {
	s := testSupervisor(t, &fakeCH{})
	h := s.handler()
	if code, _, _ := call(t, h, "POST", "/v1/vm/stop", "{bad"); code != http.StatusBadRequest {
		t.Errorf("a bad body: %d", code)
	}
	code, st, _ := call(t, h, "POST", "/v1/vm/stop", `{"agents":true}`)
	if code != http.StatusAccepted || st.State != api.VMStopping {
		t.Fatalf("stop: %d %s", code, st.State)
	}
	select {
	case req := <-s.stopc:
		if !req.Agents {
			t.Error("the stop lost Agents")
		}
	default:
		t.Fatal("no stop was asked of the supervisor")
	}
	// Stopping twice is stopping once.
	if code, _, _ := call(t, h, "POST", "/v1/vm/stop", ""); code != http.StatusAccepted {
		t.Errorf("stop again: %d", code)
	}
	if code, _, e := call(t, h, "POST", "/v1/vm/pause", ""); code != http.StatusConflict {
		t.Errorf("pause while stopping: %d %q", code, e.Error)
	}
}

func TestOffStatusAndStatus(t *testing.T) {
	p := testPaths(t)
	l := NewLayout(p, "agentbox")
	c := Config{Name: "agentbox", CPUs: 2, MemoryMin: 4 * GiB, MemoryCap: 8 * GiB}
	if st := Status(t.Context(), c, l, p); st.State != api.VMMissing || st.Problem == "" {
		t.Errorf("before init: %+v", st)
	}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	st := Status(t.Context(), c, l, p)
	if st.State != api.VMOff || st.Memory.Min != 4*GiB || st.Memory.Cap != 8*GiB || st.Memory.Resident != 0 {
		t.Errorf("off: %+v", st)
	}
	if err := Pause(t.Context(), l, p); err != ErrNotRunning {
		t.Errorf("Pause with no VM = %v", err)
	}
}

func TestRoomFor(t *testing.T) {
	c := Config{CPUs: 4, MemoryMin: 4 * GiB, MemoryCap: 24 * GiB}
	// A VM bigger than the host (a config made on another machine) still
	// boots with room for what it asks for.
	if got := roomFor(c, 2, 16*GiB); got != (Room{CPUs: 4, Memory: 24 * GiB}) {
		t.Errorf("roomFor on a small host = %+v", got)
	}
	if got := regionSize(c, Room{CPUs: 4, Memory: 4 * GiB}); got != 0 {
		t.Errorf("regionSize with no room = %d", got)
	}
}

func TestCHArgs(t *testing.T) {
	l := testLayout(t)
	c := Config{Name: "agentbox", CPUs: 4, MemoryMin: 4 * GiB, MemoryCap: 12*GiB + 100, Home: "/home/lint"}
	// Room for every core and all of the host's memory, not only the cap.
	room := roomFor(c, 16, 32*GiB+100)
	args := strings.Join(chArgs(c, l, room), " ")
	for _, want := range []string{
		"--cpus boot=4,max=16",
		"--memory size=4294967296,shared=on,thp=on,hotplug_method=virtio-mem,hotplug_size=30064771072",
		"--balloon size=0,free_page_reporting=on",
		"path=" + l.PoolDisk() + ",image_type=raw,serial=agentbox-pool ",
		"vhost_mode=client,mac=" + macAddress("agentbox"),
		"--fs tag=home,socket=" + l.FSSocket(),
		"--vsock cid=3,socket=" + l.VsockSocket(),
	} {
		if !strings.Contains(args, want) {
			t.Errorf("no %q in\n%s", want, args)
		}
	}
	if strings.Contains(args, "seed") || strings.Contains(args, "rate_limit") {
		t.Errorf("a seed or a rate limit that wasn't asked for:\n%s", args)
	}
	c.IOLimit = 64 << 20
	_ = os.MkdirAll(l.Dir(), 0o755)
	_ = os.WriteFile(l.Seed(), nil, 0o644)
	args = strings.Join(chArgs(c, l, room), " ")
	for _, want := range []string{
		"--rate-limit-group id=disks,bw_size=67108864,bw_refill_time=1000",
		"path=" + l.RootDisk() + ",image_type=raw,rate_limit_group=disks",
		"serial=agentbox-pool,rate_limit_group=disks",
		"path=" + l.Seed() + ",image_type=raw,readonly=on",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("no %q in\n%s", want, args)
		}
	}
	if mac := macAddress("agentbox"); mac != macAddress("agent"+"box") || mac == macAddress("other") {
		t.Error("the MAC address isn't one per VM name")
	}
}

// A supervisor that holds its lock runs the VM even when its sockets and its
// pid file are gone (another agentbox removed them): Status says so rather
// than off, Start doesn't start another, and neither removes anything.
func TestALostSupervisorIsntStartedAgain(t *testing.T) {
	t.Setenv(hostos.Env, "")
	p := testPaths(t)
	l := NewLayout(p, "agentbox")
	c := Config{Name: "agentbox", CPUs: 2, MemoryMin: 4 * GiB, MemoryCap: 8 * GiB}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(l.Run(), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, running := supervisorRunning(l); running {
		t.Fatal("a supervisor runs before any lock is held")
	}
	unlock, err := lockFile(l.LockFile())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	kept := filepath.Join(l.Run(), "vsock.sock")
	if err := os.WriteFile(kept, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, running := supervisorRunning(l); !running {
		t.Error("a held lock isn't a running supervisor")
	}
	st := Status(t.Context(), c, l, p)
	if st.State == api.VMOff || !strings.Contains(st.Problem, "doesn't answer") {
		t.Errorf("Status = %s (%q), want it running where it can't be reached", st.State, st.Problem)
	}
	old := vmSocketWait
	vmSocketWait = 300 * time.Millisecond
	t.Cleanup(func() { vmSocketWait = old })
	err = Start(t.Context(), c, l, p, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "agentbox vm stop") {
		t.Errorf("Start = %v, want it to say the VM runs and how to end it", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("Start removed the running VM's files: %v", err)
	}
}

// In AgentBox's own VM, nothing starts a VM, whatever its HOME holds.
func TestStartAndSuperviseRefuseInTheVM(t *testing.T) {
	t.Setenv(hostos.Env, hostos.Linux)
	p := testPaths(t)
	l := NewLayout(p, "agentbox")
	c := Config{Name: "agentbox", CPUs: 2, MemoryMin: 4 * GiB, MemoryCap: 8 * GiB}
	if err := Start(t.Context(), c, l, p, io.Discard); err != errInVM {
		t.Errorf("Start in the VM = %v", err)
	}
	if err := Supervise(t.Context(), c, l, p); err != errInVM {
		t.Errorf("Supervise in the VM = %v", err)
	}
	if _, err := os.Stat(l.Run()); !os.IsNotExist(err) {
		t.Errorf("the VM's run directory was touched: %v", err)
	}
}

func TestLockHolders(t *testing.T) {
	locks := `1: FLOCK  ADVISORY  WRITE 4242 00:2e:1234 0 EOF
1: -> FLOCK  ADVISORY  WRITE 999 00:2e:1234 0 EOF
2: POSIX  ADVISORY  WRITE 77 fd:01:1234 0 EOF
3: FLOCK  ADVISORY  WRITE 5151 00:31:51234 0 EOF
4: FLOCK  ADVISORY  WRITE 6161 00:32:1234 0 EOF
`
	if got := lockHolders(locks, 1234); !slices.Equal(got, []int{4242, 6161}) {
		t.Errorf("lockHolders = %v, want [4242 6161]", got)
	}
}
