package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"

	"github.com/spf13/cobra"
)

func newDebugCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Look inside the running daemon: Go profiles from its socket",
	}
	cmd.AddCommand(newDebugHeapCmd(a), newDebugPprofCmd(a), newDebugStatsCmd(a))
	return cmd
}

// pprofProfiles are the profiles `debug pprof` takes, with what each is.
var pprofProfiles = []struct{ name, what string }{
	{"heap", "live memory by allocation site, after a garbage collection"},
	{"allocs", "every allocation since the daemon started, by site"},
	{"goroutine", "every goroutine's stack"},
	{"profile", "where the CPU goes, sampled over --seconds"},
	{"threadcreate", "the stacks that made new OS threads"},
	{"block", "where goroutines blocked (empty unless the rate was set)"},
	{"mutex", "contended mutexes (empty unless the rate was set)"},
	{"trace", "an execution trace over --seconds, for go tool trace"},
}

func newDebugPprofCmd(a *app) *cobra.Command {
	var (
		out     string
		seconds int
		text    bool
	)
	var names []string
	long := ""
	for _, p := range pprofProfiles {
		names = append(names, p.name)
		long += fmt.Sprintf("  %-13s %s\n", p.name, p.what)
	}
	cmd := &cobra.Command{
		Use:       "pprof <" + strings.Join(names, "|") + ">",
		Short:     "Save one of the running daemon's Go profiles, read from its socket",
		ValidArgs: names,
		Long: `Saves one of the daemon's Go profiles (net/http/pprof), from its socket only:
nothing on the network serves them. Read it with go tool pprof:

` + long + `
  agentbox debug pprof goroutine --text | head -50
  agentbox debug pprof profile --seconds 30 -o /tmp/cpu.pb.gz
  go tool pprof -top /tmp/cpu.pb.gz
  agentbox debug pprof heap -o /tmp/heap.pb.gz
  go tool pprof -top -sample_index=inuse_space /tmp/heap.pb.gz`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !slices.Contains(names, name) {
				return fmt.Errorf("%q isn't one of the profiles: %s", name, strings.Join(names, ", "))
			}
			query := url.Values{}
			switch name {
			case "profile", "trace":
				if text {
					return fmt.Errorf("--text is for the other profiles: %s is read with go tool", name)
				}
				query.Set("seconds", strconv.Itoa(max(seconds, 1)))
			case "heap":
				query.Set("gc", "1")
			}
			if text {
				query.Set("debug", "1")
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if text && out == "" {
				return c.Profile(cmd.Context(), name, query.Encode(), cmd.OutOrStdout())
			}
			if out == "" {
				ext := ".pb.gz"
				if name == "trace" {
					ext = ".out"
				} else if text {
					ext = ".txt"
				}
				out = "agentbox-" + name + "-" + time.Now().Format("20060102-150405") + ext
			}
			if name == "profile" || name == "trace" {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Sampling the daemon for %d s...\n", max(seconds, 1))
			}
			var profile bytes.Buffer
			if err := c.Profile(cmd.Context(), name, query.Encode(), &profile); err != nil {
				return fmt.Errorf("reading the daemon's %s profile: %w", name, err)
			}
			if err := os.WriteFile(out, profile.Bytes(), 0o600); err != nil {
				return err
			}
			abs, _ := filepath.Abs(out)
			read := "go tool pprof -top " + abs
			if name == "trace" {
				read = "go tool trace " + abs
			} else if text {
				read = "less " + abs
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Saved %s\nRead it with: %s\n", abs, read)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "where to save it (default agentbox-<profile>-<time>.pb.gz here)")
	cmd.Flags().IntVar(&seconds, "seconds", 30, "how long profile and trace sample for")
	cmd.Flags().BoolVar(&text, "text", false, "the profile as text (debug=1), to stdout unless -o is given")
	return cmd
}

func newDebugHeapCmd(a *app) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "heap",
		Short: "Save the daemon's heap profile, after a garbage collection, and sum up its memory",
		Long: `Saves what the daemon's heap holds, by allocation site, as a pprof profile, and
prints how much is live and how much the process has from the OS. Read it with Go's
tools:

  agentbox debug heap                          # agentbox-heap-<time>.pb.gz here
  agentbox debug heap -o /tmp/heap.pb.gz
  go tool pprof -top -sample_index=inuse_space /tmp/heap.pb.gz

The same profiles are at /debug/pprof/ on the daemon's socket:
  curl --unix-socket ~/.local/share/agentbox/run/agentbox.sock http://agentbox/debug/pprof/`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if out == "" {
				out = "agentbox-heap-" + time.Now().Format("20060102-150405") + ".pb.gz"
			}
			var profile bytes.Buffer
			if err := c.Profile(cmd.Context(), "heap", "gc=1", &profile); err != nil {
				return fmt.Errorf("reading the daemon's heap profile: %w", err)
			}
			if err := os.WriteFile(out, profile.Bytes(), 0o600); err != nil {
				return err
			}
			// debug=1 is the same profile as text, ending in runtime.MemStats.
			var text bytes.Buffer
			if err := c.Profile(cmd.Context(), "heap", "debug=1", &text); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			writeHeapSummary(w, &text)
			abs, _ := filepath.Abs(out)
			_, _ = fmt.Fprintf(w, "Saved %s\nRead it with: go tool pprof -top -sample_index=inuse_space %s\n", abs, abs)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "where to save the profile (default agentbox-heap-<time>.pb.gz)")
	return cmd
}

// heapStats are the runtime.MemStats lines of a debug=1 heap profile worth
// printing, in MiB.
var heapStats = []struct{ key, label string }{
	{"HeapAlloc", "live heap (and garbage not yet collected)"},
	{"HeapInuse", "heap spans in use"},
	{"HeapIdle", "heap spans idle"},
	{"HeapReleased", "of them returned to the OS"},
	{"Sys", "from the OS in all"},
}

func writeHeapSummary(w io.Writer, text io.Reader) {
	found := map[string]uint64{}
	gcs := ""
	sc := bufio.NewScanner(text)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		key, value, ok := strings.Cut(strings.TrimPrefix(sc.Text(), "# "), " = ")
		if !ok {
			continue
		}
		if key == "NumGC" {
			gcs = value
		}
		var n uint64
		if _, err := fmt.Sscan(value, &n); err == nil {
			found[key] = n
		}
	}
	for _, s := range heapStats {
		if n, ok := found[s.key]; ok {
			_, _ = fmt.Fprintf(w, "%-13s %7.1f MiB  %s\n", s.key, float64(n)/(1<<20), s.label)
		}
	}
	if gcs != "" {
		_, _ = fmt.Fprintf(w, "%-13s %7s      garbage collections so far\n", "NumGC", gcs)
	}
}

func newDebugStatsCmd(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Print the daemon's process and memory figures: what to watch for a leak",
		Long: `Prints the running daemon's goroutines, OS threads, open files, resident and
locked memory, Go heap, state.db connections, and what it keeps in memory
(chats and their items, jobs, event subscribers). Run it now and again later:
what only ever grows is the leak.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			st, err := c.DebugStats(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(st)
			}
			writeDebugStats(cmd.OutOrStdout(), st)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func writeDebugStats(w io.Writer, st api.DebugStats) {
	mib := func(n uint64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }
	uptime := time.Duration(st.UptimeSeconds) * time.Second
	cpu := 0.0
	if st.UptimeSeconds > 0 {
		cpu = 100 * st.CPUSeconds / st.UptimeSeconds
	}
	rows := [][2]string{
		{"uptime", fmt.Sprintf("%s, %.0f s of CPU (%.1f%% of a core on average)", uptime, st.CPUSeconds, cpu)},
		{"goroutines", strconv.Itoa(st.Goroutines)},
		{"OS threads", strconv.Itoa(st.Threads)},
		{"open files", strconv.Itoa(st.FDs)},
		{"resident", mib(uint64(st.RSS)) + ", of it locked " + mib(uint64(st.Locked))},
		{"Go heap", fmt.Sprintf("%s live, %s in spans, %s idle (%s given back)", mib(st.HeapAlloc), mib(st.HeapInuse), mib(st.HeapIdle), mib(st.HeapReleased))},
		{"Go stacks", mib(st.StackInuse)},
		{"Go runtime", fmt.Sprintf("%s from the OS, soft limit %s", mib(st.Sys), mib(uint64(st.MemoryLimit)))},
		{"GC", fmt.Sprintf("%d collections, %.1f%% of CPU", st.NumGC, 100*st.GCCPUFraction)},
		{"state.db", fmt.Sprintf("%d connections open, %d in use", st.DBOpen, st.DBInUse)},
		{"chats", fmt.Sprintf("%d in memory, %d with their transcript loaded (%d items)", st.Chats, st.ChatsLoaded, st.ChatItems)},
		{"jobs", fmt.Sprintf("%d in memory", st.Jobs)},
		{"event streams", strconv.Itoa(st.EventSubscribers)},
	}
	for _, r := range rows {
		_, _ = fmt.Fprintf(w, "%-14s %s\n", r[0], r[1])
	}
}
