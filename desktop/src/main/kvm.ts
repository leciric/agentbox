// Whether this user can use /dev/kvm, which AgentBox's VM needs on Linux, and
// if not, why: the same reading as the command-line tool's (internal/kvm), so
// Setup can offer the fix before `agentbox vm init` fails on it. Setup polls
// this, so it stays an open, a stat and, only without /dev/kvm, one read.
import { accessSync, constants, readFileSync, statSync } from "node:fs";

// KVMProblem is why /dev/kvm can't be used:
//   denied  it's there and this user can't open it (Debian and Ubuntu give it to
//           the kvm group only): `agentbox host kvm` gives it, no new login
//   module  it isn't there, and the CPU can virtualize: KVM's module isn't loaded,
//           which `agentbox host kvm` does too
//   novirt  it isn't there, and the CPU says it can't: virtualization is off in
//           the firmware, or this is a VM without nested virtualization
export type KVMProblem = "denied" | "module" | "novirt";

export interface KVMProbe {
  canOpen: () => boolean; // read and write, as this user
  exists: () => boolean;
  cpuinfo: () => string; // /proc/cpuinfo, "" when unreadable
}

// kvmProblem is what keeps this user from KVM, or null when nothing does.
export function kvmProblem(probe: KVMProbe): KVMProblem | null {
  if (probe.canOpen()) return null;
  if (probe.exists()) return "denied";
  return canVirtualize(probe.cpuinfo()) ? "module" : "novirt";
}

// canVirtualize reads the CPU's flags: vmx is Intel's VT-x, svm AMD-V. Since
// Linux 5.6 the kernel hides vmx when the firmware has turned VT-x off.
export function canVirtualize(cpuinfo: string): boolean {
  const flags = cpuinfo.split("\n").find((line) => /^flags\s*:/.test(line));
  if (!flags) return false;
  const words = flags.slice(flags.indexOf(":") + 1).trim().split(/\s+/);
  return words.includes("vmx") || words.includes("svm");
}

export const systemKVM: KVMProbe = {
  canOpen: () => {
    try {
      accessSync("/dev/kvm", constants.R_OK | constants.W_OK);
      return true;
    } catch {
      return false;
    }
  },
  exists: () => {
    try {
      statSync("/dev/kvm");
      return true;
    } catch (err) {
      // Anything but a missing node is one this user may not look at.
      return (err as NodeJS.ErrnoException).code !== "ENOENT";
    }
  },
  cpuinfo: () => {
    try {
      return readFileSync("/proc/cpuinfo", "utf8");
    } catch {
      return "";
    }
  },
};
