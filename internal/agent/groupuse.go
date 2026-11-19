package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/pressure"
)

// GroupUse reads a parent cgroup's memory as it is now (agentbox.agents',
// for GET /v1/pressure): memory.current, memory.high and memory.max (0 for
// none), and how much its tasks stalled on memory lately, memory.pressure's
// some avg10. ok is false when it isn't there.
func GroupUse(dir string) (api.AgentsMemory, bool) {
	var u api.AgentsMemory
	b, err := os.ReadFile(filepath.Join(dir, "memory.current"))
	if err != nil {
		return u, false
	}
	u.Used, _ = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	for file, into := range map[string]*int64{"memory.high": &u.High, "memory.max": &u.Max} {
		if b, err := os.ReadFile(filepath.Join(dir, file)); err == nil {
			*into, _ = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64) // "max": 0
		}
	}
	if psi, err := pressure.Read(filepath.Join(dir, "memory.pressure")); err == nil {
		u.Stalled = psi.Some.Avg10
	}
	return u, true
}
