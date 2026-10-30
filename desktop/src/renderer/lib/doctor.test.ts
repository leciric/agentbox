import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { canAskHome, isCommand, problems } from './doctor.ts';

const check = (id: string, status: string, over: Partial<T.DoctorCheck> = {}): T.DoctorCheck => ({ id, title: id, status, message: '', ...over });

test('problems lists failures, then warnings, and leaves out what is ok', () => {
  const got = problems([check('daemon', 'ok'), check('github', 'warn'), check('incus', 'fail'), check('disk', 'warn'), check('image', 'fail')]);
  assert.deepEqual(got.map((c) => c.id), ['incus', 'image', 'github', 'disk']);
});

test('only a failure with no fix of its own goes to the Home chat, never its own login', () => {
  assert.equal(canAskHome(check('incus', 'fail')), true);
  assert.equal(canAskHome(check('github', 'warn')), false, 'a warning');
  assert.equal(canAskHome(check('image', 'fail', { autoFix: true })), false, 'fixes itself');
  assert.equal(canAskHome(check('image', 'fail', { job: 'job-1' })), false, 'being fixed');
  assert.equal(canAskHome(check('ai-login', 'fail')), false, 'the Home chat runs on it');
});

test('isCommand tells a command from a sentence', () => {
  assert.equal(isCommand('agentbox auth claude'), true);
  assert.equal(isCommand('sudo agentbox host kvm'), true);
  assert.equal(isCommand('brew install lima'), true);
  assert.equal(isCommand('check this machine\'s internet connection'), false);
});
