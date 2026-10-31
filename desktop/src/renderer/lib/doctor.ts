import * as T from '../../shared/api.ts';

// The doctor's checks as Setup shows them (DoctorPanel): only what isn't ok,
// failures first, and for each, whether its fix is a command to copy and
// whether the Home chat can be asked to fix it.

// problems are the checks that aren't ok, failures before warnings, each kind
// in the doctor's order.
export function problems(checks: readonly T.DoctorCheck[]): T.DoctorCheck[] {
  return [
    ...checks.filter((c) => c.status === T.DoctorFail),
    ...checks.filter((c) => c.status === T.DoctorWarn),
  ];
}

// canAskHome is whether a check is the Home chat's to look into: a failure,
// with no fix of the doctor's own, and not the Home chat's own login, without
// which it couldn't run. A warning is never handed over by itself.
export function canAskHome(c: T.DoctorCheck): boolean {
  return c.status === T.DoctorFail && !c.autoFix && !c.job && c.id !== T.DoctorAILogin;
}

// isCommand is whether a fix is a command to run, shown to copy, rather than
// a sentence of what to do.
export function isCommand(fix: string): boolean {
  return /^(agentbox|sudo|brew) /.test(fix);
}

// openHomeChatEvent takes the app to the Home chat, at the session chosen
// last (chooseSession); App listens for it.
export const openHomeChatEvent = 'agentbox:open-home-chat';
export function openHomeChat(): void {
  window.dispatchEvent(new CustomEvent(openHomeChatEvent));
}
