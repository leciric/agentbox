// Holds WebSockets to the daemon (terminals and browser views) for the
// renderer, which can't open unix sockets or send the hub's token, and relays
// them over IPC.
import { connect } from 'node:net';
import WebSocket from 'ws';
import { webSocketAddress } from './connection';

type Emit = (channel: string, ...args: unknown[]) => void;

export class Streams {
  private sockets = new Map<number, WebSocket>();
  private nextId = 1;

  constructor(private readonly emit: Emit) {}

  // open connects to a WebSocket endpoint of the daemon's API.
  open(path: string): number {
    const id = this.nextId++;
    const { url, headers, socketPath } = webSocketAddress(path);
    const ws = new WebSocket(url, { headers, ...(socketPath ? { createConnection: () => connect(socketPath) } : {}) });
    this.sockets.set(id, ws);

    let exited = false;
    const exit = (reason: string) => {
      if (exited) return;
      exited = true;
      this.sockets.delete(id);
      this.emit('stream:exited', id, reason);
    };
    ws.on('open', () => this.emit('stream:opened', id));
    ws.on('message', (data, isBinary) => {
      if (isBinary) this.emit('stream:data', id, new Uint8Array(data as Buffer));
    });
    // The daemon refuses with a JSON error, for example when the agent is stopped.
    ws.on('unexpected-response', (req, res) => {
      let body = '';
      res.setEncoding('utf8');
      res.on('data', (chunk: string) => (body += chunk));
      res.on('end', () => {
        let reason = body.trim() || `HTTP ${res.statusCode}`;
        try {
          reason = JSON.parse(body).error ?? reason;
        } catch {
          // not JSON
        }
        exit(reason);
        req.destroy();
      });
    });
    ws.on('error', (err) => exit(err.message));
    ws.on('close', (code, reason) => exit(reason.toString() || (code === 1000 ? 'the session ended' : `the connection closed (${code})`)));
    return id;
  }

  // write sends bytes as a binary message, and a string as a text message.
  write(id: number, data: Uint8Array | string): void {
    const ws = this.sockets.get(id);
    if (ws?.readyState !== WebSocket.OPEN) return;
    if (typeof data === 'string') ws.send(data);
    else ws.send(data, { binary: true });
  }

  close(id: number): void {
    this.sockets.get(id)?.close();
    this.sockets.delete(id);
  }

  closeAll(): void {
    for (const ws of this.sockets.values()) ws.close();
    this.sockets.clear();
  }
}
