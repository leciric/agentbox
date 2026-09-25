// Package incus runs the incus command line.
//
// AgentBox shells out rather than linking the Incus Go client: these are the
// commands proven in the Step 0 spike, errors read the same as in a terminal,
// and interactive sessions (incus exec -t) get terminal handling for free.
package incus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

var ErrNotFound = errors.New("instance not found")

type Client struct {
	Bin string // path to the incus binary; "incus" when empty
}

func (c Client) Path() string {
	if c.Bin == "" {
		return "incus"
	}
	return c.Bin
}

// Command prepares an incus command without running it.
func (c Client) Command(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, c.Path(), args...)
}

// Run runs incus and returns its stdout. Errors carry incus' own message.
func (c Client) Run(ctx context.Context, args ...string) (string, error) {
	return c.RunInput(ctx, nil, args...)
}

func (c Client) RunInput(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	cmd := c.Command(ctx, args...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimPrefix(strings.TrimSpace(stderr.String()), "Error: ")
		if msg == "" {
			// Keep the error itself, so callers can tell that incus isn't installed.
			return stdout.String(), fmt.Errorf("incus %s: %w", strings.Join(args, " "), err)
		}
		return stdout.String(), fmt.Errorf("incus %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

type Instance struct {
	Name   string         `json:"name"`
	Status string         `json:"status"` // Running, Stopped, Frozen, ...
	State  *InstanceState `json:"state"`
	// Config is what was set on the instance itself; ExpandedConfig adds what
	// its profiles contribute, and so is what Incus actually applies. Both
	// come back from `incus list --format json` at no extra cost, which is how
	// Usage reports every agent's limits without a query per agent.
	Config         map[string]string `json:"config"`
	ExpandedConfig map[string]string `json:"expanded_config"`
}

type InstanceState struct {
	Network map[string]struct {
		Addresses []struct {
			Family  string `json:"family"`
			Address string `json:"address"`
		} `json:"addresses"`
	} `json:"network"`
	CPU struct {
		Usage int64 `json:"usage"` // nanoseconds of CPU time
	} `json:"cpu"`
	Memory struct {
		Usage int64 `json:"usage"` // bytes
	} `json:"memory"`
	Processes int64 `json:"processes"`
}

type Snapshot struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// IPv4 returns the instance's address on eth0, or "".
func (i Instance) IPv4() string {
	if i.State == nil {
		return ""
	}
	for _, addr := range i.State.Network["eth0"].Addresses {
		if addr.Family == "inet" {
			return addr.Address
		}
	}
	return ""
}

func (c Client) Instances(ctx context.Context) ([]Instance, error) {
	out, err := c.Run(ctx, "list", "--format", "json")
	if err != nil {
		return nil, err
	}
	var instances []Instance
	if err := json.Unmarshal([]byte(out), &instances); err != nil {
		return nil, fmt.Errorf("parsing incus list: %w", err)
	}
	return instances, nil
}

func (c Client) Instance(ctx context.Context, name string) (Instance, error) {
	instances, err := c.Instances(ctx)
	if err != nil {
		return Instance{}, err
	}
	for _, inst := range instances {
		if inst.Name == name {
			return inst, nil
		}
	}
	return Instance{}, fmt.Errorf("%s: %w", name, ErrNotFound)
}

// HasSnapshot reports whether an instance exists and has the named snapshot.
func (c Client) HasSnapshot(ctx context.Context, instance, snapshot string) (bool, error) {
	out, err := c.Run(ctx, "query", "/1.0/instances/"+instance+"/snapshots")
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, err
	}
	var snapshots []string
	if err := json.Unmarshal([]byte(out), &snapshots); err != nil {
		return false, fmt.Errorf("parsing snapshots of %s: %w", instance, err)
	}
	return slices.Contains(snapshots, "/1.0/instances/"+instance+"/snapshots/"+snapshot), nil
}

// Snapshots lists an instance's snapshots, oldest first.
func (c Client) Snapshots(ctx context.Context, instance string) ([]Snapshot, error) {
	out, err := c.Run(ctx, "query", "/1.0/instances/"+instance+"/snapshots?recursion=1")
	if err != nil {
		return nil, err
	}
	var snapshots []Snapshot
	if err := json.Unmarshal([]byte(out), &snapshots); err != nil {
		return nil, fmt.Errorf("parsing snapshots of %s: %w", instance, err)
	}
	slices.SortFunc(snapshots, func(a, b Snapshot) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return snapshots, nil
}

// Details is what an instance is configured with, on the instance itself
// rather than through its profiles.
type Details struct {
	Config         map[string]string            `json:"config"`
	ExpandedConfig map[string]string            `json:"expanded_config"`
	Devices        map[string]map[string]string `json:"devices"`
}

// Details reads an instance's own configuration and devices in one query, for
// callers that want both: build and SaveBase each look at the devices a copy
// brought along and at the limits baked into it.
func (c Client) Details(ctx context.Context, instance string) (Details, error) {
	out, err := c.Run(ctx, "query", "/1.0/instances/"+instance)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return Details{}, fmt.Errorf("%s: %w", instance, ErrNotFound)
		}
		return Details{}, err
	}
	var d Details
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		return Details{}, fmt.Errorf("parsing %s: %w", instance, err)
	}
	return d, nil
}

// Config returns an instance's own configuration keys (not those from profiles).
func (c Client) Config(ctx context.Context, instance string) (map[string]string, error) {
	d, err := c.Details(ctx, instance)
	return d.Config, err
}

// Devices returns the devices configured on the instance itself (not those from profiles).
func (c Client) Devices(ctx context.Context, instance string) (map[string]map[string]string, error) {
	d, err := c.Details(ctx, instance)
	return d.Devices, err
}

// PoolSpace returns the used and total bytes of a storage pool.
func (c Client) PoolSpace(ctx context.Context, pool string) (used, total int64, err error) {
	out, err := c.Run(ctx, "query", "/1.0/storage-pools/"+pool+"/resources")
	if err != nil {
		return 0, 0, err
	}
	var r struct {
		Space struct{ Used, Total int64 }
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return 0, 0, fmt.Errorf("parsing pool %s: %w", pool, err)
	}
	return r.Space.Used, r.Space.Total, nil
}

// VolumeUsage returns the bytes an instance's own storage volume uses on the
// pool. Unlike Instances' State.Memory/CPU, this comes from the volume itself
// rather than the running instance, so it works for a stopped instance too —
// a saved base, most of the time.
func (c Client) VolumeUsage(ctx context.Context, pool, instance string) (int64, error) {
	out, err := c.Run(ctx, "query", "/1.0/storage-pools/"+pool+"/volumes/container/"+instance+"/state")
	if err != nil {
		return 0, err
	}
	var r struct {
		Usage struct {
			Used int64 `json:"used"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return 0, fmt.Errorf("parsing volume state of %s: %w", instance, err)
	}
	return r.Usage.Used, nil
}

// WaitReady waits for an instance to finish booting and get an IPv4 address.
func (c Client) WaitReady(ctx context.Context, name string, timeout time.Duration) (Instance, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Fails for a "degraded" system too, which is fine for agents.
	c.Run(ctx, "exec", name, "--", "systemctl", "is-system-running", "--wait")
	for {
		inst, err := c.Instance(ctx, name)
		if err == nil && inst.IPv4() != "" {
			return inst, nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return inst, fmt.Errorf("%s did not get an IPv4 address within %s", name, timeout)
			}
			return inst, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// UserExec runs a bash command in a login shell of user inside the instance.
func (c Client) UserExec(ctx context.Context, name, user, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := c.Command(ctx, "exec", name, "--", "runuser", "-l", user, "-c", command)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}

// WriteFile writes content to path inside the instance, creating parent
// directories. The content goes through stdin, never the command line. It is
// read with cat: reopening /dev/stdin fails in unprivileged containers, because
// the pipe belongs to host root.
func (c Client) WriteFile(ctx context.Context, name, path string, content []byte, uid, gid int, mode os.FileMode) error {
	const script = `set -e; mkdir -p "$(dirname "$1")"; (umask 077 && cat >"$1"); chown "$2" "$1"; chmod "$3" "$1"`
	_, err := c.RunInput(ctx, bytes.NewReader(content), "exec", name, "-T", "--",
		"sh", "-c", script, "sh", path, fmt.Sprintf("%d:%d", uid, gid), strconv.FormatUint(uint64(mode.Perm()), 8))
	return err
}
