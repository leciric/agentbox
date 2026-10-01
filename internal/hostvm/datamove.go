package hostvm

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"agentbox/internal/api"
	"agentbox/internal/hostvm/chv"
	"agentbox/internal/paths"
)

// StopForMove stops AgentBox's VM when it runs from p, whose Data is where an
// earlier version kept AgentBox's data, so package datamove can move it: a
// Cloud Hypervisor (or vz) VM's disks and sockets are in it, and a Lima VM
// forwards the daemon's socket into it and mounts the agents' worktrees from
// it. The agents are left as they were: the ones that ran start again with
// the VM, once its daemon has moved their devices too. Nothing is started
// again here: the next command that needs the VM starts it.
func StopForMove(ctx context.Context, p paths.Paths, log io.Writer) error {
	name := env("AGENTBOX_VM", DefaultName)
	if chv.Exists(p, name) {
		c, err := chv.Load(p, name)
		if err != nil {
			return err
		}
		l := chv.NewLayout(p, name)
		switch chvStatus(ctx, c, l, p).State {
		case api.VMOff, api.VMMissing:
			return nil
		}
		return chvStop(ctx, c, l, p, false, log)
	}
	if !useLima() {
		return nil
	}
	limactl, err := FindLimactl()
	if err != nil {
		return nil // no Lima, so no Lima VM
	}
	v := &VM{Limactl: limactl, Name: name, Paths: p, Log: log}
	st, err := v.State(ctx)
	if err != nil {
		return err
	}
	if !st.Exists || st.Status != "Running" {
		return nil
	}
	return v.limaLog(ctx, "stop", name)
}

// MoveFiles are the front end's files that name paths in AgentBox's data, for
// package datamove to rewrite when it moves: a Lima VM's definition, as Lima
// keeps it and as `vm init` wrote it, and a `vm migrate` in progress. Lima's
// also names the daemon's socket in the VM, which moves there with the VM's
// own data: that is also, its from and its to.
func MoveFiles(p paths.Paths) (files []string, also [][2]string) {
	name := env("AGENTBOX_VM", DefaultName)
	files = []string{
		filepath.Join(p.Config, "vm", name+".yaml"),
		migrationFile(p),
	}
	if !useLima() {
		return files, nil
	}
	limaHome := os.Getenv("LIMA_HOME")
	if limaHome == "" {
		if home, err := os.UserHomeDir(); err == nil {
			limaHome = filepath.Join(home, ".lima")
		}
	}
	if limaHome != "" {
		files = append(files, filepath.Join(limaHome, name, "lima.yaml"))
	}
	return files, [][2]string{{"{{.Home}}/.local/share/agentbox", "{{.Home}}/.agentbox"}}
}
