// Run with `npm test`: the power watch against a fake powerMonitor.
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { test } from 'node:test';
import { watchHostPower } from './hostpower.ts';

test('battery and thermal changes are reported once each, unknown states as none', () => {
  const monitor = Object.assign(new EventEmitter(), { isOnBatteryPower: () => false, getCurrentThermalState: () => 'nominal' });
  const seen: unknown[] = [];
  const now = watchHostPower(monitor as never, (p) => seen.push(p));
  assert.deepEqual(now(), { onBattery: false, thermal: 'nominal' });
  monitor.emit('on-battery');
  monitor.emit('on-battery');
  monitor.emit('thermal-state-change', { state: 'serious' });
  monitor.emit('thermal-state-change', { state: 'unknown' });
  monitor.emit('on-ac');
  assert.deepEqual(seen, [
    { onBattery: true, thermal: 'nominal' },
    { onBattery: true, thermal: 'serious' },
    { onBattery: true, thermal: '' },
    { onBattery: false, thermal: '' },
  ]);
});

test('a monitor that throws reads as on mains', () => {
  const monitor = Object.assign(new EventEmitter(), {
    isOnBatteryPower: () => {
      throw new Error('no battery API');
    },
  });
  assert.deepEqual(watchHostPower(monitor as never, () => {})(), { onBattery: false, thermal: '' });
});
