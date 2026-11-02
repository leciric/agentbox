package pressure

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// MemInfo is the VM's memory figures, and VMStat its counters.
const (
	MemInfo = "/proc/meminfo"
	VMStat  = "/proc/vmstat"
	Swaps   = "/proc/swaps"
)

// Mem is the VM's memory as /proc/meminfo has it, in bytes, and the most it
// may grow to.
type Mem struct {
	Total, Available    int64
	SwapTotal, SwapFree int64
	// DiskSwapTotal and DiskSwapFree are the part of that on a swapfile or
	// partition rather than zram, from /proc/swaps; 0 unread.
	DiskSwapTotal, DiskSwapFree int64
	// Cap is the most memory the VM may be given (a Cloud Hypervisor VM is
	// granted it with virtio-mem as it needs it); 0 when it can't grow, so
	// Total is all it gets.
	Cap int64
	// Refaulted is how much memory has been read back in since boot after
	// being pushed out: pages swapped in, and file pages refaulted (read
	// again soon after being dropped), from /proc/vmstat; 0 unread. Its rate
	// is the working set not fitting, which a swap merely full of cold pages
	// isn't.
	Refaulted int64
}

// Known reports whether the figures were read at all.
func (m Mem) Known() bool { return m.Total > 0 }

// AtCap reports whether the VM has been given all it may have, so what it
// has available is all there is: short of its cap, the host grows it ahead
// of what it has available. MemTotal stays a little under what was plugged
// (the kernel keeps some), hence the sixteenth's slack.
func (m Mem) AtCap() bool {
	return m.Known() && (m.Cap <= 0 || m.Total >= m.Cap-m.Cap/16)
}

// AvailableShare is MemAvailable's share of MemTotal, 0 to 1.
func (m Mem) AvailableShare() float64 {
	if m.Total <= 0 {
		return 1
	}
	return float64(m.Available) / float64(m.Total)
}

// DiskSwapTolerance is how much of the disk swap may be in use before it
// counts as memory being out: a few cold pages the kernel put there stay
// there until touched, and paging is slow only once there is a lot of it.
const DiskSwapTolerance = int64(256) << 20

// DiskSwapUsed is how much of the disk swap (not zram) is in use.
func (m Mem) DiskSwapUsed() int64 { return max(0, m.DiskSwapTotal-m.DiskSwapFree) }

// DiskSwapOut reports whether the VM has paged more than DiskSwapTolerance
// to disk, which is what a full zram spills into (the swapfile
// `agentbox vm swap on` makes is used after it): the agents are already slow
// by then, so it counts as memory being out.
func (m Mem) DiskSwapOut() bool { return m.DiskSwapUsed() >= DiskSwapTolerance }

// SwapUsed is the share of swap in use, 0 to 1; 1 with no swap, which is as
// good as full. A disk swap beside zram doesn't make more room: zram is the
// limit, so its share is what counts, and 1 once DiskSwapOut.
func (m Mem) SwapUsed() float64 {
	if m.SwapTotal <= 0 {
		return 1
	}
	total, free := m.SwapTotal, m.SwapFree
	if zram := m.SwapTotal - m.DiskSwapTotal; m.DiskSwapTotal > 0 && zram > 0 {
		if m.DiskSwapOut() {
			return 1
		}
		total, free = zram, max(0, m.SwapFree-m.DiskSwapFree)
	}
	return float64(total-free) / float64(total)
}

// Sample is one reading of the VM's memory: its pressure and its headroom.
type Sample struct {
	PSI
	Mem Mem
}

// String says what the sample saw, for a log line.
func (s Sample) String() string {
	out := fmt.Sprintf("some %.0f%%, full %.0f%%", s.Some.Avg10, s.Full.Avg10)
	if s.Mem.Known() {
		out += fmt.Sprintf(", %s of %s available", gib(s.Mem.Available), gib(s.Mem.Total))
		if s.Mem.AtCap() {
			out += " at its cap"
		}
		if s.Mem.SwapTotal > 0 {
			out += fmt.Sprintf(", swap %.0f%% used", 100*s.Mem.SwapUsed())
		}
		if s.Mem.DiskSwapOut() {
			out += fmt.Sprintf(", %s paged to the disk swap, so memory is out", gib(s.Mem.DiskSwapUsed()))
		}
	}
	return out
}

func gib(n int64) string { return fmt.Sprintf("%.1fG", float64(n)/(1<<30)) }

// Sampler reads samples through files it keeps open into a buffer of its
// own: a reading opens nothing and allocates nothing, so it gets through when
// the VM has no memory to spare, which is when it matters. It is safe for
// concurrent use.
type Sampler struct {
	mu           sync.Mutex
	psi, meminfo *os.File
	vmstat       *os.File // nil without one
	swaps        *os.File // nil without one
	cap, page    int64
	buf          [16 << 10]byte // /proc/vmstat is about 6 KiB
}

// OpenSampler opens a PSI file, a meminfo file and, unless "" or it can't, a
// vmstat file. cap is the most memory the VM may be given, 0 for what it has.
func OpenSampler(psiPath, meminfoPath, vmstatPath string, cap int64) (*Sampler, error) {
	p, err := os.Open(psiPath)
	if err != nil {
		return nil, err
	}
	m, err := os.Open(meminfoPath)
	if err != nil {
		_ = p.Close()
		return nil, err
	}
	out := &Sampler{psi: p, meminfo: m, cap: cap, page: int64(os.Getpagesize())}
	if vmstatPath != "" {
		// Without it, the headroom goes by what is available and how fast
		// that falls.
		out.vmstat, _ = os.Open(vmstatPath)
	}
	return out, nil
}

// ReadSwaps makes the sampler read the swap devices from path (/proc/swaps)
// too, to tell disk swap from zram; without it Mem has no disk swap figures.
func (s *Sampler) ReadSwaps(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, err := os.Open(path); err == nil {
		s.swaps = f
	}
}

// Close closes its files.
func (s *Sampler) Close() error {
	err := errors.Join(s.psi.Close(), s.meminfo.Close())
	if s.vmstat != nil {
		err = errors.Join(err, s.vmstat.Close())
	}
	if s.swaps != nil {
		err = errors.Join(err, s.swaps.Close())
	}
	return err
}

// errNoPSI is what a PSI file with neither line reads as.
var errNoPSI = errors.New("pressure: no some or full line in the PSI file")

// Read takes a sample.
func (s *Sampler) Read() (Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out Sample
	b, err := s.read(s.psi)
	if err != nil {
		return out, err
	}
	var ok bool
	if out.PSI, ok = parsePSI(b); !ok {
		return out, errNoPSI
	}
	// No meminfo leaves the headroom unknown: pressure alone still works.
	if b, err := s.read(s.meminfo); err == nil {
		out.Mem = parseMemInfo(b)
		out.Mem.Cap = s.cap
	}
	if s.swaps != nil {
		if b, err := s.read(s.swaps); err == nil {
			out.Mem.DiskSwapTotal, out.Mem.DiskSwapFree = parseSwaps(b)
		}
	}
	if s.vmstat != nil {
		if b, err := s.read(s.vmstat); err == nil {
			out.Mem.Refaulted = parseVMStat(b) * s.page
		}
	}
	return out, nil
}

// read reads a /proc file from its start, which makes the kernel write it
// afresh.
func (s *Sampler) read(f *os.File) ([]byte, error) {
	n, err := f.ReadAt(s.buf[:], 0)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return s.buf[:n], nil
}

// parsePSI is Parse without allocating, for the sampler; a malformed field
// reads as 0.
func parsePSI(b []byte) (PSI, bool) {
	var out PSI
	seen := false
	for len(b) > 0 {
		var line []byte
		line, b = cutByte(b, '\n')
		word, rest := cutByte(line, ' ')
		var s *Stall
		switch string(word) { // no allocation: the compiler compares in place
		case "some":
			s = &out.Some
		case "full":
			s = &out.Full
		default:
			continue
		}
		seen = true
		for len(rest) > 0 {
			var f []byte
			f, rest = cutByte(rest, ' ')
			key, value := cutByte(f, '=')
			switch string(key) {
			case "avg10":
				s.Avg10 = decimal(value)
			case "avg60":
				s.Avg60 = decimal(value)
			case "avg300":
				s.Avg300 = decimal(value)
			case "total":
				s.Total = uint64(decimal(value))
			}
		}
	}
	return out, seen
}

// parseMemInfo reads the lines of /proc/meminfo a sample needs.
func parseMemInfo(b []byte) Mem {
	var out Mem
	for len(b) > 0 {
		var line []byte
		line, b = cutByte(b, '\n')
		key, rest := cutByte(line, ':')
		var to *int64
		switch string(key) {
		case "MemTotal":
			to = &out.Total
		case "MemAvailable":
			to = &out.Available
		case "SwapTotal":
			to = &out.SwapTotal
		case "SwapFree":
			to = &out.SwapFree
		default:
			continue
		}
		*to = int64(decimal(trimSpace(rest))) << 10 // kB
	}
	return out
}

// parseSwaps is the size and the free part, in bytes, of the swap devices in
// /proc/swaps that aren't zram.
func parseSwaps(b []byte) (total, free int64) {
	_, b = cutByte(b, '\n') // the header
	for len(b) > 0 {
		var line []byte
		line, b = cutByte(b, '\n')
		var f [4][]byte
		n := 0
		for len(line) > 0 && n < len(f) {
			for len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
				line = line[1:]
			}
			i := 0
			for i < len(line) && line[i] != ' ' && line[i] != '\t' {
				i++
			}
			if i > 0 {
				f[n], n = line[:i], n+1
			}
			line = line[i:]
		}
		if n < 4 || len(f[0]) >= 9 && string(f[0][:9]) == "/dev/zram" {
			continue
		}
		size, used := int64(decimal(f[2]))<<10, int64(decimal(f[3]))<<10
		total += size
		free += max(0, size-used)
	}
	return total, free
}

// parseVMStat is the pages /proc/vmstat counts as read back in: swapped in
// (pswpin) and file pages refaulted (workingset_refault_file, or
// workingset_refault before Linux 5.9). Anon refaults are swap-ins already.
func parseVMStat(b []byte) int64 {
	var out int64
	for len(b) > 0 {
		var line []byte
		line, b = cutByte(b, '\n')
		key, rest := cutByte(line, ' ')
		switch string(key) {
		case "pswpin", "workingset_refault_file", "workingset_refault":
			out += int64(decimal(rest))
		}
	}
	return out
}

func cutByte(b []byte, sep byte) (before, after []byte) {
	for i, c := range b {
		if c == sep {
			return b[:i], b[i+1:]
		}
	}
	return b, nil
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && b[0] == ' ' {
		b = b[1:]
	}
	return b
}

// decimal is the number b starts with, digits and an optional fraction, as
// /proc writes them; what follows it is ignored.
func decimal(b []byte) float64 {
	var whole, frac, scale float64 = 0, 0, 1
	i := 0
	for ; i < len(b) && b[i] >= '0' && b[i] <= '9'; i++ {
		whole = whole*10 + float64(b[i]-'0')
	}
	if i < len(b) && b[i] == '.' {
		for i++; i < len(b) && b[i] >= '0' && b[i] <= '9'; i++ {
			frac = frac*10 + float64(b[i]-'0')
			scale *= 10
		}
	}
	return whole + frac/scale
}
