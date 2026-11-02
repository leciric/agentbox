// A prototype of the navigation as three verbs, behind a flag, off by
// default (Settings → General → "Ask, Watch, Review"): Ask is the leads'
// chats (Home's and each project's), Watch the agents and their machines,
// Review their pull requests, proof and what waits for a verdict. Everything
// else is in Settings or a project's Settings. It's how this app looks on this
// computer: localStorage, switched at once.
import { useSyncExternalStore } from 'react';
import type { View } from '../App';

const storageKey = 'agentbox.ui.verbs';
const listeners = new Set<() => void>();
let current: boolean | undefined;

export function uiVerbs(): boolean {
  if (current === undefined) {
    try {
      current = localStorage.getItem(storageKey) === '1';
    } catch {
      current = false;
    }
  }
  return current;
}

export function setUiVerbs(on: boolean) {
  current = on;
  try {
    localStorage.setItem(storageKey, on ? '1' : '0');
  } catch {
    // Kept for this session, if not for the next.
  }
  for (const l of listeners) l();
}

export const useUiVerbs = () =>
  useSyncExternalStore((l) => {
    listeners.add(l);
    return () => listeners.delete(l);
  }, uiVerbs);

export type Verb = 'ask' | 'watch' | 'review' | 'settings';

// verbOf is the area a view belongs to, which the sidebar lights.
export function verbOf(view: View): Verb {
  switch (view.kind) {
    case 'watch':
      return 'watch';
    // Reading a past agent, the week's recap and comparing a fan-out's
    // agents are reviewing what was made.
    case 'review':
    case 'pastAgents':
    case 'recap':
    case 'group':
      return 'review';
    case 'settings':
      return 'settings';
    default:
      return 'ask';
  }
}

// asVerbView is where a view of the old navigation goes in this one: the
// places that moved open where they are now, so every link into them
// (a sidebar's "N to review", a notification, a search result) still lands.
// Jobs is a section of Settings now, which App opens with openSettingsSection.
export function asVerbView(view: View): View {
  switch (view.kind) {
    case 'work':
      return { kind: 'review', tab: 'work', project: view.project, filter: view.filter };
    case 'media':
      return { kind: 'review', tab: 'proof' };
    // An agent's machine is Watch's; its chat, and the rest, stay where they are.
    case 'agent':
      return view.tab === 'terminal' || view.tab === 'browser' || view.tab === 'android' ? { kind: 'watch', ref: view.ref, place: view.tab } : view;
    default:
      return view;
  }
}
