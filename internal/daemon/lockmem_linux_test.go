package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMappingsOfReadsOnlyThatFile(t *testing.T) {
	maps := filepath.Join(t.TempDir(), "maps")
	if err := os.WriteFile(maps, []byte(`00400000-00c00000 r-xp 00000000 fd:01 123  /usr/bin/agentbox
00c00000-01200000 r--p 00800000 fd:01 123  /usr/bin/agentbox
01200000-01300000 rw-p 00e00000 fd:01 123  /usr/bin/agentbox
c000000000-c004000000 rw-p 00000000 00:00 0
7f0000000000-7f0000100000 r-xp 00000000 fd:01 99  /usr/bin/agentbox-old
7ffd00000000-7ffd00021000 rw-p 00000000 00:00 0  [stack]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := mappingsOf(maps, "/usr/bin/agentbox")
	if err != nil {
		t.Fatal(err)
	}
	want := []addrRange{{0x400000, 0xc00000}, {0xc00000, 0x1200000}, {0x1200000, 0x1300000}}
	if len(got) != len(want) {
		t.Fatalf("mappingsOf = %x, want %x", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mappingsOf = %x, want %x", got, want)
		}
	}
}

// The daemon's own binary is what it locks: never its heap, whose free pages
// a lock would keep from going back to the kernel (lockMemory).
func TestLockMemoryFindsTheExecutable(t *testing.T) {
	exe, err := os.Readlink("/proc/self/exe")
	if err != nil {
		t.Skip(err)
	}
	ranges, err := mappingsOf("/proc/self/maps", exe)
	if err != nil || len(ranges) == 0 {
		t.Fatalf("mappingsOf(/proc/self/maps, %s) = %v, %v; want the test binary's mappings", exe, ranges, err)
	}
}
