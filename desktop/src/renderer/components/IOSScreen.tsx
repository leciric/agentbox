import type * as T from '../../shared/api';
import { iosViewPath } from '../lib/api';
import { DeviceStreamView } from './device/DeviceStreamView';
import type { StreamState } from './device/session';

// IOSScreen is a booted simulator's screen, in the iOS tab: the Mac's helper
// encodes it as H.264 on the Mac's hardware encoder, and DeviceStreamView
// plays it, as it does an Android emulator's, sending your taps, scrolling
// and typing back with control on.
export function IOSScreen({ agent, device, control, onState }: { agent: T.Agent; device: T.IOSDevice; control: boolean; onState: (state: StreamState) => void }) {
  return (
    <div className="absolute inset-5" data-ios-screen={`${agent.ref}:${device.udid}`}>
      <DeviceStreamView path={iosViewPath(agent.ref, device.udid)} enabled control={control} aspect={1206 / 2622} onState={onState} />
    </div>
  );
}
