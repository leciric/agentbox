package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// vmDriver is AgentBox in one Cloud Hypervisor VM, through agentbox's own
// front end (agentbox/feat-cloud-hypervisor-vm): `agentbox vm init` makes the
// VM, and every other command runs in it. The agents are the same agentbox
// commands as today's, so they come from agentboxDriver.
//
// It runs in a data directory of its own: XDG_DATA_HOME and XDG_CONFIG_HOME
// under the harness's cache, which is where that agentbox keeps the VM, its
// programs, the forwarded daemon socket and the agents' worktrees. So the
// host's own AgentBox, and a VM the user made, are never touched: vm init
// finds no daemon and no agents there, and the harness deletes only the VM
// in that directory. The directory has to be in the home folder, which the VM
// shares at the same path.
type vmDriver struct {
	*agentboxDriver
	dataHome, configHome string
	commit               string // what the agentbox was built from, when the harness built it
}

func newVMDriver(o *options) *vmDriver {
	work := filepath.Join(o.cache, "vm")
	d := &vmDriver{
		agentboxDriver: &agentboxDriver{mode: "vm", o: o, work: work, bin: o.vmAgentbox,
			instances: map[string]string{}, cgroups: map[string]string{}},
		dataHome:   filepath.Join(work, "data"),
		configHome: filepath.Join(work, "config"),
	}
	if d.bin == "" {
		d.bin = filepath.Join(work, "agentbox")
	}
	d.sock = filepath.Join(d.dataHome, "agentbox", "run", "agentbox.sock")
	env := []string{"AGENTBOX=" + d.bin, "AGENTBOX_NO_UPDATE_CHECK=1", "XDG_DATA_HOME=" + d.dataHome, "XDG_CONFIG_HOME=" + d.configHome}
	d.r = &runner{
		env: append(env, strings.Fields(o.vmEnv)...),
		// What would point it back at the host's AgentBox, or make it
		// something other than the Cloud Hypervisor front end.
		unset: []string{"AGENTBOX_SOCKET", "AGENTBOX_WORKTREES", "AGENTBOX_FRONT_END", "AGENTBOX_VM", "AGENTBOX_VM_TYPE", "AGENTBOX_LINUX_BINARY", "AGENTBOX_HOST_OS", "AGENTBOX_HOST_HOME"},
	}
	return d
}

func (d *vmDriver) describe() map[string]any {
	c := d.agentboxDriver.describe()
	c["dataHome"], c["configHome"] = d.dataHome, d.configHome
	if d.commit != "" {
		c["builtFrom"] = d.o.vmRef + " " + d.commit
	}
	c["commands"] = map[string]any{"setup": d.o.vmSetup, "start": d.o.vmStart, "stop": d.o.vmStop, "pause": d.o.vmPause,
		"resume": d.o.vmResume, "delete": d.o.vmDelete, "status": d.o.vmStatus, "shell": d.o.vmShell, "extraEnv": d.o.vmEnv}
	return c
}

func (d *vmDriver) preflight(ctx context.Context) error {
	var problems []string
	if f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0); err != nil {
		problems = append(problems, fmt.Sprintf("/dev/kvm: %v (be in the kvm group)", err))
	} else {
		_ = f.Close()
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		problems = append(problems, "ssh isn't installed")
	}
	home, _ := os.UserHomeDir()
	if rel, err := filepath.Rel(home, d.work); err != nil || strings.HasPrefix(rel, "..") {
		problems = append(problems, fmt.Sprintf("%s isn't in your home folder, which the VM shares: pass --cache under it", d.work))
	}
	if _, err := os.Stat(d.dataHome); err == nil {
		problems = append(problems, "a VM from an earlier run is left in "+d.dataHome+": run "+os.Args[0]+" --cleanup --modes vm")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	if d.o.vmAgentbox == "" {
		if err := d.build(ctx); err != nil {
			return err
		}
	}
	if _, err := d.ab(ctx, "version"); err != nil {
		return fmt.Errorf("can't run %s: %w", d.bin, err)
	}
	return checkFree(d.work, d.o.writeGiB+30)
}

// build makes the agentbox the VM mode runs, from --vm-ref, without touching
// the repository's checkout: git archive into the cache, and go build there.
func (d *vmDriver) build(ctx context.Context) error {
	// With this process's own environment: the VM's XDG_CONFIG_HOME would
	// hide mise's configuration, and with it the go on the PATH.
	r := &runner{log: d.r.log}
	out, err := r.run(ctx, "", "git", "-C", d.o.repo, "rev-parse", "--verify", "--quiet", d.o.vmRef+"^{commit}")
	if err != nil {
		remote, branch, _ := strings.Cut(d.o.vmRef, "/")
		return fmt.Errorf("%s isn't in %s: git -C %s fetch %s %s", d.o.vmRef, d.o.repo, d.o.repo, remote, branch)
	}
	d.commit = strings.TrimSpace(out)
	src := filepath.Join(d.work, "src")
	if err := os.RemoveAll(src); err != nil {
		return err
	}
	if err := os.MkdirAll(src, 0o755); err != nil {
		return err
	}
	fmt.Printf("[vm] building agentbox from %s (%.12s)\n", d.o.vmRef, d.commit)
	if _, err := r.run(ctx, "", "sh", "-c", fmt.Sprintf("git -C %s archive %s | tar -x -C %s", shq(d.o.repo), d.commit, shq(src))); err != nil {
		return err
	}
	_, err = r.run(ctx, "", "sh", "-c", fmt.Sprintf("cd %s && go build -o %s ./cmd/agentbox", shq(src), shq(d.bin)))
	return err
}

func (d *vmDriver) setup(ctx context.Context, m *modeRun) error {
	if err := os.MkdirAll(d.work, 0o755); err != nil {
		return err
	}
	for _, line := range d.o.vmSetup {
		if _, err := m.setupStep(ctx, line, func(ctx context.Context) error { _, err := d.r.sh(ctx, line); return err }); err != nil {
			return err
		}
	}
	return d.addProject(ctx, m)
}

func (d *vmDriver) ownsVM() bool { return true }
func (d *vmDriver) vm() vmOps    { return d }

// boot counts until the daemon in the VM answers, which is what `vm start`
// waits for too.
func (d *vmDriver) boot(ctx context.Context) error {
	if _, err := d.r.sh(ctx, d.o.vmStart); err != nil {
		return err
	}
	return d.waitDaemon(ctx)
}

func (d *vmDriver) shutdown(ctx context.Context) error {
	_, err := d.r.sh(ctx, d.o.vmStop)
	return err
}

func (d *vmDriver) pause(ctx context.Context) error {
	_, err := d.r.sh(ctx, d.o.vmPause)
	return err
}

func (d *vmDriver) resume(ctx context.Context) error {
	if _, err := d.r.sh(ctx, d.o.vmResume); err != nil {
		return err
	}
	return d.waitDaemon(ctx)
}

// stopAll is the app's Free resources, before it turns the VM off.
func (d *vmDriver) stopAll(ctx context.Context) error {
	_, err := d.ab(ctx, "stop", "--all")
	return err
}

// guestCounters reads the VM's own page cache and memory pressure counters.
func (d *vmDriver) guestCounters(ctx context.Context) (vmCounters, error) {
	if d.o.vmShell == "" {
		return vmCounters{}, errors.New("no way into the VM")
	}
	out, err := d.r.sh(ctx, d.o.vmShell+" sh -c "+shq(vmCountersScript))
	return parseGuestCounters(out), err
}

// memDetail is the VM's memory as its front end reports it, in GiB: what it
// booted with, its cap, what the guest has been granted so far, what it uses,
// and what the host holds for it.
func (d *vmDriver) memDetail(ctx context.Context) map[string]float64 {
	out, err := d.r.sh(ctx, d.o.vmStatus)
	if err != nil {
		return nil
	}
	var st struct {
		Memory map[string]int64 `json:"memory"`
	}
	if json.Unmarshal([]byte(out), &st) != nil || len(st.Memory) == 0 {
		return nil
	}
	m := map[string]float64{}
	for k, v := range st.Memory {
		m[k] = gib(v)
	}
	return m
}

// held is the resident memory of the processes that run this VM: Cloud
// Hypervisor, virtiofsd and passt, whose command lines name its data
// directory, and the front end's supervisor (agentbox vm run).
func (d *vmDriver) held() int64 {
	var total int64
	for _, pid := range d.pids() {
		total += processRSS(pid)
	}
	return total
}

func (d *vmDriver) pids() []string {
	entries, _ := os.ReadDir("/proc")
	var pids []string
	for _, e := range entries {
		if e.Name()[0] < '0' || e.Name()[0] > '9' {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		mine := bytes.Contains(cmdline, []byte(d.dataHome+"/"))
		if !mine && bytes.Contains(cmdline, []byte("\x00vm\x00run")) {
			exe, _ := os.Readlink(filepath.Join("/proc", e.Name(), "exe"))
			mine = exe == d.bin
		}
		if mine {
			pids = append(pids, e.Name())
		}
	}
	return pids
}

// cleanup deletes the VM in the harness's data directory, and the directory.
// If the front end can't, what runs the VM is stopped by hand: every process
// named after the directory.
func (d *vmDriver) cleanup(ctx context.Context) error {
	var errs []error
	if _, err := os.Stat(d.dataHome); err == nil {
		if _, err := d.r.sh(ctx, d.o.vmDelete); err != nil {
			d.r.logf("vm delete failed (%v): stopping what runs the VM by hand", err)
			_, _ = d.r.sh(ctx, d.o.vmStop)
			for _, pid := range d.pids() {
				var n int
				if _, err := fmt.Sscan(pid, &n); err == nil {
					_ = syscall.Kill(n, syscall.SIGKILL)
				}
			}
			time.Sleep(time.Second)
			if left := d.pids(); len(left) > 0 {
				return fmt.Errorf("the VM's processes %v won't stop: %w", left, err)
			}
		}
	}
	if err := os.RemoveAll(d.work); err != nil {
		// A build leaves read-only files (Go's module cache) in worktrees.
		_, _ = d.r.run(ctx, "", "chmod", "-R", "u+w", d.work)
		if err := os.RemoveAll(d.work); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
