package agent

import (
	"fmt"
	"strconv"
	"strings"
)

// byteUnits are the suffixes Incus takes on a size, binary and decimal both.
var byteUnits = []struct {
	suffix string
	factor int64
}{
	{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40}, {"PiB", 1 << 50}, {"EiB", 1 << 60},
	{"kB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"TB", 1e12}, {"PB", 1e15}, {"EB", 1e18},
	{"B", 1},
}

// ParseBytes reads a size the way Incus does: a number, optionally suffixed
// with a binary (GiB) or decimal (GB) unit. A bare number is bytes.
func ParseBytes(size string) (int64, error) {
	size = strings.TrimSpace(size)
	bad := fmt.Errorf("a size is a number like 8GiB or 4096MiB; got %q", size)
	for _, unit := range byteUnits {
		rest, ok := strings.CutSuffix(size, unit.suffix)
		if !ok {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
		if err != nil || n <= 0 {
			return 0, bad
		}
		return int64(n * float64(unit.factor)), nil
	}
	n, err := strconv.ParseInt(size, 10, 64)
	if err != nil || n <= 0 {
		return 0, bad
	}
	return n, nil
}

// HumanBytes renders a size the way AgentBox shows memory everywhere else.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for rest := n / unit; rest >= unit; rest /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
