// UI v2's New task (components/v2/NewTaskDialog.tsx): a picker of where the
// task goes — a project's chat, or the Home chat for none — that opens that
// chat with its composer focused, so a task is Ctrl+N, a few letters or a
// digit, Enter, and the message. After T3 Code's "New thread in…" command
// palette (apps/web/src/components/Sidebar.tsx, handleNewThreadClick).
import { useSyncExternalStore } from 'react';

// A place a task can go: a project, or the Home chat (home).
export interface TaskTarget {
  name: string; // the project's slug, or '' for the Home chat
  label: string;
  lastAt?: string; // when its chat was last written in
  home?: boolean;
}

// taskTargets is the picker's list: the projects whose chat was written in
// most recently first, then the rest in the sidebar's order, and the Home chat
// last, kept to those whose name holds every word of query.
export function taskTargets(projects: TaskTarget[], home: TaskTarget, query: string): TaskTarget[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  const matches = (t: TaskTarget) => words.every((w) => t.label.toLowerCase().includes(w) || t.name.toLowerCase().includes(w));
  const order = new Map(projects.map((p, i) => [p.name, i]));
  const at = (t: TaskTarget) => (t.lastAt ? Date.parse(t.lastAt) || 0 : 0);
  const found = projects.filter(matches).toSorted((a, b) => at(b) - at(a) || order.get(a.name)! - order.get(b.name)!);
  return matches(home) ? [...found, home] : found;
}

// digitPick is the target a digit key chooses, 1 for the first: with nothing
// typed yet, or with Alt held, so a digit in a search still types.
export function digitPick(targets: TaskTarget[], key: string, query: string, alt: boolean): TaskTarget | undefined {
  if (!/^[1-9]$/.test(key) || (query !== '' && !alt)) return undefined;
  return targets[Number(key) - 1];
}

// isNewTaskShortcut is Ctrl+N (⌘N on a Mac), anywhere but a terminal.
export function isNewTaskShortcut(event: Pick<KeyboardEvent, 'altKey' | 'shiftKey' | 'ctrlKey' | 'metaKey' | 'key' | 'target'>): boolean {
  if (event.altKey || event.shiftKey || !(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== 'n') return false;
  const target = event.target as { closest?: (s: string) => unknown } | null;
  return !target?.closest?.('.xterm');
}

// The dialog is one for the app, opened by the sidebar's row or the shortcut.
let open = false;
const listeners = new Set<() => void>();
const set = (next: boolean) => {
  open = next;
  for (const fn of listeners) fn();
};
export const openNewTask = () => set(true);
export const closeNewTask = () => set(false);
export const toggleNewTask = () => set(!open);
export function useNewTaskOpen(): boolean {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    () => open,
  );
}

// A composer focus asked for by the picker, a project or a session opened: the
// chat it opens takes it as its composer mounts, which may be a while (a new
// session's lead starting), unless you click or type somewhere else first.
const focusWaitMs = 60_000;
let wanted: { ref: string; at: number; mark: number; exact: boolean } | undefined;
const focusListeners = new Set<() => void>();
export function requestComposerFocus(ref: string, now = Date.now(), mark = globalThis.performance?.now() ?? 0, exact = false): void {
  wanted = { ref, at: now, mark, exact };
  for (const l of focusListeners) l();
}
// requestSessionFocus asks it for one session of a project's chat, and only
// that one: "web/lead" is then the main session, not whichever is on screen,
// which, as you leave it for this one, would take it and go.
export function requestSessionFocus(ref: string, now = Date.now(), mark = globalThis.performance?.now() ?? 0): void {
  requestComposerFocus(ref, now, mark, true);
}
// dropComposerFocus forgets a request made before an event at timeStamp: you
// did something else since. The event that made it, handled after this, is
// not one.
export function dropComposerFocus(timeStamp: number): void {
  if (wanted && timeStamp > wanted.mark) wanted = undefined;
}
if (typeof window !== 'undefined') {
  for (const type of ['pointerdown', 'keydown']) window.addEventListener(type, (e) => dropComposerFocus(e.timeStamp), true);
}
// onComposerFocus hears a request, for a composer already on screen: a
// project clicked in the sidebar while its chat is open.
export function onComposerFocus(fn: () => void): () => void {
  focusListeners.add(fn);
  return () => focusListeners.delete(fn);
}
// takeComposerFocus says whether the composer of ref should take the focus,
// once: asking again is false until the next request.
// A request for a project's chat ("web/lead") is the session's on screen,
// whichever it is ("web/lead.prod").
export function takeComposerFocus(ref: string, now = Date.now()): boolean {
  const project = wanted?.ref.endsWith('/lead') && !wanted.exact ? wanted.ref.slice(0, -'/lead'.length) : undefined;
  const same = wanted?.ref === ref || (project !== undefined && ref.startsWith(`${project}/lead.`));
  if (!wanted || !same || now - wanted.at > focusWaitMs) return false;
  wanted = undefined;
  return true;
}
