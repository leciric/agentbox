package hostvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/paths"
)

// The VM has no swap until `agentbox vm swap on` gives it some: a swapfile on
// its own disk, made and turned on while it runs, and listed in its
// /etc/fstab, so the VM turns it on again whenever it boots. The front end
// does it the way it does host setup, with a script run in the VM over
// exec, the same for Lima and Cloud Hypervisor. What it last made is also
// kept on the host (swapSettingFile), so `vm status` can say it without
// reaching into the VM, and say it while the VM is off.

const (
	// swapPath is the swapfile in the VM.
	swapPath = "/swapfile"
	// MinSwap is the smallest swapfile `vm swap on` makes.
	MinSwap = int64(512) << 20
)

// swapSettingFile is where the host keeps the size of the VM's swapfile.
func swapSettingFile(p paths.Paths, name string) string {
	return filepath.Join(p.Config, "vm", name+"-swap.json")
}

type swapSetting struct {
	Size int64 `json:"size"`
}

// SwapSize is the swapfile `vm swap` last made in the VM named name: 0 when
// swap is off, or was never turned on.
func SwapSize(p paths.Paths, name string) int64 {
	b, err := os.ReadFile(swapSettingFile(p, name))
	if err != nil {
		return 0
	}
	var s swapSetting
	if json.Unmarshal(b, &s) != nil {
		return 0
	}
	return max(s.Size, 0)
}

func writeSwapSize(p paths.Paths, name string, size int64) error {
	file := swapSettingFile(p, name)
	if size == 0 {
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(swapSetting{Size: size})
	return os.WriteFile(file, b, 0o644)
}

// withSwap adds the setting to what a VM's status says of its swap.
func withSwap(st *api.VMStatus, p paths.Paths) {
	if st.Mode != api.ModeVM || st.State == api.VMMissing {
		return
	}
	if st.Swap == nil {
		st.Swap = &api.VMSwap{}
	}
	st.Swap.Size = SwapSize(p, st.Name)
}

// swapProbe prints what parseSwapProbe reads, in one round trip: the size of
// the VM's root filesystem and what it has free, the swapfile's size, and
// whether it's on.
const swapProbe = `df -B1 --output=size,avail / | tail -n1
stat -c %s ` + swapPath + ` 2>/dev/null || echo 0
grep -c '^` + swapPath + ` ' /proc/swaps || true`

// swapDisk is the VM's root disk as swapProbe finds it, in bytes.
type swapDisk struct {
	Total, Avail int64
	Current      int64 // the swapfile there is now; 0 for none
	Active       bool  // it's on
}

func parseSwapProbe(out string) (swapDisk, error) {
	f := strings.Fields(out)
	if len(f) != 4 {
		return swapDisk{}, fmt.Errorf("couldn't read the VM's disk from %q", firstLine(out))
	}
	var n [4]int64
	for i, s := range f {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return swapDisk{}, fmt.Errorf("couldn't read the VM's disk from %q", firstLine(out))
		}
		n[i] = v
	}
	return swapDisk{Total: n[0], Avail: n[1], Current: n[2], Active: n[3] > 0}, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// checkSwap refuses a swapfile of size that would leave the VM's disk with
// less free than the disk guard keeps (#148): the daemon would pause agents
// the moment it was made. The swapfile there now is given back first.
func checkSwap(d swapDisk, size int64, floor agent.DiskFloor) error {
	keep := floor.For(d.Total)
	room := d.Avail + d.Current - keep
	if size <= room {
		return nil
	}
	most := room / MinSwap * MinSwap
	if most < MinSwap {
		return fmt.Errorf("the VM's disk has %s free and keeps %s of it free (the disk floor, in Settings): there's no room for swap. Free some space, or lower the floor",
			agent.HumanBytes(d.Avail+d.Current), agent.HumanBytes(keep))
	}
	return fmt.Errorf("a %s swapfile would leave %s free on the VM's disk, under the %s it keeps free (the disk floor, in Settings): make it at most %s",
		sizeWords(size), agent.HumanBytes(max(d.Avail+d.Current-size, 0)), agent.HumanBytes(keep), sizeWords(most))
}

// swapScript is run as root in the VM: it takes the swapfile there is off and
// away, and makes one of size in its place unless size is 0. swapoff brings
// what's swapped out back into memory, so it can fail on a VM short of it;
// the script then stops there, with the swapfile as it was.
func swapScript(size int64) string {
	s := fmt.Sprintf(`set -e
f=%[1]s
if grep -q "^$f " /proc/swaps; then
  swapoff "$f" || { echo "the VM hasn't the memory free to take back what's in its swap: stop an agent or two and try again" >&2; exit 1; }
fi
rm -f "$f"
sed -i '\|^%[1]s[[:space:]]|d' /etc/fstab
`, swapPath)
	if size == 0 {
		return s
	}
	return s + fmt.Sprintf(`fallocate -l %[1]d "$f" 2>/dev/null || dd if=/dev/zero of="$f" bs=1M count=%[2]d status=none
chmod 600 "$f"
mkswap "$f" >/dev/null
swapon "$f"
echo "$f none swap sw,nofail 0 0" >>/etc/fstab
`, size, size>>20)
}

// diskFloor is the floor the VM's daemon keeps, or the default when it can't
// say.
func (v *VM) diskFloor(ctx context.Context) agent.DiskFloor {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s, err := api.NewClient(v.Paths.Socket()).Settings(ctx)
	if err != nil || s.DiskFloorMin == 0 {
		return agent.DefaultDiskFloor
	}
	return agent.DiskFloor{Min: s.DiskFloorMin, Percent: s.DiskFloorPercent}
}

// running says whether the VM runs, which a change to its swap needs.
func (v *VM) running(ctx context.Context) error {
	if v.CHV != nil {
		st := chvStatus(ctx, v.CHV.Config, v.CHV.Layout, v.Paths)
		switch {
		case st.State == api.VMMissing:
			return ErrNotCreated
		case st.State != api.VMRunning:
			return fmt.Errorf("AgentBox's VM is %s: start it (agentbox vm start), then change its swap", st.State)
		}
		return nil
	}
	st, err := v.State(ctx)
	switch {
	case err != nil:
		return err
	case !st.Exists:
		return ErrNotCreated
	case st.Status != "Running":
		return fmt.Errorf("AgentBox's VM is %s: start it (agentbox vm start), then change its swap", strings.ToLower(st.Status))
	}
	return nil
}

// Swap gives the running VM a swapfile of size, or turns its swap off with
// size 0, while every agent keeps running.
func (v *VM) Swap(ctx context.Context, size int64) error {
	if size != 0 {
		if size < MinSwap {
			return fmt.Errorf("swap is at least %s; %s is too little to be worth it", sizeWords(MinSwap), sizeWords(size))
		}
		size = size >> 20 << 20 // whole MiB, for dd
	}
	unlock, err := v.lock(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	if err := v.running(ctx); err != nil {
		return err
	}
	out, err := v.exec(ctx, nil, "sh", "-c", swapProbe)
	if err != nil {
		return fmt.Errorf("reading the VM's disk: %w", err)
	}
	d, err := parseSwapProbe(out)
	if err != nil {
		return err
	}
	if d.Current == size && d.Active == (size > 0) {
		if err := writeSwapSize(v.Paths, v.Name, size); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(v.Log, swapWords(size, "already"))
		return nil
	}
	if size > 0 {
		if err := checkSwap(d, size, v.diskFloor(ctx)); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(v.Log, "==> Making a %s swapfile in AgentBox's VM\n", sizeWords(size))
	} else {
		_, _ = fmt.Fprintln(v.Log, "==> Turning AgentBox's VM's swap off")
	}
	start := time.Now()
	if _, err := v.exec(ctx, nil, "sudo", "sh", "-c", swapScript(size)); err != nil {
		return fmt.Errorf("changing the VM's swap: %w", err)
	}
	v.took(start)
	if err := writeSwapSize(v.Paths, v.Name, size); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(v.Log, swapWords(size, "now"))
	return nil
}

func swapWords(size int64, when string) string {
	if size == 0 {
		return "AgentBox's VM has no swap " + when + "."
	}
	return fmt.Sprintf("AgentBox's VM has %s of swap %s, a swapfile on its own disk that it keeps across restarts.", sizeWords(size), when)
}

// swapArgs reads `vm swap`'s arguments: the swapfile to make, 0 for off, or
// -1 to only say what there is. on without --size keeps the size there is,
// or makes api.VMSwapDefault.
func swapArgs(args []string, sizeSet bool, size string, current int64) (int64, error) {
	if len(args) == 0 {
		if sizeSet {
			return 0, errors.New("--size goes with on: agentbox vm swap on --size 8G")
		}
		return -1, nil
	}
	switch args[0] {
	case "off":
		if sizeSet {
			return 0, errors.New("--size goes with on, not off")
		}
		return 0, nil
	case "on":
		if !sizeSet {
			if current > 0 {
				return current, nil
			}
			return api.VMSwapDefault, nil
		}
		n, err := ParseMemory(size)
		if err != nil {
			return 0, err
		}
		return n, nil
	}
	return 0, fmt.Errorf("%q isn't on or off", args[0])
}

func newSwapCmd() *cobra.Command {
	var size string
	cmd := &cobra.Command{
		Use:   "swap [on|off]",
		Short: "Give the VM swap, or take it away, while it runs",
		Long: `The VM has no swap until you give it some. agentbox vm swap on makes a swapfile on the
VM's own disk (8GiB unless --size says otherwise) and turns it on while every agent keeps
running; the VM turns it on again whenever it starts. agentbox vm swap off takes it away,
which needs the memory free for what's swapped out. A swapfile that would leave the VM's
disk with less free than the disk floor (Settings) keeps is refused. With neither, it
says what swap the VM has.`,
		Example:   "  agentbox vm swap on --size 8G\n  agentbox vm swap off",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			vm, err := New()
			if err != nil {
				return err
			}
			bytes, err := swapArgs(args, cmd.Flags().Changed("size"), size, SwapSize(vm.Paths, vm.Name))
			if err != nil {
				return err
			}
			if bytes >= 0 {
				vm.Log = cmd.OutOrStdout()
				return vm.Swap(cmd.Context(), bytes)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), describeSwap(vm.swapStatus(cmd.Context())))
			return nil
		},
	}
	cmd.Flags().StringVar(&size, "size", "", "the swapfile's size, like 8G (on only)")
	return cmd
}

// swapStatus is what the host knows of the VM's swap without reaching into
// it: the setting, and, from a running Cloud Hypervisor VM's supervisor, what
// it has and uses.
func (v *VM) swapStatus(ctx context.Context) api.VMSwap {
	sw := api.VMSwap{Size: SwapSize(v.Paths, v.Name)}
	if v.CHV != nil {
		if st := chvStatus(ctx, v.CHV.Config, v.CHV.Layout, v.Paths); st.Swap != nil {
			sw.Total, sw.Used = st.Swap.Total, st.Swap.Used
		}
	}
	return sw
}

func describeSwap(sw api.VMSwap) string {
	switch {
	case sw.Size == 0 && sw.Total == 0:
		return "AgentBox's VM has no swap: agentbox vm swap on gives it some."
	case sw.Size == 0:
		return fmt.Sprintf("AgentBox's VM has %s of swap that agentbox didn't make, %s of it in use.", sizeWords(sw.Total), sizeWords(sw.Used))
	case sw.Total > 0:
		return fmt.Sprintf("AgentBox's VM has %s of swap, %s of it in use.", sizeWords(sw.Size), sizeWords(sw.Used))
	}
	return fmt.Sprintf("AgentBox's VM has %s of swap.", sizeWords(sw.Size))
}
