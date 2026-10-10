// Where a project's or an agent's page opens, from the name of a tab or of a
// section of its Settings tab. Memory, Tokens, Secrets and Connectors were tabs
// of their own, and Overview was what Settings is now: their names still open
// the section they became, so a link or a remembered tab from before lands on
// the same panel. A project's Agents tab is gone (the sidebar lists its
// agents), and lands on its chat.

export type ProjectTab = 'chat' | 'pulls' | 'media' | 'settings';
export type ProjectSection = 'repository' | 'general' | 'brief' | 'memory' | 'tokens' | 'secrets' | 'connectors' | 'skills';

export type AgentTab = 'chat' | 'terminal' | 'browser' | 'android' | 'media' | 'settings' | 'snapshots';
export type AgentSection = 'machine' | 'code' | 'ai' | 'secrets' | 'connectors';

// What a project's page can be opened at: a tab, a section of its Settings
// tab, or the name of a tab from before them.
export type ProjectPlaceName = ProjectTab | ProjectSection | 'overview' | 'agents';

// What an agent's page can be opened at: a tab, a section of its Settings tab,
// or the name of a tab from before them.
export type AgentPlaceName = AgentTab | AgentSection | 'overview';

export const projectSections: readonly ProjectSection[] = ['repository', 'general', 'brief', 'memory', 'tokens', 'secrets', 'connectors', 'skills'];
export const agentSections: readonly AgentSection[] = ['machine', 'code', 'ai', 'secrets', 'connectors'];

const projectTabs: readonly string[] = ['chat', 'pulls', 'media', 'settings'];
const agentTabs: readonly string[] = ['chat', 'terminal', 'browser', 'android', 'media', 'settings', 'snapshots'];

export type Place<Tab, Section> = { tab: Tab; section: Section };

// projectPlace is the tab and Settings section a name opens on a project's
// page; anything it doesn't know opens the chat, where a project starts.
export function projectPlace(name: string | undefined): Place<ProjectTab, ProjectSection> {
  if (name === 'overview') return { tab: 'settings', section: 'repository' };
  if ((projectSections as readonly string[]).includes(name ?? '')) return { tab: 'settings', section: name as ProjectSection };
  if (name !== undefined && projectTabs.includes(name)) return { tab: name as ProjectTab, section: 'repository' };
  return { tab: 'chat', section: 'repository' };
}

// agentPlace is the same for an agent's page. A name it doesn't know leaves
// the tab unset, for the page to open on the agent's first tab.
export function agentPlace(name: string | undefined): Place<AgentTab | undefined, AgentSection> {
  if (name === 'overview') return { tab: 'settings', section: 'machine' };
  if ((agentSections as readonly string[]).includes(name ?? '')) return { tab: 'settings', section: name as AgentSection };
  if (name !== undefined && agentTabs.includes(name)) return { tab: name as AgentTab, section: 'machine' };
  return { tab: undefined, section: 'machine' };
}
