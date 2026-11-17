// Before a phone can see anything, it pairs (internal/daemon/lan.go): the QR
// code AgentBox shows on the computer opens this page with a one-time secret
// in its fragment, which is posted back to the daemon for a cookie of the
// phone's own. A phone that comes without one, or whose pairing was revoked,
// is told how to pair, and can scan the code or paste the link right here:
// an app installed from this page (web/pwa.ts) doesn't share the browser's
// cookies on an iPhone, so it pairs from inside itself.
import { Camera, Smartphone, TriangleAlert } from 'lucide-react';
import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react';
import type * as T from '../../shared/api';
import { Button } from '../components/ui/button';
import { Input } from '../components/ui/input';
import { useT } from '../lib/i18n';
import { InstallNote } from './Install';
import { pairSecretOf } from './pairCode.ts';
import { phoneName } from './phoneName.ts';
import { useStandalone } from './pwa';

// pairFromURL pairs with the secret in the page's address, if there's one, and
// takes it out of the address either way: it works once.
export async function pairFromURL(): Promise<{ session?: T.LANSession; error?: string } | null> {
  const secret = /[#&]pair=([^&]+)/.exec(location.hash)?.[1];
  if (!secret) return null;
  history.replaceState(null, '', location.pathname + location.search);
  try {
    const res = await fetch('/lan/pair', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ secret, name: phoneName() } satisfies T.LANPairRequest),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) return { error: (body as T.Error).error ?? `HTTP ${res.status}` };
    return { session: body as T.LANSession };
  } catch (err) {
    return { error: err instanceof Error ? err.message : String(err) };
  }
}

export async function currentSession(): Promise<T.LANSession | null> {
  const res = await fetch('/lan/session', { credentials: 'same-origin' });
  if (res.status === 401) return null;
  if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
  return (await res.json()) as T.LANSession;
}

export function PlainHTTPNote({ className = '' }: { className?: string }) {
  const t = useT();
  if (location.protocol === 'https:') return null;
  return (
    <p className={`text-caption leading-relaxed text-subtle ${className}`}>
      {t('web.pair.plainHTTP')}
    </p>
  );
}

// pairWith pairs with a secret scanned or pasted here the way opening its
// link would: the page's fragment changes, and web.tsx reloads it to pair.
function pairWith(secret: string): void {
  location.hash = `pair=${secret}`;
}

export function NotPaired({ error, unreachable }: { error?: string; unreachable?: string }) {
  const t = useT();
  const hl = (c: ReactNode) => <span className="text-primary">{c}</span>;
  const standalone = useStandalone();
  return (
    // Clear of the notch and the home bar, as PhoneApp is: the installed app
    // draws under them.
    <div className="h-dvh overflow-y-auto pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)] pt-[env(safe-area-inset-top)]">
      <div className="flex min-h-full items-center justify-center px-6 py-10">
        <div className="grid w-full max-w-sm gap-5" data-phone-unpaired>
          <div className="flex size-12 items-center justify-center rounded-2xl bg-surface-raised text-brand-300">
            <Smartphone className="size-6" />
          </div>
          <div className="grid gap-2">
            <h1 className="text-xl font-semibold tracking-tight text-title">{t('web.pair.title')}</h1>
            <p className="text-body leading-relaxed text-muted">{t.rich('web.pair.steps', { hl })}</p>
            <p className="text-body leading-relaxed text-muted">
              {t.rich('web.pair.terminal', { mono: (c) => <span className="font-mono text-ui text-primary">{c}</span> })}
            </p>
          </div>
          {standalone && <p className="text-ui leading-relaxed text-secondary">{t('web.pair.installed')}</p>}
          {unreachable && <Failure>{t('web.pair.unreachable', { error: unreachable })}</Failure>}
          {error && <Failure>{t('web.pair.failed', { error })}</Failure>}
          <PairHere />
          <InstallNote />
          <PlainHTTPNote />
        </div>
      </div>
    </div>
  );
}

function Failure({ children }: { children: ReactNode }) {
  return (
    <div className="flex gap-2 rounded-xl border border-rose-500/30 bg-rose-500/10 px-3 py-2.5 text-ui text-rose-200">
      <TriangleAlert className="mt-0.5 size-4 shrink-0" />
      <span>{children}</span>
    </div>
  );
}

// A camera is there only in a secure context: not on the local network's
// plain HTTP, where the phone's own camera app opens the link instead.
const hasCamera = () => typeof navigator.mediaDevices?.getUserMedia === 'function';

// PairHere pairs with the QR code, through the camera, or with its link or
// code pasted.
function PairHere() {
  const t = useT();
  const [scanning, setScanning] = useState(false);
  const [text, setText] = useState('');
  const [wrong, setWrong] = useState(false);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const secret = pairSecretOf(text);
    setWrong(!secret);
    if (secret) pairWith(secret);
  };
  return (
    <div className="grid gap-3">
      {hasCamera() &&
        (scanning ? (
          <Scanner onClose={() => setScanning(false)} />
        ) : (
          <div>
            <Button variant="primary" data-phone-scan onClick={() => setScanning(true)}>
              <Camera className="size-4" /> {t('web.pair.scan')}
            </Button>
          </div>
        ))}
      <form className="grid gap-1.5" onSubmit={submit}>
        <label htmlFor="phone-pair-code" className="text-ui text-muted">
          {t('web.pair.pasteLabel')}
        </label>
        <div className="flex gap-2">
          {/* 16px: iOS zooms into a smaller field it focuses. */}
          <Input
            id="phone-pair-code"
            className="min-w-0 flex-1 text-base"
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
            placeholder={t('web.pair.pastePlaceholder')}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setWrong(false);
            }}
            data-phone-code
          />
          <Button type="submit" disabled={!text.trim()}>
            {t('web.pair.pasteSubmit')}
          </Button>
        </div>
        {wrong && <p className="text-caption text-rose-300">{t('web.pair.notACode')}</p>}
      </form>
    </div>
  );
}

// Scanner reads QR codes from the back camera until one carries a pairing.
function Scanner({ onClose }: { onClose: () => void }) {
  const t = useT();
  const video = useRef<HTMLVideoElement>(null);
  const [error, setError] = useState<{ denied: boolean; message: string }>();
  useEffect(() => {
    let stopped = false;
    let stream: MediaStream | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    void (async () => {
      try {
        stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'environment' }, audio: false });
        if (stopped) {
          // Cancelled while the camera was starting: cleanup ran before there was a stream to stop.
          stream.getTracks().forEach((track) => track.stop());
          return;
        }
        const v = video.current!;
        v.srcObject = stream;
        await v.play();
        const read = await qrReader();
        const tick = async () => {
          if (stopped) return;
          const found = (await read(v).catch(() => null)) ?? '';
          if (stopped) return; // cancelled while it was reading
          const secret = pairSecretOf(found);
          if (secret) pairWith(secret);
          else timer = setTimeout(() => void tick(), 200);
        };
        void tick();
      } catch (err) {
        if (stopped) return;
        const denied = err instanceof DOMException && (err.name === 'NotAllowedError' || err.name === 'SecurityError');
        setError({ denied, message: err instanceof Error ? err.message : String(err) });
      }
    })();
    return () => {
      stopped = true;
      clearTimeout(timer);
      stream?.getTracks().forEach((track) => track.stop());
    };
  }, []);
  return (
    <div className="grid gap-2" data-phone-scanner>
      {error ? (
        <Failure>{error.denied ? t('web.pair.cameraDenied') : t('web.pair.cameraFailed', { error: error.message })}</Failure>
      ) : (
        <>
          <video ref={video} className="aspect-square w-full rounded-2xl bg-black object-cover" muted playsInline />
          <p className="text-ui text-muted">{t('web.pair.scanning')}</p>
        </>
      )}
      <div>
        <Button size="sm" variant="ghost" onClick={onClose}>
          {t('common.cancel')}
        </Button>
      </div>
    </div>
  );
}

interface BarcodeDetector {
  detect(source: HTMLVideoElement): Promise<{ rawValue: string }[]>;
}
interface BarcodeDetectorClass {
  new (options: { formats: string[] }): BarcodeDetector;
  getSupportedFormats(): Promise<string[]>;
}

// qrReader reads the QR code in a video's frame: with the browser's own
// BarcodeDetector where it has one (Chrome on Android), else with jsQR, loaded
// only then (Safari has none).
async function qrReader(): Promise<(v: HTMLVideoElement) => Promise<string | null>> {
  const Detector = (window as { BarcodeDetector?: BarcodeDetectorClass }).BarcodeDetector;
  if (Detector && (await Detector.getSupportedFormats().catch((): string[] => [])).includes('qr_code')) {
    const detector = new Detector({ formats: ['qr_code'] });
    return async (v) => (await detector.detect(v))[0]?.rawValue ?? null;
  }
  const { default: jsQR } = await import('jsqr');
  const canvas = document.createElement('canvas');
  const ctx = canvas.getContext('2d', { willReadFrequently: true })!;
  return async (v) => {
    if (!v.videoWidth) return null;
    // A smaller frame reads as well, and much faster.
    const scale = Math.min(1, 640 / Math.max(v.videoWidth, v.videoHeight));
    canvas.width = Math.round(v.videoWidth * scale);
    canvas.height = Math.round(v.videoHeight * scale);
    ctx.drawImage(v, 0, 0, canvas.width, canvas.height);
    const frame = ctx.getImageData(0, 0, canvas.width, canvas.height);
    // The app's QR code can be light on dark, in its dark mode.
    return jsQR(frame.data, frame.width, frame.height, { inversionAttempts: 'attemptBoth' })?.data ?? null;
  };
}
