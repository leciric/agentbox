import type RFB from '@novnc/novnc';
import { connectView } from './vnc';

// VncSession shows a VNC display in target, reconnecting if it drops: what
// useVncView does for the app, without React, so the page of `agentbox
// machines serve` shows a machine with the same client. noVNC only resizes the
// remote display for a client that takes input, so the client always does;
// until control is on, the caller covers the view with an overlay, and the
// view never takes focus.
export class VncSession {
  private readonly path: string;
  private readonly target: HTMLElement;
  private readonly onConnected: (connected: boolean) => void;
  private rfb: RFB | null = null;
  private active = true;
  private control = false;

  constructor(path: string, target: HTMLElement, onConnected: (connected: boolean) => void = () => {}) {
    this.path = path;
    this.target = target;
    this.onConnected = onConnected;
    this.connect();
  }

  private connect(): void {
    const client = connectView(this.path, this.target);
    client.focusOnClick = this.control;
    client.addEventListener('connect', () => this.onConnected(true));
    client.addEventListener('disconnect', () => {
      if (this.rfb !== client) return;
      this.rfb = null;
      this.onConnected(false);
      if (this.active) setTimeout(() => this.active && this.connect(), 2_000);
    });
    this.rfb = client;
  }

  // setControl gives the display your mouse and keyboard, or takes them back;
  // taking control sends your clipboard along.
  setControl(control: boolean): void {
    this.control = control;
    const client = this.rfb;
    if (!client) return;
    client.focusOnClick = control;
    if (control) {
      client.focus();
      void this.paste();
    } else {
      client.blur();
    }
  }

  // paste sends your clipboard to the display, for the app in focus to paste.
  async paste(): Promise<void> {
    const text = await window.agentbox.readText();
    if (text && this.rfb) this.rfb.clipboardPasteFrom(text);
  }

  close(): void {
    this.active = false;
    const client = this.rfb;
    this.rfb = null;
    this.onConnected(false);
    try {
      client?.disconnect();
    } catch {
      // already disconnected
    }
  }
}
