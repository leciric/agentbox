// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { apiPath } from './mediapaths.ts';

const at = (path: string) => apiPath(new URL(`agentbox-media://api${path}`));

test("a picture sent in any session of a project's chat is served", () => {
  // What api.chatImageUrl asks for: every lead ref has a session, "main" for web/lead.
  for (const path of [
    '/v1/projects/web/sessions/main/chat/images/0123456789abcdef',
    '/v1/projects/web/sessions/k3x9/chat/images/0123456789abcdef',
    '/v1/projects/_home/sessions/main/chat/images/0123456789abcdef',
    '/v1/projects/web/chat/images/0123456789abcdef',
    '/v1/agents/web/agent-01/chat/images/0123456789abcdef',
    '/v1/snaps/0123456789abcdef/image',
  ]) {
    assert.equal(at(path), path);
  }
});

test('the rest of the daemon API is not', () => {
  for (const path of [
    '/v1/projects/web/sessions/main/chat',
    '/v1/projects/web/sessions/main/chat/images/../../messages',
    '/v1/projects/web/sessions/a/b/chat/images/0123456789abcdef',
    '/v1/projects/web/sessions/main/chat/files/0123456789abcdef',
    '/v1/projects/web/chat/images/not-an-id',
    '/v1/auth',
  ]) {
    assert.equal(at(path), null, path);
  }
});

test("a pull request's picture keeps its query", () => {
  assert.equal(at('/v1/projects/web/pulls/image?src=https%3A%2F%2Fx'), '/v1/projects/web/pulls/image?src=https%3A%2F%2Fx');
});
