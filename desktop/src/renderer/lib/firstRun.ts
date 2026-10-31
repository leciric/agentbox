import type { HostSetupStatus } from '../../preload';
import type * as T from '../../shared/api.ts';

// The first run (components/FirstRun) is one screen that walks a new
// installation from nothing to its first project: the AI tool, the machine
// agents run in, the tool's sign-in, GitHub, a first project, then the
// doctor. Each step is skipped when it's already done, so the screen opens
// wherever there is something left to do.

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
  // machine: the VM (or distro) exists, its daemon answers, and every step
  // agents can't run without is done.
  machine: boolean;
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
