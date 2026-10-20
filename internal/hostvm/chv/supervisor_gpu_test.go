package chv

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeVMScripts answers the scripts the supervisor runs in the VM: uname -r
// with running (or an error, "" for a VM that isn't up), the kernel scripts
// with what they'd print, and it keeps what the guest was told.
type fakeVMScripts struct {
	mu        sync.Mutex
	running   string
	defOut    string // defaultKernelScript's
	kernelOut string // kernelScript's
	kernelErr error
	ran       []string
	guest     string // the last guestStatusScript
}

func (f *fakeVMScripts) run(_ context.Context, script string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case script == "uname -r":
		f.ran = append(f.ran, "uname")
		if f.running == "" {
			return "", errors.New("ssh: connect: connection refused")
		}
		return f.running + "\n", nil
	case script == defaultKernelScript:
		f.ran = append(f.ran, "default")
		return f.defOut, nil
	case script == kernelScript:
		f.ran = append(f.ran, "kernel")
		return f.kernelOut, f.kernelErr
	case strings.Contains(script, guestGPUFile):
		f.ran = append(f.ran, "status")
		f.guest = script
		return "", nil
	}
	return "", errors.New("unexpected script: " + script)
}

func gpuSupervisor(t *testing.T, f *fakeVMScripts) (*supervisor, *chMachine) {
	t.Helper()
	s := testSupervisor(t, &fakeCH{})
	s.runScript = f.run
	s.log = log.New(io.Discard, "", 0)
	m := s.m.(*chMachine)
	m.l = s.l
	return s, m
}

// A kernel on trial that comes up is kept; one GRUB didn't boot is failed.
func TestTrialCameUp(t *testing.T) {
	f := &fakeVMScripts{running: "6.12.43+deb13-amd64"}
	s, m := gpuSupervisor(t, f)
	st := GPUStatus{Want: true, Trial: "6.12.43+deb13-amd64"}
	if !s.trialCameUp(t.Context(), m, &st) || st.Trial != "" || st.KernelFailed != "" {
		t.Errorf("came up: %+v", st)
	}
	if got := ReadGPUStatus(s.l); got != st {
		t.Errorf("saved %+v", got)
	}

	f.running = "6.12.43+deb13-cloud-amd64"
	st = GPUStatus{Want: true, Trial: "6.12.43+deb13-amd64"}
	if !s.trialCameUp(t.Context(), m, &st) || st.Trial != "" || st.KernelFailed != "6.12.43+deb13-amd64" {
		t.Errorf("cloud came up: %+v", st)
	}
}

// A kernel on trial that doesn't come up has the VM started again, once,
// on its default kernel, and is never tried again by itself.
func TestTrialDidntComeUp(t *testing.T) {
	old, oldPoll := kernelTrialTimeout, trialPoll
	kernelTrialTimeout, trialPoll = 50*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { kernelTrialTimeout, trialPoll = old, oldPoll })
	f := &fakeVMScripts{}
	s, m := gpuSupervisor(t, f)
	st := GPUStatus{Want: true, Trial: "6.12.43+deb13-amd64"}
	if s.trialCameUp(t.Context(), m, &st) {
		t.Error("the loop goes on")
	}
	if !s.takeRestart() || s.takeRestart() {
		t.Error("the VM isn't started again, once")
	}
	if got := ReadGPUStatus(s.l); got.Trial != "" || got.KernelFailed != "6.12.43+deb13-amd64" {
		t.Errorf("saved %+v", got)
	}
	// The switch on still: the kernel isn't installed again, and Settings
	// says why there's no GPU.
	g := gpuSettler{st: ReadGPUStatus(s.l), plan: gpuPlan{reason: gpuOff}}
	s.settleGPU(t.Context(), m, &g, true)
	if strings.Contains(strings.Join(f.ran, " "), "kernel") || g.st.State != GPUNone || !strings.Contains(g.st.Reason, "didn't come up") {
		t.Errorf("ran %q, %+v", f.ran, g.st)
	}
	// Off and on again tries it again.
	s.settleGPU(t.Context(), m, &g, false)
	f.defOut, f.kernelOut = "cloud\n", "trial 6.12.48+deb13-amd64\n"
	s.settleGPU(t.Context(), m, &g, true)
	if g.st.Trial != "6.12.48+deb13-amd64" || g.st.KernelFailed != "" {
		t.Errorf("not tried again: %+v", g.st)
	}
}

// Turning the switch on in a VM on the cloud kernel installs the generic one
// on trial and says the VM gets the GPU when it restarts; the guest and
// gpu.json are told, and told again only when it changes.
func TestSettleGPUTurnedOn(t *testing.T) {
	f := &fakeVMScripts{defOut: "cloud\n", kernelOut: "trial 6.12.43+deb13-amd64\n"}
	s, m := gpuSupervisor(t, f)
	newFakeBoot(t, false, "never", "exit 0") // for its render node in sysfs
	g := gpuSettler{plan: gpuPlan{reason: gpuOff}}

	s.settleGPU(t.Context(), m, &g, false)
	if g.st.State != GPUOff || g.st.Node != "/dev/dri/renderD128" || strings.Contains(strings.Join(f.ran, " "), "kernel") {
		t.Errorf("off: %+v, ran %q", g.st, f.ran)
	}
	if !strings.Contains(f.guest, `"state":"off"`) {
		t.Errorf("guest told %q", f.guest)
	}

	s.settleGPU(t.Context(), m, &g, true)
	if g.st.State != GPURestart || g.st.Trial != "6.12.43+deb13-amd64" || !strings.Contains(g.st.Reason, "kernel with the GPU driver") {
		t.Errorf("on: %+v", g.st)
	}
	if got := ReadGPUStatus(s.l); got != g.st {
		t.Errorf("saved %+v, want %+v", got, g.st)
	}
	f.ran = nil
	s.settleGPU(t.Context(), m, &g, true)
	if len(f.ran) != 0 {
		t.Errorf("ran again: %q", f.ran)
	}
}
