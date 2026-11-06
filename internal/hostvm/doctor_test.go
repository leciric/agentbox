package hostvm

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/kvm"
)

func TestKVMCheckSaysWhatToDo(t *testing.T) {
	denied := errors.New("permission denied")
	vmx := func() ([]byte, error) { return []byte("flags\t: fpu vmx sse\n"), nil }
	none := func() ([]byte, error) { return []byte("flags\t: fpu sse\n"), nil }
	for name, tc := range map[string]struct {
		probe  kvm.Probe
		status string
		fix    string
	}{
		"ok":        {kvm.Probe{Open: func() error { return nil }}, api.DoctorOK, ""},
		"denied":    {kvm.Probe{Open: func() error { return denied }, Stat: func() error { return nil }}, api.DoctorFail, kvm.Command},
		"no module": {kvm.Probe{Open: func() error { return fs.ErrNotExist }, Stat: func() error { return fs.ErrNotExist }, CPUInfo: vmx}, api.DoctorFail, kvm.Command},
		"no virt":   {kvm.Probe{Open: func() error { return fs.ErrNotExist }, Stat: func() error { return fs.ErrNotExist }, CPUInfo: none}, api.DoctorFail, "firmware"},
	} {
		c := kvmCheck(tc.probe)
		if c.Status != tc.status || !strings.Contains(c.Fix, tc.fix) || c.AutoFix {
			t.Errorf("%s: %+v; want %s, a fix with %q, and none of its own", name, c, tc.status, tc.fix)
		}
	}
}

func TestVMCheckStartsAVMThatDoesntAnswer(t *testing.T) {
	started := false
	ready := func(context.Context) error { started = true; return nil }
	if l := vmCheck("agentbox", "/s", nil, ready); l.Check.Status != api.DoctorOK || l.Fix != nil {
		t.Errorf("answering = %+v", l.Check)
	}
	l := vmCheck("agentbox", "/s", errors.New("connect: no such file or directory"), ready)
	if l.Check.Status != api.DoctorFail || !l.Check.AutoFix || l.Check.Fix != "agentbox vm start" || !strings.Contains(l.Check.Message, "no such file") {
		t.Errorf("not answering = %+v", l.Check)
	}
	if err := l.Fix(context.Background()); err != nil || !started {
		t.Errorf("its fix didn't start the VM: %v", err)
	}
}
