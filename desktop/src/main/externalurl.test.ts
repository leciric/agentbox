import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isSafeExternalUrl } from './externalurl.ts';

test('web and mail links open', () => {
  for (const url of ['https://example.com/a?b=1', 'http://localhost:3000', 'mailto:ana@example.com', 'mailto:a@b.co?subject=Oi%20mundo', 'MAILTO:a@b.co']) {
    assert.ok(isSafeExternalUrl(url), url);
  }
});

test('other schemes and non-URLs are refused', () => {
  for (const url of ['file:///etc/passwd', 'javascript:alert(1)', 'ms-msdt:/id', 'smb://host/share', 'example.com', '', 'data:text/html,hi']) {
    assert.ok(!isSafeExternalUrl(url), url);
  }
  assert.ok(!isSafeExternalUrl(undefined));
  assert.ok(!isSafeExternalUrl(42));
});
