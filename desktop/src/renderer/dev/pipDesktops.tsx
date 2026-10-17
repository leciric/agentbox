// The PiP preview's agents' desktops (dev/pip.tsx), drawn rather than streamed: each scene is a
// 1280×800 frame of an agent's machine (its panel, Chromium or a terminal)
// as a function of time, so a dock tile can run live, a capture is the same
// scene frozen at a moment, and a recording is it played between two.
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react';

export type Scene = 'csv' | 'dark' | 'test' | 'docs' | 'newproject' | 'ci' | 'startup' | 'testdone';

export const W = 1280;
export const H = 800;

// One clock for every live frame on the page, at 12 fps: a dozen tiles cost
// one timer.
const listeners = new Set<(t: number) => void>();
const t0 = performance.now();
let timer: number | undefined;
function subscribe(fn: (t: number) => void) {
  listeners.add(fn);
  timer ??= window.setInterval(() => {
    const t = (performance.now() - t0) / 1000;
    for (const l of listeners) l(t);
  }, 83);
  return () => {
    listeners.delete(fn);
    if (listeners.size === 0) {
      window.clearInterval(timer);
      timer = undefined;
    }
  };
}

export function useClock(on = true): number {
  const [t, setT] = useState(() => (performance.now() - t0) / 1000);
  useEffect(() => (on ? subscribe(setT) : undefined), [on]);
  return t;
}

// Frame scales a 1280×800 frame to the width it's given, keeping its aspect.
export function Frame({ children, className, style }: { children: ReactNode; className?: string; style?: CSSProperties }) {
  const box = useRef<HTMLDivElement>(null);
  const [scale, setScale] = useState(0);
  useLayoutEffect(() => {
    const el = box.current!;
    const ro = new ResizeObserver(() => setScale(el.clientWidth / W));
    ro.observe(el);
    setScale(el.clientWidth / W);
    return () => ro.disconnect();
  }, []);
  return (
    <div ref={box} className={className} style={{ position: 'relative', aspectRatio: `${W} / ${H}`, overflow: 'hidden', ...style }}>
      {scale > 0 && <div style={{ position: 'absolute', left: 0, top: 0, width: W, height: H, transform: `scale(${scale})`, transformOrigin: '0 0' }}>{children}</div>}
    </div>
  );
}

// Desktop is a scene, live (its own clock) or at t seconds.
export function Desktop({ scene, t, className, style }: { scene: Scene; t?: number; className?: string; style?: CSSProperties }) {
  return (
    <Frame className={className} style={style}>
      {t === undefined ? <LiveScene scene={scene} /> : <SceneAt scene={scene} t={t} />}
    </Frame>
  );
}

function LiveScene({ scene }: { scene: Scene }) {
  const t = useClock();
  return <SceneAt scene={scene} t={t} />;
}

export function SceneAt({ scene, t }: { scene: Scene; t: number }) {
  switch (scene) {
    case 'csv':
      return <CsvScene t={t} />;
    case 'dark':
      return <DarkScene t={t} />;
    case 'test':
      return <TestScene t={t} />;
    case 'testdone':
      return <TestScene t={1000} />;
    case 'docs':
      return <DocsScene t={t} />;
    case 'newproject':
      return <NewProjectScene t={t} />;
    case 'ci':
      return <CiScene />;
    case 'startup':
      return <StartupScene />;
  }
}

// ——— The machine around every scene: wallpaper, the panel, a window. ———

const sans = 'Inter, "DejaVu Sans", system-ui, sans-serif';
const mono = '"JetBrains Mono", "DejaVu Sans Mono", ui-monospace, monospace';

function Machine({ children, clock = '14:32' }: { children: ReactNode; clock?: string }) {
  return (
    <div style={{ position: 'absolute', inset: 0, background: 'radial-gradient(120% 90% at 20% 10%, #33476b 0%, #1d2a44 45%, #121a2b 100%)', fontFamily: sans }}>
      <div style={{ position: 'absolute', left: 0, right: 0, top: 0, height: 28, background: '#14161c', color: '#c9ccd6', fontSize: 13, display: 'flex', alignItems: 'center', gap: 18, padding: '0 14px' }}>
        <span style={{ fontWeight: 600 }}>Applications</span>
        <span style={{ opacity: 0.6 }}>Places</span>
        <span style={{ marginLeft: 'auto', opacity: 0.8 }}>{clock}</span>
      </div>
      {children}
    </div>
  );
}

function Browser({ url, tab, children, x = 40, y = 52, w = 1200, h = 724, dark = true }: { url: string; tab: string; children: ReactNode; x?: number; y?: number; w?: number; h?: number; dark?: boolean }) {
  return (
    <div style={{ position: 'absolute', left: x, top: y, width: w, height: h, borderRadius: 10, overflow: 'hidden', boxShadow: '0 30px 80px -20px rgba(0,0,0,.7)', background: dark ? '#202124' : '#fff', display: 'flex', flexDirection: 'column' }}>
      <div style={{ height: 38, background: '#2b2c30', display: 'flex', alignItems: 'flex-end', padding: '0 10px', gap: 6 }}>
        <div style={{ display: 'flex', gap: 7, alignSelf: 'center', marginRight: 10 }}>
          {['#ff5f57', '#febc2e', '#28c840'].map((c) => (
            <span key={c} style={{ width: 11, height: 11, borderRadius: 6, background: c }} />
          ))}
        </div>
        <div style={{ height: 30, width: 230, background: '#3c3d42', borderRadius: '8px 8px 0 0', color: '#e8eaed', fontSize: 13, display: 'flex', alignItems: 'center', padding: '0 12px', gap: 8 }}>
          <span style={{ width: 14, height: 14, borderRadius: 3, background: '#8ab4f8' }} />
          {tab}
        </div>
      </div>
      <div style={{ height: 40, background: '#3c3d42', display: 'flex', alignItems: 'center', padding: '0 12px', gap: 12, color: '#9aa0a6', fontSize: 13 }}>
        <span>←</span>
        <span>→</span>
        <span>↻</span>
        <div style={{ flex: 1, height: 28, borderRadius: 14, background: '#202124', color: '#e8eaed', display: 'flex', alignItems: 'center', padding: '0 14px' }}>{url}</div>
      </div>
      <div style={{ position: 'relative', flex: 1, overflow: 'hidden' }}>{children}</div>
    </div>
  );
}

function Terminal({ title, lines, x = 60, y = 60, w = 1160, h = 700, cursor = true }: { title: string; lines: { text: string; color?: string }[]; x?: number; y?: number; w?: number; h?: number; cursor?: boolean }) {
  const shown = lines.slice(-Math.floor((h - 60) / 22));
  return (
    <div style={{ position: 'absolute', left: x, top: y, width: w, height: h, borderRadius: 10, overflow: 'hidden', background: '#0f1117', boxShadow: '0 30px 80px -20px rgba(0,0,0,.7)', border: '1px solid #2a2d38' }}>
      <div style={{ height: 32, background: '#1b1e27', color: '#9aa3b5', fontSize: 13, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>{title}</div>
      <div style={{ padding: '12px 16px', fontFamily: mono, fontSize: 16, lineHeight: '22px', color: '#d6deeb', whiteSpace: 'pre' }}>
        {shown.map((l, i) => (
          <div key={i} style={{ color: l.color }}>
            {l.text || ' '}
          </div>
        ))}
        {cursor && <span style={{ display: 'inline-block', width: 10, height: 18, background: '#d6deeb', verticalAlign: 'middle' }} />}
      </div>
    </div>
  );
}

function Cursor({ x, y, down }: { x: number; y: number; down?: boolean }) {
  return (
    <svg width="22" height="30" viewBox="0 0 22 30" style={{ position: 'absolute', left: x, top: y, zIndex: 10, transform: down ? 'scale(.9)' : undefined, filter: 'drop-shadow(0 2px 3px rgba(0,0,0,.5))' }}>
      <path d="M2 2 L2 24 L8 18 L12 28 L16 26 L12 17 L20 17 Z" fill="#fff" stroke="#000" strokeWidth="1.5" />
    </svg>
  );
}

// ease moves from a to b between t1 and t2.
const ease = (t: number, t1: number, t2: number, a: number, b: number) => {
  const k = Math.min(1, Math.max(0, (t - t1) / (t2 - t1)));
  const s = k * k * (3 - 2 * k);
  return a + (b - a) * s;
};

// ——— Scenes ———

const reminders: [string, string, string, string][] = [
  ['Renew the TLS certificate, "staging"', 'Oct 18', 'Ana', 'Open'],
  ['Send the Q4 invoice to Northwind', 'Oct 19', 'Leandro', 'Open'],
  ['Book the team offsite, Lisbon', 'Oct 21', 'Priya', 'Snoozed'],
  ['Rotate the Stripe webhook secret', 'Oct 22', 'Ana', 'Open'],
  ['Review the onboarding emails', 'Oct 24', 'Marco', 'Done'],
  ['Archive the 2025 analytics bucket', 'Oct 27', 'Leandro', 'Open'],
  ['Call the accountant about VAT', 'Oct 30', 'Priya', 'Open'],
  ['Replace the office router', 'Nov 3', 'Marco', 'Snoozed'],
  ['Plan the v1.0 launch post', 'Nov 5', 'Ana', 'Open'],
];

function CsvScene({ t }: { t: number }) {
  const k = t % 9;
  const cx = ease(k, 0.3, 2.2, 760, 1088);
  const cy = ease(k, 0.3, 2.2, 620, 142);
  const pressed = k > 2.3 && k < 2.6;
  const downloaded = k > 2.8 && k < 8.4;
  return (
    <Machine>
      <Browser url="localhost:5173/reminders?status=open" tab="Reminders">
        <div style={{ position: 'absolute', inset: 0, background: '#16181d', color: '#e6e7eb', padding: '28px 36px' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
            <div style={{ fontSize: 28, fontWeight: 700 }}>Reminders</div>
            <span style={{ fontSize: 13, padding: '3px 10px', borderRadius: 12, background: '#2a2f3a', color: '#aab1c0' }}>Open · 6</span>
            <div style={{ marginLeft: 'auto', width: 260, height: 36, borderRadius: 8, background: '#22252d', border: '1px solid #323642', color: '#6f7686', fontSize: 14, display: 'flex', alignItems: 'center', padding: '0 12px' }}>Search reminders…</div>
            <div style={{ height: 36, padding: '0 16px', borderRadius: 8, background: pressed ? '#4338ca' : '#5b5bf0', color: '#fff', fontSize: 14, fontWeight: 600, display: 'flex', alignItems: 'center', gap: 8 }}>⤓ Export CSV</div>
          </div>
          <div style={{ marginTop: 24, border: '1px solid #2a2e38', borderRadius: 10, overflow: 'hidden' }}>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 120px 140px 120px', background: '#1d2027', color: '#8b92a3', fontSize: 13, fontWeight: 600, padding: '12px 18px' }}>
              <span>Title</span>
              <span>Due</span>
              <span>Owner</span>
              <span>Status</span>
            </div>
            {reminders.map(([title, due, owner, status], i) => (
              <div key={i} style={{ display: 'grid', gridTemplateColumns: '1fr 120px 140px 120px', padding: '13px 18px', fontSize: 15, borderTop: '1px solid #252932', opacity: status === 'Done' ? 0.5 : 1 }}>
                <span>{title}</span>
                <span style={{ color: '#aab1c0' }}>{due}</span>
                <span style={{ color: '#aab1c0' }}>{owner}</span>
                <span style={{ color: status === 'Open' ? '#7dd3a8' : status === 'Snoozed' ? '#f2c66d' : '#8b92a3' }}>{status}</span>
              </div>
            ))}
          </div>
        </div>
        {downloaded && (
          <div style={{ position: 'absolute', right: 16, top: 10, width: 330, borderRadius: 10, background: '#2d2e33', boxShadow: '0 12px 30px rgba(0,0,0,.5)', color: '#e8eaed', padding: 14, fontSize: 14 }}>
            <div style={{ fontWeight: 600, marginBottom: 8 }}>Recent download history</div>
            <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
              <span style={{ width: 30, height: 36, borderRadius: 4, background: '#34a853', color: '#fff', fontSize: 10, fontWeight: 700, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>CSV</span>
              <div>
                <div>reminders-2026-10-17.csv</div>
                <div style={{ color: '#9aa0a6', fontSize: 12 }}>6.2 KB · Done</div>
              </div>
            </div>
          </div>
        )}
      </Browser>
      <Cursor x={cx} y={cy} down={pressed} />
    </Machine>
  );
}

function DarkScene({ t }: { t: number }) {
  const k = t % 8;
  const light = k > 4;
  const cy = ease(k, 0.2, 1.2, 520, 236) + ease(k, 4.2, 5.2, 0, 0);
  const bg = light ? '#f6f7f9' : '#15171c';
  const card = light ? '#fff' : '#1d2027';
  const ink = light ? '#1a1d24' : '#e6e7eb';
  const sub = light ? '#5f6675' : '#8b92a3';
  const line = light ? '#e3e6eb' : '#2a2e38';
  return (
    <Machine clock="14:31">
      <Browser url="localhost:5173/settings/appearance" tab="Settings · Reminders">
        <div style={{ position: 'absolute', inset: 0, background: bg, color: ink, display: 'flex', transition: 'background .4s' }}>
          <div style={{ width: 240, borderRight: `1px solid ${line}`, padding: '28px 18px', fontSize: 15, display: 'flex', flexDirection: 'column', gap: 6 }}>
            {['Profile', 'Notifications', 'Appearance', 'Integrations', 'Billing'].map((s) => (
              <div key={s} style={{ padding: '9px 12px', borderRadius: 8, background: s === 'Appearance' ? (light ? '#e8eaff' : '#272a4a') : undefined, color: s === 'Appearance' ? (light ? '#3b3bd6' : '#a5a8ff') : sub }}>
                {s}
              </div>
            ))}
          </div>
          <div style={{ flex: 1, padding: '32px 44px' }}>
            <div style={{ fontSize: 26, fontWeight: 700 }}>Appearance</div>
            <div style={{ color: sub, marginTop: 6, fontSize: 15 }}>Choose how Reminders looks on this device.</div>
            <div style={{ display: 'flex', gap: 18, marginTop: 28 }}>
              {(['System', 'Light', 'Dark'] as const).map((m) => {
                const on = (m === 'Light' && light) || (m === 'Dark' && !light);
                return (
                  <div key={m} style={{ width: 220, borderRadius: 12, border: `2px solid ${on ? '#5b5bf0' : line}`, background: card, padding: 12 }}>
                    <div style={{ height: 110, borderRadius: 8, background: m === 'Light' ? '#eef0f4' : m === 'Dark' ? '#0f1115' : 'linear-gradient(90deg,#eef0f4 50%,#0f1115 50%)', border: `1px solid ${line}` }} />
                    <div style={{ marginTop: 10, fontSize: 15, fontWeight: 600 }}>{m}</div>
                  </div>
                );
              })}
            </div>
            <div style={{ marginTop: 30, borderRadius: 12, border: `1px solid ${line}`, background: card, padding: 18, width: 696 }}>
              {['Reduce motion', 'High-contrast borders', 'Show week numbers'].map((s, i) => (
                <div key={s} style={{ display: 'flex', alignItems: 'center', padding: '10px 0', borderTop: i ? `1px solid ${line}` : undefined, fontSize: 15 }}>
                  {s}
                  <span style={{ marginLeft: 'auto', width: 40, height: 22, borderRadius: 11, background: i === 1 ? '#5b5bf0' : line, position: 'relative' }}>
                    <span style={{ position: 'absolute', top: 3, left: i === 1 ? 21 : 3, width: 16, height: 16, borderRadius: 8, background: '#fff' }} />
                  </span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </Browser>
      <Cursor x={light ? 610 : 850} y={cy} />
    </Machine>
  );
}

const testLines = (n: number) => {
  const out: { text: string; color?: string }[] = [
    { text: '$ go test ./internal/daemon -run TestReconnect -count=50 -race -v', color: '#82aaff' },
  ];
  for (let i = 1; i <= n; i++) {
    out.push({ text: `=== RUN   TestReconnect (${i}/50)` });
    out.push({ text: `--- PASS: TestReconnect (${(0.41 + ((i * 37) % 23) / 100).toFixed(2)}s)`, color: '#addb67' });
  }
  return out;
};

function TestScene({ t }: { t: number }) {
  const n = Math.min(50, 31 + Math.floor(t / 1.6) % 20);
  const done = t >= 1000;
  const lines = testLines(done ? 50 : n);
  if (done) lines.push({ text: 'PASS' }, { text: 'ok  \tgithub.com/leciric/agentbox/internal/daemon\t24.312s', color: '#addb67' }, { text: '' }, { text: '$ git commit -m "fix: wait for the socket to close before reconnecting"', color: '#82aaff' }, { text: '[agentbox/flaky-reconnect 4f2a9c1] fix: wait for the socket to close before reconnecting' }, { text: ' 2 files changed, 31 insertions(+), 9 deletions(-)' });
  return (
    <Machine clock={done ? '14:44' : '14:32'}>
      <Terminal title="lint@agent-26: ~/agentbox" lines={lines} />
    </Machine>
  );
}

const guide = `# Set up the phone app

1. Open **Settings › Phone** on your computer.
2. Scan the QR code with the AgentBox app.
3. Approve the pairing on both screens.

The phone reaches your computer through the hub,
so it works away from home too.

## When it doesn't connect

- Check that the hub shows **Online**.
- Sign out and in again on the phone.`;

function DocsScene({ t }: { t: number }) {
  const k = t % 14;
  const typed = guide.slice(0, Math.floor(ease(k, 0, 11, 120, guide.length)));
  return (
    <Machine clock="14:32">
      <div style={{ position: 'absolute', left: 40, top: 52, width: 1200, height: 724, borderRadius: 10, overflow: 'hidden', background: '#1e1e1e', boxShadow: '0 30px 80px -20px rgba(0,0,0,.7)', display: 'flex', flexDirection: 'column' }}>
        <div style={{ height: 34, background: '#2d2d2d', color: '#ccc', fontSize: 13, display: 'flex', alignItems: 'center', padding: '0 14px', gap: 16 }}>
          <span style={{ color: '#fff', borderBottom: '2px solid #569cd6', padding: '8px 0' }}>phone-setup.md</span>
          <span style={{ opacity: 0.6 }}>README.md</span>
          <span style={{ marginLeft: 'auto', opacity: 0.6 }}>docs/phone-setup.md</span>
        </div>
        <div style={{ flex: 1, display: 'flex' }}>
          <div style={{ width: 600, padding: '16px 20px', fontFamily: mono, fontSize: 15, lineHeight: '23px', color: '#d4d4d4', whiteSpace: 'pre-wrap', borderRight: '1px solid #333' }}>
            {typed.split('\n').map((l, i) => (
              <div key={i} style={{ color: l.startsWith('#') ? '#569cd6' : l.startsWith('-') || /^\d/.test(l) ? '#ce9178' : undefined }}>
                {l || ' '}
              </div>
            ))}
            <span style={{ display: 'inline-block', width: 2, height: 18, background: '#aeafad' }} />
          </div>
          <div style={{ flex: 1, padding: '22px 30px', background: '#fff', color: '#1f2328', fontSize: 15, lineHeight: '24px' }}>
            {typed.split('\n').map((l, i) =>
              l.startsWith('# ') ? (
                <div key={i} style={{ fontSize: 28, fontWeight: 700, margin: '4px 0 12px', borderBottom: '1px solid #d0d7de', paddingBottom: 8 }}>
                  {l.slice(2)}
                </div>
              ) : l.startsWith('## ') ? (
                <div key={i} style={{ fontSize: 20, fontWeight: 700, margin: '14px 0 6px' }}>
                  {l.slice(3)}
                </div>
              ) : (
                <div key={i} style={{ minHeight: 12 }}>
                  {l.replace(/\*\*/g, '')}
                </div>
              ),
            )}
          </div>
        </div>
      </div>
    </Machine>
  );
}

// The fixture's agent-12: AgentBox's own sidebar getting a New project button.
function NewProjectScene({ t }: { t: number }) {
  const k = t % 12;
  const cx = ease(k, 0.4, 2, 700, 150) + ease(k, 4.4, 5.6, 0, 520);
  const cy = ease(k, 0.4, 2, 500, 116) + ease(k, 4.4, 5.6, 0, 360);
  const open = k > 2.4 && k < 6.2;
  const typed = '~/src/reminders'.slice(0, Math.max(0, Math.floor((k - 2.8) * 9)));
  const added = k >= 6.2;
  const row = (name: string, on?: boolean, fresh?: boolean) => (
    <div key={name} style={{ padding: '9px 12px', borderRadius: 8, fontSize: 15, color: on ? '#fff' : '#b9bccb', background: on ? '#2b2940' : fresh ? 'rgba(99,102,241,.25)' : undefined, display: 'flex', gap: 10, alignItems: 'center' }}>
      <span style={{ width: 18, height: 18, borderRadius: 5, background: on ? '#6d5dfc' : '#3a3850' }} />
      {name}
    </div>
  );
  return (
    <Machine clock="14:28">
      <Browser url="localhost:5199/?app=project&v2=1" tab="AgentBox preview">
        <div style={{ position: 'absolute', inset: 0, display: 'flex', background: '#141319', color: '#e7e7ee' }}>
          <div style={{ width: 270, background: '#1b1a24', padding: 16, display: 'flex', flexDirection: 'column', gap: 6 }}>
            <div style={{ height: 38, borderRadius: 9, background: k > 2 && k < 2.5 ? '#4f46e5' : '#6366f1', color: '#fff', fontWeight: 600, fontSize: 15, display: 'flex', alignItems: 'center', justifyContent: 'center', marginBottom: 10 }}>+ New project</div>
            {row('agentbox', true)}
            {row('storefront')}
            {row('quill')}
            {added && row('reminders', false, true)}
          </div>
          <div style={{ flex: 1, padding: 40, color: '#8d8ba0', fontSize: 16 }}>Lead · agentbox</div>
          {open && (
            <div style={{ position: 'absolute', inset: 0, background: 'rgba(0,0,0,.5)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <div style={{ width: 520, borderRadius: 16, background: '#1f1e2a', border: '1px solid #34324a', padding: 26 }}>
                <div style={{ fontSize: 20, fontWeight: 700 }}>Add a project</div>
                <div style={{ color: '#9b99ad', marginTop: 6, fontSize: 14 }}>A folder with a git repository in it.</div>
                <div style={{ marginTop: 18, height: 40, borderRadius: 9, border: '1px solid #6366f1', background: '#16151f', fontFamily: mono, fontSize: 15, display: 'flex', alignItems: 'center', padding: '0 12px' }}>{typed}</div>
                <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10, marginTop: 22 }}>
                  <span style={{ padding: '9px 16px', borderRadius: 8, color: '#b9bccb' }}>Cancel</span>
                  <span style={{ padding: '9px 16px', borderRadius: 8, background: '#6366f1', color: '#fff', fontWeight: 600 }}>Add project</span>
                </div>
              </div>
            </div>
          )}
        </div>
      </Browser>
      <Cursor x={cx} y={cy} />
    </Machine>
  );
}

function CiScene() {
  const jobs: [string, string][] = [
    ['changes', '6s'],
    ['Go tests and coverage', '4m 12s'],
    ['Go vet, lint, tidy', '1m 48s'],
    ['Desktop', '2m 31s'],
    ['macOS', '7m 05s'],
    ['Windows', '5m 40s'],
  ];
  return (
    <Machine clock="13:52">
      <Browser url="github.com/leciric/agentbox/actions/runs/11873" tab="Release · leciric/agentbox">
        <div style={{ position: 'absolute', inset: 0, background: '#0d1117', color: '#e6edf3', padding: '26px 34px' }}>
          <div style={{ fontSize: 24, fontWeight: 600 }}>
            <span style={{ color: '#3fb950' }}>✓</span> release: build every platform once per tag
          </div>
          <div style={{ color: '#8d96a0', marginTop: 6, fontSize: 14 }}>Re-run #3 · agentbox/release-ci-failures · succeeded 2 minutes ago in 8m 14s</div>
          <div style={{ marginTop: 22, border: '1px solid #30363d', borderRadius: 8 }}>
            {jobs.map(([n, d], i) => (
              <div key={n} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '14px 18px', borderTop: i ? '1px solid #21262d' : undefined, fontSize: 15 }}>
                <span style={{ color: '#3fb950' }}>✓</span>
                {n}
                <span style={{ marginLeft: 'auto', color: '#8d96a0' }}>{d}</span>
              </div>
            ))}
          </div>
        </div>
      </Browser>
    </Machine>
  );
}

function StartupScene() {
  const lines = [
    { text: '$ hyperfine --warmup 2 "agentbox daemon --exit-when-ready"', color: '#82aaff' },
    { text: 'Benchmark 1: agentbox daemon --exit-when-ready' },
    { text: '  Time (mean ± σ):     412.6 ms ±  18.3 ms', color: '#addb67' },
    { text: '  Range (min … max):   389.1 ms … 447.0 ms    10 runs' },
    { text: '' },
    { text: '$ git stash && hyperfine "agentbox daemon --exit-when-ready"   # main', color: '#82aaff' },
    { text: '  Time (mean ± σ):      2.912 s ±  0.104 s', color: '#f78c6c' },
    { text: '' },
    { text: '# 7.1× faster: projects open lazily, the image check moved off the hot path', color: '#637777' },
  ];
  return (
    <Machine clock="12:10">
      <Terminal title="lint@agent-98: ~/agentbox" lines={lines} />
    </Machine>
  );
}
