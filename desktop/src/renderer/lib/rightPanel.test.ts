import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api.ts';
import type { AgentWhose, Whose } from './rightPanel.ts';

const store = new Map<string, string>();
(globalThis as { localStorage?: unknown }).localStorage = {
  getItem: (k: string) => store.get(k) ?? null,
  setItem: (k: string, v: string) => void store.set(k, v),
  removeItem: (k: string) => void store.delete(k),
};
const P = await import('./rightPanel.ts');

const agent = (name: string, state: string, createdAt = '2026-10-01T00:00:00Z', project = 'pawly'): T.Agent =>
  ({ ref: `${project}/${name}`, project, name, title: `${name} title`, state, branch: `agentbox/${name}`, createdAt, worktree: `/w/${name}` }) as T.Agent;
const past = (name: string, archivedAt = '2026-10-02T00:00:00Z', project = 'pawly'): T.ArchivedAgent =>
  ({ ref: `${project}/${name}`, project, name, title: '', branch: `agentbox/${name}`, archivedAt }) as T.ArchivedAgent;

test('a machine is running, asleep when stopped or paused, and freed when gone', () => {
  assert.equal(P.machineOf(agent('a', 'running')), 'running');
  assert.equal(P.machineOf(agent('a', 'creating')), 'running');
  assert.equal(P.machineOf(agent('a', 'stopped')), 'asleep');
  assert.equal(P.machineOf(agent('a', 'paused')), 'asleep');
  assert.equal(P.machineOf(undefined), 'freed');
});

test('the lead and a past agent have files, pages and media, but no machine tabs, and the lead no timeline', () => {
  const lead: Whose = { kind: 'lead', ref: 'pawly/lead' };
  const gone: Whose = { kind: 'past', past: past('agent-1') };
  const live: Whose = { kind: 'agent', agent: agent('agent-2', 'stopped') };
  for (const tab of P.panelKinds) {
    assert.equal(P.hasTab(lead, tab), !P.needsMachine(tab) && tab !== 'timeline', `lead ${tab}`);
    assert.equal(P.hasTab(gone, tab), !P.needsMachine(tab), `past ${tab}`);
    assert.equal(P.hasTab(live, tab), true, `agent ${tab}`);
  }
  assert.deepEqual(P.panelKinds.filter(P.needsMachine), ['desktop', 'terminal', 'browser']);
  // Whose timeline: an agent's, live or past, never the lead's.
  const whose = P.targets('timeline', 'pawly', 'pawly/lead', [agent('agent-2', 'running')], [past('agent-1')], false);
  assert.deepEqual(whose.map((w) => w.kind), ['agent', 'past']);
});

test('whose is found among the agents, then the past ones; a retired pick is a past agent', () => {
  const agents = [agent('agent-2', 'running')];
  const gone = [past('agent-1')];
  assert.deepEqual(P.resolveWhose('pawly/lead', 'pawly', agents, gone), { kind: 'lead', ref: 'pawly/lead' });
  assert.equal(P.resolveWhose('pawly/agent-2', 'pawly', agents, gone)?.kind, 'agent');
  assert.equal(P.resolveWhose('past:pawly/agent-1', 'pawly', agents, gone)?.kind, 'past');
  assert.equal(P.resolveWhose('pawly/agent-1', 'pawly', agents, gone)?.kind, 'past');
  assert.equal(P.resolveWhose('pawly/agent-9', 'pawly', agents, gone), undefined);
  assert.equal(P.whoseRef({ kind: 'past', past: gone[0] }), 'past:pawly/agent-1');
  assert.equal(P.unPast('past:pawly/agent-1'), 'pawly/agent-1');
  assert.equal(P.isPastRef('pawly/agent-1'), false);
});

test('the picker lists the project’s agents newest first, and past ones without the machine filter', () => {
  const agents = [agent('old', 'stopped', '2026-09-01T00:00:00Z'), agent('new', 'running', '2026-10-05T00:00:00Z'), agent('else', 'running', '2026-10-06T00:00:00Z', 'other')];
  const gone = [past('gone-a', '2026-09-03T00:00:00Z'), past('gone-b', '2026-10-03T00:00:00Z'), past('new', '2026-10-04T00:00:00Z')];
  const names = (list: AgentWhose[]) => list.map((w) => (w.kind === 'agent' ? w.agent.name : `past:${w.past.name}`));
  assert.deepEqual(names(P.pickable('pawly', agents, gone, true)), ['new', 'old']);
  // A past agent with a live one of its ref (continued) is listed once.
  assert.deepEqual(names(P.pickable('pawly', agents, gone, false)), ['new', 'old', 'past:gone-b', 'past:gone-a']);
});

test('the panel sits beside the chat while the chat keeps 440px, and is a drawer otherwise', () => {
  assert.deepEqual(P.panelBox(540, 1632, 1920), { width: 540, drawer: false });
  // It gives way to the chat's least, down to its own.
  assert.deepEqual(P.panelBox(540, 880, 1100), { width: 440, drawer: false });
  assert.deepEqual(P.panelBox(200, 1632, 1920), { width: P.panelMin, drawer: false });
  // No room for both: over the chat.
  assert.equal(P.panelBox(540, 720, 1100).drawer, true);
  // Below 1100px of window, always a drawer, however much room: at 1024 the
  // chat beside it was left about 280px.
  assert.equal(P.panelBox(540, 1000, 1024).drawer, true);
  // Below 1024px of window, always a drawer, no wider than the window.
  const small = P.panelBox(540, 800, 800);
  assert.equal(small.drawer, true);
  assert.ok(small.width <= 800 * 0.92);
  assert.ok(P.panelBox(540, 480, 480).width <= 480 - 24);
});

test('a chat’s panel opens closed, on its launcher, and a card turns into a tab that asks whose it is', () => {
  const chat = 'pawly/lead';
  assert.deepEqual(P.chatPanel(P.panelState(), chat), { open: false, tabs: [], active: null });
  P.setPanelOpen(chat, true);
  // Opened with nothing in it: no tab, the launcher.
  let p = P.chatPanel(P.panelState(), chat);
  assert.equal(p.open, true);
  assert.deepEqual(p.tabs, []);
  P.chooseKind(chat, null, 'desktop');
  p = P.chatPanel(P.panelState(), chat);
  assert.equal(p.tabs.length, 1);
  assert.equal(p.active, p.tabs[0].id);
  assert.equal(P.needsTarget(p.tabs[0]), true);
  P.chooseTarget(chat, p.tabs[0].id, 'pawly/agent-2');
  p = P.chatPanel(P.panelState(), chat);
  assert.deepEqual(p.tabs[0], { id: p.tabs[0].id, kind: 'desktop', target: 'pawly/agent-2' });
  assert.equal(P.needsTarget(p.tabs[0]), false);
  // The project's own kinds need no one.
  assert.equal(P.needsTarget({ id: 'x', kind: 'settings' }), false);
  assert.equal(P.needsTarget({ id: 'x', kind: null }), false);
  // + adds a new tab on the launcher; its card turns that same tab.
  P.addTab(chat);
  p = P.chatPanel(P.panelState(), chat);
  const fresh = p.tabs[1];
  assert.equal(fresh.kind, null);
  assert.equal(p.active, fresh.id);
  P.chooseKind(chat, fresh.id, 'files');
  p = P.chatPanel(P.panelState(), chat);
  assert.deepEqual(p.tabs.map((t) => [t.id, t.kind]), [
    [p.tabs[0].id, 'desktop'],
    [fresh.id, 'files'],
  ]);
  // Another chat has its own panel.
  assert.equal(P.chatPanel(P.panelState(), 'pawly/agent-3').open, false);
  P.setPanel({ chats: {} });
});

test('tabs reorder, close onto their neighbour, and the last closes the panel', () => {
  const chat = 'pawly/agent-2';
  P.chooseKind(chat, null, 'files', 'pawly/agent-2');
  P.chooseKind(chat, null, 'terminal', 'pawly/agent-2');
  P.chooseKind(chat, null, 'media', 'pawly/lead');
  const ids = () => P.chatPanel(P.panelState(), chat).tabs.map((t) => t.kind);
  const id = (kind: string) => P.chatPanel(P.panelState(), chat).tabs.find((t) => t.kind === kind)?.id ?? '';
  P.moveTab(chat, id('media'), 0);
  assert.deepEqual(ids(), ['media', 'files', 'terminal']);
  P.moveTab(chat, id('media'), 2);
  assert.deepEqual(ids(), ['files', 'terminal', 'media']);
  P.activateTab(chat, id('terminal'));
  P.closeTab(chat, id('terminal'));
  // The one to its right shows.
  assert.equal(P.chatPanel(P.panelState(), chat).active, id('media'));
  P.closeTab(chat, id('media'));
  assert.equal(P.chatPanel(P.panelState(), chat).active, id('files'));
  P.closeTab(chat, id('files'));
  assert.deepEqual(P.chatPanel(P.panelState(), chat), { open: false, tabs: [], active: null });
});

test('closing a tab shows the nearest one that shows something, not a tab still asking whose', () => {
  const chat = 'pawly/lead';
  P.chooseKind(chat, null, 'files', 'pawly/lead');
  P.chooseKind(chat, null, 'desktop', 'pawly/agent-2');
  P.chooseKind(chat, null, 'browser');
  P.addTab(chat);
  const tabs = () => P.chatPanel(P.panelState(), chat).tabs;
  const [files, desktop, browser, fresh] = tabs().map((t) => t.id);
  P.activateTab(chat, desktop);
  P.closeTab(chat, desktop);
  // Its right-hand neighbour asks "Whose browser?" and the next is a new
  // tab: the one on its left shows.
  assert.equal(P.chatPanel(P.panelState(), chat).active, files);
  P.closeTab(chat, files);
  // Only those are left: the right-hand one, as before.
  assert.equal(P.chatPanel(P.panelState(), chat).active, browser);
  P.closeTab(chat, browser);
  assert.equal(P.chatPanel(P.panelState(), chat).active, fresh);
  P.setPanel({ chats: {} });
});

test('opening the panel on a kind finds its tab or adds one, and the tabs, width and filter are remembered', () => {
  const chat = 'pawly/lead';
  P.openPanelOn(chat, 'files', 'pawly/agent-2');
  P.openPanelOn(chat, 'settings');
  P.openPanelOn(chat, 'files', 'pawly/agent-2');
  let p = P.chatPanel(P.panelState(), chat);
  assert.equal(p.tabs.length, 2);
  assert.equal(p.tabs.find((t) => t.id === p.active)?.kind, 'files');
  // Another target is another tab; the project's own kinds are one tab.
  P.openPanelOn(chat, 'files', 'pawly/lead');
  P.openPanelOn(chat, 'settings');
  p = P.chatPanel(P.panelState(), chat);
  assert.equal(p.tabs.length, 3);
  P.setPanel({ width: 600, withMachine: false });
  const saved = JSON.parse(store.get('agentbox.ui.v2.panel') ?? '{}');
  assert.equal(saved.width, 600);
  assert.equal(saved.withMachine, false);
  assert.deepEqual(saved.chats[chat], p);
  P.setPanel({ chats: {}, withMachine: true });
});

test('a tab asks for the repository or an agent, or only an agent with a machine, the chat’s own first', () => {
  const agents = [agent('agent-1', 'running', '2026-10-01T00:00:00Z'), agent('agent-2', 'stopped', '2026-10-05T00:00:00Z')];
  const gone = [past('agent-0')];
  const refs = (list: Whose[]) => list.map(P.whoseRef);
  assert.deepEqual(refs(P.targets('files', 'pawly', 'pawly/lead', agents, gone, false)), ['pawly/lead', 'pawly/agent-2', 'pawly/agent-1', 'past:pawly/agent-0']);
  assert.deepEqual(refs(P.targets('files', 'pawly', 'pawly/agent-1', agents, gone, true)), ['pawly/lead', 'pawly/agent-1', 'pawly/agent-2']);
  // Desktop, Terminal and Browser: agents with a machine, filter or not.
  assert.deepEqual(refs(P.targets('terminal', 'pawly', 'pawly/lead', agents, gone, false)), ['pawly/agent-2', 'pawly/agent-1']);
});

test('the Browser tab offers the ports the agent’s Chromium has open, through the preview proxy', () => {
  const ports = P.browserPorts([
    { url: 'http://localhost:5173/preview.html?x=1', title: 'Preview' },
    { url: 'http://127.0.0.1:5173/other', title: 'Same port' },
    { url: 'https://example.com/', title: 'Elsewhere' },
    { url: 'about:blank', title: '' },
    { url: 'http://localhost:3000/', title: '' },
    { url: 'not a url', title: '' },
  ]);
  assert.deepEqual(
    ports.map((p) => [p.port, p.path]),
    [
      ['5173', '/preview.html?x=1'],
      ['3000', '/'],
    ],
  );
  assert.equal(P.previewUrl({ name: 'agent-2', project: 'pawly' }, '5173', '127.0.0.1:7777', '/preview.html'), 'http://5173.agent-2.pawly.localhost:7777/preview.html');
  assert.equal(P.previewUrl({ name: 'agent-2', project: 'pawly' }, '3000', ':7777'), 'http://3000.agent-2.pawly.localhost:7777/');
});

test('pull requests and settings are the project’s, whoever is picked', () => {
  assert.deepEqual(P.panelKinds.filter(P.projectWide), ['pulls', 'settings']);
  assert.equal(P.needsMachine('settings'), false);
  assert.equal(P.panelState().section, 'repository');
  // One width for every tab: no kind asks for more.
  assert.equal('panelWant' in P, false);
});

test('fitTabs shows what fits, the active tab always, and the rest in the menu', () => {
  const { fitTabs } = P;
  const tabs = ['a', 'b', 'c', 'd', 'e'].map((id) => ({ id, width: 60 }));
  assert.deepEqual(fitTabs(tabs, 'a', 300, 40), { shown: ['a', 'b', 'c', 'd', 'e'], hidden: [] });
  assert.deepEqual(fitTabs(tabs, 'a', 250, 40), { shown: ['a', 'b', 'c'], hidden: ['d', 'e'] });
  assert.deepEqual(fitTabs(tabs, 'e', 250, 40), { shown: ['a', 'b', 'e'], hidden: ['c', 'd'] });
  assert.deepEqual(fitTabs(tabs, 'e', 50, 40), { shown: ['e'], hidden: ['a', 'b', 'c', 'd'] });
  assert.deepEqual(fitTabs([], null, 100, 40), { shown: [], hidden: [] });
});

test('fitTabs keeps the tabs shown before: picking one from the menu takes the last place, clicking a shown one moves nothing', () => {
  const { fitTabs } = P;
  const tabs = ['a', 'b', 'c', 'd', 'e', 'f'].map((id) => ({ id, width: 60 }));
  const first = fitTabs(tabs, 'a', 280, 40);
  assert.deepEqual(first.shown, ['a', 'b', 'c', 'd']);
  // f picked from the menu: d makes room, a, b and c stay.
  const picked = fitTabs(tabs, 'f', 280, 40, first.shown);
  assert.deepEqual(picked, { shown: ['a', 'b', 'c', 'f'], hidden: ['d', 'e'] });
  // Then a clicked: f stays in view rather than d coming back.
  assert.deepEqual(fitTabs(tabs, 'a', 280, 40, picked.shown).shown, ['a', 'b', 'c', 'f']);
  // A kept tab that was closed is forgotten, and the room goes to the next.
  assert.deepEqual(fitTabs(tabs.filter((t) => t.id !== 'b'), 'a', 280, 40, picked.shown).shown, ['a', 'c', 'd', 'f']);
});

test('the launcher opens Files and the machine kinds on the agent whose page it is in, and asks in the lead\'s', () => {
  for (const kind of ['files', 'desktop', 'terminal', 'browser'] as const) {
    assert.equal(P.launcherTarget('pawly/agent-2', kind), 'pawly/agent-2');
    assert.equal(P.launcherTarget('pawly/lead', kind), undefined);
    assert.equal(P.launcherTarget('pawly', kind), undefined);
  }
  // Pages and Media keep asking, and the project's own tabs have no target.
  for (const kind of ['pages', 'media', 'pulls', 'settings'] as const) assert.equal(P.launcherTarget('pawly/agent-2', kind), undefined);
});
