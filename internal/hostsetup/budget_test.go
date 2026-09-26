package hostsetup

import (
	"strings"
	"testing"
)

func TestBudgetUnit(t *testing.T) {
	unit := BudgetUnit("lint", "1000")
	for _, want := range []string{
		"Before=incus.service",
		"Type=oneshot",
		"RemainAfterExit=yes",
		"mkdir -p /sys/fs/cgroup/agentbox",
		"echo '+memory +cpu' > /sys/fs/cgroup/agentbox/cgroup.subtree_control",
		"chown 1000 memory.high memory.max memory.swap.max cpu.max",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("the unit has no %q:\n%s", want, unit)
		}
	}
	// It hands over the four budget files and nothing else: not the
	// directory, which would let the user make cgroups of their own in it,
	// and not cgroup.procs, which would let them move processes.
	script := BudgetScript("1000")
	if strings.Contains(script, "chown 1000 "+BudgetCgroupDir) || strings.Contains(script, "cgroup.procs") || strings.Contains(script, "-R") {
		t.Errorf("the script hands over more than the budget files: %s", script)
	}
}

func TestInstallBudgetRefusesANonUID(t *testing.T) {
	if err := InstallBudget("lint", "1000; rm -rf /", func(string) {}); err == nil {
		t.Error("InstallBudget took a UID with shell in it")
	}
}
