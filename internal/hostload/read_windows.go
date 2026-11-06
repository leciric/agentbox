package hostload

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
)

// memoryStatusEx is MEMORYSTATUSEX.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// reader keeps the last GetSystemTimes, as the CPU's busy share is the
// difference between two.
type reader struct {
	idle, total uint64
}

func newReader() *reader { return &reader{} }

func ft(f windows.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }

func (rd *reader) read() (Reading, error) {
	r := Reading{OS: "windows", Memory: -1, CPU: -1, IO: -1, CPUShare: -1}
	m := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if ok, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); ok == 0 {
		return r, err
	}
	r.MemoryTotal, r.Available = int64(m.totalPhys), int64(m.availPhys)
	var idle, kernel, user windows.Filetime
	if ok, _, _ := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); ok != 0 {
		// Kernel time includes idle time.
		i, t := ft(idle), ft(kernel)+ft(user)
		if rd.total > 0 && t > rd.total {
			r.CPUShare = 100 * (1 - float64(i-rd.idle)/float64(t-rd.total))
		}
		rd.idle, rd.total = i, t
	}
	return r, nil
}
