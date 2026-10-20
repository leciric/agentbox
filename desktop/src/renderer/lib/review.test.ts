import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { countLabel, remainingAfter, wrapWalk, goBack, goForward, mergeBlock, nextPending, startWalk, walkItems, walkOrder, queueOf, reviewKeys, reviewShortcut, repeatable, isTyping, firstLine, pressable, proofKey, gallery, chapterAt, proofOf, readableName, step } from './review.ts';

const page = (id: string, createdAt: string) => ({ id, createdAt }) as T.Artifact;
const pr = { number: 12, title: 'feat: dark mode', state: 'open', checks: 'passing', url: '' } as T.PullRequest;

const item = (id: string, kind: string, createdAt: string, more: Partial<T.MediaItem> = {}) => ({ id, kind, name: id, createdAt, meta: {}, ...more }) as T.MediaItem;

test('proofOf puts the latest recording first, screenshots and pages oldest first, and leaves out the rest', () => {
  const proof = proofOf(
    [
      item('rec1', 'recording', '2026-10-14T10:00:00Z', { meta: { duration: 30 } }),
      item('shot2', 'screenshot', '2026-10-14T10:20:00Z'),
      item('rec2', 'recording', '2026-10-14T10:10:00Z', { meta: { duration: 30 } }),
      item('shot1', 'screenshot', '2026-10-14T09:00:00Z'),
      item('log', 'log', '2026-10-14T10:00:00Z'),
    ],
    [page('pg2', '2026-10-14T10:02:00Z'), page('pg1', '2026-10-14T10:01:00Z')],
  );
  assert.deepEqual(proof.recordings.map((r) => r.media.id), ['rec2', 'rec1']);
  assert.deepEqual(proof.screenshots.map((s) => s.id), ['shot1', 'shot2']);
  assert.deepEqual(proof.pages.map((p) => p.id), ['pg1', 'pg2']);
});

test('the notes saved while a recording ran are its chapters, each with the screenshots taken in it', () => {
  const at = (s: number) => new Date(Date.parse('2026-10-14T10:00:00Z') + s * 1000).toISOString();
  const proof = proofOf(
    [
      item('rec', 'recording', at(0), { meta: { duration: 60 } }),
      item('before', 'note', at(-5), { name: 'Before it started' }),
      item('n1', 'note', at(2), { name: 'Settings at 1024px', text: 'Settings at 1024px' }),
      item('s0', 'screenshot', at(1)),
      item('s1', 'screenshot', at(10)),
      item('n2', 'note', at(20), { name: 'In pt-BR', text: 'In pt-BR\nThe longest labels wrap.' }),
      item('s2', 'screenshot', at(25)),
      item('after', 'screenshot', at(90)),
    ],
    [],
  );
  const chapters = proof.recordings[0].chapters;
  assert.deepEqual(
    chapters.map((c) => [c.at, c.title, c.detail, c.shots.map((s) => s.id)]),
    [
      [2, 'Settings at 1024px', undefined, ['s0', 's1']],
      [20, 'In pt-BR', 'The longest labels wrap.', ['s2']],
    ],
  );
  assert.equal(chapterAt(chapters, 0), 0);
  assert.equal(chapterAt(chapters, 19.99), 1, 'a chapter starts a moment early, as a seek lands');
  assert.equal(chapterAt(chapters, 40), 1);
});

test('a trimmed recording places its chapters on the video, without what was cut', () => {
  const at = (s: number) => new Date(Date.parse('2026-10-14T10:00:00Z') + s * 1000).toISOString();
  const proof = proofOf(
    [
      // 63s on the clock, 15s of video: 10s–40s and 45s–63s stood still.
      item('rec', 'recording', at(0), { meta: { duration: 15, recorded: 63, cuts: [{ from: 10, to: 40 }, { from: 45, to: 63 }] } }),
      item('n1', 'note', at(5), { name: 'Opens settings' }),
      item('n2', 'note', at(42), { name: 'Saves' }),
      item('n3', 'note', at(50), { name: 'Done, while nothing moved' }),
    ],
    [],
  );
  assert.deepEqual(
    proof.recordings[0].chapters.map((c) => [c.at, c.title]),
    [
      [5, 'Opens settings'],
      [12, 'Saves'],
      [15, 'Done, while nothing moved'],
    ],
  );
});

test('a recording with no notes is in chapters by its screenshots, and with neither has none', () => {
  const proof = proofOf(
    [item('rec', 'recording', '2026-10-14T10:00:00Z', { meta: { duration: 60 } }), item('settings-at-1024', 'screenshot', '2026-10-14T10:00:12Z')],
    [],
  );
  assert.deepEqual(
    proof.recordings[0].chapters.map((c) => [c.at, c.title]),
    [[12, 'Settings at 1024']],
  );
  assert.deepEqual(proofOf([item('rec', 'recording', '2026-10-14T10:00:00Z')], []).recordings[0].chapters, []);
  assert.equal(readableName('sidebar_new-project.png'), 'Sidebar new project');
});

test('step stops at either end', () => {
  assert.equal(step(0, 3, -1), 0);
  assert.equal(step(1, 3, 1), 2);
  assert.equal(step(2, 3, 1), 2);
  assert.equal(step(0, 0, 1), 0);
});

test('the list helpers', () => {
  assert.equal(countLabel(7), '7');
  assert.equal(countLabel(120), '99+');
});

test('mergeBlock says why a pull request can’t be merged from the review', () => {
  assert.equal(mergeBlock(pr), null);
  assert.equal(mergeBlock({ ...pr, checks: 'pending' }), null, 'pending checks are the confirmation’s to warn about');
  assert.equal(mergeBlock({ ...pr, checks: 'failing' }), 'failing');
  assert.equal(mergeBlock({ ...pr, conflict: true, checks: 'failing' }), 'conflict');
  assert.equal(mergeBlock({ ...pr, draft: true }), 'draft');
  assert.equal(mergeBlock({ ...pr, state: 'merged' }), 'merged');
  assert.equal(mergeBlock({ ...pr, state: 'closed' }), 'closed');
  assert.equal(mergeBlock(pr, { canMergeKnown: true, canMerge: false }), 'noPermission');
  assert.equal(mergeBlock(pr, { canMergeKnown: false, canMerge: false }), null, 'not known is not a no');
});

const waiting = (...refs: string[]) => refs.map((ref) => ({ ref }) as T.ReviewItem);

test('Review all walks what was waiting, then what arrived, skipping what was reviewed', () => {
  const order = ['p/a', 'p/b', 'p/c'];
  assert.deepEqual(queueOf(order, waiting('p/d', 'p/a')), ['p/a', 'p/b', 'p/c', 'p/d']);
  assert.equal(nextPending(order, 'p/a', waiting('p/a', 'p/b', 'p/c'))?.ref, 'p/b');
  // a was just reviewed, and b in another window: c is next.
  assert.equal(nextPending(order, 'p/a', waiting('p/c'))?.ref, 'p/c');
  // An agent that finished meanwhile comes after the rest.
  assert.equal(nextPending(order, 'p/c', waiting('p/d'))?.ref, 'p/d');
  assert.equal(nextPending(order, 'p/c', waiting('p/c')), undefined);
  // Earlier ones still waiting aren't gone back to.
  assert.equal(nextPending(order, 'p/b', waiting('p/a')), undefined);
});

test('reviewShortcut maps keys to actions, and leaves modified ones alone', () => {
  assert.equal(reviewShortcut({ key: 'm' }), 'merge');
  assert.equal(reviewShortcut({ key: 'C' }), 'requestChanges');
  assert.equal(reviewShortcut({ key: 'r' }), 'reviewed');
  assert.equal(reviewShortcut({ key: 'n' }), 'nextItem');
  assert.equal(reviewShortcut({ key: 'p' }), 'previousItem');
  assert.equal(reviewShortcut({ key: 'd' }), 'toggleDiff');
  assert.equal(reviewShortcut({ key: 'j' }), 'nextFile');
  assert.equal(reviewShortcut({ key: 'k' }), 'previousFile');
  assert.equal(reviewShortcut({ key: 'i' }), 'toggleDetails');
  assert.equal(reviewShortcut({ key: 'o' }), 'openPr');
  assert.equal(reviewShortcut({ key: '?' }), 'help');
  // The arrows, Space, [ ] and F work the proof's gallery.
  assert.equal(reviewShortcut({ key: 'ArrowRight' }), 'nextMedia');
  assert.equal(reviewShortcut({ key: 'ArrowLeft' }), 'previousMedia');
  assert.equal(reviewShortcut({ key: ' ' }), 'play');
  assert.equal(reviewShortcut({ key: ']' }), 'nextChapter');
  assert.equal(reviewShortcut({ key: 'f' }), 'expand');
  assert.ok(proofKey('nextMedia') && !proofKey('toggleDiff'));
  assert.equal(reviewShortcut({ key: 'r', metaKey: true }), null);
  assert.equal(reviewShortcut({ key: 'm', ctrlKey: true }), null);
  assert.equal(reviewShortcut({ key: 'x' }), null);
  // Every key the review lists does what it says.
  for (const k of reviewKeys) for (const key of k.match ?? k.keys) assert.equal(reviewShortcut({ key }), k.action, key);
  // A held key moves through files, never merges twice.
  assert.ok(repeatable('nextFile'));
  assert.ok(!repeatable('merge'));
});

test('isTyping leaves keys to fields, editable text and menus', () => {
  const el = (tagName: string, inMenu = false, isContentEditable = false) => ({ tagName, isContentEditable, closest: () => (inMenu ? {} : null) });
  assert.ok(isTyping(el('TEXTAREA')));
  assert.ok(isTyping(el('input')));
  assert.ok(isTyping(el('DIV', false, true)));
  assert.ok(isTyping(el('DIV', true)));
  assert.ok(!isTyping(el('BUTTON')));
  assert.ok(!isTyping(null));
});

test('pressable leaves Space to buttons and links', () => {
  const el = (pressed: boolean) => ({ tagName: 'DIV', closest: () => (pressed ? {} : null) });
  assert.ok(pressable(el(true)));
  assert.ok(!pressable(el(false)));
  assert.ok(!pressable(null));
});

test('gallery is the recordings, then the screenshots, then the pages', () => {
  const m = (id: string, kind: string) => ({ id, kind }) as unknown as T.MediaItem;
  const shown = gallery({ recordings: [{ media: m('r', 'recording'), chapters: [] }], screenshots: [m('s1', 'screenshot'), m('s2', 'screenshot')], pages: [{ id: 'p' } as T.Artifact] });
  assert.deepEqual(
    shown.map((s) => s.kind),
    ['recording', 'screenshot', 'screenshot', 'page'],
  );
  assert.deepEqual(gallery({ recordings: [], screenshots: [], pages: [] }), []);
});

test('firstLine is the summary as one plain line', () => {
  assert.equal(firstLine('## Fixed the clip\n\nMore here.'), 'Fixed the clip');
  assert.equal(firstLine('\n- **Settings** fits at 1024px\n- and pt-BR'), 'Settings fits at 1024px');
  assert.equal(firstLine(undefined), '');
});

test("a project's walk goes through its agents only, and ends after its last", () => {
  const item = (ref: string) => ({ ref, project: ref.split('/')[0] }) as T.ReviewItem;
  const live = [item('p/a'), item('q/x'), item('p/b')];
  const items = walkItems(live, 'p');
  assert.deepEqual(items.map((i) => i.ref), ['p/a', 'p/b']);
  const order = items.map((i) => i.ref);
  assert.equal(nextPending(order, 'p/a', items)?.ref, 'p/b');
  // Sending changes on the last one ends the walk, and the carousel closes.
  assert.equal(nextPending(order, 'p/b', items), undefined);
  // Opened from everything, it goes on to the other projects.
  assert.equal(walkItems(live).length, 3);
});

test('openReview starts on the agent a card shows, or walks the waiting ones', () => {
  const item = (ref: string) => ({ ref, project: ref.split('/')[0] }) as T.ReviewItem;
  // From an agent's card: on that agent, even when the lists don't have it yet.
  assert.deepEqual(startWalk([], { item: item('p/a') }), { item: item('p/a'), order: null, project: 'p' });
  const waiting = [item('q/x'), item('p/a'), item('p/b')];
  assert.deepEqual(startWalk(waiting, { project: 'p' }), { item: item('p/a'), order: ['p/a', 'p/b'], project: 'p' });
  assert.equal(startWalk(waiting)?.order?.length, 3);
  // Nothing waiting: nothing opens, and nothing is marked.
  assert.equal(startWalk([], { project: 'p' }), null);
});

test('Back returns to the agent shown just before, one step at a time', () => {
  const item = (ref: string) => ({ ref, project: 'p' }) as T.ReviewItem;
  const start = startWalk([item('p/a'), item('p/b'), item('p/c')])!;
  // Nothing before the first agent.
  assert.equal(goBack(start), start);
  const atC = goForward(goForward(start, item('p/b')), item('p/c'));
  assert.deepEqual(atC.trail?.map((i) => i.ref), ['p/a', 'p/b']);
  const atB = goBack(atC);
  assert.equal(atB.item.ref, 'p/b');
  assert.equal(goBack(atB).item.ref, 'p/a');
  assert.equal(goBack(goBack(atB)).item.ref, 'p/a');
  assert.deepEqual(atB.order, start.order);
});

test('remainingAfter finds the agents skipped on the walk and those that arrived', () => {
  const live = ['a', 'b', 'c', 'd'].map((ref) => ({ ref }) as T.ReviewItem);
  // c was last in the walk; a was skipped, d arrived since.
  assert.deepEqual(remainingAfter(['a', 'b', 'c'], 'c', live.filter((i) => i.ref !== 'b')).map((i) => i.ref), ['a', 'd']);
  assert.deepEqual(remainingAfter(['c'], 'c', [live[2]]), []);
});

test('wrapWalk goes back to the first remaining agent, walking only them', () => {
  const [a, d] = ['a', 'd'].map((ref) => ({ ref }) as T.ReviewItem);
  const walk = wrapWalk({ item: { ref: 'c' } as T.ReviewItem, order: ['a', 'c'] }, [a, d]);
  assert.equal(walk.item.ref, 'a');
  assert.deepEqual(walk.order, ['a', 'd']);
});

test('a review opened on one agent goes on to the others waiting', () => {
  const live = ['a', 'b', 'c'].map((ref) => ({ ref }) as T.ReviewItem);
  // Opened on b from its card: b, then the rest in the list's order.
  const order = walkOrder({ item: live[1], order: null });
  assert.equal(nextPending(order, 'b', live)?.ref, 'a');
  // A walk keeps its own order.
  assert.deepEqual(walkOrder({ item: live[0], order: ['a', 'c'] }), ['a', 'c']);
});
