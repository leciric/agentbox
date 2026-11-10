// The computer's power, for the daemon's background work (presence leases,
// lib/presence.ts): whether it runs on battery, and how hot it is. The
// renderer puts it in its lease; the daemon backs off its housekeeping while
// it says save. Electron's powerMonitor knows both: battery everywhere, the
// thermal state on macOS (elsewhere it stays "", which counts as fine).
// Electron comes in as types only, so the tests can run it against a fake.
import type { PowerMonitor } from 'electron';
import type { HostPower } from '../shared/api';

type Monitor = Pick<PowerMonitor, 'isOnBatteryPower' | 'on'> & { getCurrentThermalState?: () => string };

const thermalStates = new Set(['nominal', 'fair', 'serious', 'critical']);

// watchHostPower reads the power now and calls onChange whenever it changes.
export function watchHostPower(monitor: Monitor, onChange: (power: HostPower) => void): () => HostPower {
  const thermalOf = (s: string | undefined) => (s && thermalStates.has(s) ? s : '');
  let power: HostPower = { onBattery: safe(() => monitor.isOnBatteryPower(), false), thermal: thermalOf(safe(() => monitor.getCurrentThermalState?.(), undefined)) };
  const set = (next: HostPower) => {
    if (next.onBattery === power.onBattery && next.thermal === power.thermal) return;
    power = next;
    onChange(power);
  };
  monitor.on('on-battery', () => set({ ...power, onBattery: true }));
  monitor.on('on-ac', () => set({ ...power, onBattery: false }));
  // macOS only; the event carries { state }.
  (monitor.on as (event: string, fn: (details: { state?: string }) => void) => void)('thermal-state-change', (details) => set({ ...power, thermal: thermalOf(details?.state) }));
  return () => power;
}

function safe<V>(fn: () => V, fallback: V): V {
  try {
    return fn();
  } catch {
    return fallback;
  }
}
