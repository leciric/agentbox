// Mockups: using an agent's browser from the phone's web app
// (web/PhoneApp.tsx), for when an agent needs the user to log in somewhere or
// click something on its display. On the computer that's the Desktop tab, a
// noVNC view of the agent's display (GET /v1/agents/{p}/{a}/browser/view).
// Three static models to choose from, each a storyboard of phone frames:
//
//   ?phone=browser&option=a   Take over: the display full screen, touched
//                             directly, a key row over the phone's keyboard
//   ?phone=browser&option=b   Trackpad: a magnified view that follows a
//                             cursor the thumb drives, and a compose line that
//                             is a real password field
//   ?phone=browser&option=c   Guided: the agent points at what it needs, the
//                             phone opens on just that, and it hands back by
//                             answering the agent's question
//   &screen=N                 one frame alone, filling the page: what
//                             ?phone=frame&open=browser&option=a&screen=N
//                             shows in the 390×844 frame
//   &theme=light              the light appearance
//
// Each storyboard shows getting there from the agent's chat, driving a
// 1280×800 display on a 390×844 screen (tap, pan and zoom, scrolling, the
// keyboard, a password, Enter/Tab/Esc/paste), giving control back, and the two
// refusals: this phone lacks the new "desktop" scope (off by default, like
// merge), and the page came over the plain-http LAN link (passwords would cross
// the Wi-Fi in clear: only the https tunnel or the tailnet). Dashed pink marks
// are annotations, not UI. Mock only: nothing here ships or is translated.
import {
  ArrowLeft,
  ArrowRightToLine,
  Bell,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  ClipboardPaste,
  CornerDownLeft,
  Delete,
  Eye,
  Hand,
  Keyboard,
  KeyRound,
  Lock,
  Maximize2,
  MessageCircleQuestionMark,
  Minimize2,
  Monitor,
  MousePointer2,
  Send,
  Settings,
  ShieldAlert,
  ShieldCheck,
  Smartphone,
  Undo2,
  Wifi,
  X,
} from 'lucide-react';
import type { CSSProperties, ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import { ToolMark } from '../components/state';
import { cn } from '../lib/utils';

// ——— The agent's display: Chromium full screen on 1280×800, a sign-in page ———

const DW = 1280;
const DH = 800;
// Where things are on the display, in its own pixels, so marks line up.
const F = {
  email: { x: 460, y: 300, w: 360, h: 40 },
  password: { x: 460, y: 378, w: 360, h: 40 },
  button: { x: 460, y: 470, w: 360, h: 44 },
  form: { x: 430, y: 168, w: 420, h: 470 },
  cookie: { x: 0, y: 736, w: 1280, h: 64 },
};
const center = (r: { x: number; y: number; w: number; h: number }): [number, number] => [r.x + r.w / 2, r.y + r.h / 2];

interface Shown {
  email?: string;
  password?: number;
  focus?: 'email' | 'password';
  cursor?: [number, number];
  done?: boolean;
  cookies?: boolean;
}

// Desktop draws the page the agent left open: a made-up cloud's sign-in, to
// authorize its CLI. It's a picture of someone else's page, so its colours are
// the page's own, not the app's tokens.
function Desktop({ s }: { s: Shown }) {
  const field = (which: 'email' | 'password', r: typeof F.email, label: string) => (
    <div style={{ position: 'absolute', left: r.x, top: r.y - 22 }}>
      <div style={{ font: '600 13px system-ui', color: '#30343b', marginBottom: 6 }}>{label}</div>
      <div
        style={{
          width: r.w,
          height: r.h,
          boxSizing: 'border-box',
          borderRadius: 6,
          background: '#fff',
          border: s.focus === which ? '2px solid #2f6feb' : '1px solid #c9ced6',
          boxShadow: s.focus === which ? '0 0 0 3px rgb(47 111 235 / 0.2)' : undefined,
          padding: '0 12px',
          display: 'flex',
          alignItems: 'center',
          font: '15px system-ui',
          color: '#111',
          letterSpacing: which === 'password' ? 3 : 0,
        }}
      >
        {which === 'email' ? s.email ?? <span style={{ color: '#9aa1ab' }}>you@company.com</span> : '•'.repeat(s.password ?? 0)}
        {s.focus === which && <span style={{ width: 1.5, height: 20, background: '#111', marginLeft: 1 }} />}
      </div>
    </div>
  );
  return (
    <div style={{ width: DW, height: DH, position: 'relative', background: '#f4f5f7', overflow: 'hidden', font: '14px system-ui' }}>
      {/* Chromium's tabs and toolbar */}
      <div style={{ height: 34, background: '#dfe1e5', display: 'flex', alignItems: 'end', paddingLeft: 8 }}>
        <div style={{ height: 28, width: 240, background: '#fff', borderRadius: '8px 8px 0 0', display: 'flex', alignItems: 'center', gap: 8, padding: '0 12px', font: '12px system-ui', color: '#333' }}>
          <span style={{ width: 14, height: 14, borderRadius: 4, background: '#0f766e' }} />
          {s.done ? 'Device authorized · Northwind' : 'Sign in · Northwind Cloud'}
        </div>
      </div>
      <div style={{ height: 40, background: '#fff', borderBottom: '1px solid #e3e5e8', display: 'flex', alignItems: 'center', gap: 10, padding: '0 12px' }}>
        <span style={{ color: '#888' }}>←&nbsp;&nbsp;→&nbsp;&nbsp;↻</span>
        <div style={{ flex: 1, height: 28, borderRadius: 14, background: '#f1f3f4', display: 'flex', alignItems: 'center', padding: '0 14px', font: '13px system-ui', color: '#333' }}>
          🔒&nbsp;login.northwind.example/device?code=WDJB-MJHT
        </div>
      </div>
      {/* The page */}
      <div style={{ height: 56, display: 'flex', alignItems: 'center', gap: 10, padding: '0 32px', background: '#fff', borderBottom: '1px solid #e8eaed' }}>
        <span style={{ width: 22, height: 22, borderRadius: 6, background: '#0f766e' }} />
        <b style={{ font: '700 16px system-ui', color: '#0b3b37' }}>Northwind Cloud</b>
        <span style={{ marginLeft: 'auto', color: '#5f6670', font: '13px system-ui' }}>Docs&nbsp;&nbsp;&nbsp;Pricing&nbsp;&nbsp;&nbsp;Status</span>
      </div>
      <div style={{ position: 'absolute', left: F.form.x, top: F.form.y, width: F.form.w, height: F.form.h, background: '#fff', borderRadius: 10, border: '1px solid #e1e4e8', boxShadow: '0 1px 3px rgb(0 0 0 / 0.06)' }} />
      {s.done ? (
        <div style={{ position: 'absolute', left: F.form.x, top: F.form.y + 120, width: F.form.w, textAlign: 'center' }}>
          <div style={{ width: 56, height: 56, borderRadius: 28, background: '#dcfce7', color: '#15803d', margin: '0 auto', display: 'grid', placeItems: 'center', font: '700 28px system-ui' }}>✓</div>
          <div style={{ font: '700 22px system-ui', color: '#111', marginTop: 18 }}>Device authorized</div>
          <div style={{ color: '#5f6670', marginTop: 8, lineHeight: 1.5 }}>
            The Northwind CLI is signed in as
            <br />
            sam@company.com. You can close this tab.
          </div>
        </div>
      ) : (
        <>
          <div style={{ position: 'absolute', left: F.email.x, top: F.form.y + 30 }}>
            <div style={{ font: '700 22px system-ui', color: '#111' }}>Sign in</div>
            <div style={{ color: '#5f6670', marginTop: 6 }}>
              to authorize the CLI with code <b style={{ color: '#111' }}>WDJB-MJHT</b>
            </div>
          </div>
          {field('email', F.email, 'Email')}
          {field('password', F.password, 'Password')}
          <div style={{ position: 'absolute', left: F.email.x, top: 432, display: 'flex', alignItems: 'center', gap: 8, color: '#30343b', font: '13px system-ui' }}>
            <span style={{ width: 14, height: 14, border: '1px solid #9aa1ab', borderRadius: 3, background: '#fff' }} /> Keep me signed in
          </div>
          <div style={{ position: 'absolute', left: F.button.x, top: F.button.y, width: F.button.w, height: F.button.h, borderRadius: 6, background: '#0f766e', color: '#fff', display: 'grid', placeItems: 'center', font: '600 15px system-ui' }}>
            Sign in and authorize
          </div>
          <div style={{ position: 'absolute', left: F.email.x, top: 534, width: F.email.w, textAlign: 'center', color: '#2f6feb', font: '13px system-ui' }}>Forgot password?</div>
          <div style={{ position: 'absolute', left: F.email.x, top: 572, width: F.email.w, height: 40, borderRadius: 6, border: '1px solid #c9ced6', display: 'grid', placeItems: 'center', color: '#30343b', font: '600 14px system-ui' }}>
            Continue with SSO
          </div>
        </>
      )}
      <div style={{ position: 'absolute', left: 32, top: 690, color: '#9aa1ab', font: '12px system-ui' }}>© Northwind · Terms · Privacy</div>
      {s.cookies !== false && (
        <div style={{ position: 'absolute', left: F.cookie.x, top: F.cookie.y, width: F.cookie.w, height: F.cookie.h, background: '#1f2328', color: '#e6e8eb', display: 'flex', alignItems: 'center', gap: 16, padding: '0 32px', boxSizing: 'border-box', font: '13px system-ui' }}>
          We use cookies to keep you signed in.
          <span style={{ marginLeft: 'auto', padding: '8px 16px', borderRadius: 6, background: '#fff', color: '#111', fontWeight: 600 }}>Accept</span>
          <span style={{ padding: '8px 16px', borderRadius: 6, border: '1px solid #555' }}>Manage</span>
        </div>
      )}
      {s.cursor && (
        <svg style={{ position: 'absolute', left: s.cursor[0], top: s.cursor[1] }} width="20" height="26" viewBox="0 0 20 26" aria-hidden>
          <path d="M1 1 L1 21 L6 16 L10 25 L13 23.5 L9 15 L16 15 Z" fill="#fff" stroke="#000" strokeWidth="1.4" strokeLinejoin="round" />
        </svg>
      )}
    </div>
  );
}

// ——— Annotations: what a finger does, drawn over a view in its own pixels ———

interface Mark {
  at: [number, number];
  kind: 'tap' | 'pinch' | 'drag' | 'two' | 'ring';
  label?: string;
  side?: 'left' | 'right' | 'below' | 'above';
  ring?: { w: number; h: number };
}

const INK = '#f472b6';

function Marks({ marks, map }: { marks: Mark[]; map: (p: [number, number]) => [number, number] }) {
  return (
    <>
      {marks.map((m, i) => {
        const [x, y] = m.kind === 'ring' ? m.at : map(m.at);
        const label = m.label && (
          <span
            className="absolute whitespace-nowrap rounded-md px-1.5 py-0.5 text-micro font-semibold"
            style={{
              background: INK,
              color: '#1a0410',
              ...(m.side === 'left' ? { right: 26, top: -9 } : m.side === 'below' ? { top: 24, left: -40 } : m.side === 'above' ? { bottom: 24, left: -40 } : { left: 26, top: -9 }),
            }}
          >
            {m.label}
          </span>
        );
        if (m.kind === 'ring')
          return (
            <div key={i} className="pointer-events-none absolute z-40 rounded-lg" style={{ left: x, top: y, width: m.ring!.w, height: m.ring!.h, border: `2px dashed ${INK}` }}>
              {label}
            </div>
          );
        return (
          <div key={i} className="pointer-events-none absolute z-40" style={{ left: x, top: y }}>
            {m.kind === 'tap' && <div className="absolute -left-4 -top-4 size-8 rounded-full" style={{ border: `2px dashed ${INK}`, background: 'rgb(244 114 182 / 0.25)' }} />}
            {m.kind === 'pinch' && (
              <>
                <div className="absolute size-6 rounded-full" style={{ left: -34, top: 10, border: `2px dashed ${INK}` }} />
                <div className="absolute size-6 rounded-full" style={{ left: 10, top: -34, border: `2px dashed ${INK}` }} />
                <div className="absolute" style={{ left: -22, top: -22, width: 44, height: 44, borderTop: `2px dashed ${INK}`, transform: 'rotate(-45deg) translateY(22px)' }} />
              </>
            )}
            {m.kind === 'drag' && (
              <>
                <div className="absolute -left-3 -top-3 size-6 rounded-full" style={{ border: `2px dashed ${INK}` }} />
                <div className="absolute left-3 top-0 h-0 w-12" style={{ borderTop: `2px dashed ${INK}` }} />
                <ChevronRight className="absolute size-4" style={{ left: 52, top: -8, color: INK }} />
              </>
            )}
            {m.kind === 'two' && (
              <>
                <div className="absolute size-6 rounded-full" style={{ left: -16, top: -3, border: `2px dashed ${INK}` }} />
                <div className="absolute size-6 rounded-full" style={{ left: 12, top: -3, border: `2px dashed ${INK}` }} />
                <div className="absolute h-12 w-0" style={{ left: 8, top: 24, borderLeft: `2px dashed ${INK}` }} />
                <ChevronDown className="absolute size-4" style={{ left: 0, top: 66, color: INK }} />
              </>
            )}
            {label}
          </div>
        );
      })}
    </>
  );
}

// Remote is a w×h window onto the display at scale s, centred on (cx, cy);
// marks are given in the display's pixels and drawn at the view's.
function Remote({ w, h, s, cx, cy, shown, marks = [], className, children }: { w: number | string; h: number; s: number; cx: number; cy: number; shown: Shown; marks?: Mark[]; className?: string; children?: ReactNode }) {
  const vw = typeof w === 'number' ? w : 390;
  const left = Math.min(0, Math.max(vw - DW * s, vw / 2 - cx * s));
  const top = DH * s <= h ? (h - DH * s) / 2 : Math.min(0, Math.max(h - DH * s, h / 2 - cy * s));
  return (
    <div className={cn('relative shrink-0 overflow-hidden bg-black', className)} style={{ width: w, height: h }}>
      <div className="absolute" style={{ left, top, transform: `scale(${s})`, transformOrigin: '0 0' }}>
        <Desktop s={shown} />
      </div>
      <Marks marks={marks} map={([x, y]) => [left + x * s, top + y * s]} />
      {children}
    </div>
  );
}

// ——— The phone's own pieces, drawn after web/PhoneApp.tsx ———

function StatusBar({ dark }: { dark?: boolean }) {
  return (
    <div className={cn('flex h-7 shrink-0 items-center justify-between px-6 text-caption font-semibold', dark ? 'bg-black text-white' : 'text-title')}>
      <span>9:41</span>
      <span className="flex items-center gap-1">
        <Wifi className="size-3.5" />
        <span className="h-2.5 w-5 rounded-sm border border-current" />
      </span>
    </div>
  );
}

function Header({ title, subtitle, action, status }: { title: string; subtitle?: string; action?: ReactNode; status?: ReactNode }) {
  return (
    <header className="flex shrink-0 items-center gap-1 border-b border-line bg-overlay/90 px-1.5 py-1">
      <span className="flex size-11 shrink-0 items-center justify-center text-secondary">
        <ArrowLeft className="size-5" />
      </span>
      <div className="min-w-0 flex-1 px-1">
        <p className="flex items-center gap-1 text-heading font-semibold text-title">
          <span className="truncate">{title}</span>
          <ChevronDown className="size-4 shrink-0 text-subtle" />
        </p>
        {status ?? <p className="truncate text-caption text-subtle">{subtitle}</p>}
      </div>
      {action}
    </header>
  );
}

function Waiting({ text }: { text: string }) {
  return (
    <p className="flex items-center gap-1.5 truncate text-caption text-amber-300">
      <span className="size-2 shrink-0 rounded-full bg-amber-400" />
      {text}
    </p>
  );
}

function AgentSays({ children }: { children: ReactNode }) {
  return (
    <div className="flex gap-2.5">
      <ToolMark ai="claude" className="mt-0.5 size-6" />
      <div className="min-w-0 flex-1 text-body leading-relaxed text-primary">{children}</div>
    </div>
  );
}

function UserSays({ children }: { children: ReactNode }) {
  return <div className="ml-auto max-w-[80%] rounded-2xl rounded-br-md bg-surface-raised px-3.5 py-2 text-body text-primary">{children}</div>;
}

function Composer() {
  return (
    <div className="shrink-0 border-t border-line bg-composer px-3 py-2.5">
      <div className="flex min-h-11 items-center rounded-full border border-line bg-surface px-4 text-body text-faint">Message agent-12…</div>
    </div>
  );
}

function ChatBody({ children }: { children: ReactNode }) {
  return <div className="flex min-h-0 flex-1 flex-col justify-end gap-4 overflow-hidden px-4 py-4">{children}</div>;
}

// The agent's message that asks, the same words in all three.
function TheAsk() {
  return (
    <AgentSays>
      To deploy the preview I need the Northwind CLI signed in, and it wants a person to log in. I've opened the sign-in page in my browser (code{' '}
      <code className="rounded bg-surface-raised px-1 text-ui">WDJB-MJHT</code>): can you log in with your work account?
    </AgentSays>
  );
}

// Keys is the row of what a phone keyboard lacks.
function Keys({ keys, className }: { keys: { icon?: ReactNode; label: string; on?: boolean }[]; className?: string }) {
  return (
    <div className={cn('flex shrink-0 items-center gap-1.5 overflow-hidden border-t border-line bg-overlay px-2 py-1.5', className)}>
      {keys.map((k) => (
        <span
          key={k.label}
          className={cn('flex h-9 shrink-0 items-center gap-1 rounded-lg px-2.5 text-ui font-medium', k.on ? 'bg-brand-500 text-on-brand' : 'bg-surface-raised text-secondary')}
        >
          {k.icon}
          {k.label}
        </span>
      ))}
    </div>
  );
}

const KEYS = {
  esc: { label: 'Esc' },
  tab: { icon: <ArrowRightToLine className="size-3.5" />, label: 'Tab' },
  enter: { icon: <CornerDownLeft className="size-3.5" />, label: 'Enter' },
  paste: { icon: <ClipboardPaste className="size-3.5" />, label: 'Paste' },
  ctrl: { label: 'Ctrl' },
  arrows: { label: '← ↑ ↓ →' },
};

// PhoneKeyboard is the phone's own keyboard, drawn: the system's, not ours.
// suggest is its bar above the keys: a password manager offers a login there
// only when the focused field is a real password input.
function PhoneKeyboard({ suggest }: { suggest?: ReactNode }) {
  const row = (keys: string, pad = 0) => (
    <div className="flex justify-center gap-[5px]" style={{ paddingLeft: pad, paddingRight: pad }}>
      {keys.split('').map((k) => (
        <span key={k} className="flex h-[38px] flex-1 items-center justify-center rounded-[5px] bg-[#6b6b70] text-[17px] text-white shadow-[0_1px_0_#000]">
          {k}
        </span>
      ))}
    </div>
  );
  return (
    <div className="shrink-0 bg-[#2b2b2e] px-[3px] pb-6 pt-1.5">
      <div className="mb-1.5 flex h-9 items-center justify-center gap-2 border-b border-white/10 text-[14px] text-white/90">{suggest ?? <span className="text-white/40">“the” · “I” · “to”</span>}</div>
      <div className="grid gap-[10px]">
        {row('qwertyuiop')}
        {row('asdfghjkl', 18)}
        <div className="flex gap-[5px]">
          <span className="flex h-[38px] w-11 items-center justify-center rounded-[5px] bg-[#46464a] text-white">⇧</span>
          <div className="flex flex-1 gap-[5px] px-2">
            {'zxcvbnm'.split('').map((k) => (
              <span key={k} className="flex h-[38px] flex-1 items-center justify-center rounded-[5px] bg-[#6b6b70] text-[17px] text-white">
                {k}
              </span>
            ))}
          </div>
          <span className="flex h-[38px] w-11 items-center justify-center rounded-[5px] bg-[#46464a] text-white">
            <Delete className="size-5" />
          </span>
        </div>
        <div className="flex gap-[5px]">
          <span className="flex h-[38px] w-20 items-center justify-center rounded-[5px] bg-[#46464a] text-[15px] text-white">123</span>
          <span className="flex h-[38px] flex-1 items-center justify-center rounded-[5px] bg-[#6b6b70] text-[15px] text-white">space</span>
          <span className="flex h-[38px] w-24 items-center justify-center rounded-[5px] bg-[#46464a] text-[15px] text-white">return</span>
        </div>
      </div>
    </div>
  );
}

const SavedLogin = () => (
  <span className="flex items-center gap-1.5 rounded-md bg-white/10 px-3 py-1">
    <KeyRound className="size-4" /> sam@company.com · northwind.example
  </span>
);

function Toast({ children, tone = 'ok', className }: { children: ReactNode; tone?: 'ok' | 'info'; className?: string }) {
  return (
    <div className={cn('absolute inset-x-6 z-10 flex items-center gap-2 rounded-xl border border-line-strong bg-modal px-3 py-2 text-ui text-primary shadow-pop', className)}>
      {tone === 'ok' ? <Check className="size-4 shrink-0 text-emerald-300" /> : <ClipboardPaste className="size-4 shrink-0 text-brand-300" />}
      {children}
    </div>
  );
}

function Sheet({ children }: { children: ReactNode }) {
  return (
    <>
      <div className="absolute inset-0 z-20 bg-scrim" />
      <div className="absolute inset-x-0 bottom-0 z-30 grid gap-3 rounded-t-3xl border-t border-line-strong bg-modal px-5 pb-8 pt-2 shadow-modal">
        <div className="mx-auto h-1 w-10 rounded-full bg-line-strong" />
        {children}
      </div>
    </>
  );
}

function Btn({ children, kind = 'primary', className }: { children: ReactNode; kind?: 'primary' | 'quiet' | 'danger'; className?: string }) {
  return (
    <span
      className={cn(
        'flex min-h-11 items-center justify-center gap-2 rounded-full px-4 text-body font-semibold',
        kind === 'primary' && 'bg-brand-500 text-on-brand',
        kind === 'quiet' && 'border border-line-strong text-secondary',
        kind === 'danger' && 'bg-rose-500/15 text-rose-200',
        className,
      )}
    >
      {children}
    </span>
  );
}

// The two refusals, worded once and placed differently by each option.
const REFUSE = {
  scopeTitle: 'This phone may not use agents’ desktops',
  scopeText: 'Seeing and driving an agent’s browser is off for Pixel 9, as merging is until you allow it.',
  scopeFix: (
    <>
      On your computer: <b className="text-primary">Settings → Phone → Pixel 9</b>, turn on <b className="text-primary">Use agents’ desktops</b>.
    </>
  ),
  httpTitle: 'Needs the https link',
  httpText: (
    <>
      You opened AgentBox at <code className="text-ui text-primary">http://192.168.1.20:7780</code>. On that link what you type crosses the Wi-Fi unencrypted,
      passwords included, so the desktop stays shut.
    </>
  ),
  httpFix: (
    <>
      Open the https one (the tunnel or your tailnet): it’s in <b className="text-primary">Settings → Phone → Reach from anywhere</b> on your computer, and
      this phone stays paired.
    </>
  ),
  httpLink: 'https://3fa9c0e1….agentbox.dev',
};

// ——— Option A: take over — the display full screen, touched directly ———

function AChat({ refused }: { refused?: 'scope' | 'http' }) {
  return (
    <div className="relative flex h-full flex-col bg-chat">
      <StatusBar />
      <Header
        title="Deploy the preview"
        status={<Waiting text="needs you · in its browser" />}
        action={
          <span className="relative flex size-11 items-center justify-center text-secondary">
            <Monitor className="size-5" />
            <span className="absolute right-2.5 top-2.5 size-2 rounded-full bg-amber-400" />
          </span>
        }
      />
      <ChatBody>
        <UserSays>deploy the preview of the checkout branch</UserSays>
        <TheAsk />
        <div className="overflow-hidden rounded-2xl border border-amber-500/40 bg-surface">
          <Remote w={356} h={150} s={0.36} cx={640} cy={400} shown={{ cookies: true }} className="w-full" />
          <div className="grid gap-2 p-3">
            <p className="flex items-center gap-2 text-ui font-medium text-amber-200">
              <Hand className="size-4" /> Its browser needs you
            </p>
            {refused ? (
              <Btn kind="quiet" className="opacity-60">
                <Lock className="size-4" /> Take over the browser
              </Btn>
            ) : (
              <div className="relative">
                <Btn>
                  <Monitor className="size-4" /> Take over the browser
                </Btn>
                <Marks marks={[{ at: [250, 22], kind: 'tap', label: 'one tap from the ask', side: 'above' }]} map={(p) => p} />
              </div>
            )}
          </div>
        </div>
      </ChatBody>
      <Composer />
      {refused === 'scope' && (
        <Sheet>
          <ShieldAlert className="size-7 text-amber-300" />
          <h2 className="text-heading font-semibold text-title">{REFUSE.scopeTitle}</h2>
          <p className="text-body text-secondary">{REFUSE.scopeText}</p>
          <p className="flex gap-2 rounded-xl bg-surface-raised p-3 text-ui text-secondary">
            <Settings className="mt-0.5 size-4 shrink-0" />
            <span>{REFUSE.scopeFix}</span>
          </p>
          <Btn kind="quiet">Do it on the computer instead</Btn>
          <p className="text-center text-caption text-subtle">Answering in the chat still works from here.</p>
        </Sheet>
      )}
      {refused === 'http' && (
        <Sheet>
          <Lock className="size-7 text-amber-300" />
          <h2 className="text-heading font-semibold text-title">{REFUSE.httpTitle}</h2>
          <p className="text-body text-secondary">{REFUSE.httpText}</p>
          <Btn>
            <ShieldCheck className="size-4" /> Open {REFUSE.httpLink}
          </Btn>
          <p className="text-caption text-subtle">{REFUSE.httpFix}</p>
        </Sheet>
      )}
    </div>
  );
}

// The viewer's top bar: who's driving, and the way back.
function ABar({ zoom }: { zoom: string }) {
  return (
    <div className="flex shrink-0 items-center gap-2 bg-black/90 px-2 py-1.5 text-white">
      <span className="flex size-10 items-center justify-center">
        <ChevronLeft className="size-5" />
      </span>
      <div className="min-w-0 flex-1">
        <p className="truncate text-ui font-semibold">agent-12’s browser</p>
        <p className="flex items-center gap-1 text-micro text-emerald-300">
          <span className="size-1.5 rounded-full bg-emerald-400" /> You’re driving · the agent waits
        </p>
      </div>
      <span className="rounded-full bg-white/10 px-2 py-1 text-micro tabular-nums">{zoom}</span>
      <span className="flex h-9 items-center gap-1 rounded-full bg-white px-3 text-ui font-semibold text-black">
        <Undo2 className="size-4" /> Give back
      </span>
    </div>
  );
}

function AFit() {
  return (
    <div className="relative flex h-full flex-col bg-black">
      <StatusBar dark />
      <ABar zoom="30%" />
      <div className="relative flex min-h-0 flex-1 items-center">
        <Remote
          w={390}
          h={540}
          s={390 / DW}
          cx={640}
          cy={400}
          shown={{ cursor: [700, 250] }}
          marks={[
            { at: [640, 420], kind: 'pinch', label: 'pinch: zoom', side: 'right' },
            { at: center(F.form), kind: 'tap', label: 'double-tap: zoom to here', side: 'below' },
          ]}
        />
      </div>
      <div className="grid shrink-0 grid-cols-2 gap-x-3 gap-y-1.5 border-t border-white/10 bg-black px-4 pb-7 pt-3 text-micro text-white/70">
        <span>Tap · click there</span>
        <span>Drag · pan the view</span>
        <span>Long-press · right-click</span>
        <span>Two fingers · scroll the page</span>
      </div>
      <div className="absolute bottom-24 right-3 flex flex-col gap-2">
        <span className="flex size-11 items-center justify-center rounded-full bg-white/15 text-white">
          <Keyboard className="size-5" />
        </span>
        <span className="flex size-11 items-center justify-center rounded-full bg-white/15 text-white">
          <Maximize2 className="size-5" />
        </span>
      </div>
    </div>
  );
}

function AType({ password }: { password?: boolean }) {
  const s = 1;
  const shown: Shown = password ? { email: 'sam@company.com', password: 9, focus: 'password', cursor: [700, 392] } : { email: 'sam@comp', focus: 'email', cursor: [600, 314] };
  return (
    <div className="relative flex h-full flex-col bg-black">
      <StatusBar dark />
      <ABar zoom="100%" />
      <Remote
        w={390}
        h={340}
        s={s}
        cx={640}
        cy={password ? 395 : 360}
        shown={shown}
        marks={[password ? { at: center(F.password), kind: 'tap', label: 'tap the field: keyboard comes up', side: 'below' } : { at: [700, 316], kind: 'tap', label: 'tap = click', side: 'below' }]}
      >
        <div className="absolute right-2 top-2 h-[44px] w-[70px] overflow-hidden rounded border border-white/40 bg-black/60">
          <div className="absolute border border-brand-300" style={{ left: 22, top: 13, width: 22, height: 15 }} />
        </div>
      </Remote>
      <div className="min-h-0 flex-1 bg-black" />
      {password ? (
        <div className="flex shrink-0 items-center gap-2 bg-emerald-500/15 px-3 py-1.5 text-micro text-emerald-200">
          <Lock className="size-3.5" /> Encrypted (https) · keys go straight to the browser, nothing is kept
        </div>
      ) : null}
      <Keys keys={[KEYS.esc, KEYS.tab, KEYS.enter, KEYS.paste, KEYS.ctrl, KEYS.arrows]} />
      <PhoneKeyboard />
      {password && (
        <Toast tone="info" className="top-[300px]">
          Pasted 18 characters from the clipboard
        </Toast>
      )}
      {password && <Marks marks={[{ at: [222, 545], kind: 'tap', label: 'Paste: from your password manager', side: 'above' }]} map={(p) => p} />}
    </div>
  );
}

function AScroll() {
  return (
    <div className="relative flex h-full flex-col bg-black">
      <StatusBar dark />
      <ABar zoom="100%" />
      <Remote
        w={390}
        h={760}
        s={1}
        cx={640}
        cy={560}
        shown={{ email: 'sam@company.com', password: 18, cursor: [660, 765] }}
        marks={[
          { at: [560, 560], kind: 'two', label: 'two fingers: scroll the page', side: 'right' },
          { at: [470, 650], kind: 'drag', label: 'one finger: pan', side: 'above' },
          { at: [1025, 768], kind: 'tap', label: 'Accept', side: 'left' },
        ]}
      />
      <div className="absolute bottom-8 right-3 flex flex-col gap-2">
        <span className="flex size-11 items-center justify-center rounded-full bg-white/15 text-white">
          <Keyboard className="size-5" />
        </span>
        <span className="flex size-11 items-center justify-center rounded-full bg-white/15 text-white">
          <Minimize2 className="size-5" />
        </span>
      </div>
    </div>
  );
}

function AGiveBack() {
  return (
    <div className="relative flex h-full flex-col bg-black">
      <StatusBar dark />
      <ABar zoom="30%" />
      <div className="flex min-h-0 flex-1 items-start pt-6">
        <Remote w={390} h={244} s={390 / DW} cx={640} cy={400} shown={{ done: true, cookies: false }} />
      </div>
      <Marks marks={[{ at: [338, 56], kind: 'tap', side: 'below', label: 'or ‹ back: it asks the same' }]} map={(p) => p} />
      <Sheet>
        <h2 className="text-heading font-semibold text-title">Give the browser back to agent-12?</h2>
        <p className="text-body text-secondary">It carries on from what’s on the screen. Tell it what you did, if it helps:</p>
        <div className="min-h-11 rounded-xl border border-line bg-surface px-3 py-2.5 text-body text-primary">Signed in as sam@company.com</div>
        <Btn>
          <Undo2 className="size-4" /> Give back and tell it
        </Btn>
        <Btn kind="quiet">Keep driving</Btn>
      </Sheet>
    </div>
  );
}

function AAfter() {
  return (
    <div className="relative flex h-full flex-col bg-chat">
      <StatusBar />
      <Header title="Deploy the preview" status={<p className="flex items-center gap-1.5 text-caption text-brand-300"><span className="size-2 rounded-full bg-brand-400" />working</p>} action={<span className="flex size-11 items-center justify-center text-secondary"><Monitor className="size-5" /></span>} />
      <ChatBody>
        <TheAsk />
        <div className="flex items-center gap-2 self-center rounded-full border border-line px-3 py-1.5 text-caption text-subtle">
          <Monitor className="size-3.5" /> You drove its browser for 1 min 40 s · gave it back
        </div>
        <UserSays>Signed in as sam@company.com</UserSays>
        <AgentSays>Thanks — the CLI is authorized. Deploying the preview now; I’ll post the URL when it’s up.</AgentSays>
      </ChatBody>
      <Composer />
    </div>
  );
}

// ——— Option B: trackpad — a magnified view that follows a cursor ———

function BTabs({ desktop }: { desktop?: boolean }) {
  return (
    <div className="flex shrink-0 gap-1 border-b border-line bg-overlay/90 px-3 pb-2">
      <span className={cn('flex h-8 flex-1 items-center justify-center rounded-lg text-ui font-medium', desktop ? 'text-subtle' : 'bg-surface-raised text-title')}>Chat</span>
      <span className={cn('flex h-8 flex-1 items-center justify-center gap-1.5 rounded-lg text-ui font-medium', desktop ? 'bg-surface-raised text-title' : 'text-subtle')}>
        <Monitor className="size-3.5" /> Desktop
        {!desktop && <span className="size-2 rounded-full bg-amber-400" />}
      </span>
    </div>
  );
}

function BList() {
  return (
    <div className="relative flex h-full flex-col bg-ink">
      <StatusBar />
      <header className="flex items-center gap-2.5 border-b border-line bg-overlay/90 px-4 py-3">
        <h1 className="text-heading font-semibold text-title">Chats</h1>
      </header>
      <div className="grid gap-5 px-3 py-4">
        <section className="grid gap-1">
          <h2 className="px-2 pb-1 text-caption font-medium text-amber-300">Needs you</h2>
          <div className="flex items-center gap-3 rounded-2xl bg-surface px-3 py-3">
            <ToolMark ai="claude" className="size-9" />
            <div className="min-w-0 flex-1">
              <p className="truncate text-body font-medium text-title">Deploy the preview</p>
              <p className="flex items-center gap-1.5 truncate text-caption text-amber-300">
                <Monitor className="size-3.5 shrink-0" /> wants you in its browser · 1 min
              </p>
            </div>
            <ChevronRight className="size-4 text-faint" />
          </div>
        </section>
        <section className="grid gap-1">
          <h2 className="px-2 pb-1 text-caption font-medium text-subtle">agentbox</h2>
          {['Lead', 'Fix the flaky checkout test', 'Translate the settings page'].map((t, i) => (
            <div key={t} className="flex items-center gap-3 rounded-2xl px-3 py-3">
              <ToolMark ai={i === 2 ? 'codex' : 'claude'} className="size-9" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-body text-title">{t}</p>
                <p className="truncate text-caption text-subtle">{i === 1 ? 'working · running the tests' : 'idle'}</p>
              </div>
            </div>
          ))}
        </section>
      </div>
      <div className="absolute inset-x-3 top-9 z-10 flex gap-3 rounded-2xl bg-[#2c2c30]/95 p-3 text-white shadow-modal">
        <Bell className="mt-0.5 size-5 shrink-0" />
        <div className="min-w-0 text-ui">
          <p className="font-semibold">agent-12 wants you in its browser</p>
          <p className="text-white/70">“can you log in with your work account?” Tap to open its Desktop.</p>
        </div>
      </div>
      <Marks marks={[{ at: [200, 70], kind: 'tap', label: 'a notification, or the row', side: 'below' }]} map={(p) => p} />
    </div>
  );
}

function BChat({ refused }: { refused?: 'scope' | 'http' }) {
  if (refused) return <BRefused which={refused} />;
  return (
    <div className="relative flex h-full flex-col bg-chat">
      <StatusBar />
      <Header title="Deploy the preview" status={<Waiting text="needs you · in its browser" />} />
      <BTabs />
      <ChatBody>
        <UserSays>deploy the preview of the checkout branch</UserSays>
        <TheAsk />
        <span className="relative flex items-center gap-2 self-start rounded-full border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-ui font-medium text-amber-200">
          <Monitor className="size-4" /> Open its Desktop
          <ChevronRight className="size-4" />
          <Marks marks={[{ at: [150, 18], kind: 'tap', label: 'or this, under the ask', side: 'right' }]} map={(p) => p} />
        </span>
      </ChatBody>
      <Composer />
      <Marks
        marks={[
          { at: [258, 96], kind: 'tap', label: 'its Desktop tab, as on the computer', side: 'left' },
        ]}
        map={(p) => p}
      />
    </div>
  );
}

// BTop is the Desktop tab's head: the agent's state, and handing back.
function BTop() {
  return (
    <>
      <StatusBar />
      <Header
        title="Deploy the preview"
        status={<p className="flex items-center gap-1.5 text-caption text-emerald-300"><span className="size-2 rounded-full bg-emerald-400" />you have its browser · it waits</p>}
        action={
          <span className="mr-1 flex h-9 items-center gap-1 rounded-full bg-brand-500 px-3 text-ui font-semibold text-on-brand">
            <Undo2 className="size-4" /> Hand back
          </span>
        }
      />
      <BTabs desktop />
    </>
  );
}

// BMini is the whole display, small, with the loupe's rectangle on it.
function BMini({ cx, cy, z }: { cx: number; cy: number; z: number }) {
  const s = 120 / DW;
  const w = (390 / z) * s;
  const h = (300 / z) * s;
  return (
    <div className="relative h-[75px] w-[120px] shrink-0 overflow-hidden rounded-md border border-line-strong">
      <div className="absolute left-0 top-0" style={{ transform: `scale(${s})`, transformOrigin: '0 0' }}>
        <Desktop s={{}} />
      </div>
      <div className="absolute border-2 border-brand-400" style={{ left: cx * s - w / 2, top: cy * s - h / 2, width: w, height: h }} />
    </div>
  );
}

function Trackpad({ marks, children, short }: { marks?: Mark[]; children?: ReactNode; short?: boolean }) {
  return (
    <div className={cn('relative mx-3 mb-3 flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-line bg-surface', short && 'flex-none h-[96px]')}>
      <div className="flex-1" />
      <div className="absolute inset-y-3 right-2 w-6 rounded-full bg-surface-raised">
        <ChevronUp className="mx-auto mt-1 size-4 text-faint" />
        <ChevronDown className="absolute bottom-1 left-1 size-4 text-faint" />
      </div>
      {!short && (
        <div className="grid grid-cols-2 border-t border-line text-center text-ui text-secondary">
          <span className="border-r border-line py-2.5">Click</span>
          <span className="py-2.5">Right-click</span>
        </div>
      )}
      <p className="pointer-events-none absolute left-4 top-3 text-caption text-faint">{short ? 'Trackpad' : 'Trackpad · move the cursor, tap to click'}</p>
      {children}
      {marks && <Marks marks={marks} map={(p) => p} />}
    </div>
  );
}

function BDrive() {
  return (
    <div className="relative flex h-full flex-col bg-ink">
      <BTop />
      <Remote w={390} h={300} s={1} cx={640} cy={330} shown={{ cursor: [628, 318] }} marks={[{ at: [700, 250], kind: 'pinch', label: 'pinch here: 100%', side: 'right' }]} />
      <div className="flex shrink-0 items-center gap-3 px-3 py-2">
        <BMini cx={640} cy={330} z={1} />
        <p className="text-caption text-subtle">The view follows the cursor. Drag the box to look elsewhere.</p>
      </div>
      <Trackpad
        marks={[
          { at: [120, 90], kind: 'drag', label: 'drag: move the cursor', side: 'below' },
          { at: [336, 70], kind: 'two', label: 'edge or two fingers: scroll', side: 'left' },
        ]}
      />
      <span className="absolute bottom-[86px] left-1/2 flex size-12 -translate-x-1/2 items-center justify-center rounded-full bg-surface-raised text-secondary shadow-pop">
        <Keyboard className="size-5" />
      </span>
    </div>
  );
}

// BCompose is the line typing goes through: written on the phone, sent as
// keystrokes on Send. A password goes in a real password input, so the phone's
// password manager can fill it.
function BType({ password }: { password?: boolean }) {
  return (
    <div className="relative flex h-full flex-col bg-ink">
      <BTop />
      <Remote w={390} h={226} s={1} cx={640} cy={password ? 380 : 320} shown={password ? { email: 'sam@company.com', focus: 'password', cursor: [700, 392] } : { focus: 'email', cursor: [700, 314] }} />
      <Trackpad short />
      <div className="mt-auto shrink-0 border-t border-line bg-overlay px-3 pt-2">
        <div className="flex items-center gap-2">
          <div className="flex min-h-11 flex-1 items-center gap-2 rounded-xl border border-brand-400 bg-surface px-3 text-body text-primary">
            {password ? (
              <>
                <Lock className="size-4 shrink-0 text-subtle" />
                <span className="tracking-[3px]">••••••••••••••••••</span>
                <Eye className="ml-auto size-4 text-subtle" />
              </>
            ) : (
              <>
                sam@company.com
                <span className="h-5 w-px bg-primary" />
              </>
            )}
          </div>
          <span className="flex h-11 items-center gap-1 rounded-xl bg-brand-500 px-3 text-ui font-semibold text-on-brand">
            <Send className="size-4" /> Type
          </span>
        </div>
        <p className="py-1 text-micro text-subtle">{password ? 'Password field: never shown, never kept, typed into the focused field.' : 'Sent as keystrokes to wherever the cursor clicked. Type ⇢ then Tab.'}</p>
      </div>
      <Keys keys={[KEYS.tab, KEYS.enter, KEYS.esc, KEYS.paste, { label: '⌫' }, KEYS.arrows]} className="border-t-0" />
      <PhoneKeyboard suggest={password ? <SavedLogin /> : undefined} />
      {password && <Marks marks={[{ at: [14, 592], kind: 'ring', ring: { w: 262, h: 34 }, label: 'the phone’s own password manager offers it', side: 'above' }]} map={(p) => p} />}
    </div>
  );
}

function BBack() {
  return (
    <div className="relative flex h-full flex-col bg-chat">
      <StatusBar />
      <Header title="Deploy the preview" status={<p className="flex items-center gap-1.5 text-caption text-brand-300"><span className="size-2 rounded-full bg-brand-400" />working</p>} />
      <BTabs />
      <ChatBody>
        <TheAsk />
        <div className="grid gap-2 rounded-2xl border border-line bg-surface p-3">
          <p className="flex items-center gap-2 text-ui font-medium text-title">
            <Undo2 className="size-4 text-brand-300" /> You handed the browser back
          </p>
          <p className="text-caption text-subtle">After 1 min 40 s. It was on “Device authorized”. Clicks: 4 · typed: 2 fields, 1 password (not shown).</p>
          <p className="text-caption text-subtle">Left untouched for 5 min, it goes back by itself.</p>
        </div>
        <AgentSays>The CLI is authorized — deploying the preview now.</AgentSays>
      </ChatBody>
      <Composer />
      <Toast className="top-[110px]">Handed back. The agent goes on.</Toast>
    </div>
  );
}

function BRefused({ which }: { which: 'scope' | 'http' }) {
  return (
    <div className="relative flex h-full flex-col bg-ink">
      <StatusBar />
      <Header title="Deploy the preview" status={<Waiting text="needs you · in its browser" />} />
      <BTabs desktop />
      <div className="relative">
        <Remote w={390} h={244} s={390 / DW} cx={640} cy={400} shown={{}} className="opacity-25 blur-[3px]" />
        <span className="absolute inset-0 flex items-center justify-center">
          {which === 'scope' ? <ShieldAlert className="size-10 text-amber-300" /> : <Lock className="size-10 text-amber-300" />}
        </span>
      </div>
      <div className="grid gap-3 px-5 py-5">
        <h2 className="text-heading font-semibold text-title">{which === 'scope' ? REFUSE.scopeTitle : REFUSE.httpTitle}</h2>
        <p className="text-body text-secondary">{which === 'scope' ? REFUSE.scopeText : REFUSE.httpText}</p>
        <ol className="grid gap-2 text-ui text-secondary">
          {which === 'scope' ? (
            <>
              <li className="flex gap-2">
                <span className="text-subtle">1.</span>
                <span>{REFUSE.scopeFix}</span>
              </li>
              <li className="flex gap-2">
                <span className="text-subtle">2.</span>
                <span>Come back here: the tab opens by itself once it’s allowed.</span>
              </li>
            </>
          ) : (
            <>
              <li className="flex gap-2">
                <span className="text-subtle">1.</span>
                <span>{REFUSE.httpFix}</span>
              </li>
              <li className="flex gap-2">
                <span className="text-subtle">2.</span>
                <span>Add that page to your home screen instead of this one.</span>
              </li>
            </>
          )}
        </ol>
        {which === 'http' && (
          <Btn>
            <ShieldCheck className="size-4" /> Open {REFUSE.httpLink}
          </Btn>
        )}
        <Btn kind="quiet">
          <Monitor className="size-4" /> Remind me on the computer
        </Btn>
      </div>
    </div>
  );
}

// ——— Option C: guided — the agent points, the phone opens on just that ———

function CAsk({ refused }: { refused?: 'scope' | 'http' }) {
  return (
    <div className="relative flex h-full flex-col bg-chat">
      <StatusBar />
      <Header title="Deploy the preview" status={<Waiting text="asks you something" />} />
      <ChatBody>
        <UserSays>deploy the preview of the checkout branch</UserSays>
        <TheAsk />
        <div className="overflow-hidden rounded-2xl border border-amber-500/40 bg-surface">
          <div className="flex items-center gap-2 px-3 pt-3 text-ui font-medium text-amber-200">
            <MessageCircleQuestionMark className="size-4" /> Log in to Northwind Cloud
          </div>
          <div className="relative m-3 overflow-hidden rounded-xl">
            <Remote w={334} h={refused ? 130 : 250} s={0.48} cx={640} cy={403} shown={{}} className={cn('w-full', refused && 'opacity-30 blur-[2px]')} />
            {!refused && (
              <div className="absolute rounded-md border-2 border-amber-400" style={{ left: 167 - 0.48 * 214, top: 125 - 0.48 * 239, width: 0.48 * 428, height: 0.48 * 478 }}>
                <span className="absolute -top-px right-2 -translate-y-full rounded-t bg-amber-400 px-1.5 text-micro font-semibold text-black">marked by agent-12</span>
              </div>
            )}
          </div>
          {refused ? (
            <div className="grid gap-2 px-3 pb-3">
              <p className="flex items-start gap-2 text-ui text-amber-200">
                {refused === 'scope' ? <ShieldAlert className="mt-0.5 size-4 shrink-0" /> : <Lock className="mt-0.5 size-4 shrink-0" />}
                <span>
                  <b>{refused === 'scope' ? REFUSE.scopeTitle : REFUSE.httpTitle}.</b>{' '}
                  <span className="text-secondary">{refused === 'scope' ? REFUSE.scopeFix : REFUSE.httpFix}</span>
                </span>
              </p>
              {refused === 'http' && (
                <Btn>
                  <ShieldCheck className="size-4" /> Open the https link
                </Btn>
              )}
              <Btn kind="quiet">I did it on the computer</Btn>
            </div>
          ) : (
            <div className="grid gap-2 px-3 pb-3">
              <Btn>
                <Smartphone className="size-4" /> Do it on this phone
              </Btn>
              <Btn kind="quiet">I did it on the computer</Btn>
            </div>
          )}
        </div>
      </ChatBody>
      <Composer />
    </div>
  );
}

// CGuide is the agent's ask kept in view while the phone drives.
function CGuide({ step }: { step: string }) {
  return (
    <div className="flex shrink-0 items-center gap-2 border-b border-line bg-overlay px-3 py-2">
      <ToolMark ai="claude" className="size-5" />
      <p className="min-w-0 flex-1 truncate text-caption text-secondary">
        <span className="text-title">Log in with your work account.</span> {step}
      </p>
      <span className="flex h-8 items-center gap-1 rounded-full bg-emerald-500/20 px-2.5 text-caption font-semibold text-emerald-200">
        <Check className="size-3.5" /> Done
      </span>
    </div>
  );
}

function CTop() {
  return (
    <div className="flex shrink-0 items-center gap-1 bg-black px-1.5 py-1 text-white">
      <span className="flex size-10 items-center justify-center">
        <X className="size-5" />
      </span>
      <p className="min-w-0 flex-1 truncate text-ui font-semibold">Northwind sign-in · agent-12</p>
      <span className="flex h-8 items-center gap-1 rounded-full bg-white/10 px-2.5 text-caption">
        <Maximize2 className="size-3.5" /> Whole screen
      </span>
    </div>
  );
}

function CFocus() {
  return (
    <div className="relative flex h-full flex-col bg-black">
      <StatusBar dark />
      <CTop />
      <CGuide step="Then I’ll take it from there." />
      <Remote
        w={390}
        h={560}
        s={0.9}
        cx={640}
        cy={410}
        shown={{}}
        marks={[
          { at: center(F.email), kind: 'tap', label: 'tap a field', side: 'below' },
          { at: [560, 600], kind: 'two', label: 'two fingers: scroll', side: 'right' },
        ]}
      >
        <div className="absolute rounded-lg border-2 border-amber-400/80" style={{ left: 195 - 0.9 * 210, top: 280 - 0.9 * 242, width: 0.9 * 420, height: 0.9 * 470 }} />
      </Remote>
      <div className="flex shrink-0 items-center justify-between bg-black px-4 pb-7 pt-3 text-micro text-white/70">
        <span>Opened on what the agent marked</span>
        <span className="flex items-center gap-1">
          <MousePointer2 className="size-3.5" /> pinch · drag · tap
        </span>
      </div>
    </div>
  );
}

// CType: tapping a field brings the keyboard and a mirror of the field above
// it, so the field stays visible under a thumb and the keyboard.
function CType({ password }: { password?: boolean }) {
  return (
    <div className="relative flex h-full flex-col bg-black">
      <StatusBar dark />
      <CTop />
      <CGuide step={password ? 'Password, then Enter.' : 'Email first.'} />
      <Remote w={390} h={200} s={0.9} cx={640} cy={password ? 395 : 320} shown={password ? { email: 'sam@company.com', password: 18, focus: 'password' } : { email: 'sam@company.com', focus: 'email' }} />
      <div className="min-h-0 flex-1" />
      <div className="shrink-0 border-t border-line bg-overlay px-3 pb-1.5 pt-2">
        <p className="pb-1 text-micro text-subtle">{password ? 'Typing into · Password' : 'Typing into · Email'}</p>
        <div className="flex min-h-11 items-center gap-2 rounded-xl border border-brand-400 bg-surface px-3 text-body text-primary">
          {password ? (
            <>
              <Lock className="size-4 text-subtle" />
              <span className="tracking-[3px]">••••••••••••••••••</span>
              <Eye className="ml-auto size-4 text-subtle" />
            </>
          ) : (
            <>
              sam@company.com
              <span className="h-5 w-px bg-primary" />
            </>
          )}
        </div>
      </div>
      <Keys keys={[{ ...KEYS.tab, label: 'Next field' }, { ...KEYS.enter, on: !!password }, KEYS.esc, KEYS.paste, KEYS.arrows]} className="border-t-0" />
      <PhoneKeyboard suggest={password ? <SavedLogin /> : undefined} />
      {password && <Marks marks={[{ at: [128, 568], kind: 'tap', label: 'Enter submits', side: 'above' }]} map={(p) => p} />}
    </div>
  );
}

function CDone() {
  return (
    <div className="relative flex h-full flex-col bg-black">
      <StatusBar dark />
      <CTop />
      <div className="flex min-h-0 flex-1 items-start">
        <Remote w={390} h={480} s={0.75} cx={640} cy={400} shown={{ done: true, cookies: false }} />
      </div>
      <div className="absolute inset-x-0 bottom-0 grid gap-3 rounded-t-3xl border-t border-line-strong bg-modal px-5 pb-8 pt-4 shadow-modal">
        <div className="flex items-start gap-2.5">
          <ToolMark ai="claude" className="mt-0.5 size-6" />
          <p className="text-body text-primary">That looks done — the page says the device is authorized. Shall I take it from here?</p>
        </div>
        <Btn>
          <Undo2 className="size-4" /> Yes, take it back
        </Btn>
        <Btn kind="quiet">Not yet</Btn>
      </div>
    </div>
  );
}

function CAfter() {
  return (
    <div className="relative flex h-full flex-col bg-chat">
      <StatusBar />
      <Header title="Deploy the preview" status={<p className="flex items-center gap-1.5 text-caption text-brand-300"><span className="size-2 rounded-full bg-brand-400" />working</p>} />
      <ChatBody>
        <TheAsk />
        <div className="grid gap-1.5 rounded-2xl border border-line bg-surface p-3">
          <p className="flex items-center gap-2 text-ui font-medium text-subtle">
            <MessageCircleQuestionMark className="size-4" /> Log in to Northwind Cloud
          </p>
          <p className="flex items-center gap-2 text-ui text-emerald-300">
            <Check className="size-4" /> Done on your phone · 1 min 40 s
          </p>
        </div>
        <AgentSays>Authorized. Deploying the preview now.</AgentSays>
      </ChatBody>
      <Composer />
    </div>
  );
}

// ——— On the computer: the new scope, in Settings → Phone ———

function SettingsCard() {
  const scope = (name: string, text: string, on: boolean, fresh?: boolean) => (
    <div className={cn('flex items-start gap-3 py-2.5', fresh && '-mx-3 rounded-lg bg-brand-500/10 px-3')}>
      <div className="min-w-0 flex-1">
        <p className="text-ui font-medium text-title">{name}</p>
        <p className="text-caption text-subtle">{text}</p>
      </div>
      <span className={cn('mt-0.5 flex h-5 w-9 shrink-0 items-center rounded-full p-0.5', on ? 'justify-end bg-brand-500' : 'bg-surface-strong')}>
        <span className="size-4 rounded-full bg-white" />
      </span>
    </div>
  );
  return (
    <div className="w-[390px] self-start rounded-xl border border-line bg-surface p-4">
      <p className="mb-1 flex items-center gap-2 text-caption text-subtle">
        <Settings className="size-3.5" /> On the computer · Settings → Phone
      </p>
      <p className="mb-2 text-heading font-semibold text-title">Pixel 9</p>
      {scope('Read and chat', 'See chats and write in them.', true)}
      {scope('Answer', 'Give permissions and answer questions.', true)}
      {scope('Merge pull requests', 'Off unless you turn it on.', false)}
      {scope('Use agents’ desktops', 'See and drive an agent’s browser: click, type, passwords included. Off unless you turn it on, and only over https.', false, true)}
      <p className="mt-2 text-caption text-subtle">Turning it on asks to confirm, like merge: “Whoever holds this phone could type into any agent’s browser.”</p>
    </div>
  );
}

// ——— The storyboard ———

interface Screen {
  title: string;
  note: string;
  el: ReactNode;
}

const OPTIONS: Record<string, { name: string; pitch: string; good: string; cost: string; screens: Screen[] }> = {
  a: {
    name: 'A · Take over',
    pitch: 'The display full screen under your finger, like a remote-desktop app: tap where you want to click, pinch and pan, a key row over the phone’s keyboard.',
    good: 'Closest to the computer’s Desktop tab (same noVNC view, same pointer model); nothing to learn; works for any page, not just logins.',
    cost: 'Fingers are coarse on a 1280-wide page: you zoom a lot. Keys go one by one into a hidden input, so the phone’s password manager won’t offer a login: paste it.',
    screens: [
      { title: '1 · The ask', note: 'The agent’s message, and a live card of its display with one button. The header gains a Desktop button with a dot while it waits.', el: <AChat /> },
      { title: '2 · Taking over', note: 'Fitted to the width (30%). Tap is a click where you tap; double-tap zooms to that spot; the agent is told it waits.', el: <AFit /> },
      { title: '3 · Typing', note: 'Tapping a field clicks it and raises the keyboard; Esc, Tab, Enter, Paste, Ctrl and arrows ride above it. The minimap shows where you are.', el: <AType /> },
      { title: '4 · A password', note: 'Same keyboard; keys are sent as typed and kept nowhere. Paste fills it from the clipboard (your password manager). Shown only over https.', el: <AType password /> },
      { title: '5 · Pan and scroll', note: 'One finger pans the zoomed view, two fingers scroll the page (wheel events), as a laptop’s trackpad does.', el: <AScroll /> },
      { title: '6 · Giving back', note: '“Give back” (or leaving) asks once, with a line to tell the agent, prefilled from nothing: you write it or skip it.', el: <AGiveBack /> },
      { title: '7 · Back in the chat', note: 'A quiet line says you drove it and for how long, then your note, then the agent carries on.', el: <AAfter /> },
      { title: '✕ · This phone may not', note: 'The card’s button is locked; tapping it says why and where to turn it on.', el: <AChat refused="scope" /> },
      { title: '✕ · Needs the https link', note: 'On the LAN’s plain http, the same sheet points at the https link, one tap away when the tunnel is on.', el: <AChat refused="http" /> },
    ],
  },
  b: {
    name: 'B · Trackpad',
    pitch: 'The agent’s Desktop as a tab, driven like a laptop: your thumb moves a cursor on a trackpad, a magnified view follows it, and typing goes through a real field on the phone.',
    good: 'Precise clicks without zooming by hand; the thumb never covers what it clicks; a real password field means the phone’s password manager fills it.',
    cost: 'Indirect: moving a cursor is slower than tapping; less screen for the page; a compose line is one more step than typing straight in.',
    screens: [
      { title: '1 · The ask', note: 'A notification and a “Needs you” row: “wants you in its browser”, opening the agent’s Desktop tab.', el: <BList /> },
      { title: '2 · From the chat', note: 'The agent’s chat gains Chat | Desktop tabs, as on the computer; a dot while it waits, and a chip under the ask.', el: <BChat /> },
      { title: '3 · Driving', note: 'Magnified view on top follows the cursor; the minimap shows where; the trackpad below moves it. Tap = click, right edge or two fingers = scroll, pinch the view to zoom.', el: <BDrive /> },
      { title: '4 · Typing', note: 'Click the field, then type in the compose line; Type sends it as keystrokes. Tab, Enter, Esc, Paste, ⌫ and arrows go at once.', el: <BType /> },
      { title: '5 · A password', note: 'Clicking a password field turns the line into a password input: the phone offers the saved login. Never shown or kept.', el: <BType password /> },
      { title: '6 · Handing back', note: '“Hand back” in the header returns it, and a card sums up what you did; it also goes back by itself after 5 minutes untouched.', el: <BBack /> },
      { title: '✕ · This phone may not', note: 'The Desktop tab is there but shut, with the steps; it opens by itself once allowed.', el: <BChat refused="scope" /> },
      { title: '✕ · Needs the https link', note: 'Shut too, with the https link and a reminder to keep that one on the home screen.', el: <BChat refused="http" /> },
    ],
  },
  c: {
    name: 'C · Guided',
    pitch: 'The agent says what it needs and marks where; the phone opens on just that part, mirrors the field you type in, and hands back by answering the agent’s question.',
    good: 'Fewest steps for the common case (log in, click Allow); the agent’s words stay on screen; giving back is answering, so the agent knows it’s done.',
    cost: 'Needs the agent to mark a region (a new field on its ask); for a free-form errand you fall back to “Whole screen”, which is option A.',
    screens: [
      { title: '1 · The ask', note: 'The agent’s question carries a picture of its display with what it marked, and two answers: do it here, or on the computer.', el: <CAsk /> },
      { title: '2 · Opened on the mark', note: 'Zoomed to the marked form, the agent’s ask pinned on top with Done. Tap to click, pinch, drag; “Whole screen” fits it all.', el: <CFocus /> },
      { title: '3 · Typing', note: 'A tapped field is mirrored above the keyboard, so you see what you type; “Next field” is Tab.', el: <CType /> },
      { title: '4 · A password', note: 'The mirror becomes a password input, so the phone offers the saved login; Enter submits. Never shown or kept.', el: <CType password /> },
      { title: '5 · Giving back', note: 'When the page changes the agent offers to take over again; Done in the bar does the same any time.', el: <CDone /> },
      { title: '6 · Answered', note: 'The question shows how it was answered, and the agent carries on.', el: <CAfter /> },
      { title: '✕ · This phone may not', note: 'The question says why in place, and still takes “I did it on the computer”.', el: <CAsk refused="scope" /> },
      { title: '✕ · Needs the https link', note: 'The same, with the https link to open.', el: <CAsk refused="http" /> },
    ],
  },
};

const FRAME: CSSProperties = { width: 390, height: 844, border: '10px solid #1c1c22', borderRadius: 44, boxSizing: 'content-box', boxShadow: '0 20px 60px rgb(0 0 0 / 0.5)' };

function Storyboard({ option }: { option: string }) {
  const o = OPTIONS[option] ?? OPTIONS.a;
  return (
    <div className="min-h-screen bg-ink p-8" data-phone-browser-option={option}>
      <div className="mb-6 grid max-w-[1260px] gap-2">
        <p className="text-caption text-subtle">Mockup · using an agent’s browser from the phone · ?phone=browser&amp;option=a|b|c</p>
        <h1 className="text-display font-semibold text-title">{o.name}</h1>
        <p className="text-body text-primary">{o.pitch}</p>
        <p className="text-ui text-secondary">
          <b className="text-emerald-300">For:</b> {o.good}
        </p>
        <p className="text-ui text-secondary">
          <b className="text-rose-300">Against:</b> {o.cost}
        </p>
      </div>
      <div className="flex flex-wrap gap-x-8 gap-y-10">
        {o.screens.map((sc, i) => (
          <figure key={sc.title} className="grid w-[410px] content-start gap-2" data-screen={i + 1}>
            <figcaption className="text-ui font-semibold text-title">{sc.title}</figcaption>
            <div className="overflow-hidden bg-ink" style={FRAME}>
              <div className="relative h-full w-full overflow-hidden">{sc.el}</div>
            </div>
            <p className="text-caption leading-relaxed text-subtle">{sc.note}</p>
          </figure>
        ))}
        <figure className="grid w-[410px] content-start gap-2">
          <figcaption className="text-ui font-semibold text-title">The scope · same for all three</figcaption>
          <SettingsCard />
        </figure>
      </div>
    </div>
  );
}

// startPhoneBrowser draws ?phone=browser: the storyboard, or with &screen=N
// one frame filling the page (inside ?phone=frame's 390×844 iframe).
export function startPhoneBrowser(params: URLSearchParams): void {
  const option = params.get('option') ?? 'a';
  const n = Number(params.get('screen'));
  const screens = (OPTIONS[option] ?? OPTIONS.a).screens;
  document.body.style.margin = '0';
  createRoot(document.getElementById('root')!).render(
    n >= 1 && n <= screens.length ? (
      <div className="relative h-dvh w-full overflow-hidden bg-ink">{screens[n - 1].el}</div>
    ) : (
      <Storyboard option={option} />
    ),
  );
}
