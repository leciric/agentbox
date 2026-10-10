package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// A machine sees every core of the VM unless it's told otherwise, and test
// runners and build tools start a worker per core they see: go test, vitest,
// jest, cargo, Playwright. With several agents running, that's several times
// the workers the VM has cores for, each with its own memory, so agents ran
// out of memory long before they ran out of CPU.
//
// So each running agent's machine gets a share of the VM's cores, its
// limits.cpu: a count, which Incus turns into a cpuset that it spreads across
// the cores and changes on a running machine. Inside it, nproc,
// os.availableParallelism(), os.cpus() (lxcfs's /proc/cpuinfo), Go's
// GOMAXPROCS and cargo's job count all follow it. The env file sets the knobs
// of the tools that don't count cores themselves, or count them once
// (workersScript). The share is worked out again whenever an agent starts or
// stops: the daemon calls BalanceCPU when anything changes, and the machine
// that is starting gets its share before it boots (setStartShare).
//
// This is not the per-agent CPU cap earlier releases had (oldlimits.go): the
// user doesn't set it, and an agent running alone has every core.

// cpuShareKey is the Incus key holding an agent machine's share.
const cpuShareKey = "limits.cpu"

// minCPUShare is the least share an agent gets, however many run: its
// desktop, browser and AI tool want a core beside what it builds.
const minCPUShare = 2

// CPUShare is each running agent's share of the VM's cores: the cores split
// between the agents running, rounded up, so the shares overlap a little
// rather than leave a core to nobody, and never fewer than minCPUShare.
func CPUShare(cores, running int) int {
	cores = max(cores, 1)
	share := (cores + max(running, 1) - 1) / max(running, 1)
	return min(max(share, minCPUShare), cores)
}

// cpuShareValue is what limits.cpu is set to for a share: nothing when the
// share is every core, so an agent running alone runs as it always has.
func cpuShareValue(share, cores int) string {
	if share >= cores {
		return ""
	}
	return strconv.Itoa(share)
}

// cores is how many cores the agents share.
func (m *Manager) cores() int {
	if m.Cores > 0 {
		return m.Cores
	}
	return HostCores()
}

// agentInstances is every agent machine Incus has, and how many of them run.
// A paused machine uses no CPU, so it doesn't count; it gets its share back
// when it resumes.
func (m *Manager) agentInstances(ctx context.Context) ([]incus.Instance, int, error) {
	agents, err := m.Store.Agents(ctx, "")
	if err != nil || len(agents) == 0 {
		return nil, 0, err
	}
	ours := make(map[string]bool, len(agents))
	for _, a := range agents {
		if !a.IsLead() && a.Instance != "" {
			ours[a.Instance] = true
		}
	}
	all, err := m.Incus.Instances(ctx)
	if err != nil {
		return nil, 0, err
	}
	var instances []incus.Instance
	running := 0
	for _, inst := range all {
		if !ours[inst.Name] {
			continue
		}
		instances = append(instances, inst)
		if inst.Status == "Running" {
			running++
		}
	}
	return instances, running, nil
}

// setShare gives one machine a share, from have, its own configuration, and
// reports whether that changed anything.
func (m *Manager) setShare(ctx context.Context, instance string, have map[string]string, want string) (bool, error) {
	if current, ok := have[cpuShareKey]; ok && current == want || !ok && want == "" {
		return false, nil
	}
	if want == "" {
		return true, m.Incus.UnsetConfig(ctx, instance, cpuShareKey)
	}
	return true, m.Incus.SetConfig(ctx, instance, cpuShareKey+"="+want)
}

// setStartShare gives a machine about to boot the share it will have once it
// runs, so the first process inside already sees it. The others' shrink
// after, when the daemon balances them.
func (m *Manager) setStartShare(ctx context.Context, instance string) error {
	instances, running, err := m.agentInstances(ctx)
	if err != nil {
		return err
	}
	var have map[string]string
	for _, inst := range instances {
		if inst.Name != instance {
			continue
		}
		if inst.Status == "Running" {
			running--
		}
		have = inst.Config
	}
	// A machine being made isn't an agent's yet, and may be a copy that
	// carries the share of the one it was copied from.
	if have == nil {
		d, err := m.Incus.Details(ctx, instance)
		if err != nil {
			return err
		}
		have = d.Config
	}
	cores := m.cores()
	_, err = m.setShare(ctx, instance, have, cpuShareValue(CPUShare(cores, running+1), cores))
	return err
}

// BalanceCPU gives every running or paused agent machine its share of the
// VM's cores for the agents running now, and reports how many it changed. A
// stopped machine is left as it is: it gets its share as it starts. It goes on
// past a machine it can't change, and returns every such error.
func (m *Manager) BalanceCPU(ctx context.Context) (changed int, err error) {
	instances, running, err := m.agentInstances(ctx)
	if err != nil {
		return 0, err
	}
	cores := m.cores()
	want := cpuShareValue(CPUShare(cores, running), cores)
	var errs []error
	for _, inst := range instances {
		if inst.Status != "Running" && inst.Status != "Frozen" {
			continue
		}
		did, err := m.setShare(ctx, inst.Name, inst.Config, want)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", inst.Name, err))
			continue
		}
		if did {
			changed++
		}
	}
	return changed, errors.Join(errs...)
}

// workersScript sets the worker counts of the tools that don't follow the
// machine's share by themselves: make, which runs one job unless told, and
// the explicit counts go, cargo and vitest read, which agents' commands and
// projects' scripts often pass on. It runs in every shell that starts (the env
// file, and BASH_ENV for the Bash tool's shells), so the counts follow the
// share as agents start and stop rather than staying what they were when the
// AI tool started. A variable set to something else, by the user or a
// project's scripts, is left alone: only the value this script set last,
// remembered in AGENTBOX_CPUS, is replaced.
const workersScript = `# Worker counts follow this machine's share of the VM's cores (nproc).
_ab_n=$(nproc 2>/dev/null)
if [ -n "$_ab_n" ]; then
  for _ab_v in GOMAXPROCS CARGO_BUILD_JOBS VITEST_MAX_WORKERS VITEST_MAX_THREADS VITEST_MAX_FORKS; do
    eval "_ab_c=\${$_ab_v-}"
    if [ -z "$_ab_c" ] || [ "$_ab_c" = "${AGENTBOX_CPUS-}" ]; then export "$_ab_v=$_ab_n"; fi
  done
  if [ -z "${MAKEFLAGS-}" ] || [ "$MAKEFLAGS" = "-j${AGENTBOX_CPUS-}" ]; then export MAKEFLAGS="-j$_ab_n"; fi
  _ab_f=
  for _ab_w in ${GOFLAGS-}; do
    case $_ab_w in -p=${AGENTBOX_CPUS-none}) ;; *) _ab_f="$_ab_f $_ab_w" ;; esac
  done
  case " $_ab_f " in *" -p="*) ;; *) _ab_f="$_ab_f -p=$_ab_n" ;; esac
  export GOFLAGS="${_ab_f# }"
  export AGENTBOX_CPUS="$_ab_n"
fi
unset _ab_n _ab_v _ab_c _ab_f _ab_w
`

// BashEnvPath is the file BASH_ENV names in an agent's Claude Code, which
// every Bash tool call's shell reads: the worker counts, and the heavy
// command's join into its run's cgroup when one just started (heavyhooks.go).
func (m *Manager) BashEnvPath() string { return "/home/" + m.User.Name + "/.config/agentbox/bash_env" }

func (m *Manager) bashEnv() string {
	heavy := shellQuote("/home/" + m.User.Name + "/" + HeavyEnvFile)
	return "# Written by AgentBox.\n" + workersScript + "if [ -r " + heavy + " ]; then . " + heavy + "; fi\n"
}

func (m *Manager) writeBashEnv(ctx context.Context, a state.Agent) error {
	return m.Incus.WriteFile(ctx, a.Instance, m.BashEnvPath(), []byte(m.bashEnv()), m.User.UID, m.User.GID, 0o644)
}

// withBashEnv points BASH_ENV in Claude Code's settings.json at bash_env.
func (m *Manager) withBashEnv(b []byte) ([]byte, error) {
	return withClaudeEnv(b, map[string]string{"BASH_ENV": m.BashEnvPath()})
}
