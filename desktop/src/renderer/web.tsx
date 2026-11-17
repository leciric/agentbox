// The app in a browser, served by a hub: sign in, pick an environment, then the
// same app as the desktop one, on the web bridge. Or served by a daemon to a
// phone on its local network (web/bridge.ts's lan): pair, then the chats.
import '@fontsource-variable/inter';
import './styles.css';
import { StrictMode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { restoreLanguage } from './lib/i18n';
import { restoreMode } from './lib/theme';
import { currentWebTarget, followPhoneEvents, installWebBridge, lan } from './web/bridge';
import { currentSession, NotPaired, pairFromURL } from './web/PhonePair';
import { registerShell } from './web/pwa';
import { SignIn } from './web/SignIn';

installWebBridge();
// Sign-in runs before there is an environment to ask about the theme, so the
// page opens in whichever mode this browser last saw (lib/theme.ts).
restoreMode();
// The language it was last in, for the pages before there is a daemon to ask
// (sign-in, pairing); the app and the phone's chats confirm it from the
// settings (main.tsx, phone.tsx).
restoreLanguage();

const container = document.getElementById('root')!;
let root: Root | undefined = createRoot(container);

async function start(): Promise<void> {
  // The app renders into a fresh root; main.tsx creates it.
  root?.unmount();
  root = undefined;
  await import('./main');
}

async function bootPhone(): Promise<void> {
  registerShell();
  const paired = await pairFromURL();
  // Not reaching the daemon isn't being unpaired: the installed app opens
  // offline from its cache (web/sw.ts), and tries again once back online, or
  // when it's opened again.
  let unreachable: string | undefined;
  const session =
    paired?.session ??
    (await currentSession().catch((err: unknown) => {
      unreachable = err instanceof Error ? err.message : String(err);
      window.addEventListener('online', () => location.reload());
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') location.reload();
      });
      return null;
    }));
  if (session) {
    root?.unmount();
    root = undefined;
    followPhoneEvents();
    const { startPhone } = await import('./phone');
    return startPhone(session.phone);
  }
  // Opening a pairing link on this very page changes only its fragment.
  window.addEventListener('hashchange', () => {
    if (/[#&]pair=/.test(location.hash)) location.reload();
  });
  root!.render(
    <StrictMode>
      <NotPaired error={paired?.error} unreachable={unreachable} />
    </StrictMode>,
  );
}

async function boot(): Promise<void> {
  if (lan) return bootPhone();
  const signedIn = await fetch('/v1/me', { credentials: 'same-origin' }).then((res) => res.ok).catch(() => false);
  if (signedIn && currentWebTarget().environmentId) return start();
  root!.render(
    <StrictMode>
      <SignIn signedIn={signedIn} onReady={() => void start()} />
    </StrictMode>,
  );
}

void boot();
