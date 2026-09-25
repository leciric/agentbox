// Follows the current environment's event stream (server-sent events). When the
// stream drops, it reconnects with backoff, starting this machine's daemon again
// if needed; switching environments reconnects it to the new one.
import type http from 'node:http';
import { isLocal, requestOptions } from './connection';
import { ensureDaemon } from './daemon';

export interface ConnectionState {
  state: 'connecting' | 'connected' | 'disconnected';
  error?: string;
}

// parseEvents splits the complete events off the front of buf and returns their
// data fields, plus the incomplete rest.
export function parseEvents(buf: string): { data: string[]; rest: string } {
  const data: string[] = [];
  for (let end = buf.indexOf('\n\n'); end >= 0; end = buf.indexOf('\n\n')) {
    const lines = buf
      .slice(0, end)
      .split('\n')
      .filter((line) => line.startsWith('data:'))
      .map((line) => line.slice(5).replace(/^ /, ''));
    if (lines.length > 0) data.push(lines.join('\n'));
    buf = buf.slice(end + 2);
  }
  return { data, rest: buf };
}

export class EventStream {
  state: ConnectionState = { state: 'connecting' };
  private req?: http.ClientRequest;
  private timer?: NodeJS.Timeout;
  private stopped = false;
  // Each connection has a generation, so a stream that was replaced can't retry.
  private generation = 0;

  constructor(
    private readonly onEvent: (event: unknown) => void,
    private readonly onState: (state: ConnectionState) => void,
  ) {}

  start(): void {
    this.stopped = false;
    void this.connect(0);
  }

  stop(): void {
    this.stopped = true;
    this.generation++;
    clearTimeout(this.timer);
    this.req?.destroy();
  }

  // restart follows the current environment's stream instead.
  restart(): void {
    this.stop();
    this.state = { state: 'connecting' };
    this.start();
  }

  private setState(state: ConnectionState): void {
    this.state = state;
    this.onState(state);
  }

  private async connect(failures: number): Promise<void> {
    if (this.stopped) return;
    const generation = ++this.generation;
    let done = false;
    const retry = (err: Error) => {
      if (done || this.stopped || generation !== this.generation) return;
      done = true;
      this.req?.destroy();
      this.setState({ state: 'disconnected', error: err.message });
      this.timer = setTimeout(() => void this.connect(failures + 1), Math.min(500 * 2 ** failures, 5000));
    };

    // Only a first attempt is news. Once one has failed, the next ones stay
    // "disconnected", with the last error, until one gets through: flipping to
    // "connecting" and back on every retry blinked everything that shows the
    // connection, and on Windows before the WSL distro is set up, where each
    // retry fails after a round trip through wsl.exe, that was the setup card.
    if (this.state.state !== 'disconnected') this.setState({ state: 'connecting' });
    try {
      if (isLocal()) await ensureDaemon();
    } catch (err) {
      return retry(err as Error);
    }
    if (generation !== this.generation) return;
    const { module, options } = requestOptions('/v1/events');
    const req = module.request({ ...options, headers: { ...(options.headers as Record<string, string>), Accept: 'text/event-stream' } }, (res) => {
      if (res.statusCode !== 200) {
        let body = '';
        res.setEncoding('utf8');
        res.on('data', (chunk: string) => (body += chunk));
        res.on('end', () => {
          let reason = `the event stream answered HTTP ${res.statusCode}`;
          try {
            reason = JSON.parse(body).error ?? reason;
          } catch {
            // not JSON
          }
          retry(new Error(reason));
        });
        return;
      }
      failures = 0;
      this.setState({ state: 'connected' });
      let buf = '';
      res.setEncoding('utf8');
      res.on('data', (chunk: string) => {
        if (generation !== this.generation) return;
        const { data, rest } = parseEvents(buf + chunk);
        buf = rest;
        for (const item of data) {
          try {
            this.onEvent(JSON.parse(item));
          } catch {
            // not an event we understand
          }
        }
      });
      res.on('end', () => retry(new Error('the daemon closed the event stream')));
      res.on('error', retry);
    });
    req.on('error', retry);
    req.end();
    this.req = req;
  }
}
