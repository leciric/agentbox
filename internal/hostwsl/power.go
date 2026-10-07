package hostwsl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// The app's top bar shows AgentBox's VM, its memory and Free resources
// through `agentbox vm power|start|stop` (internal/api/vm.go has the
// contract). On Windows the front end answers those itself rather than
// forwarding them, because a command run in a stopped distro starts it again:
// the top bar's poll would bring back the distro Free resources had just
// stopped. The rest of `agentbox vm` is forwarded, and says there's no VM.

// Power is `agentbox vm power --json` for the distro: the shape the app reads
// (hostvm.Power), with Driver telling it apart from a VM it could pause.
type Power struct {
	State         string `json:"state"` // api.VMRunning or api.VMOff
	MemoryUsed    int64  `json:"memoryUsed"`
	MemoryGranted int64  `json:"memoryGranted"`
	MemoryCap     int64  `json:"memoryCap"`
	CPUs          int    `json:"cpus"`
	Driver        string `json:"driver"` // api.VMDriverWSL
	Error         string `json:"error,omitempty"`
}

// vmCommand reports whether args are an `agentbox vm` command the front end
// answers itself.
func vmCommand(args []string) bool {
	if len(args) < 2 || args[0] != "vm" {
		return false
	}
	switch args[1] {
	case "power", "start", "stop", "pause", "resume":
		return true
	}
	return false
}

func newVMCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "agentbox vm",
		Short:         "AgentBox's WSL distro, as the app's top bar sees it",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	var asJSON bool
	power := &cobra.Command{
		Use:   "power",
		Short: "Say whether the distro runs, and how much memory it uses (for the app)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := distro()
			p := Power{State: api.VMOff, Driver: api.VMDriverWSL}
			if err != nil {
				p.Error = err.Error()
			} else {
				p = d.Power(cmd.Context())
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(p)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s, %d CPUs, %s in use of %s\n", d.Name, p.State, p.CPUs, gib(p.MemoryUsed), gib(p.MemoryGranted))
			if p.Error != "" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), p.Error)
			}
			return nil
		},
	}
	power.Flags().BoolVar(&asJSON, "json", false, "print it as JSON")
	start := newStartCmd()
	start.Short = "Start the distro and its daemon (agentbox wsl start)"
	stop := &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon and the distro, giving its memory back to Windows",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := distro()
			if err != nil {
				return err
			}
			return d.Stop(cmd.Context())
		},
	}
	cantPause := func(verb string) *cobra.Command {
		return &cobra.Command{
			Use:    verb,
			Short:  "WSL can't " + verb + " a distro",
			Args:   cobra.NoArgs,
			Hidden: true,
			RunE: func(*cobra.Command, []string) error {
				return fmt.Errorf("WSL can't %s a distro: stop it instead (agentbox vm stop)", verb)
			},
		}
	}
	root.AddCommand(power, start, stop, cantPause("pause"), cantPause("resume"))
	return root
}

func gib(b int64) string { return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30)) }

// Power is the distro's state, and while it runs its memory and CPUs as its
// kernel sees them: MemTotal is all WSL lets it have (.wslconfig's memory, half
// of Windows' by default), so it's both what's granted and the cap, and what's
// used is MemTotal less MemAvailable, without the page cache. A stopped distro
// is only asked about through wsl --list, which doesn't start it.
func (d *Distro) Power(ctx context.Context) Power {
	p := Power{State: api.VMOff, Driver: api.VMDriverWSL}
	st, err := d.State(ctx)
	switch {
	case err != nil:
		p.Error = err.Error()
		return p
	case !st.Exists:
		p.Error = ErrNotCreated.Error()
		return p
	case st.State == "Installing":
		p.State = api.VMStarting
		return p
	case st.State != "Running":
		return p
	}
	p.State = api.VMRunning
	out, err := d.exec(ctx, false, nil, "sh", "-c", "cat /proc/meminfo; nproc")
	if err != nil {
		p.Error = err.Error()
		return p
	}
	total, available, cpus := parseMeminfo(out)
	p.MemoryGranted, p.MemoryCap, p.CPUs = total, total, cpus
	p.MemoryUsed = max(total-available, 0)
	return p
}

// parseMeminfo reads /proc/meminfo, in kB, followed by nproc's line.
func parseMeminfo(out string) (total, available int64, cpus int) {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		switch {
		case len(fields) == 1:
			cpus, _ = strconv.Atoi(fields[0])
		case len(fields) >= 2 && fields[0] == "MemTotal:":
			n, _ := strconv.ParseInt(fields[1], 10, 64)
			total = n << 10
		case len(fields) >= 2 && fields[0] == "MemAvailable:":
			n, _ := strconv.ParseInt(fields[1], 10, 64)
			available = n << 10
		}
	}
	return total, available, cpus
}

// Stop stops the daemon, so it closes its database and Incus cleanly, and
// then the distro, which gives Windows back the memory it held once WSL's VM
// has nothing else running. A daemon that won't stop is cut off with the
// distro, as `agentbox wsl stop` does.
func (d *Distro) Stop(ctx context.Context) error {
	st, err := d.State(ctx)
	if err != nil {
		return err
	}
	if !st.Exists {
		return ErrNotCreated
	}
	if st.State != "Running" {
		return nil
	}
	if _, err := d.exec(ctx, false, nil, "agentbox", "daemon", "stop"); err != nil && !strings.Contains(err.Error(), "no daemon is answering") {
		_, _ = fmt.Fprintf(d.Log, "The daemon didn't stop cleanly (%v): stopping %s anyway\n", err, d.Name)
	}
	return d.Shutdown(ctx)
}
