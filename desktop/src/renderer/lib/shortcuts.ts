// UI v2's keyboard: the shortcut sheet's list (components/v2/ShortcutSheet.tsx,
// Ctrl+/), the app-wide keys App listens for, and F6, which moves the focus
// between the sidebar, the chat and the panel, as a browser's F6 moves
// between its toolbar and the page.
import { useSyncExternalStore } from 'react';
import type { MessageKey } from './i18n';

type Keyish = Pick<KeyboardEvent, 'altKey' | 'shiftKey' | 'ctrlKey' | 'metaKey' | 'key' | 'target'>;

const inTerminal = (event: Keyish) => !!(event.target as { closest?: (s: string) => unknown } | null)?.closest?.('.xterm');
const mod = (event: Keyish) => (event.ctrlKey || event.metaKey) && !event.altKey;

// isSheetShortcut is Ctrl+/ (⌘/ on a Mac), anywhere but a terminal.
export const isSheetShortcut = (event: Keyish) => mod(event) && !event.shiftKey && event.key === '/' && !inTerminal(event);

// isSettingsShortcut is Ctrl+, (⌘, on a Mac), as most desktop apps have it.
export const isSettingsShortcut = (event: Keyish) => mod(event) && !event.shiftKey && event.key === ',' && !inTerminal(event);

// isBackToLeadShortcut is Alt+← (⌥← on a Mac), from an agent's chat back to
// its lead's, as a browser's Back: not in a terminal or a desktop you
// control, which get their keys, nor on a Mac in a text field, where ⌥←
// moves a word.
const closest = (event: Keyish, selector: string) => !!(event.target as { closest?: (s: string) => unknown } | null)?.closest?.(selector);
export const isBackToLeadShortcut = (event: Keyish, mac: boolean) =>
  event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey && event.key === 'ArrowLeft' && !closest(event, '.xterm, canvas') && !(mac && closest(event, 'input, textarea, [contenteditable="true"]'));

// The places F6 moves between, in order, each found by its selector; one that
// isn't on screen (the panel, closed) is skipped. focus is what in it takes
// the focus, the first that is there.
export const regions: { name: 'sidebar' | 'chat' | 'panel'; selector: string; focus: string[] }[] = [
  { name: 'sidebar', selector: '[data-sidebar-v2]', focus: ['[aria-current="page"]', '[data-v2-new-task]'] },
  { name: 'chat', selector: '[data-chat-composer]', focus: ['textarea:not(:disabled)', 'button:not(:disabled)'] },
  { name: 'panel', selector: '[data-right-panel]', focus: ['[role="tab"][aria-selected="true"]', '[data-panel-launcher]', 'button'] },
];

// nextRegion is the index F6 goes to from current (-1 for none of them),
// among the ones there: forward, or back with Shift.
export function nextRegion(current: number, there: boolean[], back: boolean): number {
  const n = there.length;
  const from = current === -1 && back ? n : current;
  for (let step = 1; step <= n; step++) {
    const i = (((back ? from - step : from + step) % n) + n) % n;
    if (there[i]) return i;
  }
  return -1;
}

// cycleRegion moves the focus to the next place on F6.
export function cycleRegion(back: boolean, doc: Document = document): boolean {
  const visible = (el: Element | null): el is HTMLElement => !!el && (el as HTMLElement).getClientRects().length > 0;
  const els = regions.map((r) => [...doc.querySelectorAll(r.selector)].find(visible) ?? null);
  const active = doc.activeElement;
  const current = els.findIndex((el) => !!el && !!active && el.contains(active));
  const to = nextRegion(current, els.map(Boolean), back);
  if (to < 0) return false;
  const el = els[to]!;
  const target = regions[to].focus.map((s) => el.querySelector<HTMLElement>(s)).find((x) => x) ?? el;
  if (!target.hasAttribute('tabindex') && target === el) target.tabIndex = -1;
  target.focus();
  return true;
}

// The shortcut sheet's groups. keys is how each reads, with "Mod" for Ctrl
// (⌘ on a Mac).
export const sheet: { title: MessageKey; rows: { keys: string[]; label: MessageKey }[] }[] = [
  {
    title: 'keys.group.app',
    rows: [
      { keys: ['Mod', 'K'], label: 'keys.search' },
      { keys: ['Mod', 'N'], label: 'keys.newTask' },
      { keys: ['Mod', ','], label: 'keys.settings' },
      { keys: ['Mod', '/'], label: 'keys.sheet' },
      { keys: ['Alt', '←'], label: 'keys.backToLead' },
      { keys: ['F6'], label: 'keys.f6' },
      { keys: ['Shift', 'F6'], label: 'keys.f6back' },
    ],
  },
  {
    title: 'keys.group.chat',
    rows: [
      { keys: ['Enter'], label: 'keys.send' },
      { keys: ['Shift', 'Enter'], label: 'keys.newLine' },
      { keys: ['↑'], label: 'keys.recall' },
      { keys: ['@'], label: 'keys.mention' },
      { keys: ['/'], label: 'keys.commands' },
      { keys: ['Mod', 'F'], label: 'keys.find' },
    ],
  },
  {
    title: 'keys.group.panel',
    rows: [
      { keys: ['Mod', 'D'], label: 'keys.panel' },
      { keys: ['F', 'D', 'T', 'B', 'P', 'M'], label: 'keys.launcher' },
      { keys: ['Delete'], label: 'keys.closeTab' },
    ],
  },
  {
    title: 'keys.group.review',
    rows: [
      { keys: ['←', '→'], label: 'keys.reviewSlides' },
      { keys: ['N'], label: 'keys.reviewNext' },
      { keys: ['P'], label: 'keys.reviewPrevious' },
      { keys: ['C'], label: 'keys.reviewChanges' },
      { keys: ['M'], label: 'keys.reviewMerge' },
      { keys: ['Esc'], label: 'keys.reviewClose' },
    ],
  },
];

// The sheet is one for the app, opened by Ctrl+/.
let open = false;
const listeners = new Set<() => void>();
const set = (next: boolean) => {
  open = next;
  for (const fn of listeners) fn();
};
export const setSheetOpen = (next: boolean) => set(next);
export const toggleSheet = () => set(!open);
export function useSheetOpen(): boolean {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    () => open,
  );
}
