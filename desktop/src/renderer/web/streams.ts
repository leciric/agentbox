import type { Bridge } from '../../preload';
import { t } from '../../shared/i18n/index.ts';

// webStreams is the bridge's stream API over the browser's own WebSockets,
// for a page outside the desktop app: the web app, and the page of
// `agentbox machines serve`. url turns a stream's path into its address.
export function webStreams(url: (path: string) => string): { stream: Bridge['stream']; closeAll: () => void } {
  let nextStream = 1;
  const sockets = new Map<number, WebSocket>();
  const opened = new Set<(id: number) => void>();
  const data = new Set<(id: number, bytes: Uint8Array) => void>();
  const exited = new Set<(id: number, reason: string) => void>();
  const listen = <F>(set: Set<F>, fn: F): (() => void) => {
    set.add(fn);
    return () => {
      set.delete(fn);
    };
  };

  const stream: Bridge['stream'] = {
    open: (path: string) => {
      const id = nextStream++;
      const ws = new WebSocket(url(path));
      ws.binaryType = 'arraybuffer';
      sockets.set(id, ws);
      let done = false;
      const exit = (reason: string) => {
        if (done) return;
        done = true;
        sockets.delete(id);
        for (const fn of exited) fn(id, reason);
      };
      ws.onopen = () => {
        for (const fn of opened) fn(id);
      };
      ws.onmessage = (message) => {
        if (message.data instanceof ArrayBuffer) for (const fn of data) fn(id, new Uint8Array(message.data));
      };
      ws.onerror = () => exit(t('web.stream.failed'));
      ws.onclose = (event) => exit(event.reason || (event.code === 1000 ? t('web.stream.ended') : t('web.stream.closed', { code: event.code })));
      return Promise.resolve(id);
    },
    write: (id, payload) => {
      const ws = sockets.get(id);
      if (ws?.readyState === WebSocket.OPEN) ws.send(typeof payload === 'string' ? payload : payload.slice());
    },
    close: (id) => {
      sockets.get(id)?.close();
      sockets.delete(id);
    },
    onOpened: (fn) => listen(opened, fn),
    onData: (fn) => listen(data, fn),
    onExited: (fn) => listen(exited, fn),
  };
  const closeAll = () => {
    for (const ws of sockets.values()) ws.close();
    sockets.clear();
  };
  return { stream, closeAll };
}
