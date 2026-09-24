// The app in a browser, served by a hub: sign in, pick an environment, then the
// same app as the desktop one, on the web bridge.
import '@fontsource-variable/inter';
import './styles.css';
import { StrictMode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { restoreMode } from './lib/theme';
import { currentWebTarget, installWebBridge } from './web/bridge';
import { SignIn } from './web/SignIn';

installWebBridge();
// Sign-in runs before there is an environment to ask about the theme, so the
// page opens in whichever mode this browser last saw (lib/theme.ts).
restoreMode();

const container = document.getElementById('root')!;
let root: Root | undefined = createRoot(container);

async function start(): Promise<void> {
  // The app renders into a fresh root; main.tsx creates it.
  root?.unmount();
  root = undefined;
  await import('./main');
}

async function boot(): Promise<void> {
  const signedIn = await fetch('/v1/me', { credentials: 'same-origin' }).then((res) => res.ok).catch(() => false);
  if (signedIn && currentWebTarget().environmentId) return start();
  root!.render(
    <StrictMode>
      <SignIn signedIn={signedIn} onReady={() => void start()} />
    </StrictMode>,
  );
}

void boot();
