// Which of an agent's or a project's tabs the Classic tab bar shows. The ones
// that only show what an agent made (its emulator, snapshots, desktop, media)
// wait until there is something in them; the rest are always there. A tab
// that's open stays while it is, even once what it showed is gone, so it
// doesn't vanish under the user; it goes when they leave it. A hidden tab is
// opened from the bar's + menu, where starting what it shows begins.
import type * as T from '../../shared/api';
import type { AgentTab, ProjectTab } from './tabs';

// The agent's tabs that show only once there's something in them, in the
// order the bar has them.
export const contextualAgentTabs = ['browser', 'android', 'media', 'snapshots'] as const satisfies readonly AgentTab[];
export type ContextualAgentTab = (typeof contextualAgentTabs)[number];

export const contextualProjectTabs = ['media', 'past'] as const satisfies readonly ProjectTab[];
export type ContextualProjectTab = (typeof contextualProjectTabs)[number];

// hiddenTabs is the contextual tabs the bar leaves out: those with nothing in
// them, but never the open one.
export function hiddenTabs<Tab extends string>(tabs: readonly Tab[], has: Record<Tab, boolean>, active: string): Tab[] {
  return tabs.filter((tab) => !has[tab] && tab !== active);
}

// browserInUse is whether the agent's desktop has a page open on it. A
// browser showing nothing but a blank page has nothing to see yet.
export function browserInUse(status: T.BrowserStatus | undefined): boolean {
  return !!status?.running && (status.pages ?? []).some((page) => page.url !== '' && page.url !== 'about:blank');
}
