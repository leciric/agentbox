import { type DeviceConfig, type DeviceInput, parseMessage } from './protocol';

export type StreamState = { phase: 'connecting' } | { phase: 'playing'; config: DeviceConfig } | { phase: 'failed'; reason: string };

// DeviceStreamSession shows a device stream (protocol.ts) on a canvas: it
// opens the stream through the bridge (the main process's relay in the app,
// the browser's WebSockets on a web page), decodes its H.264 with WebCodecs,
// draws each frame as soon as it is decoded, and reconnects if it drops.
// What React adds is DeviceStreamView's; this is the part a page without
// React can use too.
export class DeviceStreamSession {
  private id?: number;
  private active = true;
  private decoder: VideoDecoder | null = null;
  private needKey = true;
  private config: DeviceConfig | null = null;
  private retry?: ReturnType<typeof setTimeout>;
  private readonly unlisten: (() => void)[] = [];

  constructor(
    private readonly path: string,
    private readonly canvas: HTMLCanvasElement,
    private readonly onState: (state: StreamState) => void,
  ) {
    const { stream } = window.agentbox;
    this.unlisten.push(
      stream.onOpened((id) => id === this.id && this.onState({ phase: 'connecting' })),
      stream.onData((id, data) => id === this.id && this.received(data)),
      stream.onExited((id, reason) => id === this.id && this.exited(reason)),
    );
    this.connect();
  }

  private connect(): void {
    this.onState({ phase: 'connecting' });
    void window.agentbox.stream.open(this.path).then((id) => {
      if (!this.active) return window.agentbox.stream.close(id);
      this.id = id;
    });
  }

  private exited(reason: string): void {
    this.id = undefined;
    this.resetDecoder();
    if (!this.active) return;
    this.onState({ phase: 'failed', reason });
    this.retry = setTimeout(() => this.active && this.connect(), 2_000);
  }

  private received(data: Uint8Array): void {
    const msg = parseMessage(data);
    if (!msg) return;
    if (msg.kind === 'status') {
      this.onState({ phase: 'failed', reason: msg.error });
      return;
    }
    if (msg.kind === 'config') {
      void this.configure(msg.config);
      return;
    }
    const decoder = this.decoder;
    if (!decoder || decoder.state !== 'configured') return;
    // After a failure, nothing decodes until the keyframe asked for.
    if (this.needKey && !msg.key) return;
    this.needKey = false;
    decoder.decode(new EncodedVideoChunk({ type: msg.key ? 'key' : 'delta', timestamp: msg.timestamp, data: msg.data }));
  }

  private async configure(config: DeviceConfig): Promise<void> {
    this.resetDecoder();
    this.config = config;
    const base: VideoDecoderConfig = {
      codec: config.codec,
      description: config.description,
      codedWidth: config.width,
      codedHeight: config.height,
      // Each frame out as soon as it's in, rather than queued for smoothness.
      optimizeForLatency: true,
    };
    // Hardware when the machine has it for this stream, software otherwise.
    let chosen: VideoDecoderConfig | null = null;
    for (const hardwareAcceleration of ['prefer-hardware', 'no-preference'] as const) {
      const candidate = { ...base, hardwareAcceleration };
      try {
        if ((await VideoDecoder.isConfigSupported(candidate)).supported) {
          chosen = candidate;
          break;
        }
      } catch {
        // an invalid config: try the next
      }
    }
    if (this.config !== config || !this.active) return; // a newer one came meanwhile
    if (!chosen) {
      this.onState({ phase: 'failed', reason: `this computer can't decode ${config.codec} video` });
      return;
    }
    const decoder = new VideoDecoder({
      output: (frame) => this.draw(frame),
      error: () => {
        // Start over from a keyframe, with a decoder of its own.
        if (this.decoder === decoder) this.decoder = null;
        this.needKey = true;
        this.send({ type: 'keyframe' });
      },
    });
    decoder.configure(chosen);
    this.decoder = decoder;
    this.needKey = true;
    this.onState({ phase: 'playing', config });
  }

  private draw(frame: VideoFrame): void {
    const { canvas } = this;
    if (canvas.width !== frame.displayWidth || canvas.height !== frame.displayHeight) {
      canvas.width = frame.displayWidth;
      canvas.height = frame.displayHeight;
    }
    canvas.getContext('2d')?.drawImage(frame, 0, 0);
    frame.close();
  }

  private resetDecoder(): void {
    const decoder = this.decoder;
    this.decoder = null;
    if (decoder && decoder.state !== 'closed') decoder.close();
  }

  // send passes input to the device.
  send(input: DeviceInput): void {
    if (this.id !== undefined) window.agentbox.stream.write(this.id, JSON.stringify(input));
  }

  close(): void {
    this.active = false;
    clearTimeout(this.retry);
    for (const fn of this.unlisten) fn();
    if (this.id !== undefined) window.agentbox.stream.close(this.id);
    this.id = undefined;
    this.resetDecoder();
  }
}
