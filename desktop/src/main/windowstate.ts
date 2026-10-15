// The window's place, size and zoom, kept from one launch to the next and
// kept on a display as displays come and go. Where it goes is windowbounds.ts;
// this is the Electron side: reading and writing window.json in the app's
// userData, and listening to the window and the screen.
import { app, screen, type BrowserWindow, type BrowserWindowConstructorOptions } from 'electron';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { minSize, placeWindow, refit, type Rect } from './windowbounds';

interface Saved {
  bounds: Rect;
  maximized: boolean;
  // zoom is Chromium's zoom level (Ctrl +/-, Ctrl 0), 0 at 100%, which it
  // doesn't keep across launches for a file:// page by itself.
  zoom: number;
}

const file = () => join(app.getPath('userData'), 'window.json');

function load(): Partial<Saved> {
  try {
    return JSON.parse(readFileSync(file(), 'utf8')) as Partial<Saved>;
  } catch {
    return {};
  }
}

function displays() {
  return screen.getAllDisplays();
}

// windowOptions are the BrowserWindow's size and place: last launch's,
// fitted to the displays there are now (placeWindow), or a new window's on
// the display under the pointer. It opens hidden; showWindow shows it once
// it has painted, maximised again if it was.
export function windowOptions(): BrowserWindowConstructorOptions {
  const saved = load();
  const fallback = screen.getDisplayNearestPoint(screen.getCursorScreenPoint());
  return { ...placeWindow(saved.bounds, displays(), fallback), minWidth: minSize.width, minHeight: minSize.height, show: false };
}

// keepWindowState restores the window's zoom and maximised state, shows it,
// saves its place as it changes, and brings it back onto a display when the
// one it was on goes away or changes shape.
export function keepWindowState(win: BrowserWindow): void {
  const saved = load();
  const placed = win.getBounds();
  win.once('ready-to-show', () => {
    // On Windows, a window created on a display whose scale differs from the
    // primary's can come out at the primary's scale: set it again, now that
    // it's on that display, so it's the size it was.
    if (process.platform === 'win32') win.setBounds(placed);
    if (saved.maximized) win.maximize();
    win.show();
  });
  // And if it never paints (a renderer that fails to load), show it anyway.
  setTimeout(() => {
    if (!win.isDestroyed() && !win.isVisible()) win.show();
  }, 5_000);
  const zoom = typeof saved.zoom === 'number' && Number.isFinite(saved.zoom) ? Math.max(-3, Math.min(3, saved.zoom)) : 0;
  win.webContents.on('did-finish-load', () => {
    if (zoom) win.webContents.setZoomLevel(zoom);
  });

  // What's saved is the window's own place, not a maximised or full-screen
  // window's, whose getNormalBounds is where it goes back to. A snapped
  // window (Windows' Snap, GNOME's tiling) is saved where it's snapped.
  let timer: NodeJS.Timeout | undefined;
  const save = () => {
    clearTimeout(timer);
    if (win.isDestroyed() || win.isMinimized()) return;
    const whole = win.isMaximized() || win.isFullScreen();
    const state: Saved = { bounds: whole ? win.getNormalBounds() : win.getBounds(), maximized: win.isMaximized(), zoom: win.webContents.getZoomLevel() };
    try {
      writeFileSync(file(), JSON.stringify(state));
    } catch (err) {
      console.error('window state:', err);
    }
  };
  const later = () => {
    clearTimeout(timer);
    timer = setTimeout(save, 500);
  };
  win.on('resize', later);
  win.on('move', later);
  win.on('maximize', later);
  win.on('unmaximize', later);
  win.webContents.on('zoom-changed', later);
  win.on('close', save);

  // A display unplugged, rotated to portrait, rescaled or moved: the OS
  // doesn't always bring the window back (X11 doesn't), so a window whose
  // title bar is now on no display, or that's bigger than its display, is
  // fitted to the nearest. A maximised one follows its display by itself.
  const onDisplays = () => {
    if (win.isDestroyed() || win.isMaximized() || win.isFullScreen() || win.isMinimized()) return;
    const next = refit(win.getBounds(), displays());
    if (next) win.setBounds(next);
  };
  screen.on('display-removed', onDisplays);
  screen.on('display-metrics-changed', onDisplays);
  win.on('closed', () => {
    clearTimeout(timer);
    screen.off('display-removed', onDisplays);
    screen.off('display-metrics-changed', onDisplays);
  });
}
