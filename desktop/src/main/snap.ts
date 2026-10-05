// SnapShots (agentbox snap, internal/snap): a global shortcut that runs the
// command-line tool, which captures the window in front and hands it to the
// daemon; the daemon's snap event then brings this window forward, where the
// renderer's SnapComposer has it. The capture itself is the CLI's, so a
// desktop's own key bind (Hyprland's `bind = SUPER SHIFT, S, exec, agentbox
// snap`) and this shortcut end in the same composer.
import { execFile } from 'node:child_process';
import { app, globalShortcut, type BrowserWindow } from 'electron';
import { agentboxBin } from './cli';

export const snapShortcut = 'CommandOrControl+Alt+S';

// enableShortcutPortal must run before the app is ready. On Wayland a global
// shortcut is the compositor's to grant, through the desktop portal, which
// Chromium only asks with this feature on. It joins whatever enable-features
// is already on the command line (voicegpu.ts): Chromium reads the last one.
export function enableShortcutPortal(): void {
  if (process.platform !== 'linux') return;
  const features = app.commandLine.getSwitchValue('enable-features');
  app.commandLine.appendSwitch('enable-features', [features, 'GlobalShortcutsPortal'].filter(Boolean).join(','));
}

export function registerSnapShortcut(): void {
  const ok = globalShortcut.register(snapShortcut, () => {
    execFile(agentboxBin(), ['snap'], { timeout: 60_000 }, (err, _stdout, stderr) => {
      if (err) console.error('agentbox snap:', stderr.trim().split('\n').pop() || err.message);
    });
  });
  if (!ok) console.error(`SnapShots: ${snapShortcut} is taken by another app; bind \`agentbox snap\` in your desktop instead`);
  app.on('will-quit', () => globalShortcut.unregisterAll());
}

// showForSnap brings the window forward for a SnapShot that just arrived.
export function showForSnap(win: BrowserWindow | undefined, event: unknown): void {
  if (!win || win.isDestroyed() || (event as { type?: string }).type !== 'snap') return;
  if (win.isMinimized()) win.restore();
  win.show();
  win.focus();
}
