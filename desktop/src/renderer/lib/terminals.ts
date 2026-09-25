// One xterm.js terminal per agent. It stays alive while you switch agents and
// tabs, so scrollback and full-screen tools (Claude Code, Codex) keep their state.
import { FitAddon } from '@xterm/addon-fit';
import { Terminal, type ITheme } from '@xterm/xterm';
import { useSyncExternalStore } from 'react';
import { agentPath } from './api';
import { onMode } from './theme';

export interface TerminalStatus {
  state: 'idle' | 'connecting' | 'open' | 'closed';
  reason?: string;
}

// xterm.js paints onto a canvas, so it takes its colours as strings rather
// than through a class. They are read out of the same custom properties
// everything else reaches a colour through (styles.css declares a --ab-term-*
// for each), so a terminal turns over with the rest of the window instead of
// keeping its own idea of what dark means. The cursor and the selection come
// from the accent, so it wears the desktop's theme too.
function terminalTheme(): ITheme {
  const style = getComputedStyle(document.documentElement);
  const read = (name: string) => style.getPropertyValue(name).trim();
  const background = read('--ab-term-background');
  return {
    background,
    foreground: read('--ab-term-foreground'),
    cursor: read('--ab-brand-400'),
    cursorAccent: background,
    selectionBackground: read('--ab-selection'),
    black: read('--ab-term-black'),
    brightBlack: read('--ab-term-bright-black'),
    blue: read('--ab-term-blue'),
    brightBlue: read('--ab-term-bright-blue'),
    magenta: read('--ab-term-magenta'),
    brightMagenta: read('--ab-term-bright-magenta'),
    cyan: read('--ab-term-cyan'),
    green: read('--ab-term-green'),
    yellow: read('--ab-term-yellow'),
    red: read('--ab-term-red'),
  };
}

const byId = new Map<number, AgentTerminal>();
const byRef = new Map<string, AgentTerminal>();
const encoder = new TextEncoder();

// A module-level subscription, not a component's: this module is now reached
// from more than just the terminal tab (AgentContextMenu's destroy action
// disposes a terminal too), including contexts like the preview harness that
// import it before window.agentbox exists. Guarded rather than reordered,
// since nothing here can control which of two sibling imports resolves first.
window.agentbox?.stream.onData((id, data) => byId.get(id)?.term.write(data));
window.agentbox?.stream.onOpened((id) => byId.get(id)?.opened());
window.agentbox?.stream.onExited((id, reason) => byId.get(id)?.exited(reason));

export class AgentTerminal {
  readonly term: Terminal;
  readonly element = document.createElement('div');
  status: TerminalStatus = { state: 'idle' };
  private readonly fit = new FitAddon();
  private readonly listeners = new Set<() => void>();
  private id?: number;
  private disposed = false;

  constructor(readonly ref: string) {
    this.element.style.height = '100%';
    this.term = new Terminal({
      fontFamily: '"JetBrains Mono Variable", "JetBrainsMono Nerd Font", "JetBrains Mono", monospace',
      fontSize: 13,
      lineHeight: 1.2,
      cursorBlink: true,
      scrollback: 10_000,
      theme: terminalTheme(),
    });
    this.term.loadAddon(this.fit);
    this.term.onData((data) => this.send(encoder.encode(data)));
    // Some mouse reports are binary: one byte per character.
    this.term.onBinary((data) => this.send(Uint8Array.from(data, (c) => c.charCodeAt(0))));
    this.term.onResize(({ cols, rows }) => {
      if (this.id !== undefined) window.agentbox.stream.write(this.id, JSON.stringify({ cols, rows }));
    });
    // Ctrl+Shift+C and Ctrl+Shift+V copy and paste; plain Ctrl+C still goes to the agent.
    this.term.attachCustomKeyEventHandler((event) => {
      if (event.type !== 'keydown' || !event.ctrlKey || !event.shiftKey) return true;
      if (event.code === 'KeyC') {
        const selection = this.term.getSelection();
        if (selection) window.agentbox.copyText(selection);
        return false;
      }
      if (event.code === 'KeyV') {
        void window.agentbox.readText().then((text) => this.term.paste(text));
        return false;
      }
      return true;
    });
  }

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  getStatus = (): TerminalStatus => this.status;

  attach(container: HTMLElement): void {
    container.appendChild(this.element);
    if (!this.term.element) this.term.open(this.element);
    this.fitToContainer();
    if (this.status.state === 'idle') this.connect();
    this.term.focus();
  }

  detach(): void {
    this.element.remove();
  }

  fitToContainer(): void {
    if (this.element.isConnected && this.element.clientWidth > 0) this.fit.fit();
  }

  connect(): void {
    if (this.disposed || this.status.state === 'connecting' || this.status.state === 'open') return;
    this.setStatus({ state: 'connecting' });
    const path = `${agentPath(this.ref)}/terminal?cols=${this.term.cols}&rows=${this.term.rows}`;
    void window.agentbox.stream.open(path).then((id) => {
      if (this.disposed) return window.agentbox.stream.close(id);
      this.id = id;
      byId.set(id, this);
    });
  }

  opened(): void {
    this.setStatus({ state: 'open' });
    this.fitToContainer();
    if (this.id !== undefined) window.agentbox.stream.write(this.id, JSON.stringify({ cols: this.term.cols, rows: this.term.rows }));
  }

  exited(reason: string): void {
    if (this.id !== undefined) byId.delete(this.id);
    this.id = undefined;
    this.setStatus({ state: 'closed', reason });
  }

  dispose(): void {
    this.disposed = true;
    if (this.id !== undefined) window.agentbox.stream.close(this.id);
    this.term.dispose();
    this.element.remove();
  }

  private send(data: Uint8Array): void {
    if (this.id !== undefined && this.status.state === 'open') window.agentbox.stream.write(this.id, data);
  }

  private setStatus(status: TerminalStatus): void {
    this.status = status;
    for (const listener of this.listeners) listener();
  }
}

// Every terminal alive when the window turns over is repainted: they outlive
// the tab they are shown in, so one that is off screen would otherwise still
// be dark when you came back to it.
onMode(() => {
  const theme = terminalTheme();
  for (const terminal of byRef.values()) terminal.term.options.theme = theme;
});

export function terminalFor(ref: string): AgentTerminal {
  let terminal = byRef.get(ref);
  if (!terminal) {
    terminal = new AgentTerminal(ref);
    byRef.set(ref, terminal);
  }
  return terminal;
}

export function disposeTerminal(ref: string): void {
  byRef.get(ref)?.dispose();
  byRef.delete(ref);
}

export function useTerminalStatus(terminal: AgentTerminal): TerminalStatus {
  return useSyncExternalStore(terminal.subscribe, terminal.getStatus);
}
