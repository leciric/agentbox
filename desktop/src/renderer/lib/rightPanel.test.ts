import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api.ts';
import type { Whose } from './rightPanel.ts';

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

test('the lead and a past agent have files and evidence, but no machine views, and the lead no timeline', () => {
  const lead: Whose = { kind: 'lead', ref: 'pawly/lead' };
  const gone: Whose = { kind: 'past', past: past('agent-1') };
  const live: Whose = { kind: 'agent', agent: agent('agent-2', 'stopped') };
  for (const view of P.panelViews) {
    const wide = P.projectWide(view);
    assert.equal(P.hasView(lead, view), wide || (!P.needsMachine(view) && view !== 'timeline'), `lead ${view}`);
    assert.equal(P.hasView(gone, view), wide || !P.needsMachine(view), `past ${view}`);
    assert.equal(P.hasView(live, view), true, `agent ${view}`);
  }
  assert.deepEqual(P.panelViews.filter(P.needsMachine), ['desktop', 'terminal', 'browser']);
  assert.deepEqual(P.panelViews.filter(P.projectWide), ['pulls', 'schedules', 'settings']);
  // The switch's four, and the ⋯'s the rest: every view is in one of them.
  assert.deepEqual(P.switchViews, ['desktop', 'terminal', 'files', 'evidence']);
  // Pull requests and Settings open from the project's header, not the ⋯.
  assert.deepEqual([...P.switchViews, ...P.moreViews, 'pulls', 'settings'].toSorted(), [...P.panelViews].toSorted());
  // What each opens on before it was shown in any.
  assert.equal(P.defaultView(live), 'desktop');
  assert.equal(P.defaultView(lead), 'files');
  assert.equal(P.defaultView(gone), 'evidence');
});

test('the picker lists the project’s agents with a machine, newest first', () => {
  const agents = [agent('old', 'stopped', '2026-09-01T00:00:00Z'), agent('new', 'running', '2026-10-05T00:00:00Z'), agent('else', 'running', '2026-10-06T00:00:00Z', 'other')];
  assert.deepEqual(
    P.pickable('pawly', agents).map((a) => a.name),
    ['new', 'old'],
  );
});

test('an agent’s computer is live while its desktop is used, on, or asleep once it finished', () => {
  assert.equal(P.deskOf(agent('a', 'running'), true), 'live');
  assert.equal(P.deskOf(agent('a', 'running'), false), 'on');
  assert.equal(P.deskOf(agent('a', 'stopped'), true), 'asleep');
  assert.equal(P.deskOf(agent('a', 'paused'), false), 'asleep');
});

test('beside a lead’s chat the panel keeps its one width; beside an agent’s it takes half the room until dragged', () => {
  assert.equal(P.panelWant({ width: 560 }, 'pawly/lead', 1600), 560);
  assert.equal(P.panelWant({ width: 560 }, 'pawly/agent-2', 1600), 800);
  assert.equal(P.panelWant({ width: 560, agentWidth: 700 }, 'pawly/agent-2', 1600), 700);
});

test('an agent’s chat shows its own agent until another is picked; the lead’s shows what was picked in it, else its list', () => {
  P.setPanel({ chats: {}, views: {}, last: {} });
  const lead = 'pawly/lead';
  const chat = 'pawly/agent-2';
  assert.equal(P.chatPanel(P.panelState(), chat).open, false);
  assert.equal(P.panelTarget(P.panelState(), chat), chat);
  // Nothing picked yet: the lead's shows the list.
  assert.equal(P.panelTarget(P.panelState(), lead), undefined);

  P.pickTarget(chat, 'pawly/agent-3');
  assert.equal(P.panelTarget(P.panelState(), chat), 'pawly/agent-3');
  assert.equal(P.chatPanel(P.panelState(), chat).open, true);
  // The lead's keeps its own: its list, until something is picked there.
  assert.equal(P.panelTarget(P.panelState(), lead), undefined);
  P.pickTarget(lead, 'pawly/lead');
  assert.equal(P.panelTarget(P.panelState(), lead), 'pawly/lead');
  assert.equal(P.chatPanel(P.panelState(), lead).target, undefined);
  P.setPanelOpen(chat, false);
  assert.equal(P.chatPanel(P.panelState(), chat).open, false);
});

test('each agent keeps the view it was last shown in; the project’s own views go over it', () => {
  P.setPanel({ chats: {}, views: {}, last: {} });
  const lead = 'pawly/lead';
  const two: Whose = { kind: 'agent', agent: agent('agent-2', 'running') };
  const three: Whose = { kind: 'agent', agent: agent('agent-3', 'running') };
  const repo: Whose = { kind: 'lead', ref: lead };
  P.openPanelOn(lead, 'terminal', 'pawly/agent-2');
  assert.equal(P.viewOf(P.panelState(), two), 'terminal');
  assert.equal(P.viewOf(P.panelState(), three), 'desktop');
  P.setView(lead, 'pawly/agent-3', 'evidence', 'media-1');
  assert.equal(P.viewOf(P.panelState(), three), 'evidence');
  assert.equal(P.chatPanel(P.panelState(), lead).focus, 'media-1');
  // The repository was last in a view it hasn't (it can't be, but a saved
  // state can say anything): it opens on its own instead.
  P.setPanel({ views: { ...P.panelState().views, [lead]: 'desktop' } });
  assert.equal(P.viewOf(P.panelState(), repo), 'files');

  P.openPanelOn(lead, 'pulls');
  assert.equal(P.chatPanel(P.panelState(), lead).wide, 'pulls');
  assert.equal(P.viewOf(P.panelState(), two), 'terminal');
  assert.equal(P.onList(P.panelState(), lead), false);
  // A view of the switch, or another agent, leaves it.
  P.pickTarget(lead, 'pawly/agent-2');
  assert.equal(P.chatPanel(P.panelState(), lead).wide, undefined);
  assert.equal(P.panelTarget(P.panelState(), lead), 'pawly/agent-2');
  assert.equal(P.panelState().views['pawly/agent-2'], 'terminal');
});

test('opening an agent’s chat opens its panel on its Desktop, where it fits beside the chat', () => {
  P.setPanel({ chats: {}, views: { 'pawly/agent-2': 'files' }, last: {} });
  P.setFitsBeside(false);
  P.openAgentChat('pawly/agent-2');
  assert.equal(P.chatPanel(P.panelState(), 'pawly/agent-2').open, false);
  P.setFitsBeside(true);
  P.pickTarget('pawly/agent-2', 'pawly/agent-9');
  P.setPanelOpen('pawly/agent-2', false);
  P.openAgentChat('pawly/agent-2');
  assert.deepEqual(P.chatPanel(P.panelState(), 'pawly/agent-2'), { open: true, target: 'pawly/agent-2' });
  assert.equal(P.panelState().views['pawly/agent-2'], 'desktop');
  // The lead's panel keeps its own: back there, it shows what it showed.
  assert.equal(P.panelState().last.pawly, undefined);
});

test('the panel is remembered, but not what Evidence was opened on; a panel saved with tabs opens again', async () => {
  P.setPanel({ width: 610, chats: {}, views: {}, last: {} });
  P.setView('pawly/lead', 'pawly/agent-2', 'evidence', 'media-1');
  const saved = JSON.parse(store.get('agentbox.ui.v2.panel')!);
  assert.equal(saved.width, 610);
  assert.deepEqual(saved.chats['pawly/lead'], { open: true });
  assert.deepEqual(saved.views, { 'pawly/agent-2': 'evidence' });
  // Before the switch: tabs, an active one, a machine filter.
  store.set('agentbox.ui.v2.panel', JSON.stringify({ width: 500, withMachine: false, chats: { 'pawly/lead': { open: true, tabs: [{ id: 'a', kind: 'files' }], active: 'a' } }, views: { x: 'nonsense' } }));
  // A second copy of the module, to read it as a launch would.
  const again = './rightPanel.ts?saved';
  const fresh = (await import(again)) as typeof P;
  assert.deepEqual(fresh.panelState().chats, { 'pawly/lead': { open: true } });
  assert.deepEqual(fresh.panelState().views, {});
  assert.equal(fresh.panelState().width, 500);
  assert.equal(fresh.panelState().section, 'repository');
});

test('a recording’s chapters are the titled steps it shows, from the one under way as it started', () => {
  const rec = { createdAt: '2026-10-01T10:00:00Z', meta: { duration: 120 } };
  const steps = [
    { title: 'Read the brief', start: '2026-10-01T09:58:00Z', end: '2026-10-01T09:59:00Z' },
    { title: 'Opened the page', start: '2026-10-01T09:59:30Z', end: '2026-10-01T10:00:40Z' },
    { title: '', start: '2026-10-01T10:00:40Z', end: '2026-10-01T10:01:00Z' },
    { title: 'Ran the tests', start: '2026-10-01T10:01:30Z', end: '2026-10-01T10:03:00Z' },
    { title: 'Opened the PR', start: '2026-10-01T10:02:00Z', end: '2026-10-01T10:02:10Z' },
  ];
  assert.deepEqual(P.chaptersOf(rec, steps), [
    { at: 0, title: 'Opened the page' },
    { at: 90, title: 'Ran the tests' },
  ]);
  // A recording inside one step has no chapters, nor one with no length.
  assert.deepEqual(P.chaptersOf({ createdAt: '2026-10-01T10:01:40Z', meta: { duration: 10 } }, steps), []);
  assert.deepEqual(P.chaptersOf({ createdAt: rec.createdAt, meta: {} }, steps), []);
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

test('the Browser view offers the ports the agent’s Chromium has open, through the preview proxy', () => {
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

test('the back arrow leaves the project’s own view first, then goes to the list', () => {
  P.setPanel({ chats: {}, views: {}, last: {} });
  const lead = 'pawly/lead';
  const chat = 'pawly/agent-2';
  P.openPanelOn(lead, 'desktop', 'pawly/agent-3');
  P.openPanelOn(lead, 'settings');
  P.panelBack(lead);
  assert.equal(P.chatPanel(P.panelState(), lead).wide, undefined);
  assert.equal(P.panelTarget(P.panelState(), lead), 'pawly/agent-3');
  P.panelBack(lead);
  assert.equal(P.panelTarget(P.panelState(), lead), undefined);
  assert.equal(P.chatPanel(P.panelState(), lead).open, true);
  assert.equal(P.onList(P.panelState(), lead), true);
  // An agent's chat shows itself until its back arrow; opening its chat again shows it again.
  P.panelBack(chat);
  assert.equal(P.panelTarget(P.panelState(), chat), undefined);
  P.setFitsBeside(true);
  P.openAgentChat(chat);
  assert.equal(P.panelTarget(P.panelState(), chat), chat);
});

test('the list is narrow, as dragged while the chat keeps its least, and folds to a strip', () => {
  assert.deepEqual(P.listBox({}, 1600, 1900), { width: P.listDefault, drawer: false });
  assert.deepEqual(P.listBox({ listWidth: 900 }, 1000, 1300), { width: 1000 - P.chatMin, drawer: false });
  assert.deepEqual(P.listBox({ listWidth: 100 }, 1600, 1900), { width: P.listMin, drawer: false });
  assert.deepEqual(P.listBox({ listFolded: true }, 1600, 1900), { width: P.listStrip, drawer: false });
  // Below besideMin, a drawer, as the panel is.
  assert.equal(P.listBox({}, 900, 1000).drawer, true);
});

test('✕ goes back to the list; the panel button folds the list and back, and never closes it', () => {
  P.setPanel({ chats: {}, views: {}, last: {}, listFolded: false });
  P.setFitsBeside(true);
  const lead = 'pawly/lead';
  P.pickTarget(lead, lead);
  P.panelClose(lead);
  assert.equal(P.onList(P.panelState(), lead), true);
  P.openPanelOn(lead, 'settings');
  P.panelClose(lead);
  assert.equal(P.onList(P.panelState(), lead), true);
  // An agent's chat goes back to its list too.
  P.openAgentChat('pawly/agent-2');
  P.panelClose('pawly/agent-2');
  assert.equal(P.onList(P.panelState(), 'pawly/agent-2'), true);
  // The button and ⌘D: from an agent, to the list; on the list, fold and back.
  P.openPanelOn(lead, 'files', 'pawly/agent-3');
  P.panelToggle(lead);
  assert.equal(P.onList(P.panelState(), lead), true);
  assert.equal(P.panelState().listFolded, false);
  P.panelToggle(lead);
  assert.equal(P.panelState().listFolded, true);
  P.panelToggle(lead);
  assert.equal(P.panelState().listFolded, false);
  // Where it doesn't fit beside the chat, it opens and closes the drawer over the strip.
  P.setFitsBeside(false);
  P.setPanelOpen(lead, false);
  P.panelToggle(lead);
  assert.equal(P.chatPanel(P.panelState(), lead).open, true);
  P.panelToggle(lead);
  assert.equal(P.chatPanel(P.panelState(), lead).open, false);
  P.setFitsBeside(true);
});

test('the lead’s panel switches between Agents, Pull requests and Files, remembered per project', async () => {
  P.setPanel({ chats: {}, views: {}, last: {} });
  const lead = 'pawly/lead';
  assert.equal(P.leadTab(P.panelState(), 'pawly'), 'agents');
  P.setView(lead, lead, 'evidence');
  P.setLeadTab('pawly', 'files');
  assert.equal(P.leadTab(P.panelState(), 'pawly'), 'files');
  assert.equal(P.panelTarget(P.panelState(), lead), lead);
  // Files is the repository's files, whatever it was last shown in.
  assert.equal(P.viewOf(P.panelState(), { kind: 'lead', ref: lead }), 'files');
  assert.equal(P.leadTab(P.panelState(), 'other'), 'agents');
  // Another project's lead keeps its own tab.
  P.setLeadTab('other', 'files');
  P.setLeadTab('pawly', 'agents');
  assert.equal(P.leadTab(P.panelState(), 'pawly'), 'agents');
  assert.equal(P.onList(P.panelState(), lead), true);
  assert.equal(P.leadTab(P.panelState(), 'other'), 'files');
  // Remembered across a launch.
  const again = './rightPanel.ts?tabs';
  const fresh = (await import(again)) as typeof P;
  assert.equal(fresh.leadTab(fresh.panelState(), 'other'), 'files');
  // An agent shown in the lead's panel is neither tab: back goes to Agents.
  P.openPanelOn(lead, 'desktop', 'pawly/agent-3');
  assert.equal(P.leadTab(P.panelState(), 'pawly'), 'agents');
});

test('Pull requests is a tab of the lead’s panel, over whoever it showed, and what opens them switches to it', () => {
  P.setPanel({ chats: {}, views: {}, last: {} });
  const lead = 'pawly/lead';
  P.setLeadTab('pawly', 'files');
  P.setLeadTab('pawly', 'pulls');
  assert.equal(P.leadTab(P.panelState(), 'pawly'), 'pulls');
  assert.equal(P.chatPanel(P.panelState(), lead).wide, 'pulls');
  // Files again, as it was under them.
  P.setLeadTab('pawly', 'files');
  assert.equal(P.leadTab(P.panelState(), 'pawly'), 'files');
  assert.equal(P.chatPanel(P.panelState(), lead).wide, undefined);
  // From Pull requests, Agents is the list, not the Files they went over.
  P.setLeadTab('pawly', 'pulls');
  P.setLeadTab('pawly', 'agents');
  assert.equal(P.leadTab(P.panelState(), 'pawly'), 'agents');
  assert.equal(P.onList(P.panelState(), lead), true);
  // A link or the search opens them as the panel's tab, over an agent too.
  P.openPanelOn(lead, 'desktop', 'pawly/agent-3');
  P.openPanelOn(lead, 'pulls');
  assert.equal(P.leadTab(P.panelState(), 'pawly'), 'pulls');
  assert.equal(P.chatPanel(P.panelState(), lead).open, true);
  // ✕, the back arrow and ⌘D beside the chat leave them for the list.
  P.panelClose(lead);
  assert.equal(P.leadTab(P.panelState(), 'pawly'), 'agents');
});
