// Runs `agentbox host setup` as root from the app, through pkexec: every
// polkit desktop has it, and it shows the system's own password dialog.
//
// On a Mac there is no host to set up: AgentBox runs in a Linux VM, and
// setting it up is `agentbox vm init`, which makes the VM and runs host setup
// inside it (internal/hostvm). It needs no password.
//
// On Linux AgentBox runs in a VM of its own too, made with Cloud Hypervisor by
// `agentbox vm init` with no password, the way a Mac's is made with Lima
// (vmmode.ts), and setting up means the VM. Host setup as root is left for a
// machine set up to run agents itself before that, until it moves into the VM
// (`agentbox vm migrate`), and for what runs it inside the VM.
//
// On Windows there is no host to set up either: AgentBox runs in a WSL distro
// of its own, and setting it up is `agentbox.exe wsl init`, which makes the
// distro and runs host setup inside it as the distro's root (internal/hostwsl,
// D94). It needs no password; WSL itself, which does, is a documented step.
//
// There is no AgentBox polkit policy file on purpose. Installing one needs
// root, which is the very step this removes, so pkexec falls back to
// org.freedesktop.policykit.exec and asks for an administrator's password —
// the same thing sudo would have asked for in a terminal.
//
// The daemon runs as you, not as the app, and it can't do this even if it
// wanted to, so the app runs the command-line tool itself instead of calling
// the daemon's API.
//
// Resizing the VM is the same kind of thing: `agentbox vm resize` stops the VM,
// has Lima change it and starts it again, and the daemon goes down with it. On
// Linux it changes the Cloud Hypervisor VM while it runs when it can, and
// restarts it only when asked to (--restart).
import { execFile, spawn } from "node:child_process";
import { accessSync, closeSync, constants, mkdirSync, openSync } from "node:fs";
import { cpus as hostCPUs, totalmem, userInfo } from "node:os";
import { delimiter, isAbsolute, join } from "node:path";
import { app, ipcMain } from "electron";
import { agentboxBin } from "./cli";
import { onWindows } from "./relay";
import type * as T from "../shared/api";
import { learnMode, linuxVM } from "./vmmode";

export interface HostSetupStatus {
  pkexec: string | null; // the pkexec on this machine, if it has one
  user: string; // who the setup is for: whoever is running the app
  running: boolean;
  resizing: boolean; // `agentbox vm resize` is running
  vm: VMStatus | null; // AgentBox's VM, on a Mac; null elsewhere
  wsl: WSLStatus | null; // AgentBox's WSL distro, on Windows; null elsewhere
  linux: LinuxSetup | null; // where AgentBox runs, on Linux; null elsewhere
  // AgentBox's Cloud Hypervisor VM, on Linux in VM mode (`agentbox vm status
  // --json`): its size, and what it can be resized to. null elsewhere.
  chv: T.VMStatus | null;
}

// LinuxSetup is which way a Linux machine runs AgentBox, and whether it could
// run it in a VM instead.
export interface LinuxSetup {
  // In AgentBox's VM, made or not yet, or on the machine itself: a host-mode
  // installation from before AgentBox ran in a VM on Linux, until it moves.
  mode: "host" | "vm";
  kvm: boolean; // /dev/kvm is there for this user, which the VM needs
  cores: number; // the machine's, the most CPUs the VM can have
  memory: number; // bytes, the machine's, the highest memory cap the VM can have
  // The size `agentbox vm init` gives the VM unless it's told otherwise.
  defaultCpus: number;
  defaultMemoryCap: number; // bytes
}

// WSLStatus is `agentbox wsl status --json` (hostwsl.Status).
export interface WSLStatus {
  wsl: string; // WSL's version, "" when there's no WSL that will do
  problem?: string; // why there's no distro to use, and what to do
  name: string;
  user: string;
  exists: boolean;
  state?: string; // Running, Stopped
  version?: number; // 1 or 2
}

// VMStatus is `agentbox vm status --json`.
export interface VMStatus {
  // "vz" when the VM is the experimental vz driver's, which Lima doesn't run;
  // missing for Lima's.
  driver?: "vz";
  lima: string;
  problem?: string;
  name: string;
  exists: boolean;
  status?: string;
  cpus?: number;
  memory?: number; // bytes
  disk?: number; // bytes
  // The sizes `agentbox vm resize` takes on this Mac. Missing from a front end
  // older than the command.
  limits?: VMLimits;
  vmType?: string; // vz, krunkit
  // Whether `agentbox vm init` would make the VM with krunkit, which gives
  // memory back to the Mac: only before there's a VM, on Apple Silicon.
  krunkit?: KrunkitCheck;
}

export interface KrunkitCheck {
  available: boolean;
  missing?: "krunkit" | "driver"; // what isn't installed
}

export interface VMLimits {
  minCpus: number;
  maxCpus: number;
  minMemory: number; // bytes
  maxMemory: number; // bytes
}

export const onMac = process.platform === "darwin";

// which finds a command on this process's PATH, the way a shell would.
export function which(name: string): string | null {
  for (const dir of (process.env.PATH ?? "").split(delimiter)) {
    if (!dir) continue;
    try {
      const path = join(dir, name);
      accessSync(path, constants.X_OK);
      return path;
    } catch {
      // keep looking
    }
  }
  return null;
}

// canUseIncus mirrors the daemon's own check (internal/incus/reachable.go): the
// incus command on PATH, and a socket this process may open. Host setup's ACL
// is on the user's UID, so it counts for processes that were already running.
export function canUseIncus(): boolean {
  if (!which("incus")) return false;
  try {
    accessSync(
      process.env.INCUS_SOCKET || "/var/lib/incus/unix.socket",
      constants.R_OK | constants.W_OK,
    );
    return true;
  } catch {
    return false;
  }
}

// setupBinary is the agentbox pkexec runs. pkexec resolves a bare name in the
// caller's PATH, but the app's own copy is what the daemon runs, so it names
// that one by its path whenever it knows it.
function setupBinary(): string | null {
  const bin = agentboxBin();
  if (isAbsolute(bin)) return bin;
  return which(bin);
}

let running: Promise<void> | undefined;

// hostSetupStatus is what the Setup page polls, so on Linux it stays three
// syscalls: the agentbox to run is worked out when there is a run, because
// agentboxBin() hashes the binary to keep the app's copy current. On a Mac it
// asks the front end about the VM, and on Windows agentbox.exe about the
// distro, at most every few seconds.
export async function hostSetupStatus(): Promise<HostSetupStatus> {
  return {
    pkexec: onMac || onWindows ? null : which("pkexec"),
    user: userInfo().username,
    running: running !== undefined,
    resizing: resizing !== undefined,
    vm: await vmStatus(),
    wsl: await wslStatus(),
    linux: onMac || onWindows ? null : linuxSetup(),
    chv: await chvStatus(),
  };
}

const GiB = 1024 ** 3;

// linuxSetup is which way this Linux machine runs AgentBox, and the size its
// VM would be made at: `agentbox vm init`'s defaults (hostvm.DefaultConfig):
// half the cores, from 2 to 8, and a memory cap of three quarters of the
// memory, leaving at least 4 GiB, in whole GiB.
function linuxSetup(): LinuxSetup {
  const cores = hostCPUs().length;
  const memory = totalmem();
  const cap = Math.floor(Math.min(memory - 4 * GiB, (memory / 4) * 3) / GiB) * GiB;
  return {
    mode: linuxVM() ? "vm" : "host",
    kvm: canUseKVM(),
    cores,
    memory,
    defaultCpus: Math.min(Math.max(Math.floor(cores / 2), 2), 8),
    defaultMemoryCap: Math.max(cap, 4 * GiB),
  };
}

let lastCHV: { at: number; value: T.VMStatus | null } | undefined;

// chvStatus is `agentbox vm status --json` on Linux in VM mode, at most every
// few seconds; null elsewhere, or when the command can't say.
function chvStatus(): Promise<T.VMStatus | null> {
  if (onMac || onWindows || !linuxVM()) return Promise.resolve(null);
  if (lastCHV && Date.now() - lastCHV.at < 3_000) return Promise.resolve(lastCHV.value);
  return new Promise((resolve) => {
    execFile(agentboxBin(), ["vm", "status", "--json"], { timeout: 15_000 }, (_err, stdout) => {
      let value: T.VMStatus | null = null;
      try {
        value = JSON.parse(stdout) as T.VMStatus;
      } catch {
        value = lastCHV?.value ?? null;
      }
      lastCHV = { at: Date.now(), value };
      resolve(value);
    });
  });
}

function canUseKVM(): boolean {
  try {
    accessSync("/dev/kvm", constants.R_OK | constants.W_OK);
    return true;
  } catch {
    return false;
  }
}

let lastVM: { at: number; value: VMStatus } | undefined;

export function vmStatus(): Promise<VMStatus | null> {
  if (!onMac) return Promise.resolve(null);
  if (lastVM && Date.now() - lastVM.at < 3_000)
    return Promise.resolve(lastVM.value);
  return new Promise((resolve) => {
    execFile(
      agentboxBin(),
      ["vm", "status", "--json"],
      { timeout: 15_000 },
      (err, stdout, stderr) => {
        let value: VMStatus;
        try {
          value = JSON.parse(stdout) as VMStatus;
        } catch {
          value = {
            lima: "",
            name: "agentbox",
            exists: false,
            problem: (
              stderr.trim() ||
              err?.message ||
              "agentbox vm status failed"
            ).replace(/^error: /, ""),
          };
        }
        lastVM = { at: Date.now(), value };
        resolve(value);
      },
    );
  });
}

// stopVM stops AgentBox's VM on a Mac, for when the app quits: `agentbox vm
// stop`, when there is a VM and it runs. It settles when the stop has finished,
// failed or not. The stop runs detached, with its output in vm-stop.log in the
// app's logs, so an app that gives up waiting and quits leaves it to finish.
export async function stopVM(): Promise<void> {
  if (!onMac) return;
  lastVM = undefined;
  const vm = await vmStatus();
  if (!vm?.exists || vm.status !== "Running") return;
  let out: number | "ignore" = "ignore";
  try {
    mkdirSync(app.getPath("logs"), { recursive: true });
    out = openSync(join(app.getPath("logs"), "vm-stop.log"), "w");
  } catch {
    // stop it anyway, without a log
  }
  try {
    const child = spawn(agentboxBin(), ["vm", "stop"], {
      detached: true,
      stdio: ["ignore", out, out],
    });
    await new Promise<void>((resolve) => {
      child.on("error", () => resolve());
      child.on("exit", () => resolve());
    });
  } finally {
    if (typeof out === "number") closeSync(out);
    lastVM = undefined;
  }
}

let lastWSL: { at: number; value: WSLStatus; parsed: boolean } | undefined;

export function wslStatus(): Promise<WSLStatus | null> {
  if (!onWindows) return Promise.resolve(null);
  if (lastWSL && Date.now() - lastWSL.at < 3_000)
    return Promise.resolve(lastWSL.value);
  return new Promise((resolve) => {
    execFile(
      agentboxBin(),
      ["wsl", "status", "--json"],
      { timeout: 30_000, windowsHide: true },
      (err, stdout, stderr) => {
        let value: WSLStatus;
        let parsed = true;
        try {
          value = JSON.parse(stdout) as WSLStatus;
        } catch {
          parsed = false;
          // A probe that failed says nothing new about the distro: keep the
          // last answer that did, or the card switches to "install WSL" and
          // back whenever wsl.exe is slow or busy booting it.
          if (lastWSL?.parsed) {
            lastWSL = { ...lastWSL, at: Date.now() };
            return resolve(lastWSL.value);
          }
          value = {
            wsl: "",
            name: "AgentBox",
            user: "",
            exists: false,
            problem: (
              stderr.trim() ||
              err?.message ||
              "agentbox wsl status failed"
            ).replace(/^error: /, ""),
          };
        }
        lastWSL = { at: Date.now(), value, parsed };
        resolve(value);
      },
    );
  });
}

// HostSetupOptions are a setup run's. vm switches a Linux machine that runs
// AgentBox itself to a VM (`agentbox vm init`); before runs first, and stops
// the machine's own daemon, which vm init won't switch it under.
export interface HostSetupOptions {
  vm?: boolean;
  // The VM's size, for vm: its CPUs and its memory cap (like 12GiB).
  // `agentbox vm init`'s own defaults when missing.
  cpus?: number;
  memoryCap?: string;
  // On a Mac, "vz" makes the VM with Apple's Virtualization framework, run by
  // AgentBox itself, instead of with Lima: experimental.
  driver?: "vz";
  before?: (onOutput: (text: string) => void) => Promise<void>;
}

// initing is a Linux `agentbox vm init` under way, which the app's own
// daemon start waits for (vmInitDone).
let initing: Promise<void> | undefined;

// vmInitDone settles once no `agentbox vm init` is running on Linux.
export function vmInitDone(): Promise<void> {
  return initing?.catch(() => {}) ?? Promise.resolve();
}

// runHostSetup runs host setup as root and streams what it prints, line by
// line, to onOutput. It resolves when the setup succeeded.
export function runHostSetup(
  onOutput: (text: string) => void,
  options: HostSetupOptions = {},
): Promise<void> {
  if (running)
    return Promise.reject(new Error("host setup is already running"));
  const linuxVMInit = !onMac && !onWindows && (options.vm === true || linuxVM());
  running = (async () => {
    if (linuxVMInit && !linuxVM()) await options.before?.(onOutput);
    await run(onOutput, linuxVMInit, options);
  })().finally(() => {
    running = undefined;
    initing = undefined;
  });
  if (linuxVMInit) initing = running;
  return running;
}

function run(onOutput: (text: string) => void, linuxVMInit: boolean, options: HostSetupOptions): Promise<void> {
  if (onMac) return initVM(onOutput, options.driver);
  if (onWindows) return initWSL(onOutput);
  if (linuxVMInit) {
    const args = ["init"];
    if (options.cpus) args.push("--cpus", String(options.cpus));
    if (options.memoryCap) args.push("--memory-cap", options.memoryCap);
    return runVM(args, "setting up AgentBox's VM failed", onOutput).finally(learnMode);
  }
  return runAsRoot(["host", "setup"], onOutput);
}

// VMMigration is `agentbox vm migrate --status --json` (hostvm.MigrationStatus):
// on a Linux machine that runs AgentBox itself, what there is to move into
// AgentBox's VM, and how far a move got.
export interface VMMigration {
  state: "none" | "available" | "started" | "verified" | "removed";
  projects?: string[];
  agents?: string[];
  oldMachines?: string[]; // what --remove-old removes, once the move is checked
  backup?: string; // the state.db from before the move
  found?: string[]; // what the check found in the VM
}

// vmMigration asks the command-line tool; null where there's nothing it could
// say (a Mac, Windows, or a tool from before vm migrate).
export function vmMigration(): Promise<VMMigration | null> {
  if (onMac || onWindows) return Promise.resolve(null);
  return new Promise((resolve) => {
    execFile(agentboxBin(), ["vm", "migrate", "--status", "--json"], { timeout: 15_000 }, (err, stdout) => {
      if (err) return resolve(null);
      try {
        resolve(JSON.parse(stdout) as VMMigration);
      } catch {
        resolve(null);
      }
    });
  });
}

// runVMMigration is `agentbox vm migrate`: this machine's own AgentBox,
// projects, agents and all, moved into AgentBox's VM. The command stops this
// machine's daemon itself, and the app's own daemon start waits for it
// (vmInitDone), as it waits for vm init: the daemon it finds afterwards is
// the VM's. removeOld runs `vm migrate --remove-old --yes`, which the app
// asks the user about first.
export function runVMMigration(
  onOutput: (text: string) => void,
  removeOld = false,
): Promise<void> {
  if (running)
    return Promise.reject(new Error("host setup is already running"));
  running = (
    removeOld
      ? runVM(["migrate", "--remove-old", "--yes"], "removing the old machines failed", onOutput)
      : runVM(["migrate"], "moving AgentBox into its VM failed", onOutput).finally(learnMode)
  ).finally(() => {
    running = undefined;
    initing = undefined;
  });
  if (!removeOld) initing = running;
  return running;
}

// runAsRoot runs an agentbox subcommand as root through pkexec, for the user
// running the app, and streams what it prints to onOutput.
function runAsRoot(
  command: string[],
  onOutput: (text: string) => void,
): Promise<void> {
  const pkexec = which("pkexec");
  const user = userInfo().username;
  const binary = setupBinary();
  if (!pkexec) {
    return Promise.reject(
      new Error(
        "this machine has no pkexec, so the app can't ask for your password: run the command below in a terminal",
      ),
    );
  }
  if (!binary) {
    return Promise.reject(
      new Error(
        "this app has no agentbox binary to run: build it with go build -o bin/agentbox ./cmd/agentbox",
      ),
    );
  }
  // pkexec sets PKEXEC_UID to the user who asked, which is what host setup
  // goes by; --user says the same thing, for a pkexec that doesn't.
  const args = [binary, ...command, "--user", user];
  return new Promise((resolve, reject) => {
    onOutput(`$ pkexec ${args.join(" ")}\n`);
    const child = spawn(pkexec, args, { stdio: ["ignore", "pipe", "pipe"] });
    let tail = "";
    const collect = (chunk: Buffer) => {
      const text = chunk.toString("utf8");
      tail = (tail + text).slice(-2_000);
      onOutput(text);
    };
    child.stdout.on("data", collect);
    child.stderr.on("data", collect);
    child.on("error", (err) =>
      reject(new Error(`couldn't run ${pkexec}: ${err.message}`)),
    );
    child.on("close", (code) => {
      if (code === 0) return resolve();
      reject(new Error(failure(code, tail)));
    });
  });
}

// initWSL makes AgentBox's WSL distro and sets AgentBox up in it: `agentbox
// wsl init`, which is safe to run again. The first time it downloads Ubuntu
// and host setup installs Incus inside, which takes a few minutes.
function initWSL(onOutput: (text: string) => void): Promise<void> {
  const binary = agentboxBin();
  return new Promise((resolve, reject) => {
    onOutput("> agentbox wsl init\n");
    const child = spawn(binary, ["wsl", "init"], {
      stdio: ["ignore", "pipe", "pipe"],
      windowsHide: true,
    });
    let tail = "";
    const collect = (chunk: Buffer) => {
      const text = chunk.toString("utf8");
      tail = (tail + text).slice(-2_000);
      onOutput(text);
    };
    child.stdout.on("data", collect);
    child.stderr.on("data", collect);
    child.on("error", (err) =>
      reject(new Error(`couldn't run ${binary}: ${err.message}`)),
    );
    child.on("close", (code) => {
      lastWSL = undefined;
      if (code === 0) return resolve();
      const last = tail.trim().split("\n").filter(Boolean).at(-1) ?? "";
      reject(
        new Error(
          `setting up AgentBox's WSL distro failed${last ? `: ${last.replace(/^error: /, "")}` : ""}`,
        ),
      );
    });
  });
}

// initVM makes AgentBox's VM on a Mac and sets AgentBox up in it: `agentbox vm
// init`, which is safe to run again. It takes a few minutes the first time:
// Lima downloads Debian, and host setup installs Incus inside. With driver
// "vz" the VM is the experimental vz driver's, which needs no Lima.
function initVM(onOutput: (text: string) => void, driver?: "vz"): Promise<void> {
  return runVM(driver === "vz" ? ["init", "--driver", "vz"] : ["init"], "setting up AgentBox's VM failed", onOutput);
}

let resizing: Promise<void> | undefined;

// resizeVM gives AgentBox's VM cpus CPUs and memory (like 12GiB), streaming
// what `agentbox vm resize` prints to onOutput. The command checks both against
// what the machine has. On a Mac it restarts the VM and its daemon: every agent
// stops. On Linux memory is the VM's memory cap, and a running VM changes
// without a restart when it can; when it can't, restart has it restarted,
// which stops every agent, and otherwise it keeps the new size for its next
// start. On Linux disk, when given (like 200GiB), grows the VM's disk for its
// agents, while it runs.
export function resizeVM(
  cpus: number,
  memory: string,
  restart: boolean,
  onOutput: (text: string) => void,
  disk?: string,
): Promise<void> {
  if (onWindows)
    return Promise.reject(new Error("AgentBox's WSL distro has no size to change"));
  if (!onMac && !linuxVM())
    return Promise.reject(new Error("AgentBox doesn't run in a VM on this machine"));
  if (resizing)
    return Promise.reject(new Error("AgentBox's VM is already being resized"));
  const args = onMac
    ? ["resize", "--cpus", String(cpus), "--memory", memory]
    : ["resize", "--cpus", String(cpus), "--memory-cap", memory, ...(disk ? ["--disk", disk] : []), ...(restart ? ["--restart"] : [])];
  resizing = runVM(args, "resizing AgentBox's VM failed", onOutput).finally(() => {
    resizing = undefined;
    lastCHV = undefined;
  });
  return resizing;
}

// The renderer asks for a resize here rather than in index.ts, which only
// wires up the rest; the output goes back to the window that asked.
ipcMain.handle("vm:resize", (event, cpus: number, memory: string, restart?: boolean, disk?: string) =>
  resizeVM(
    cpus,
    memory,
    restart === true,
    (text) => {
      if (!event.sender.isDestroyed()) event.sender.send("vm:output", text);
    },
    typeof disk === "string" && disk !== "" ? disk : undefined,
  ),
);

// runVM runs `agentbox vm <args>` and streams what it prints to onOutput. It
// rejects with failed and the command's last line when the command fails.
function runVM(
  args: string[],
  failed: string,
  onOutput: (text: string) => void,
): Promise<void> {
  const binary = agentboxBin();
  return new Promise((resolve, reject) => {
    onOutput(`$ agentbox vm ${args.join(" ")}\n`);
    const child = spawn(binary, ["vm", ...args], {
      stdio: ["ignore", "pipe", "pipe"],
    });
    let tail = "";
    const collect = (chunk: Buffer) => {
      const text = chunk.toString("utf8");
      tail = (tail + text).slice(-2_000);
      onOutput(text);
    };
    child.stdout.on("data", collect);
    child.stderr.on("data", collect);
    child.on("error", (err) =>
      reject(new Error(`couldn't run ${binary}: ${err.message}`)),
    );
    child.on("close", (code) => {
      lastVM = undefined;
      if (code === 0) return resolve();
      const last = tail.trim().split("\n").filter(Boolean).at(-1) ?? "";
      reject(
        new Error(
          `${failed}${last ? `: ${last.replace(/^error: /, "")}` : ""}`,
        ),
      );
    });
  });
}

// failure turns an exit code into something worth reading. pkexec answers with
// 126 when it couldn't get authorization — a dismissed dialog, a wrong
// password, or no authentication agent on this desktop — and 127 when it
// couldn't run the command at all.
function failure(code: number | null, output: string): string {
  const last = output.trim().split("\n").filter(Boolean).at(-1) ?? "";
  switch (code) {
    case 126:
      return `the password dialog was dismissed, or this desktop has no polkit agent to show it${last ? ` (${last})` : ""}`;
    case 127:
      return `pkexec couldn't run agentbox${last ? `: ${last}` : ""}`;
    default:
      return `host setup failed (exit ${code ?? "signal"})${last ? `: ${last}` : ""}`;
  }
}
