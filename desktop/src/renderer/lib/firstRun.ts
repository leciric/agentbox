import type { ConnectionState, HostSetupStatus } from '../../preload';
import type * as T from '../../shared/api.ts';

// The first run (components/FirstRun) is one screen that walks a new
// installation from nothing to its first project: the AI tool, the machine
// agents run in, the tool's sign-in, GitHub, a first project, then the
// doctor. Each step is skipped when it's already done, so the screen opens
// wherever there is something left to do.
//
// Nothing holds it on the machine: making the VM (or distro) runs in the
// background once started (lib/machineRun), and the base image downloads in
// the daemon's (fetchFirstImage), each with a compact indicator in the rail,
// and in the top bar after the first run. The steps that ask the daemon wait
// for it only when they are reached before it answers, and nothing waits for
// the base image: the first agent made does.

export type FirstRunStep = 'tool' | 'machine' | 'login' | 'github' | 'project' | 'check';
export const firstRunSteps: readonly FirstRunStep[] = ['tool', 'machine', 'login', 'github', 'project', 'check'];

// The AI tools a first run offers, Claude Code first and chosen unless
// another is.
export const firstRunTools = ['claude', 'codex', 'opencode', 'cursor', 'gemini'] as const;
export type FirstRunTool = (typeof firstRunTools)[number];
export const isFirstRunTool = (ai: string | undefined): ai is FirstRunTool => firstRunTools.includes(ai as FirstRunTool);

// What a first run knows about how far the installation has got.
export interface FirstRunProgress {
  // tool is the AI tool chosen, undefined until it is.
  tool?: FirstRunTool;
  // machine: the VM (or distro) is made, being made in the background, or
  // failed to be (the indicator retries it); with the daemon up, what agents
  // can't run without, the base image aside, is there too.
  machine: boolean;
  // daemon: it answers. The steps after the machine's ask it, and say they
  // wait for it until it does.
  daemon: boolean;
  // login: the chosen tool is signed in for agents.
  login: boolean;
  // github and project are each done, or skipped.
  github: boolean;
  project: boolean;
}

// firstRunStep is the step the first run is on: the first one not done.
export function firstRunStep(p: FirstRunProgress): FirstRunStep {
  if (!p.tool) return 'tool';
  if (!p.machine) return 'machine';
  if (!p.login) return 'login';
  if (!p.github) return 'github';
  if (!p.project) return 'project';
  return 'check';
}

// stepDone is whether a step is behind the first run, for its rail.
export function stepDone(step: FirstRunStep, p: FirstRunProgress): boolean {
  return firstRunSteps.indexOf(step) < firstRunSteps.indexOf(firstRunStep(p));
}

// needsDaemon is whether a step asks the daemon: every one after the
// machine's. The tool is told once it answers.
export function needsDaemon(step: FirstRunStep): boolean {
  return firstRunSteps.indexOf(step) > firstRunSteps.indexOf('machine');
}

// waitsForMachine is whether a step, reached before the daemon answers, shows
// that it waits for it (and continues by itself once it does).
export function waitsForMachine(step: FirstRunStep, p: FirstRunProgress): boolean {
  return needsDaemon(step) && !p.daemon;
}

// MachineRun is this app's run of `agentbox vm init` (`agentbox wsl init` on
// Windows), which goes on in the background while the first run moves on:
// idle until started, with what it has printed and why it failed.
export interface MachineRun {
  state: 'idle' | 'running' | 'failed' | 'done';
  lines: string[];
  error?: string;
  since?: number; // ms, when it started
}

// MachineState is what the machine's indicator shows: nothing started yet,
// being made (or its daemon starting) with its latest line, failed with why,
// or ready.
export type MachineState =
  | { kind: 'idle' }
  | { kind: 'running'; line: string; since?: number }
  | { kind: 'failed'; error: string }
  | { kind: 'ready' };

// machineMade is whether the host has its VM (or distro), finished or not.
function machineMade(host: HostSetupStatus | undefined): boolean {
  if (!host) return false;
  if (host.vm) return host.vm.exists;
  if (host.wsl) return host.wsl.exists;
  return !!host.chv && host.chv.state !== 'missing';
}

// machineState works the indicator out. A run started before the window
// was reloaded is the main process's (HostSetupStatus.running), with no
// lines here; one that failed then left a VM with a problem, which is a
// failure to retry, not a machine to choose again.
export function machineState(o: { run: MachineRun; host: HostSetupStatus | undefined; connection: ConnectionState }): MachineState {
  if (o.connection.state === 'connected') return { kind: 'ready' };
  const { run, host } = o;
  if (run.state === 'running' || (run.state === 'idle' && host?.running && !host.resizing && !host.remaking))
    return { kind: 'running', line: lastLine(run.lines), since: run.since };
  if (run.state === 'failed') return { kind: 'failed', error: run.error ?? '' };
  if (o.connection.starting) return { kind: 'running', line: o.connection.starting.step, since: o.connection.starting.since };
  if (run.state === 'done') return { kind: 'running', line: lastLine(run.lines), since: run.since };
  const problem = host?.vm?.problem ?? host?.wsl?.problem ?? host?.chv?.problem;
  if (machineMade(host) && problem) return { kind: 'failed', error: problem };
  return { kind: 'idle' };
}

// machineStarted is whether the first run is past the machine step: its
// indicator has anything to say.
export function machineStarted(s: MachineState): boolean {
  return s.kind !== 'idle';
}

// eslint-disable-next-line no-control-regex
const ansi = /\x1b\[[0-9;?]*[ -/]*[@-~]/g;

// lastLine is the latest thing a run printed, for one line of indicator: the
// last line with text, from its last carriage return on (a download redraws
// its progress with them), without a step's "==> " or terminal colours.
export function lastLine(lines: readonly string[]): string {
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = (lines[i].split('\r').filter((part) => part.trim()).at(-1) ?? '')
      .replace(ansi, '')
      .replace(/^==>\s*/, '')
      .trim();
    if (line) return line;
  }
  return '';
}

// ImageState is what the base image's indicator shows: waiting for the
// machine, which it downloads in; downloading, with its latest step; failed,
// to retry; not there with nothing under way (the first agent made starts
// it); or ready.
export type ImageState =
  | { kind: 'waiting' }
  | { kind: 'running'; step: string; job?: string }
  | { kind: 'failed'; error: string; job?: string }
  | { kind: 'missing' }
  | { kind: 'ready' };

export function imageState(connected: boolean, image: T.ImageStatus | undefined): ImageState {
  if (!connected || !image) return { kind: 'waiting' };
  if (image.build === 'building') return { kind: 'running', step: [image.step, image.progress].filter(Boolean).join(' · '), job: image.job };
  if (image.ready) return { kind: 'ready' };
  if (image.build === 'failed') return { kind: 'failed', error: image.error ?? '', job: image.job };
  return { kind: 'missing' };
}

// neverSetUp is whether the host has no VM (or WSL distro) yet: a machine
// AgentBox has never run on, whose first run starts before any daemon does.
// One that has a VM that won't start is an installation with a problem, not a
// new one, and gets Setup's card as before.
export function neverSetUp(status: HostSetupStatus | undefined): boolean {
  if (!status) return false;
  if (status.vm) return !status.vm.exists;
  if (status.wsl) return !status.wsl.exists;
  return status.linux?.mode === 'vm' && status.chv?.state === 'missing';
}

// FirstRunMark is this app's own record of the first run: started once it
// has shown it, so the VM coming into being, or the app being quit while it
// does, doesn't drop it before the daemon answers; done once it is finished,
// so a VM removed later doesn't bring it back.
export type FirstRunMark = 'started' | 'done' | null;

// showFirstRun is whether the app shows the first run instead of its pages:
// on an installation made after it existed (Settings.firstRun) until it is
// finished, or, before there is a daemon to ask, on a machine never set up,
// or one whose first run has started.
export function showFirstRun(o: { connected: boolean; settings: T.Settings | undefined; host: HostSetupStatus | undefined; mark: FirstRunMark }): boolean {
  if (o.mark === 'done') return false;
  // Until the settings are read, one that has started stays on screen.
  if (o.connected) return o.settings ? o.settings.firstRun : o.mark === 'started';
  return o.mark === 'started' || neverSetUp(o.host);
}

// filterRepos is the repositories whose name or description has every word
// of query, in the order GitHub gave them (newest pushed first).
export function filterRepos(repos: readonly T.GitHubRepo[], query: string): T.GitHubRepo[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return [...repos];
  return repos.filter((r) => {
    const text = `${r.fullName} ${r.description ?? ''}`.toLowerCase();
    return words.every((w) => text.includes(w));
  });
}
