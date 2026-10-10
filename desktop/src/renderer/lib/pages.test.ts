// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { beforeEach, test } from 'node:test';
import { QueryClient } from '@tanstack/react-query';
import * as T from '../../shared/api.ts';
import {
  allLivePages,
  arrivals,
  byAgent,
  countByAgent,
  isExpired,
  isNew,
  isPageCall,
  knowProject,
  livePageOf,
  livePages,
  markSeen,
  matchesPage,
  onChatItem,
  pageIdOf,
  pagesFor,
  pagesKey,
  readPages,
  resetSeen,
  showsArrival,
  stackPages,
  tileHue,
  watchChat,
  onScreenChats,
} from './pages.ts';

const now = new Date('2026-10-09T12:00:00Z').getTime();
const page = (a: Partial<T.Artifact>): T.Artifact => ({
  id: 'x', title: 'X', url: 'https://hatch.linting.dev/p/x', agent: 'pawly/lead', item: 'i', agents: ['pawly/lead'], version: 1,
  createdAt: '2026-10-09T11:00:00Z', updatedAt: '2026-10-09T11:00:00Z', expiresAt: '2026-10-10T11:00:00Z', ...a,
});
const hatch = (...artifacts: T.Artifact[]): T.Artifacts => ({ connector: 'hatch', artifacts });
const noHatch = (...artifacts: T.Artifact[]): T.Artifacts => ({ connector: '', artifacts });
const expired = page({ id: 'old', agent: 'pawly/agent-02', agents: ['pawly/agent-02'], expired: true });
const lapsed = page({ id: 'lapsed', agent: 'pawly/agent-02', agents: ['pawly/agent-02'], expiresAt: '2026-10-09T11:59:00Z' });
const tool = (t: Partial<T.ChatTool>): T.ChatTool => ({ callId: 'c', title: '', kind: 'other', status: 'completed', name: 'mcp__hatch__publish_page', ...t });
const ids = (list: { id: string }[]) => list.map((a) => a.id);

beforeEach(() => resetSeen());

test('a page Hatch let expire is in no list', () => {
  assert.ok(isExpired(expired, now));
  assert.ok(isExpired(lapsed, now));
  assert.ok(!isExpired(page({ permanent: true, expiresAt: undefined }), now));
  assert.ok(!isExpired(page({ expiresAt: undefined }), now));
  assert.deepEqual(ids(livePages(hatch(page({ id: 'a' }), expired, lapsed), now)), ['a']);
});

test('the project chat shows every live page, an agent its own, and nothing without Hatch', () => {
  const list = hatch(page({ id: 'a' }), page({ id: 'b', agent: 'pawly/agent-02', agents: ['pawly/agent-02', 'pawly/lead'] }), page({ id: 'c', agent: 'pawly/agent-03', agents: ['pawly/agent-03'] }), expired);
  assert.deepEqual(ids(pagesFor(list, 'pawly/lead', now)), ['a', 'b', 'c']);
  assert.deepEqual(ids(pagesFor(list, 'pawly', now)), ['a', 'b', 'c']);
  assert.deepEqual(ids(pagesFor(list, 'pawly/agent-02', now)), ['b']);
  assert.deepEqual(pagesFor(list, 'pawly/agent-09', now), []);
  assert.deepEqual(pagesFor({ ...list, connector: '' }, 'pawly/lead', now), []);
  assert.deepEqual(pagesFor(undefined, 'pawly/lead', now), []);
});

test('the stack holds the new pages of its chat, newest first, and hides otherwise', () => {
  const a = page({ id: 'a', updatedAt: '2026-10-09T11:10:00Z' });
  const b = page({ id: 'b', updatedAt: '2026-10-09T11:50:00Z', agent: 'pawly/agent-02', agents: ['pawly/agent-02'] });
  assert.deepEqual(ids(stackPages(hatch(a, b), 'pawly/lead', now)), ['b', 'a']);
  assert.deepEqual(ids(stackPages(hatch(a, b), 'pawly/agent-02', now)), ['b']);
  // Hidden: no Hatch, only expired pages, every page seen.
  assert.deepEqual(stackPages(noHatch(a, b), 'pawly/lead', now), []);
  assert.deepEqual(stackPages(hatch(expired, lapsed), 'pawly/lead', now), []);
  markSeen([a, b]);
  assert.deepEqual(stackPages(hatch(a, b), 'pawly/lead', now), []);
  // An update makes it new again.
  assert.deepEqual(ids(stackPages(hatch(a, { ...b, version: 2 }), 'pawly/lead', now)), ['b']);
});

test('a project read for the first time is all seen; markSeen keeps the highest version', () => {
  const a = page({ id: 'a', version: 2 });
  assert.ok(isNew(a));
  assert.ok(knowProject('pawly', hatch(a)));
  assert.ok(!isNew(a));
  assert.ok(!knowProject('pawly', hatch(page({ id: 'b' }))), 'only the first read');
  assert.ok(isNew(page({ id: 'b' })));
  markSeen([{ id: 'a', version: 1 }]);
  assert.ok(!isNew(a));
  assert.ok(isNew({ id: 'a', version: 3 }));
});

test('readPages knows the project before the pages reach anything that shows them', async () => {
  const list = hatch(page({ id: 'a' }));
  const read = await readPages('pawly', async () => list)();
  assert.equal(read, list);
  assert.ok(!isNew(list.artifacts[0]));
});

test('arrivals are new or updated live pages you have not seen, never on a first read', () => {
  const a = page({ id: 'a' });
  const b = page({ id: 'b' });
  assert.deepEqual(arrivals(undefined, hatch(a, b), now), [], 'a first read is not news');
  assert.deepEqual(ids(arrivals(hatch(a), hatch(a, b), now)), ['b'], 'a page already there at that version is not news');
  assert.deepEqual(ids(arrivals(hatch(a), hatch({ ...a, version: 2 }), now)), ['a']);
  // Hidden: no Hatch, an expired page, one already seen.
  assert.deepEqual(arrivals(hatch(), noHatch(b), now), []);
  assert.deepEqual(arrivals(hatch(), hatch(expired), now), []);
  markSeen([b]);
  assert.deepEqual(arrivals(hatch(a), hatch(a, b), now), []);
});

test('no toast while a chat whose stack shows the page is on screen', () => {
  const p = page({ id: 'a', agent: 'pawly/agent-02', agents: ['pawly/agent-02', 'pawly/agent-03'] });
  assert.ok(showsArrival(p, []));
  assert.ok(!showsArrival(p, ['pawly/lead']));
  assert.ok(!showsArrival(p, ['pawly/agent-03']));
  assert.ok(showsArrival(p, ['pawly/agent-04']));
  assert.ok(showsArrival(p, ['other/lead']));
  const stop = watchChat('pawly/lead');
  const again = watchChat('pawly/lead');
  stop();
  assert.deepEqual(onScreenChats(), ['pawly/lead']);
  again();
  assert.deepEqual(onScreenChats(), []);
});

test('the Pages tab and the agents\' page icons count live pages only, and nothing without Hatch', () => {
  const list = hatch(page({ id: 'a' }), page({ id: 'b', agent: 'pawly/agent-02' }), page({ id: 'c', agent: 'pawly/agent-02' }), expired);
  assert.deepEqual([...countByAgent(livePages(list, now))], [['pawly/lead', 1], ['pawly/agent-02', 2]]);
  // Hidden: no tab and no icon without Hatch, or with only expired pages.
  assert.equal(livePages(noHatch(page({ id: 'a' })), now).length, 0);
  assert.equal(livePages(hatch(expired, lapsed), now).length, 0);
  assert.equal(countByAgent(livePages(hatch(expired), now)).get('pawly/agent-02'), undefined);
});

test('the Pages tab groups by agent, the newest group first', () => {
  const list = [
    page({ id: 'a', agent: 'pawly/lead', updatedAt: '2026-10-09T10:00:00Z' }),
    page({ id: 'b', agent: 'pawly/agent-02', updatedAt: '2026-10-09T11:00:00Z' }),
    page({ id: 'c', agent: 'pawly/lead', updatedAt: '2026-10-09T11:30:00Z' }),
  ];
  assert.deepEqual(byAgent(list).map(([ref, l]) => [ref, ids(l)]), [['pawly/lead', ['c', 'a']], ['pawly/agent-02', ['b']]]);
});

test('the Media Pages tabs: every project\'s live pages, none from a project without Hatch', () => {
  const found = allLivePages([['pawly', hatch(page({ id: 'a' }), expired)], ['quiet', noHatch(page({ id: 'q' }))], ['new', undefined]], now);
  assert.deepEqual(found.map((e) => `${e.project}/${e.page.id}`), ['pawly/a']);
  assert.deepEqual(allLivePages([['quiet', noHatch(page({ id: 'q' }))], ['old', hatch(expired)]], now), []);
});

test('a search matches every word in the title or the agent', () => {
  const p = page({ title: 'Q3 usage report', agent: 'pawly/agent-12' });
  assert.ok(matchesPage(p, ''));
  assert.ok(matchesPage(p, 'usage q3'));
  assert.ok(matchesPage(p, 'agent-12'));
  assert.ok(matchesPage(p, 'sidebar', 'Sidebar button'));
  assert.ok(!matchesPage(p, 'usage flame'));
});

test('isPageCall knows publish_page and update_page whatever the AI tool calls them', () => {
  assert.ok(isPageCall({ name: 'mcp__hatch__publish_page', title: '' }));
  assert.ok(isPageCall({ title: 'hatch.update_page' }));
  assert.ok(isPageCall({ name: 'Hatch_Publish_Page', title: 'x' }));
  assert.ok(!isPageCall({ name: 'mcp__hatch__get_page', title: 'Get page' }));
  assert.ok(!isPageCall({ name: 'mcp__hatch__republish_page', title: '' }));
  assert.ok(!isPageCall(undefined));
});

test('a publish_page line opens its page only while it is live and Hatch is connected', () => {
  const published = tool({ output: 'Published "X" (id abc_1, version 1): https://hatch.linting.dev/p/abc_1' });
  assert.equal(pageIdOf(published), 'abc_1');
  assert.equal(pageIdOf(tool({ name: 'mcp__hatch__update_page', page: { id: 'abc_1' }, output: 'Updated' })), 'abc_1');
  const list = hatch(page({ id: 'abc_1' }));
  assert.equal(livePageOf(list, published, now)?.id, 'abc_1');
  // Hidden: no Hatch, the page expired, a call still running or failed, another tool.
  assert.equal(livePageOf(noHatch(page({ id: 'abc_1' })), published, now), undefined);
  assert.equal(livePageOf(hatch(page({ id: 'abc_1', expired: true })), published, now), undefined);
  assert.equal(livePageOf(list, { ...published, status: 'in_progress' }, now), undefined);
  assert.equal(livePageOf(list, { ...published, status: 'failed' }, now), undefined);
  assert.equal(livePageOf(list, { ...published, name: 'mcp__hatch__get_page' }, now), undefined);
  assert.equal(livePageOf(undefined, published, now), undefined);
});

test('a finished publish reads its project\'s pages again, nothing else does', async () => {
  const client = new QueryClient();
  let reads = 0;
  const fetch = async () => {
    reads++;
    return hatch();
  };
  const item = (t: Partial<T.ChatTool>): T.ChatItem => ({ id: 'i', turn: 't', kind: 'tool', createdAt: '', updatedAt: '', tool: tool(t) });
  onChatItem(client, 'pawly/agent-02', item({ name: 'mcp__hatch__get_page' }), fetch);
  onChatItem(client, 'pawly/agent-02', item({ status: 'in_progress' }), fetch);
  assert.equal(reads, 0);
  onChatItem(client, 'pawly/agent-02', item({}), fetch);
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(reads, 1);
  assert.deepEqual(client.getQueryData(pagesKey('pawly')), hatch());
});

test('a page keeps its tile colour', () => {
  assert.equal(tileHue('abc'), tileHue('abc'));
  assert.ok(tileHue('abc') >= 0 && tileHue('abc') < 360);
});
