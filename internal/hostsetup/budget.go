package hostsetup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The shared budget's cgroup (agent.SharedBudget) is the one thing the budget
// needs root for: a top-level cgroup, /sys/fs/cgroup/agentbox, whose four
// budget files belong to the user the daemon runs as. It is made by a oneshot
// unit, since cgroupfs forgets everything at reboot, and the unit only makes
// the directory, enables the memory and CPU controllers for its children, and
// hands over those four files. Incus, which is root, makes the agents' own
// cgroups inside it and owns them; nothing else on the host changes.
//
// It is its own command rather than part of host setup: the budget is off by
// default, and a host that never turns it on never gets the unit.

// BudgetCgroupDir is the shared budget's cgroup.
const BudgetCgroupDir = "/sys/fs/cgroup/agentbox"

// BudgetFiles are the parent cgroup's files the daemon writes the budget to.
var BudgetFiles = []string{"memory.high", "memory.max", "memory.swap.max", "cpu.max"}

// BudgetUnitName is the unit that makes the cgroup at every boot.
const BudgetUnitName = "agentbox-budget.service"

// BudgetCommand is how to run the budget's setup from a shell.
const BudgetCommand = `sudo "$(command -v agentbox)" host budget`

// budgetUnitDir is where the unit is written. A variable for tests.
var budgetUnitDir = "/etc/systemd/system"

// BudgetScript is the shell the unit runs, for the user with the given UID.
// Enabling the controllers at the root is what lets the cgroup have them at
// all; systemd has usually done it already, and it does no harm again.
func BudgetScript(uid string) string {
	return strings.Join([]string{
		"echo '+memory +cpu' > /sys/fs/cgroup/cgroup.subtree_control || true",
		"mkdir -p " + BudgetCgroupDir,
		"echo '+memory +cpu' > " + BudgetCgroupDir + "/cgroup.subtree_control",
		"cd " + BudgetCgroupDir,
		"chown " + uid + " " + strings.Join(BudgetFiles, " "),
	}, " && ")
}

// BudgetUnit is the systemd unit that runs BudgetScript at boot, before
// Incus starts any agent.
func BudgetUnit(name, uid string) string {
	return fmt.Sprintf(`[Unit]
Description=AgentBox: the shared budget's cgroup, for %s
Before=incus.service incus-startup.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c "%s"

[Install]
WantedBy=multi-user.target
`, name, BudgetScript(uid))
}

// InstallBudget writes the unit, enables it and runs it now. On a host
// without systemd it only runs the script, and says the cgroup is gone at the
// next reboot.
func InstallBudget(name, uid string, log func(string)) error {
	if !isUID(uid) {
		return fmt.Errorf("%q is not a UID", uid)
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		log("This host doesn't run systemd: making the cgroup now, but it's gone at the next reboot — run this again after one.")
		return runShell(BudgetScript(uid))
	}
	path := filepath.Join(budgetUnitDir, BudgetUnitName)
	if err := os.WriteFile(path, []byte(BudgetUnit(name, uid)), 0o644); err != nil {
		return err
	}
	log("Wrote " + path)
	for _, args := range [][]string{{"daemon-reload"}, {"enable", BudgetUnitName}, {"restart", BudgetUnitName}} {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	log(fmt.Sprintf("%s is %s's to set; the unit makes it again at every boot.", BudgetCgroupDir, name))
	return nil
}

// RemoveBudget disables and deletes the unit. The cgroup itself stays until
// the next reboot, since agents may still be running in it.
func RemoveBudget(log func(string)) error {
	path := filepath.Join(budgetUnitDir, BudgetUnitName)
	if _, err := os.Stat(path); err != nil {
		log("There is no " + path + " to remove.")
		return nil
	}
	_ = exec.Command("systemctl", "disable", BudgetUnitName).Run()
	if err := os.Remove(path); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	log("Removed " + path + "; " + BudgetCgroupDir + " goes at the next reboot.")
	return nil
}

func isUID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func runShell(script string) error {
	out, err := exec.Command("/bin/sh", "-c", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
