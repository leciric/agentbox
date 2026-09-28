package chv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
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
		ch:     newCHClient(ch.serve(t)),
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
	if st.Disk.Size != 2*GiB || st.Disk.Used <= 0 || st.Disk.Used >= GiB {
		t.Errorf("disk = %+v, want 2 GiB, sparse", st.Disk)
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

func TestCHArgs(t *testing.T) {
	l := testLayout(t)
	c := Config{Name: "agentbox", CPUs: 4, MemoryMin: 4 * GiB, MemoryCap: 12*GiB + 100, Home: "/home/lint"}
	args := strings.Join(chArgs(c, l), " ")
	for _, want := range []string{
		"--cpus boot=4",
		"--memory size=4294967296,shared=on,hotplug_method=virtio-mem,hotplug_size=8589934592",
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
	args = strings.Join(chArgs(c, l), " ")
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
