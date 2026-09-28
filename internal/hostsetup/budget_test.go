package hostsetup

import (
	"os"
	"path/filepath"
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
		"chown 1000 memory.high memory.max memory.swap.max cpu.max && {",
		"echo +io > /sys/fs/cgroup/agentbox/cgroup.subtree_control",
		"chown 1000 io.weight io.max /sys/fs/cgroup/io.cost.qos; true; }",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("the unit has no %q:\n%s", want, unit)
		}
	}
	// It hands over the budget files and nothing else: not the
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

// Host setup on a host without cgroup v2 skips the budget, saying so, and
// doesn't fail over it.
func TestSetUpBudgetSkipsWithoutCgroupV2(t *testing.T) {
	old := cgroupControllers
	cgroupControllers = filepath.Join(t.TempDir(), "cgroup.controllers")
	t.Cleanup(func() { cgroupControllers = old })
	var said []string
	if err := SetUpBudget("lint", "1000", func(s string) { said = append(said, s) }); err != nil {
		t.Fatal(err)
	}
	if len(said) != 1 || !strings.Contains(said[0], "cgroup v2") {
		t.Errorf("said %q", said)
	}
}

func TestRemoveBudget(t *testing.T) {
	old := budgetUnitDir
	budgetUnitDir = t.TempDir()
	t.Cleanup(func() { budgetUnitDir = old })
	var said []string
	log := func(s string) { said = append(said, s) }

	if err := RemoveBudget(log); err != nil || len(said) != 1 || !strings.Contains(said[0], "There is no") {
		t.Errorf("with no unit: %v %q", err, said)
	}
	path := filepath.Join(budgetUnitDir, BudgetUnitName)
	if err := os.WriteFile(path, []byte(BudgetUnit("lint", "1000")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveBudget(log); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the unit is still there: %v", err)
	}
}

func TestRunShell(t *testing.T) {
	if err := runShell("true"); err != nil {
		t.Error(err)
	}
	if err := runShell("echo nope >&2; exit 3"); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("a failing script = %v, want its output in the error", err)
	}
}
