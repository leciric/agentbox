package kvm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// addACL writes an ACL the kernel takes: on a file of the test's own, which
// its owner may give an ACL without root.
func TestAddACLOnARealFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "kvm")
	if err := os.WriteFile(file, nil, 0o660); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o660); err != nil { // past the umask
		t.Fatal(err)
	}
	if err := addACL(file, 4242); err != nil {
		if errors.Is(err, unix.EOPNOTSUPP) {
			t.Skip("no POSIX ACLs on this filesystem")
		}
		t.Fatal(err)
	}
	// Twice, as a second Grant would.
	if err := addACL(file, 4242); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, err := unix.Getxattr(file, "system.posix_acl_access", buf)
	if err != nil {
		t.Fatal(err)
	}
	got := decode(t, buf[:n])
	want := []entry{{aclUserObj, 6, aclNoID}, {aclUser, 6, 4242}, {aclGroupObj, 6, aclNoID}, {aclMask, 6, aclNoID}, {aclOther, 0, aclNoID}}
	if !equal(got, want) {
		t.Errorf("ACL = %v, want %v", got, want)
	}
	// The mode's group bits are the mask's, unchanged.
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o660 {
		t.Errorf("mode = %v, %v; want 0660", fi.Mode(), err)
	}
}
