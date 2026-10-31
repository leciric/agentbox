package kvm

import (
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
)

// Grant writes /dev/kvm's POSIX ACL itself, as the system.posix_acl_access
// extended attribute, rather than running setfacl: Debian doesn't install the
// acl package by default, and this is the one machine that needs it.

// The attribute is a version, then entries of a tag, permissions and an ID,
// little-endian (linux/posix_acl_xattr.h).
const (
	aclVersion = 2

	aclUserObj  = 0x01
	aclUser     = 0x02
	aclGroupObj = 0x04
	aclGroup    = 0x08
	aclMask     = 0x10
	aclOther    = 0x20

	aclNoID = 0xFFFFFFFF
)

type aclEntry struct {
	tag  uint16
	perm uint16
	id   uint32
}

// withUser is the access ACL acl (a system.posix_acl_access value, or nil for
// a file that has none, whose ACL is its mode's) with uid given read and
// write, as `setfacl -m u:<uid>:rw` would leave it: the user's entry added or
// widened, and the mask widened to cover every group-class entry.
func withUser(acl []byte, mode uint32, uid uint32) ([]byte, error) {
	entries, err := parseACL(acl, mode)
	if err != nil {
		return nil, err
	}
	found := false
	for i := range entries {
		if entries[i].tag == aclUser && entries[i].id == uid {
			entries[i].perm |= 6
			found = true
		}
	}
	if !found {
		entries = append(entries, aclEntry{tag: aclUser, perm: 6, id: uid})
	}
	var mask uint16
	for _, e := range entries {
		if e.tag == aclUser || e.tag == aclGroupObj || e.tag == aclGroup {
			mask |= e.perm
		}
	}
	entries = slices.DeleteFunc(entries, func(e aclEntry) bool { return e.tag == aclMask })
	entries = append(entries, aclEntry{tag: aclMask, perm: mask, id: aclNoID})
	// The kernel wants them by tag, then by ID.
	slices.SortFunc(entries, func(a, b aclEntry) int {
		return cmp.Or(cmp.Compare(a.tag, b.tag), cmp.Compare(a.id, b.id))
	})
	out := binary.LittleEndian.AppendUint32(nil, aclVersion)
	for _, e := range entries {
		out = binary.LittleEndian.AppendUint16(out, e.tag)
		out = binary.LittleEndian.AppendUint16(out, e.perm)
		out = binary.LittleEndian.AppendUint32(out, e.id)
	}
	return out, nil
}

func parseACL(acl []byte, mode uint32) ([]aclEntry, error) {
	if len(acl) == 0 {
		return []aclEntry{
			{tag: aclUserObj, perm: uint16(mode>>6) & 7, id: aclNoID},
			{tag: aclGroupObj, perm: uint16(mode>>3) & 7, id: aclNoID},
			{tag: aclOther, perm: uint16(mode) & 7, id: aclNoID},
		}, nil
	}
	if len(acl) < 4 || (len(acl)-4)%8 != 0 {
		return nil, errors.New("not a POSIX ACL")
	}
	if v := binary.LittleEndian.Uint32(acl); v != aclVersion {
		return nil, fmt.Errorf("a POSIX ACL of version %d, not %d", v, aclVersion)
	}
	var entries []aclEntry
	for b := acl[4:]; len(b) > 0; b = b[8:] {
		entries = append(entries, aclEntry{
			tag:  binary.LittleEndian.Uint16(b),
			perm: binary.LittleEndian.Uint16(b[2:]),
			id:   binary.LittleEndian.Uint32(b[4:]),
		})
	}
	return entries, nil
}
