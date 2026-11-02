package daemon

import (
	"context"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"agentbox/internal/chat"
)

// socketHandler is the API as the socket serves it: routes(), plus Go's
// profiler under /debug/pprof/ (agentbox debug heap). Only the socket has it,
// which is as private as state.db: routes() is also what phones on the LAN and
// the hub reach (lan.go, remote.go), and a profile names projects and paths.
func (s *Server) socketHandler() http.Handler {
	api := s.routes()
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/", api)
	return mux
}

// DefaultMemoryLimit is the daemon's soft memory limit (debug.SetMemoryLimit)
// when GOMEMLIMIT doesn't set one. Without one, the collector lets the heap
// grow to twice what was live at the last collection, and a burst (a long
// transcript loaded, a big diff) left the VM's daemon at 400–600 MB. Soft:
// near it the GC works harder; it never fails an allocation.
const DefaultMemoryLimit = 256 << 20

// setMemoryLimit applies DefaultMemoryLimit unless GOMEMLIMIT is set, which
// the runtime has already read ("off" turns the limit off).
func setMemoryLimit() int64 {
	if strings.TrimSpace(os.Getenv("GOMEMLIMIT")) != "" {
		return debug.SetMemoryLimit(-1)
	}
	debug.SetMemoryLimit(DefaultMemoryLimit)
	return DefaultMemoryLimit
}

// unloadChats lets go, every few minutes, of the transcripts of the chats
// nobody has read or written for chat.UnloadAfter (chat.Manager.Unload).
func (s *Server) unloadChats(ctx context.Context) {
	tick := time.NewTicker(chat.UnloadAfter / 2)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.chat.Unload(chat.UnloadAfter)
		}
	}
}
