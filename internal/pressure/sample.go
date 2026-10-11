package pressure

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// MemInfo is the VM's memory figures.
const MemInfo = "/proc/meminfo"

// Mem is the VM's memory as /proc/meminfo has it, in bytes, and the most it
// may grow to.
type Mem struct {
	Total, Available    int64
	SwapTotal, SwapFree int64
	// Cap is the most memory the VM may be given (a Cloud Hypervisor VM is
	// granted it with virtio-mem as it needs it); 0 when it can't grow, so
	// Total is all it gets.
	Cap int64
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

// SwapUsed is the share of swap (zram) in use, 0 to 1; 1 with no swap, which
// is as good as full.
func (m Mem) SwapUsed() float64 {
	if m.SwapTotal <= 0 {
		return 1
	}
	return float64(m.SwapTotal-m.SwapFree) / float64(m.SwapTotal)
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
	cap          int64
	buf          [4096]byte
}

// OpenSampler opens a PSI file and a meminfo file. cap is the most memory the
// VM may be given, 0 for what it has.
func OpenSampler(psiPath, meminfoPath string, cap int64) (*Sampler, error) {
	p, err := os.Open(psiPath)
	if err != nil {
		return nil, err
	}
	m, err := os.Open(meminfoPath)
	if err != nil {
		_ = p.Close()
		return nil, err
	}
	return &Sampler{psi: p, meminfo: m, cap: cap}, nil
}

// Close closes its files.
func (s *Sampler) Close() error {
	return errors.Join(s.psi.Close(), s.meminfo.Close())
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
