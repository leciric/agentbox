package hostwsl

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// WSL runs every distro in one utility VM whose memory is .wslconfig's
// memory under [wsl2] (%UserProfile%\.wslconfig), half of Windows' when it
// isn't set. AgentBox's VM may never take more than the computer's memory less
// the larger of 4 GiB and a quarter, as on Linux and the Mac
// (chv.HostReserve): Setup lowers a memory= above that to it (fitMemory). One
// within it is the user's, and stays, and with none WSL's half always is. WSL reads the file when its VM starts, so
// what Setup writes counts from the next `wsl --shutdown` or restart, which
// Setup doesn't force: it would stop the user's other distros.

const (
	gibBytes          = int64(1) << 30
	wslReserveMin     = 4 * gibBytes
	wslReserveDivisor = 4
)

// wslCeiling is the most WSL's VM may have on a computer of total bytes, in
// whole GiB; 0 when total isn't known.
func wslCeiling(total int64) int64 {
	if total <= 0 {
		return 0
	}
	return max((total-max(wslReserveMin, total/wslReserveDivisor))/gibBytes*gibBytes, wslReserveMin)
}

// fitWSLConfig is .wslconfig's text with its [wsl2] memory held to ceiling
// bytes, and whether that changed it. A memory= within the ceiling is kept,
// and none is added.
func fitWSLConfig(text string, ceiling int64) (string, bool) {
	if ceiling <= 0 {
		return text, false
	}
	want := "memory=" + strconv.FormatInt(ceiling/gibBytes, 10) + "GB"
	lines := strings.Split(text, "\n")
	section := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.ToLower(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if section != "wsl2" || !ok || !strings.EqualFold(strings.TrimSpace(key), "memory") {
			continue
		}
		if have, ok := wslSize(value); ok && have <= ceiling {
			return text, false
		}
		if strings.HasSuffix(line, "\r") {
			want += "\r"
		}
		lines[i] = want
		return strings.Join(lines, "\n"), true
	}
	return text, false
}

// wslSize reads a .wslconfig size, the way WSL does: a number of bytes, or
// with KB, MB, GB or TB (powers of two), the B optional.
func wslSize(s string) (int64, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if i := strings.IndexAny(s, "#;"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = strings.TrimSuffix(s, "B")
	scale := int64(1)
	for unit, n := range map[string]int64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40} {
		if v, ok := strings.CutSuffix(s, unit); ok {
			s, scale = strings.TrimSpace(v), n
			break
		}
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return int64(n * float64(scale)), true
}

// fitMemory holds the user's .wslconfig to the ceiling for this computer's
// memory (windowsMemory), saying so on Log when it changes it.
func (d *Distro) fitMemory() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return fitMemoryFile(filepath.Join(home, ".wslconfig"), windowsMemory(), d.logf)
}

func fitMemoryFile(path string, total int64, logf func(format string, args ...any)) error {
	ceiling := wslCeiling(total)
	if ceiling == 0 {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	text, changed := fitWSLConfig(string(b), ceiling)
	if !changed {
		return nil
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return fmt.Errorf("holding WSL's memory to %d GB in %s: %w", ceiling/gibBytes, path, err)
	}
	logf("==> WSL may use at most %d GB of this computer's %.0f GB (memory= in %s), from WSL's next start (wsl --shutdown)\n",
		ceiling/gibBytes, float64(total)/float64(gibBytes), path)
	return nil
}

func (d *Distro) logf(format string, args ...any) {
	if d.Log != nil {
		_, _ = fmt.Fprintf(d.Log, format, args...)
	}
}
