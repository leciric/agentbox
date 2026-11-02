// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { cardFileName, compact, duration, isEmpty, modelLabel, peakHour, recapQuery, spendOf } from './recap.ts';

const week = (over: Partial<T.Recap> = {}): T.Recap => ({
  start: '2026-10-18T00:00:00+01:00',
  end: '2026-10-25T00:00:00+01:00',
  agents: 0,
  prsOpened: 0,
  prsMerged: 0,
  insertions: 0,
  deletions: 0,
  files: 0,
  workSeconds: 0,
  awaySeconds: 0,
  days: [],
  hours: Array(24).fill(0),
  projects: 0,
  models: [],
  tokens: 0,
  costUsd: 0,
  estimatedUsd: 0,
  unpricedTokens: 0,
  streak: 0,
  bestStreak: 0,
  ...over,
});

test('duration says hours as a person would, and minutes under one', () => {
  assert.equal(duration(42 * 3600 + 1200), '42h');
  assert.equal(duration(3.46 * 3600), '3.5h');
  assert.equal(duration(45 * 60), '45m');
  assert.equal(duration(0), '0m');
});

test('compact keeps a card number short', () => {
  assert.equal(compact(940), '940');
  assert.equal(compact(1234), '1.2K');
  assert.equal(compact(12_400), '12K');
  assert.equal(compact(18_200_000), '18.2M');
});

test('modelLabel names Claude models as people do and leaves the rest', () => {
  assert.equal(modelLabel('claude-opus-4-5'), 'Opus 4.5');
  assert.equal(modelLabel('claude-sonnet-5-20261001'), 'Sonnet 5');
  assert.equal(modelLabel('claude-haiku-5-5'), 'Haiku 5.5');
  assert.equal(modelLabel('gpt-5.1-codex'), 'gpt-5.1-codex');
});

test('the query carries the time zone east of UTC', () => {
  const at = new Date();
  assert.equal(recapQuery(2, at), `/v1/recap?offset=${-at.getTimezoneOffset()}&weeks=2`);
});

test('a card is saved under the week’s last day', () => {
  assert.equal(cardFileName(week(), 'square'), 'agentbox-week-2026-10-24-square.png');
});

test('peakHour and isEmpty read an empty week as one', () => {
  assert.equal(peakHour(week()), -1);
  assert.ok(isEmpty(week()));
  const hours = Array(24).fill(0);
  hours[14] = 600;
  assert.equal(peakHour(week({ hours, workSeconds: 600 })), 14);
  assert.ok(!isEmpty(week({ hours, workSeconds: 600 })));
});

test('spendOf says whether a cost was reported, estimated, or both', () => {
  assert.equal(spendOf({ costUsd: 0, estimatedUsd: 0 }), 'none');
  assert.equal(spendOf({ costUsd: 12.5, estimatedUsd: 0 }), 'reported');
  assert.equal(spendOf({ costUsd: 12.5, estimatedUsd: 2 }), 'partly');
  assert.equal(spendOf({ costUsd: 12.5, estimatedUsd: 12.5 }), 'estimated');
  assert.equal(spendOf({ costUsd: 0.1 + 0.2, estimatedUsd: 0.3 }), 'estimated');
});
