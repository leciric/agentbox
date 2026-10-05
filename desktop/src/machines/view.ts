// The live view of a machine on the page of `agentbox machines serve`
// (internal/machinesweb/static), built into its vnc.js by
// scripts/build-machines-view.mjs: the app's own VNC client, over a WebSocket
// to the server, which relays the machine's display.
import './bridge.ts';
import { VncSession } from '../renderer/lib/vncSession.ts';

// open shows the machine called name in target; app.js imports this module
// when a machine is first opened.
export function open(target: HTMLElement, name: string, onConnected: (connected: boolean) => void): VncSession {
  return new VncSession(`/api/machines/${encodeURIComponent(name)}/view`, target, onConnected);
}
