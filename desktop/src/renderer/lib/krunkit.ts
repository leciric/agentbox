// What a Mac can do about krunkit, from `agentbox vm status`'s check
// (hostvm.KrunkitCheck): a VM made with krunkit has a GPU for agents and gives
// memory back to the Mac, one made with Apple's Virtualization framework (vz)
// has neither. Homebrew installs what's missing when it can (`agentbox vm
// krunkit`, internal/hostvm/krunkit.go): krunkit from its tap, and Lima's
// krunkit driver with a newer Homebrew Lima. Otherwise the app says how.

export interface KrunkitCheck {
  available: boolean;
  missing?: 'krunkit' | 'driver';
  brew?: boolean;
}

// krunkitInstall is what installs krunkit by hand: hostvm's krunkitInstall.
export const krunkitInstall = 'brew tap slp/krun && brew trust slp/krun && brew install krunkit';

// KrunkitPlan is what the app offers: nothing to install (ready), a button
// that has Homebrew install it (brew), or the commands to run by hand
// (manual), with what's missing and the command that installs it ("" when no
// one command does, as for a Lima that isn't Homebrew's).
export type KrunkitPlan =
  | { kind: 'ready' }
  | { kind: 'brew' | 'manual'; missing: 'krunkit' | 'driver'; command: string };

// krunkitPlan is null where krunkit doesn't run: not a Mac with Apple Silicon,
// or a front end too old to say.
export function krunkitPlan(check: KrunkitCheck | undefined | null): KrunkitPlan | null {
  if (!check) return null;
  if (check.available) return { kind: 'ready' };
  const missing = check.missing === 'driver' ? 'driver' : 'krunkit';
  if (check.brew) return { kind: 'brew', missing, command: missing === 'driver' ? 'brew upgrade lima' : krunkitInstall };
  return { kind: 'manual', missing, command: missing === 'driver' ? '' : krunkitInstall };
}

// offersRemake reports whether Settings offers to make a Mac's VM again with
// krunkit: a VM that exists, wasn't made with it, on a Mac that can run it.
export function offersRemake(vm: { exists: boolean; vmType?: string; krunkit?: KrunkitCheck } | null | undefined): boolean {
  return !!vm?.exists && vm.vmType !== 'krunkit' && krunkitPlan(vm.krunkit) !== null;
}
