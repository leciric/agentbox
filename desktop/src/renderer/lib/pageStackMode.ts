// When a chat's pages stack shows (components/pages/PageStack.tsx): always,
// with the chat's live pages and the new ones marked, only while there are
// new pages (the default), or never. It's how this app looks on this
// computer, so it lives in localStorage beside the top bar's layout
// (topBarLayout.ts). Toasts and notifications for a new page are not part of it.
import { useSyncExternalStore } from 'react';

export type PageStackMode = 'always' | 'new' | 'never';
export const pageStackModes: PageStackMode[] = ['always', 'new', 'never'];

const storageKey = 'agentbox.pages.stack';
const listeners = new Set<() => void>();
let current: PageStackMode | undefined;

const parse = (v: string | null): PageStackMode => (pageStackModes.includes(v as PageStackMode) ? (v as PageStackMode) : 'new');

export function pageStackMode(): PageStackMode {
  if (current === undefined) {
    try {
      current = parse(localStorage.getItem(storageKey));
    } catch {
      current = 'new';
    }
  }
  return current;
}

export function setPageStackMode(mode: PageStackMode) {
  current = mode;
  try {
    localStorage.setItem(storageKey, mode);
  } catch {
    // Kept for this session, if not for the next.
  }
  for (const listener of listeners) listener();
}

export function usePageStackMode(): PageStackMode {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    pageStackMode,
  );
}
