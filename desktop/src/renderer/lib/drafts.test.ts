// Run with `npm test` (node's own test runner, which strips the types).
//
// There is no localStorage outside a browser, so this exercises the module's
// in-memory cache: load() and scheduleWrite() both swallow the ReferenceError
// and fall back silently, which is the same thing a browser that refuses
// storage does.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { PendingImage } from './chatImages.ts';
import { composed, draftAttachmentsLoaded, getDraft, getDraftAttachments, setAttachmentStore, setDraft, setDraftAttachments, type AttachmentStore, type StoredAttachments } from './drafts.ts';

test('a ref with nothing typed has an empty draft', () => {
  assert.equal(getDraft('agent-never-seen'), '');
});

test('setDraft is read back by getDraft', () => {
  setDraft('agent-01', 'hello there');
  assert.equal(getDraft('agent-01'), 'hello there');
});

test('setting an empty draft clears it', () => {
  setDraft('agent-02', 'something');
  assert.equal(getDraft('agent-02'), 'something');
  setDraft('agent-02', '');
  assert.equal(getDraft('agent-02'), '');
});

test('text composed into a chat goes before what was typed there, not over it', () => {
  assert.equal(composed('Ask @pawly’s lead ', ''), 'Ask @pawly’s lead ');
  assert.equal(composed('Ask @pawly’s lead ', '  about the release notes'), 'Ask @pawly’s lead about the release notes');
});

const picture = (key: string): PendingImage => ({ key, name: 'image.png', mimeType: 'image/png', size: 3, data: 'iVBO' });

test("a draft's attachments come back with it, apart from its text", () => {
  assert.deepEqual(getDraftAttachments('p/lead.a'), []);
  setDraft('p/lead.a', 'see this');
  setDraftAttachments('p/lead.a', [picture('img-1')]);
  // Another session's draft doesn't touch it.
  setDraftAttachments('p/lead.b', [picture('img-2')]);
  assert.deepEqual(getDraftAttachments('p/lead.a').map((p) => p.key), ['img-1']);
  assert.equal(getDraft('p/lead.a'), 'see this');
  // Sent, or every one taken off: nothing waits.
  setDraftAttachments('p/lead.a', []);
  assert.deepEqual(getDraftAttachments('p/lead.a'), []);
  assert.deepEqual(getDraftAttachments('p/lead.b').map((p) => p.key), ['img-2']);
});

test('attachments are written through to the store, and read back from it after a restart', async () => {
  const kept: Record<string, StoredAttachments> = { 'q/lead': { items: [picture('img-old')], updatedAt: 1 }, 'q/agent-01': { items: [picture('img-mine')], updatedAt: 1 } };
  const store: AttachmentStore = {
    load: async () => structuredClone(kept),
    put: async (ref, draft) => void (kept[ref] = draft),
    delete: async (ref) => void delete kept[ref],
  };
  // Attached before the store was read back: what is new wins.
  setDraftAttachments('q/agent-01', [picture('img-new')]);
  await setAttachmentStore(store);
  await draftAttachmentsLoaded();
  assert.deepEqual(getDraftAttachments('q/lead').map((p) => p.key), ['img-old']);
  assert.deepEqual(getDraftAttachments('q/agent-01').map((p) => p.key), ['img-new']);
  setDraftAttachments('q/lead.x', [picture('img-3')]);
  assert.deepEqual(kept['q/lead.x'].items.map((p) => p.key), ['img-3']);
  setDraftAttachments('q/lead', []);
  assert.equal(kept['q/lead'], undefined);
  await setAttachmentStore(null);
});
