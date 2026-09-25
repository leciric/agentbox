// Shows an agent's browser with noVNC. noVNC usually opens its own WebSocket;
// here it gets a stand-in that sends the bytes through the main process, which
// holds the connection to the daemon's unix socket.
import RFB from '@novnc/novnc';
import { agentPath } from './api';

const channels = new Map<number, IpcChannel>();

window.agentbox.stream.onOpened((id) => channels.get(id)?.opened());
window.agentbox.stream.onData((id, data) => channels.get(id)?.received(data));
window.agentbox.stream.onExited((id, reason) => channels.get(id)?.exited(reason));

// IpcChannel has the parts of the WebSocket interface that noVNC uses.
class IpcChannel {
  binaryType = 'arraybuffer';
  protocol = '';
  readyState = 0; // connecting
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  private id?: number;
  private closing = false;

  constructor(path: string) {
    void window.agentbox.stream.open(path).then((id) => {
      if (this.closing) return window.agentbox.stream.close(id);
      this.id = id;
      channels.set(id, this);
    });
  }

  opened(): void {
    this.readyState = 1;
    this.onopen?.(new Event('open'));
  }

  received(data: Uint8Array): void {
    const buffer = data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength);
    this.onmessage?.(new MessageEvent('message', { data: buffer }));
  }

  exited(reason: string): void {
    if (this.id !== undefined) channels.delete(this.id);
    this.id = undefined;
    this.readyState = 3;
    this.onclose?.(new CloseEvent('close', { code: 1000, reason, wasClean: true }));
  }

  send(data: ArrayBuffer | Uint8Array): void {
    if (this.id === undefined || this.readyState !== 1) return;
    window.agentbox.stream.write(this.id, data instanceof Uint8Array ? data.slice() : new Uint8Array(data.slice(0)));
  }

  close(): void {
    this.closing = true;
    this.readyState = 2;
    if (this.id !== undefined) window.agentbox.stream.close(this.id);
    else setTimeout(() => this.exited('closed'), 0);
  }
}

// connectView shows the display behind a daemon WebSocket path in target. The
// display resizes to fit, and text copied on it lands on your clipboard.
export function connectView(path: string, target: HTMLElement): RFB {
  const rfb = new RFB(target, new IpcChannel(path), { shared: true });
  rfb.resizeSession = true;
  rfb.scaleViewport = true;
  rfb.background = getComputedStyle(document.documentElement).getPropertyValue('--ab-stage').trim();
  rfb.addEventListener('clipboard', (event) => window.agentbox.copyText((event as CustomEvent<{ text: string }>).detail.text));
  return rfb;
}

export const browserViewPath = (ref: string) => `${agentPath(ref)}/browser/view`;
export const androidViewPath = (ref: string) => `${agentPath(ref)}/android/view`;
