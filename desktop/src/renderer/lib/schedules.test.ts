import assert from 'node:assert/strict';
import { test } from 'node:test';
import { enUS } from '../../shared/i18n/en-US.ts';
import { t } from '../../shared/i18n/index.ts';
import { everyLabel, parseWeekly, templates, weeklyCron, whenLabel } from './schedules.ts';


test('a weekly cron expression is read and written back', () => {
  assert.deepEqual(parseWeekly('30 8 * * 1-5'), { days: [1, 2, 3, 4, 5], hour: 8, minute: 30 });
  assert.deepEqual(parseWeekly('0 9 * * 7'), { days: [0], hour: 9, minute: 0 });
  assert.deepEqual(parseWeekly('0 0 * * *')?.days, [0, 1, 2, 3, 4, 5, 6]);
  assert.equal(parseWeekly('*/15 * * * *'), null);
  assert.equal(parseWeekly('0 9 1 * *'), null);
  assert.equal(weeklyCron({ days: [1, 2, 3, 4, 5], hour: 8, minute: 30 }), '30 8 * * 1-5');
  assert.equal(weeklyCron({ days: [5, 1], hour: 9, minute: 0 }), '0 9 * * 1,5');
  assert.equal(weeklyCron({ days: [0, 1, 2, 3, 4, 5, 6], hour: 7, minute: 5 }), '5 7 * * *');
  for (const cron of ['0 9 * * 1,3,5', '15 18 * * 0-2,6']) assert.equal(weeklyCron(parseWeekly(cron)!), cron);
});

test('when a schedule runs is said in words', () => {
  // In the language's own clock: en-US is 12-hour.
  assert.match(whenLabel({ kind: 'cron', cron: '0 9 * * 1-5', timezone: 'UTC' }, t), /^Weekdays at 9:00\sAM$/);
  assert.match(whenLabel({ kind: 'cron', cron: '0 18 * * *', timezone: 'UTC' }, t), /^Daily at 6:00\sPM$/);
  assert.match(whenLabel({ kind: 'cron', cron: '0 8 * * 1', timezone: 'UTC' }, t), /^Monday at 8:00\sAM$/);
  assert.match(whenLabel({ kind: 'cron', cron: '0 8 * * 1,3', timezone: 'UTC' }, t), /^Mon and Wed at 8:00\sAM$/);
  assert.equal(whenLabel({ kind: 'cron', cron: '0 0 1 * *', timezone: 'UTC' }, t), 'Cron 0 0 1 * *');
  assert.equal(whenLabel({ kind: 'every', everyMinutes: 120, timezone: 'UTC' }, t), 'Every 2 hours');
  assert.match(whenLabel({ kind: 'once', at: '2026-10-19T09:00:00Z', timezone: 'UTC' }, t), /^Once, .*2026/);
  assert.equal(everyLabel(1440, t), 'Every day');
  assert.equal(everyLabel(45, t), 'Every 45 minutes');
});

test('every template has a time and words in the catalog', () => {
  for (const tpl of templates) {
    assert.ok(tpl.cron || tpl.every, tpl.id);
    assert.ok(enUS[tpl.title] && enUS[tpl.task], tpl.id);
    if (tpl.cron) assert.ok(parseWeekly(tpl.cron), tpl.cron);
  }
});
