// What the tray icon (tray.ts) shows, worked out from what the daemon's API
// answers, the same reads the window's inbox makes (renderer/lib/inbox.ts):
// GET /v1/agents, the unseen questions (GET /v1/notifications?unseen=1) and
// Ready to review (GET /v1/review). Kept apart from Electron so it can be
// tested; tray.ts turns the entries into a Menu.
//
// Working is an agent mid-turn (chat "running", not stalled), as the rail
// says "Working". Waiting on the user is a question passed to them, an AI
// tool asking for a permission (chat "waiting") and an agent ready to review.
import type * as T from '../shared/api';
import { t } from '../shared/i18n/index.ts';
import type { TrayTone } from './trayicon.ts';

export interface TrayData {
  agents: T.Agent[];
  projects: T.Project[];
  questions: T.NotificationPage;
  // Absent when the user turned reviewing off (Settings → General).
  review?: T.ReviewPage;
}

// Where an entry goes: an agent's page, a question (where it's answered), a
// project's page, or a pull request in the browser.
export type TrayTarget =
  | { kind: 'agent'; ref: string }
  | { kind: 'question'; notice: T.Notification }
  | { kind: 'project'; project: string }
  | { kind: 'url'; url: string };

export type TrayItemKind = 'question' | 'permission' | 'review' | 'working';

export interface TrayItem {
  kind: TrayItemKind;
  label: string;
  target: TrayTarget;
}

export interface TrayProject {
  name: string;
  label: string;
  items: TrayItem[];
  more: number; // items left out, for "N more"
}

export interface TrayStatus {
  connected: boolean;
  working: number;
  waiting: number;
  running: number; // agents whose machine runs, which Pause all would pause
  tone: TrayTone;
  projects: TrayProject[];
}

// perProject is the most items a project lists before "N more".
export const perProject = 6;
const labelLength = 60;

export const offline: TrayStatus = { connected: false, working: 0, waiting: 0, running: 0, tone: 'offline', projects: [] };

// oneLine keeps a label to a menu's single line.
export function oneLine(text: string, limit = labelLength): string {
  const flat = text.replace(/\s+/g, ' ').trim();
  return flat.length > limit ? `${flat.slice(0, limit - 1).trimEnd()}…` : flat;
}

const agentLabel = (a: Pick<T.Agent, 'name' | 'title'>) => a.title || a.name;

export function trayStatus(data: TrayData | undefined): TrayStatus {
  if (!data) return offline;
  const byRef = new Map(data.agents.map((a) => [a.ref, a]));
  const items = new Map<string, TrayItem[]>();
  const add = (project: string, item: TrayItem) => items.set(project, [...(items.get(project) ?? []), item]);

  for (const n of data.questions.items) {
    const who = n.title || byRef.get(n.ref)?.title || n.agent;
    add(n.project, { kind: 'question', label: oneLine(n.text ? `${who}: ${n.text}` : who), target: { kind: 'question', notice: n } });
  }
  const permissions = data.agents.filter((a) => a.chat === 'waiting');
  for (const a of permissions) add(a.project, { kind: 'permission', label: oneLine(t('main.tray.permission', { agent: agentLabel(a) })), target: { kind: 'agent', ref: a.ref } });
  for (const r of data.review?.items ?? []) {
    const label = oneLine(r.pr ? t('main.tray.reviewPR', { agent: r.title || r.agent, number: String(r.pr.number) }) : r.title || r.agent);
    add(r.project, { kind: 'review', label, target: r.pr?.url ? { kind: 'url', url: r.pr.url } : { kind: 'agent', ref: r.ref } });
  }
  const working = data.agents.filter((a) => a.chat === 'running' && !a.stalledSince);
  for (const a of working) add(a.project, { kind: 'working', label: oneLine(agentLabel(a)), target: { kind: 'agent', ref: a.ref } });

  // In the sidebar's order, with any project the list didn't have after.
  const order = [...data.projects.map((p) => p.name), ...[...items.keys()].filter((name) => !data.projects.some((p) => p.name === name))];
  const projects: TrayProject[] = [];
  for (const name of order) {
    const list = items.get(name);
    if (!list?.length) continue;
    const p = data.projects.find((x) => x.name === name);
    projects.push({ name, label: p?.displayName || name, items: list.slice(0, perProject), more: Math.max(0, list.length - perProject) });
  }

  const waiting = Math.max(data.questions.total, data.questions.items.length) + permissions.length + Math.max(data.review?.total ?? 0, data.review?.items.length ?? 0);
  return {
    connected: true,
    working: working.length,
    waiting,
    running: data.agents.filter((a) => a.state === 'running').length,
    tone: waiting ? 'attention' : working.length ? 'working' : 'idle',
    projects,
  };
}

// summary is the menu's first line and the icon's tooltip.
export function summary(status: TrayStatus): string {
  if (!status.connected) return t('main.tray.offline');
  if (!status.working && !status.waiting) return t('main.tray.quiet');
  const parts: string[] = [];
  if (status.working) parts.push(t('main.tray.working', { count: status.working }));
  if (status.waiting) parts.push(t('main.tray.waiting', { count: status.waiting }));
  return parts.join(' · ');
}

// macTitle is the text beside a Mac's menu bar icon, which has no room for
// badges: what waits for the user, then what's working.
export function macTitle(status: TrayStatus): string {
  return [status.waiting ? `●${status.waiting}` : '', status.working ? `▶${status.working}` : ''].filter(Boolean).join(' ');
}

export type TrayAction = { kind: 'open' } | { kind: 'go'; target: TrayTarget } | { kind: 'pauseAll' } | { kind: 'stopVM' } | { kind: 'keepRunning' } | { kind: 'quit' };

export interface TrayEntry {
  type?: 'separator' | 'checkbox';
  label?: string;
  enabled?: boolean;
  checked?: boolean;
  icon?: TrayItemKind;
  action?: TrayAction;
}

export interface TrayMenuOptions {
  // AgentBox's VM, when it runs in one: whether it's on, so it can be shut down.
  vm: 'on' | 'off' | 'none';
  keepRunning: boolean;
}

// trayMenu is the menu, top to bottom: the summary, what needs the user and
// what's working by project, then the app's actions.
export function trayMenu(status: TrayStatus, options: TrayMenuOptions): TrayEntry[] {
  const entries: TrayEntry[] = [{ label: summary(status), enabled: false }];
  for (const p of status.projects) {
    entries.push({ type: 'separator' }, { label: p.label, enabled: false });
    for (const item of p.items) entries.push({ label: item.label, icon: item.kind, action: { kind: 'go', target: item.target } });
    if (p.more) entries.push({ label: t('main.tray.more', { count: p.more }), action: { kind: 'go', target: { kind: 'project', project: p.name } } });
  }
  entries.push(
    { type: 'separator' },
    { label: t('main.tray.open'), action: { kind: 'open' } },
    { label: t('main.tray.pauseAll'), enabled: status.running > 0, action: { kind: 'pauseAll' } },
  );
  if (options.vm !== 'none') entries.push({ label: t('main.tray.stopVM'), enabled: options.vm === 'on', action: { kind: 'stopVM' } });
  entries.push(
    { type: 'separator' },
    { type: 'checkbox', label: t('main.tray.keepRunning'), checked: options.keepRunning, action: { kind: 'keepRunning' } },
    { label: t('main.tray.quit'), action: { kind: 'quit' } },
  );
  return entries;
}
