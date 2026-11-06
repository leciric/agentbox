package daemon

import (
	"bufio"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
)

// processStart is when this daemon's process started, for its uptime.
var processStart = time.Now()

// debugStats serves /debug/stats on the socket (socketHandler): the figures
// that tell a leak from a daemon that is merely busy, read in a moment
// rather than from a profile.
func (s *Server) debugStats(w http.ResponseWriter, _ *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	out := api.DebugStats{
		UptimeSeconds: time.Since(processStart).Seconds(),
		Goroutines:    runtime.NumGoroutine(),
		HeapAlloc:     ms.HeapAlloc,
		HeapInuse:     ms.HeapInuse,
		HeapIdle:      ms.HeapIdle,
		HeapReleased:  ms.HeapReleased,
		StackInuse:    ms.StackInuse,
		Sys:           ms.Sys,
		MemoryLimit:   debug.SetMemoryLimit(-1),
		NumGC:         ms.NumGC,
		GCCPUFraction: ms.GCCPUFraction,
	}
	readProcStatus(&out)
	if fds, err := os.ReadDir("/proc/self/fd"); err == nil {
		out.FDs = len(fds)
	}
	if s.store != nil {
		st := s.store.DB().Stats()
		out.DBOpen, out.DBInUse = st.OpenConnections, st.InUse
	}
	if s.chat != nil {
		out.Chats, out.ChatsLoaded, out.ChatItems = s.chat.Sizes()
	}
	if s.jobs != nil {
		s.jobs.mu.Lock()
		out.Jobs = len(s.jobs.byID)
		s.jobs.mu.Unlock()
	}
	out.EventSubscribers = s.events.subscribers()
	_ = writeJSON(w, http.StatusOK, out)
}

// readProcStatus fills in what Linux's /proc says of the process: threads,
// resident and locked memory, and CPU time. Elsewhere they stay 0.
func readProcStatus(out *api.DebugStats) {
	if f, err := os.Open("/proc/self/status"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			key, value, ok := strings.Cut(sc.Text(), ":")
			if !ok {
				continue
			}
			fields := strings.Fields(value)
			if len(fields) == 0 {
				continue
			}
			n, _ := strconv.ParseInt(fields[0], 10, 64)
			switch key {
			case "Threads":
				out.Threads = int(n)
			case "VmRSS":
				out.RSS = n << 10
			case "VmLck":
				out.Locked = n << 10
			}
		}
		_ = f.Close()
	}
	// utime and stime, fields 14 and 15, after the command's parentheses.
	if b, err := os.ReadFile("/proc/self/stat"); err == nil {
		if i := strings.LastIndexByte(string(b), ')'); i >= 0 {
			fields := strings.Fields(string(b[i+1:]))
			if len(fields) > 12 {
				utime, _ := strconv.ParseInt(fields[11], 10, 64)
				stime, _ := strconv.ParseInt(fields[12], 10, 64)
				out.CPUSeconds = float64(utime+stime) / clockTicks
			}
		}
	}
}

// clockTicks is USER_HZ, what /proc/self/stat counts CPU time in: 100 on
// every Linux the daemon runs on.
const clockTicks = 100
