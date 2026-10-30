package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newDebugCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Look inside the running daemon: Go profiles from its socket",
	}
	cmd.AddCommand(newDebugHeapCmd(a))
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
