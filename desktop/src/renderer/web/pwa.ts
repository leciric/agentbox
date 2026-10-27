// The phone page as an app on the phone's home screen: its service worker
// (sw.ts), which only a secure context gets, so only over https (the
// Cloudflare Tunnel, Reach from anywhere) and not on the local network's
// plain HTTP; and the browser's own offer to install it, kept for a button.
import { useSyncExternalStore } from 'react';

// registerShell starts the service worker, where there can be one.
export function registerShell(): void {
  if (!installable() || !('serviceWorker' in navigator)) return;
  navigator.serviceWorker.register('./sw.js', { scope: './' }).catch((err: unknown) => console.warn('service worker:', err));
}

// installable says this page can become an app: a page served over plain
// HTTP can't, as a service worker needs https.
export const installable = () => window.isSecureContext;

// useStandalone says the page is the installed app, opened from the home
// screen. Installing it moves the very page it was in into the app's window
// (Chrome), so it changes while the page is open.
const standaloneQuery = matchMedia('(display-mode: standalone)');
const standalone = () => standaloneQuery.matches || (navigator as { standalone?: boolean }).standalone === true;

export function useStandalone(): boolean {
  return useSyncExternalStore((l) => {
    standaloneQuery.addEventListener('change', l);
    return () => standaloneQuery.removeEventListener('change', l);
  }, standalone);
}

// ios says it's an iPhone's or iPad's browser, which never offers to install a
// page: Safari's Share menu has Add to Home Screen. An iPad says it's a Mac,
// but a Mac has no touch screen.
export const ios = () => /iPhone|iPad|iPod/.test(navigator.userAgent) || (/Macintosh/.test(navigator.userAgent) && navigator.maxTouchPoints > 1);

interface InstallPrompt extends Event {
  prompt(): Promise<void>;
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>;
}

// Chrome offers to install the page once, as it loads, before there's anything
// to show a button in: the offer is kept from the start.
let offer: InstallPrompt | null = null;
const listeners = new Set<() => void>();
const changed = () => listeners.forEach((l) => l());

window.addEventListener('beforeinstallprompt', (e) => {
  e.preventDefault();
  offer = e as InstallPrompt;
  changed();
});
window.addEventListener('appinstalled', () => {
  offer = null;
  changed();
});

// useInstall is what installs the page, while the browser offers to.
export function useInstall(): (() => Promise<void>) | null {
  const current = useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => offer,
  );
  if (!current) return null;
  return async () => {
    await current.prompt();
    await current.userChoice;
    offer = null;
    changed();
  };
}
