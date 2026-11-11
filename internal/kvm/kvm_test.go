package kvm

import (
	"encoding/binary"
	"errors"
	"io/fs"
	"strings"
	"testing"
)

const (
	intelInfo = "processor\t: 0\nvendor_id\t: GenuineIntel\nflags\t\t: fpu vme de pse tsc msr vmx smx est\n\nprocessor\t: 1\nflags\t\t: fpu vmx\n"
	amdInfo   = "processor\t: 0\nvendor_id\t: AuthenticAMD\nflags\t\t: fpu vme svm extapic\n"
	// A VM without nested virtualization, or VT-x off in the firmware on
	// Linux 5.6 or later: no vmx.
	plainInfo = "processor\t: 0\nvendor_id\t: GenuineIntel\nflags\t\t: fpu vme de pse tsc msr hypervisor\n"
)

func probe(open, stat error, cpuinfo string) Probe {
	return Probe{
		Open:    func() error { return open },
		Stat:    func() error { return stat },
		CPUInfo: func() ([]byte, error) { return []byte(cpuinfo), nil },
	}
}

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		name       string
		open, stat error
		cpuinfo    string
		want       State
	}{
		{"opens", nil, nil, intelInfo, OK},
		// Debian and Ubuntu: root:kvm 0660, and the user isn't in kvm yet.
		{"permission denied", fs.ErrPermission, nil, intelInfo, Denied},
		// /dev/kvm is there but can't be stat'ed: still a permission.
		{"stat denied", fs.ErrPermission, fs.ErrPermission, intelInfo, Denied},
		{"no module, Intel", fs.ErrNotExist, fs.ErrNotExist, intelInfo, NoModule},
		{"no module, AMD", fs.ErrNotExist, fs.ErrNotExist, amdInfo, NoModule},
		{"no virtualization", fs.ErrNotExist, fs.ErrNotExist, plainInfo, NoVirt},
		{"no cpuinfo", fs.ErrNotExist, fs.ErrNotExist, "", NoVirt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Check(probe(tc.open, tc.stat, tc.cpuinfo)); got != tc.want {
				t.Errorf("Check = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestModule(t *testing.T) {
	for info, want := range map[string]string{intelInfo: "kvm_intel", amdInfo: "kvm_amd", plainInfo: "", "": ""} {
		if got := Module(probe(nil, nil, info)); got != want {
			t.Errorf("Module(%.40q) = %q, want %q", info, got, want)
		}
	}
	unreadable := Probe{CPUInfo: func() ([]byte, error) { return nil, errors.New("no /proc") }}
	if got := Module(unreadable); got != "" {
		t.Errorf("Module without /proc/cpuinfo = %q", got)
	}
}

func TestError(t *testing.T) {
	if err := Error(OK, nil); err != nil {
		t.Errorf("Error(OK) = %v", err)
	}
	for s, want := range map[State]string{
		Denied:   Command,
		NoModule: "module isn't loaded",
		NoVirt:   "firmware",
	} {
		err := Error(s, fs.ErrPermission)
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), fs.ErrPermission.Error()) {
			t.Errorf("Error(%s) = %v, want it to say %q and why", s, err, want)
		}
	}
	// Nobody is told to log in again: that's what Grant saves them.
	if strings.Contains(Error(Denied, nil).Error(), "log in") {
		t.Errorf("Error(Denied) still says to log in again")
	}
}

type entry struct{ tag, perm, id uint32 }

func decode(t *testing.T, acl []byte) []entry {
	t.Helper()
	if binary.LittleEndian.Uint32(acl) != aclVersion || (len(acl)-4)%8 != 0 {
		t.Fatalf("not an ACL: %x", acl)
	}
	var out []entry
	for b := acl[4:]; len(b) > 0; b = b[8:] {
		out = append(out, entry{uint32(binary.LittleEndian.Uint16(b)), uint32(binary.LittleEndian.Uint16(b[2:])), binary.LittleEndian.Uint32(b[4:])})
	}
	return out
}

func TestWithUserFromMode(t *testing.T) {
	// Debian's /dev/kvm: crw-rw---- root kvm, no ACL yet.
	acl, err := withUser(nil, 0o20660, 1000)
	if err != nil {
		t.Fatal(err)
	}
	want := []entry{
		{aclUserObj, 6, aclNoID},
		{aclUser, 6, 1000},
		{aclGroupObj, 6, aclNoID},
		{aclMask, 6, aclNoID},
		{aclOther, 0, aclNoID},
	}
	if got := decode(t, acl); !equal(got, want) {
		t.Errorf("ACL = %v, want %v", got, want)
	}
}

func TestWithUserKeepsWhatsThere(t *testing.T) {
	// logind's uaccess gave 1001 read and write already, with a mask; 1000
	// is added between, and the mask still covers both.
	there, err := withUser(nil, 0o20600, 1001)
	if err != nil {
		t.Fatal(err)
	}
	acl, err := withUser(there, 0o20600, 1000)
	if err != nil {
		t.Fatal(err)
	}
	want := []entry{
		{aclUserObj, 6, aclNoID},
		{aclUser, 6, 1000},
		{aclUser, 6, 1001},
		{aclGroupObj, 0, aclNoID},
		{aclMask, 6, aclNoID},
		{aclOther, 0, aclNoID},
	}
	if got := decode(t, acl); !equal(got, want) {
		t.Errorf("ACL = %v, want %v", got, want)
	}
	// Again changes nothing: Grant is safe to run twice.
	again, err := withUser(acl, 0o20600, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if got := decode(t, again); !equal(got, want) {
		t.Errorf("ACL after a second run = %v, want %v", got, want)
	}
}

func TestWithUserWidensANarrowMask(t *testing.T) {
	// 1000 had read only, under a read-only mask.
	acl := binary.LittleEndian.AppendUint32(nil, aclVersion)
	for _, e := range []entry{{aclUserObj, 6, aclNoID}, {aclUser, 4, 1000}, {aclGroupObj, 4, aclNoID}, {aclMask, 4, aclNoID}, {aclOther, 0, aclNoID}} {
		acl = binary.LittleEndian.AppendUint16(acl, uint16(e.tag))
		acl = binary.LittleEndian.AppendUint16(acl, uint16(e.perm))
		acl = binary.LittleEndian.AppendUint32(acl, e.id)
	}
	got, err := withUser(acl, 0o20640, 1000)
	if err != nil {
		t.Fatal(err)
	}
	want := []entry{{aclUserObj, 6, aclNoID}, {aclUser, 6, 1000}, {aclGroupObj, 4, aclNoID}, {aclMask, 6, aclNoID}, {aclOther, 0, aclNoID}}
	if g := decode(t, got); !equal(g, want) {
		t.Errorf("ACL = %v, want %v", g, want)
	}
}

func TestWithUserRefusesWhatIsntAnACL(t *testing.T) {
	for _, bad := range [][]byte{{1, 2, 3}, binary.LittleEndian.AppendUint32(nil, 1), append(binary.LittleEndian.AppendUint32(nil, aclVersion), 1, 2, 3)} {
		if _, err := withUser(bad, 0o660, 1000); err == nil {
			t.Errorf("withUser(%x) took it", bad)
		}
	}
}

func equal(a, b []entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
