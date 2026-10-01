package hostsetup

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Earlier releases installed a oneshot unit that made a cgroup at every boot,
// /sys/fs/cgroup/agentbox, for an optional shared budget every agent ran
// under, and gave its files and the memory.low of user.slice and system.slice
// to the user the daemon ran as. AgentBox runs its agents in a VM of its own
// now, whose size is what they share, so host setup takes the unit away
// again (RemoveBudget).

// BudgetUnitName is the unit earlier releases installed.
const BudgetUnitName = "agentbox-budget.service"

// budgetUnitDir is where the unit was installed. A variable for tests.
var budgetUnitDir = "/etc/systemd/system"

// RemoveBudget disables and deletes the unit, when there is one, and says so.
// The cgroup itself stays until the next reboot, since agents may still be
// running in it; the daemon lifts its limits (agent.ResetLegacyBudget).
func RemoveBudget(log func(string)) error {
	path := filepath.Join(budgetUnitDir, BudgetUnitName)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	_ = exec.Command("systemctl", "disable", BudgetUnitName).Run()
	if err := os.Remove(path); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	log("Removed " + path + ", which made a cgroup for the shared agent budget earlier releases had.")
	return nil
}
