// Package incus drives Incus for the daemon.
//
// It talks to Incus' REST API over the local unix socket, through Incus' own
// Go client: what the incus command does, without starting one per call. Each
// operation still knows the command it stands for, which is what its errors
// name, so they read as they did when AgentBox shelled out, and what runs
// instead when Bin is set — how tests stand a shell script in for Incus.
//
// A few things stay on the command line, where a process is what the caller
// wants: an interactive shell (ShellCommand in internal/agent, with its
// terminal handling), a long-lived pipe to an agent's ACP adapter, the image
// build's scripts, whose output streams into its log, and Init, which reads
// the user's image remotes. Command and Path are for those.
package incus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"

	incusclient "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

var ErrNotFound = errors.New("instance not found")

type Client struct {
	// Bin is the incus binary. When it's set, every operation runs it with the
	// arguments the operation stands for, instead of calling the API: tests set
	// it to a fake. Command and Path use "incus" when it's empty.
	Bin string
}

func (c Client) Path() string {
	if c.Bin == "" {
		return "incus"
	}
	return c.Bin
}

// cli reports whether operations run the incus binary instead of the API.
func (c Client) cli() bool { return c.Bin != "" }

// Command prepares an incus command without running it.
func (c Client) Command(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, c.Path(), args...)
}

// run runs incus and returns its stdout. Errors carry incus' own message.
func (c Client) run(ctx context.Context, args ...string) (string, error) {
	return c.runInput(ctx, nil, args...)
}

func (c Client) runInput(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
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

// query runs `incus query path` and decodes its JSON into v; what names what
// was read, when it can't be parsed.
func (c Client) query(ctx context.Context, path string, v any, what string) error {
	out, err := c.run(ctx, "query", path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(out), v); err != nil {
		return fmt.Errorf("parsing %s: %w", what, err)
	}
	return nil
}

// Ping checks that Incus answers: the incus command is installed, since
// terminals and the image build still run it, and the daemon's API answers
// this process. It is what `incus query /1.0` checked, and fails the same way.
func (c Client) Ping(ctx context.Context) error {
	args := []string{"query", "/1.0"}
	if c.cli() {
		_, err := c.run(ctx, args...)
		return err
	}
	if _, err := exec.LookPath(c.Path()); err != nil {
		return c.fail(args, err)
	}
	return c.do(ctx, args, func(s incusclient.InstanceServer) error {
		_, _, err := s.GetServer()
		return err
	})
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
	Network map[string]Network `json:"network"`
	CPU     struct {
		Usage int64 `json:"usage"` // nanoseconds of CPU time
	} `json:"cpu"`
	Memory struct {
		Usage int64 `json:"usage"` // bytes
	} `json:"memory"`
	Processes int64 `json:"processes"`
}

// Network is one of an instance's network interfaces.
type Network struct {
	Addresses []Address `json:"addresses"`
}

type Address struct {
	Family  string `json:"family"`
	Address string `json:"address"`
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

// Instances lists every instance with its state, in Incus' own order, as
// `incus list --format json` does.
func (c Client) Instances(ctx context.Context) ([]Instance, error) {
	args := []string{"list", "--format", "json"}
	if c.cli() {
		out, err := c.run(ctx, args...)
		if err != nil {
			return nil, err
		}
		var instances []Instance
		if err := json.Unmarshal([]byte(out), &instances); err != nil {
			return nil, fmt.Errorf("parsing incus list: %w", err)
		}
		return instances, nil
	}
	var full []api.InstanceFull
	err := c.do(ctx, args, func(s incusclient.InstanceServer) (err error) {
		full, err = s.GetInstancesFull(api.InstanceTypeAny)
		return err
	})
	if err != nil {
		return nil, err
	}
	instances := make([]Instance, len(full))
	for i := range full {
		instances[i] = instanceOf(&full[i])
	}
	return instances, nil
}

// Instance returns one instance with its state, or ErrNotFound.
func (c Client) Instance(ctx context.Context, name string) (Instance, error) {
	if !c.cli() {
		var full *api.InstanceFull
		s, err := server(ctx)
		if err == nil {
			full, _, err = s.GetInstanceFull(name)
		}
		if err != nil {
			if isNotFound(err) {
				return Instance{}, errNotFound(name, err)
			}
			return Instance{}, c.fail([]string{"list", "--format", "json"}, err)
		}
		return instanceOf(full), nil
	}
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

// instanceOf keeps what Instance holds of what Incus answers: the same fields
// `incus list --format json` gave, read from the same JSON.
func instanceOf(full *api.InstanceFull) Instance {
	inst := Instance{
		Name:           full.Name,
		Status:         full.Status,
		Config:         full.Config,
		ExpandedConfig: full.ExpandedConfig,
	}
	if st := full.State; st != nil {
		inst.State = &InstanceState{}
		inst.State.CPU.Usage = st.CPU.Usage
		inst.State.Memory.Usage = st.Memory.Usage
		inst.State.Processes = st.Processes
		if st.Network != nil {
			inst.State.Network = make(map[string]Network, len(st.Network))
			for name, network := range st.Network {
				var n Network
				for _, addr := range network.Addresses {
					n.Addresses = append(n.Addresses, Address{Family: addr.Family, Address: addr.Address})
				}
				inst.State.Network[name] = n
			}
		}
	}
	return inst
}

// HasSnapshot reports whether an instance exists and has the named snapshot.
func (c Client) HasSnapshot(ctx context.Context, instance, snapshot string) (bool, error) {
	path := "/1.0/instances/" + instance + "/snapshots"
	var names []string
	var err error
	if c.cli() {
		var urls []string
		err = c.query(ctx, path, &urls, "snapshots of "+instance)
		for _, u := range urls {
			names = append(names, strings.TrimPrefix(u, path+"/"))
		}
	} else {
		err = c.do(ctx, []string{"query", path}, func(s incusclient.InstanceServer) (err error) {
			names, err = s.GetInstanceSnapshotNames(instance)
			return err
		})
	}
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, err
	}
	return slices.Contains(names, snapshot), nil
}

// Snapshots lists an instance's snapshots, oldest first.
func (c Client) Snapshots(ctx context.Context, instance string) ([]Snapshot, error) {
	path := "/1.0/instances/" + instance + "/snapshots?recursion=1"
	var snapshots []Snapshot
	if c.cli() {
		if err := c.query(ctx, path, &snapshots, "snapshots of "+instance); err != nil {
			return nil, err
		}
	} else {
		err := c.do(ctx, []string{"query", path}, func(s incusclient.InstanceServer) error {
			all, err := s.GetInstanceSnapshots(instance)
			for _, snap := range all {
				snapshots = append(snapshots, Snapshot{Name: snap.Name, CreatedAt: snap.CreatedAt})
			}
			return err
		})
		if err != nil {
			return nil, err
		}
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
	args := []string{"query", "/1.0/instances/" + instance}
	if !c.cli() {
		var d Details
		s, err := server(ctx)
		if err == nil {
			var inst *api.Instance
			if inst, _, err = s.GetInstance(instance); err == nil {
				d = Details{Config: inst.Config, ExpandedConfig: inst.ExpandedConfig, Devices: inst.Devices}
			}
		}
		if isNotFound(err) {
			return Details{}, errNotFound(instance, err)
		}
		return d, c.fail(args, err)
	}
	out, err := c.run(ctx, args...)
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
	path := "/1.0/storage-pools/" + pool + "/resources"
	if !c.cli() {
		err = c.do(ctx, []string{"query", path}, func(s incusclient.InstanceServer) error {
			r, err := s.GetStoragePoolResources(pool)
			if err == nil {
				used, total = int64(r.Space.Used), int64(r.Space.Total)
			}
			return err
		})
		return used, total, err
	}
	var r struct {
		Space struct{ Used, Total int64 }
	}
	if err := c.query(ctx, path, &r, "pool "+pool); err != nil {
		return 0, 0, err
	}
	return r.Space.Used, r.Space.Total, nil
}

// PoolDriver returns a storage pool's driver, e.g. "btrfs", "zfs" or "dir".
func (c Client) PoolDriver(ctx context.Context, pool string) (string, error) {
	path := "/1.0/storage-pools/" + pool
	if !c.cli() {
		var driver string
		err := c.do(ctx, []string{"query", path}, func(s incusclient.InstanceServer) error {
			p, _, err := s.GetStoragePool(pool)
			if err == nil {
				driver = p.Driver
			}
			return err
		})
		return driver, err
	}
	var r struct{ Driver string }
	if err := c.query(ctx, path, &r, "pool "+pool); err != nil {
		return "", err
	}
	return r.Driver, nil
}

// VolumeUsage returns the bytes an instance's own storage volume uses on the
// pool. Unlike Instances' State.Memory/CPU, this comes from the volume itself
// rather than the running instance, so it works for a stopped instance too —
// a saved base, most of the time.
func (c Client) VolumeUsage(ctx context.Context, pool, instance string) (int64, error) {
	path := "/1.0/storage-pools/" + pool + "/volumes/container/" + instance + "/state"
	if !c.cli() {
		var used int64
		err := c.do(ctx, []string{"query", path}, func(s incusclient.InstanceServer) error {
			st, err := s.GetStoragePoolVolumeState(pool, "container", instance)
			if err == nil && st.Usage != nil {
				used = int64(st.Usage.Used)
			}
			return err
		})
		return used, err
	}
	var r struct {
		Usage struct {
			Used int64 `json:"used"`
		} `json:"usage"`
	}
	if err := c.query(ctx, path, &r, "volume state of "+instance); err != nil {
		return 0, err
	}
	return r.Usage.Used, nil
}

// WaitReady waits for an instance to finish booting and get an IPv4 address.
func (c Client) WaitReady(ctx context.Context, name string, timeout time.Duration) (Instance, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Fails for a "degraded" system too, which is fine for agents.
	_, _ = c.Exec(ctx, name, "systemctl", "is-system-running", "--wait")
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
