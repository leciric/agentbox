// The search modal (components/SearchModal.tsx) is one for the whole app,
// rendered by App, and anything can open it: Ctrl+K or Ctrl+Shift+F, the top
// bar's button, or a component with a search to hand over — a chat's find bar
// opening it on what it was finding, narrowed to chats.
import { useSyncExternalStore } from 'react';

// SearchScope is what the modal opens on; anything left out starts empty or
// at "all".
export type SearchScope = { query?: string; kind?: string; project?: string; since?: SearchSince };

// SearchSince is how far back the modal looks: any time, or the past day,
// week or month.
export type SearchSince = '' | 'day' | 'week' | 'month';

type State = { open: boolean; scope: SearchScope; opening: number };
let state: State = { open: false, scope: {}, opening: 0 };
const listeners = new Set<() => void>();
const set = (next: State) => {
  state = next;
  for (const fn of listeners) fn();
};

// openSearch opens the modal on scope, starting it afresh even when it is
// already open.
export function openSearch(scope: SearchScope = {}): void {
  set({ open: true, scope, opening: state.opening + 1 });
}

export function closeSearch(): void {
  if (state.open) set({ ...state, open: false });
}

// toggleSearch is the shortcuts': open it unscoped, or close it.
export function toggleSearch(): void {
  if (state.open) closeSearch();
  else openSearch();
}

// searchState is where the modal is: open or not, on what scope, and how
// many times it has been opened, which starts it afresh each time.
export function searchState(): State {
  return state;
}

export function useSearchModal(): State {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    searchState,
  );
}

// sinceTime is the ISO time a SearchSince reaches back to from now.
export function sinceTime(since: SearchSince | undefined, now = Date.now()): string | undefined {
  const day = 24 * 60 * 60 * 1000;
  const back = since === 'day' ? day : since === 'week' ? 7 * day : since === 'month' ? 30 * day : 0;
  return back ? new Date(now - back).toISOString() : undefined;
}
