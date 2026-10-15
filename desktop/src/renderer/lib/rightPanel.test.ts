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

test('the lead and a past agent have files, pages and media, but no machine tabs', () => {
  const lead: Whose = { kind: 'lead', ref: 'pawly/lead' };
  const gone: Whose = { kind: 'past', past: past('agent-1') };
  const live: Whose = { kind: 'agent', agent: agent('agent-2', 'stopped') };
  for (const tab of P.panelTabs) {
    assert.equal(P.hasTab(lead, tab), !P.needsMachine(tab), `lead ${tab}`);
    assert.equal(P.hasTab(gone, tab), !P.needsMachine(tab), `past ${tab}`);
    assert.equal(P.hasTab(live, tab), true, `agent ${tab}`);
  }
  assert.deepEqual(P.panelTabs.filter(P.needsMachine), ['desktop', 'terminal', 'browser']);
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

test('the panel sits beside the chat while the chat keeps 360px, and is a drawer otherwise', () => {
  assert.deepEqual(P.panelBox(540, 1632, 1920), { width: 540, drawer: false });
  // It gives way to the chat's least, down to its own.
  assert.deepEqual(P.panelBox(540, 800, 1100), { width: 440, drawer: false });
  assert.deepEqual(P.panelBox(200, 1632, 1920), { width: P.panelMin, drawer: false });
  // No room for both: over the chat.
  assert.equal(P.panelBox(540, 680, 1100).drawer, true);
  // Below 1024px of window, always a drawer, no wider than the window.
  const small = P.panelBox(540, 800, 800);
  assert.equal(small.drawer, true);
  assert.ok(small.width <= 800 * 0.92);
  assert.ok(P.panelBox(540, 480, 480).width <= 480 - 24);
});

test('the panel opens closed, remembers its tab, width and filter, and a pick holds for its chat only', () => {
  assert.equal(P.panelState().open, false);
  assert.equal(P.panelState().withMachine, true);
  P.openPanelOn('pawly/lead', 'desktop', 'pawly/agent-2');
  let s = P.panelState();
  assert.equal(s.open, true);
  assert.equal(s.tab, 'desktop');
  assert.equal(P.pickedFor(s, 'pawly/lead'), 'pawly/agent-2');
  // Another chat in the middle follows that chat.
  assert.equal(P.pickedFor(s, 'pawly/agent-3'), 'pawly/agent-3');
  // Picking the chat itself is no pick.
  P.openPanelOn('pawly/lead', 'files', 'pawly/lead');
  s = P.panelState();
  assert.equal(s.picked, null);
  P.setPanel({ width: 600, withMachine: false });
  const saved = JSON.parse(store.get('agentbox.ui.v2.panel') ?? '{}');
  assert.deepEqual(saved, { tab: 'files', width: 600, withMachine: false });
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
  assert.deepEqual(P.panelTabs.filter(P.projectWide), ['pulls', 'settings']);
  assert.equal(P.needsMachine('settings'), false);
  assert.equal(P.panelState().section, 'repository');
});

test('the project’s settings and pull requests open the panel wide', () => {
  assert.equal(P.panelWant({ tab: 'settings', width: 540 }), P.panelWide);
  assert.equal(P.panelWant({ tab: 'pulls', width: 1000 }), 1000);
  assert.equal(P.panelWant({ tab: 'files', width: 540 }), 540);
});
