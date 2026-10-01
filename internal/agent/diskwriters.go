package agent

import "context"

// DiskWriters are the agents whose machines are running or paused, each with
// everything its machine has written to disk so far, from its cgroup's
// io.stat: what the disk guard picks the agents filling a disk from. An
// agent whose io.stat can't be read counts as having written nothing, so it's
// never the one paused. Writes a machine makes to its worktree through a
// share the VM's host serves (virtiofs) aren't block I/O in the machine and
// aren't counted. It is never nil without an error: nil tells the guard the
// agents are unknown.
func (m *Manager) DiskWriters(ctx context.Context) ([]DiskWriter, error) {
	agents, err := m.Store.Agents(ctx, "")
	if err != nil {
		return nil, err
	}
	instances, err := m.Incus.Instances(ctx)
	if err != nil {
		return nil, err
	}
	status := map[string]string{}
	for _, inst := range instances {
		status[inst.Name] = inst.Status
	}
	out := []DiskWriter{}
	for _, a := range agents {
		st := status[a.Instance]
		if a.Instance == "" || st != "Running" && st != "Frozen" {
			continue
		}
		w := DiskWriter{Ref: a.Ref(), Instance: a.Instance, Paused: st == "Frozen"}
		if io, ok := cgroupIO(agentCgroup(cgroupRoot, a.Instance), sysRoot); ok {
			w.Written = int64(io.write)
		}
		out = append(out, w)
	}
	return out, nil
}
