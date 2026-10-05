// OS notifications for what agents did (the renderer's lib/notifications.ts
// decides what they say). They're only shown while the window isn't the one
// in front: there, the renderer's own toast says it. Clicking one brings the
// window forward and hands the notification's ID back to the renderer, which
// goes where it points, the same as clicking its toast.
import { Notification, type BrowserWindow } from 'electron';

export interface OSNotice {
  id: string;
  title: string;
  body: string;
}

// Shown notifications are kept until they close: one Electron no longer
// references can be collected, and its click with it.
const shown = new Map<string, Notification>();

export function showNotice(win: BrowserWindow | undefined, notice: OSNotice, onClick: (id: string) => void): boolean {
  if (!win || win.isDestroyed() || (win.isFocused() && win.isVisible()) || !Notification.isSupported()) return false;
  const n = new Notification({ title: notice.title, body: notice.body });
  shown.set(notice.id, n);
  n.on('click', () => {
    if (win.isDestroyed()) return;
    if (win.isMinimized()) win.restore();
    win.show();
    win.focus();
    onClick(notice.id);
  });
  n.on('close', () => shown.delete(notice.id));
  n.show();
  return true;
}
