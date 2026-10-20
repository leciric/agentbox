package chv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBoot is a VM whose programs are shell scripts: passt and crosvm make
// their sockets, Cloud Hypervisor makes its API socket (served by a fakeCH)
// unless chFails says why it exits at once, and every command line goes to
// a log.
type fakeBoot struct {
	l   Layout
	m   *chMachine
	log string
}

func newFakeBoot(t *testing.T, want bool, chFails, crosvmVersion string) *fakeBoot {
	t.Helper()
	root := t.TempDir()
	l := Layout{Root: root, Name: "vm"}
	b := &fakeBoot{l: l, log: filepath.Join(root, "calls")}
	for _, d := range []string{l.Run(), filepath.Dir(l.Bin(gpuDir + "/bin/crosvm"))} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script := func(name, body string) {
		if err := os.WriteFile(l.Bin(name), []byte("#!/bin/sh\necho \""+filepath.Base(name)+" $*\" >>\""+b.log+"\"\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script("passt", `while [ $# -gt 0 ]; do [ "$1" = --socket ] && : >"$2"; shift; done
exec sleep 60
`)
	script("cloud-hypervisor", `case "$*" in `+chFails+`) echo "$*" | grep -q -- --gpu && { echo "Error: GPU socket" >&2; exit 1; } ;; esac
for a; do case $a in path=*/ch.sock) : >"${a#path=}" ;; esac; done
exec sleep 60
`)
	script(gpuDir+"/bin/crosvm", `[ "$1" = version ] && { `+crosvmVersion+`; }
while [ $# -gt 0 ]; do [ "$1" = --socket-path ] && : >"$2"; shift; done
exec sleep 60
`)
	if err := os.MkdirAll(l.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeGPUStatus(l, GPUStatus{Want: want}); err != nil {
		t.Fatal(err)
	}

	// One render node, which the user can open.
	old, oldOpen := sysDRM, openRenderNode
	t.Cleanup(func() { sysDRM, openRenderNode = old, oldOpen })
	sysDRM = filepath.Join(root, "sys")
	dev := filepath.Join(root, "pci", "0000:03:00.0")
	_ = os.MkdirAll(filepath.Join(sysDRM, "renderD128"), 0o755)
	_ = os.MkdirAll(dev, 0o755)
	_ = os.MkdirAll(filepath.Join(root, "drivers", "amdgpu"), 0o755)
	_ = os.Symlink(dev, filepath.Join(sysDRM, "renderD128", "device"))
	_ = os.Symlink(filepath.Join(root, "drivers", "amdgpu"), filepath.Join(dev, "driver"))
	openRenderNode = func(string) error { return nil }
	t.Setenv("AGENTBOX_VM_RENDER_NODE", "")

	f := &fakeCH{}
	var logged strings.Builder
	b.m = &chMachine{c: Config{Name: "vm", CPUs: 1, MemoryMin: 2 * GiB, MemoryCap: 2 * GiB}, l: l, log: &logged,
		logf: func(format string, args ...any) {}, ch: newCHClient(f.serve(t)), room: Room{CPUs: 1, Memory: 2 * GiB}}
	t.Cleanup(func() {
		for _, c := range []*child{b.m.cloud, b.m.gpu, b.m.passt, b.m.fs} {
			c.stop(childGrace)
		}
	})
	return b
}

// calls are the command lines prog was started with.
func (b *fakeBoot) calls(prog string) []string {
	data, _ := os.ReadFile(b.log)
	var calls []string
	for line := range strings.Lines(string(data)) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), prog+" "); ok {
			calls = append(calls, rest)
		}
	}
	return calls
}

func TestBootWithTheGPU(t *testing.T) {
	b := newFakeBoot(t, true, "never", "exit 0")
	if err := b.m.boot(t.Context()); err != nil {
		t.Fatal(err)
	}
	if b.m.gpu == nil || !b.m.gpuPlan.ok() {
		t.Fatalf("no GPU: %+v", b.m.gpuPlan)
	}
	ch := b.calls("cloud-hypervisor")
	if len(ch) != 1 || !strings.Contains(ch[0], "--gpu socket="+b.l.GPUSocket()) {
		t.Errorf("cloud-hypervisor: %q", ch)
	}
	if cv := b.calls("crosvm"); len(cv) != 2 || cv[0] != "version" || !strings.Contains(cv[1], `"render-node":"/dev/dri/renderD128"`) {
		t.Errorf("crosvm: %q", cv)
	}
	if !strings.Contains(strings.Join(b.m.runFiles(), " "), b.l.GPUSocket()) {
		t.Error("gpu.sock isn't one of the run files")
	}
}

// With the switch off, nothing about the VM changes: no crosvm, no --gpu.
func TestBootWithTheGPUOff(t *testing.T) {
	b := newFakeBoot(t, false, "never", "exit 0")
	if err := b.m.boot(t.Context()); err != nil {
		t.Fatal(err)
	}
	if b.m.gpu != nil || len(b.calls("crosvm")) != 0 || strings.Contains(strings.Join(b.calls("cloud-hypervisor"), " "), "--gpu") {
		t.Errorf("the GPU: %v, crosvm %q, cloud-hypervisor %q", b.m.gpu, b.calls("crosvm"), b.calls("cloud-hypervisor"))
	}
}

// Cloud Hypervisor that can't start with the GPU: everything starts again
// without it (passt exits with its client), and the reason says so.
func TestBootFallsBackWithoutTheGPU(t *testing.T) {
	b := newFakeBoot(t, true, "*", "exit 0")
	if err := b.m.boot(t.Context()); err != nil {
		t.Fatal(err)
	}
	ch := b.calls("cloud-hypervisor")
	if len(ch) != 2 || !strings.Contains(ch[0], "--gpu") || strings.Contains(ch[1], "--gpu") {
		t.Errorf("cloud-hypervisor: %q", ch)
	}
	if len(b.calls("passt")) != 2 {
		t.Errorf("passt wasn't started again: %q", b.calls("passt"))
	}
	if b.m.gpu != nil || !strings.Contains(b.m.gpuPlan.reason, "couldn't start with the GPU") {
		t.Errorf("gpu %v, reason %q", b.m.gpu, b.m.gpuPlan.reason)
	}
	if !b.m.running() {
		t.Error("the VM doesn't run")
	}
}

// crosvm that can't run on this host (its libraries) leaves the VM booting
// without the GPU, with a reason that names what to install.
func TestBootWhenCrosvmCantRun(t *testing.T) {
	b := newFakeBoot(t, true, "never", `echo "crosvm: error while loading shared libraries: libepoxy.so.0: cannot open shared object file" >&2; exit 127`)
	if err := b.m.boot(t.Context()); err != nil {
		t.Fatal(err)
	}
	if b.m.gpu != nil || !strings.Contains(b.m.gpuPlan.reason, "libepoxy.so.0") || !strings.Contains(b.m.gpuPlan.reason, "install") {
		t.Errorf("gpu %v, reason %q", b.m.gpu, b.m.gpuPlan.reason)
	}
	if ch := b.calls("cloud-hypervisor"); len(ch) != 1 || strings.Contains(ch[0], "--gpu") {
		t.Errorf("cloud-hypervisor: %q", ch)
	}
}
