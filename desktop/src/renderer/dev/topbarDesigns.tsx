// Mock-ups of a redesigned top bar, for choosing one before any of it is
// built: ?topbar=a|b|c (and ?theme=light). Static and hardcoded on purpose —
// no queries, no i18n, nothing wired — so each option can be judged across
// every state at once, its popovers drawn open beside the bar rather than
// portalled. Whichever option is picked gets built in TopBar.tsx and
// ResourceControls.tsx; this file goes once it has.
import {
  ArrowUpRight,
  Bell,
  ChevronRight,
  Cpu,
  HardDrive,
  LoaderCircle,
  MemoryStick,
  MonitorCog,
  Pause,
  Play,
  Power,
  RefreshCw,
  Square,
  TriangleAlert,
  Unplug,
} from 'lucide-react';
import type { ReactNode } from 'react';
import { cn } from '../lib/utils';

// Scene is one moment the bar has to make sense in.
interface Scene {
  id: string;
  title: string;
  platform: 'Linux (VM)' | 'Mac (VM)' | 'Windows (WSL)';
  machine: 'VM' | 'WSL';
  daemon: 'connected' | 'connecting' | 'offline';
  vm: 'running' | 'starting' | 'off';
  five: { pct: number; left: string };
  week: { pct: number; left: string };
  asOf: string;
  mem: { used: number; total: number; cap?: number };
  cpu: { pct: number; cores: number };
  disk: { used: number; size: number; free: number };
  agents: { running: number; working: number; asking: number };
  restorable?: number;
  bell: number;
  // open is which popovers each option draws open for this scene.
  open: ('claude' | 'machine' | 'stop')[];
}

const base: Omit<Scene, 'id' | 'title' | 'open'> = {
  platform: 'Linux (VM)',
  machine: 'VM',
  daemon: 'connected',
  vm: 'running',
  five: { pct: 38, left: '3h 12m' },
  week: { pct: 22, left: '4d 6h' },
  asOf: '2 min ago',
  mem: { used: 5.1, total: 12, cap: 16 },
  cpu: { pct: 31, cores: 6 },
  disk: { used: 41.2, size: 120, free: 212 },
  agents: { running: 4, working: 1, asking: 0 },
  bell: 2,
};

const scenes: Scene[] = [
  { ...base, id: 'normal', title: 'Normal: 4 agents running, plenty of room', open: ['claude', 'machine'] },
  {
    ...base,
    id: 'quota',
    title: 'Claude near its limit: 92% of the 5-hour window, 47 min to reset',
    five: { pct: 92, left: '47m' },
    week: { pct: 71, left: '1d 3h' },
    open: ['claude'],
  },
  {
    ...base,
    id: 'memory',
    title: 'VM memory tight: 7 agents, 11.2 of 12 GiB in use',
    mem: { used: 11.2, total: 12, cap: 16 },
    cpu: { pct: 78, cores: 6 },
    agents: { running: 7, working: 2, asking: 1 },
    open: ['machine', 'stop'],
  },
  {
    ...base,
    id: 'offline',
    title: 'Daemon disconnected: the VM is up, the app lost the daemon',
    daemon: 'offline',
    asOf: '14 min ago',
    open: ['machine'],
  },
  {
    ...base,
    id: 'starting',
    title: 'Starting: the VM boots, the daemon not reachable yet',
    vm: 'starting',
    daemon: 'connecting',
    mem: { used: 0, total: 0 },
    cpu: { pct: 0, cores: 6 },
    agents: { running: 0, working: 0, asking: 0 },
    open: ['machine'],
  },
  {
    ...base,
    id: 'off',
    title: 'Off: everything stopped to give the memory back, 4 agents to bring back',
    vm: 'off',
    daemon: 'offline',
    mem: { used: 0, total: 0 },
    cpu: { pct: 0, cores: 6 },
    agents: { running: 0, working: 0, asking: 0 },
    restorable: 4,
    open: ['machine'],
  },
  {
    ...base,
    id: 'wsl',
    title: "Windows: AgentBox's WSL distro (#213): no pause, memory against WSL's limit",
    platform: 'Windows (WSL)',
    machine: 'WSL',
    mem: { used: 6.5, total: 16 },
    cpu: { pct: 24, cores: 8 },
    open: ['machine'],
  },
];

const busiest = [
  { name: 'Top bar redesign previews', mem: 2.4, cpu: 41, doing: 'working' },
  { name: 'agent-244', mem: 1.6, cpu: 12, doing: 'asking' },
  { name: 'Fix WSL relay restart', mem: 1.3, cpu: 3, doing: 'idle' },
];

const pct = (used: number, total: number) => (total > 0 ? Math.round((used / total) * 100) : 0);
const gib = (n: number) => `${n.toFixed(1)} GiB`;
const tone = (p: number) => (p > 85 ? 'high' : p > 65 ? 'warn' : 'ok');
const fill = (p: number) => (p > 85 ? 'bg-rose-400' : p > 65 ? 'bg-amber-400' : 'bg-gradient-to-r from-brand-400 to-sky-400');
const text = (p: number) => (p > 85 ? 'text-rose-300' : p > 65 ? 'text-amber-300' : 'text-tertiary');

// healthOf is the one state a single status item shows: the worst of the
// machine, the daemon, and the memory and disk inside it.
function healthOf(s: Scene): { tone: 'ok' | 'warn' | 'bad' | 'busy' | 'off'; word: string; detail: string } {
  if (s.vm === 'off') return { tone: 'off', word: `${s.machine} off`, detail: `Turned off to give its memory back` };
  if (s.vm === 'starting') return { tone: 'busy', word: `${s.machine} starting…`, detail: 'Booting; agents come back once it is up' };
  if (s.daemon === 'offline') return { tone: 'bad', word: 'Not connected', detail: 'The app lost AgentBox inside the VM; retrying' };
  if (s.daemon === 'connecting') return { tone: 'busy', word: 'Connecting…', detail: '' };
  const m = pct(s.mem.used, s.mem.total);
  if (m > 85) return { tone: 'warn', word: 'Memory almost full', detail: `${gib(s.mem.used)} of ${gib(s.mem.total)}` };
  return { tone: 'ok', word: 'Running', detail: `${s.agents.running} agents` };
}

const dotOf = { ok: 'bg-emerald-400 animate-glow', warn: 'bg-amber-400', bad: 'bg-rose-400', busy: 'bg-brand-400 animate-pulse', off: 'bg-faint' };

// ---------------------------------------------------------------------------
// Shared pieces

// ClaudeMark is a small orange starburst, so the Claude item is told apart
// from the machine's meters by colour and shape before it's read.
function ClaudeMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={cn('size-3.5 shrink-0', className)} aria-hidden>
      {Array.from({ length: 8 }, (_, i) => (
        <rect key={i} x="11" y="2" width="2" height="9" rx="1" fill="#d97757" transform={`rotate(${i * 45} 12 12)`} />
      ))}
    </svg>
  );
}

function Meter({ p, className }: { p: number | null; className?: string }) {
  return (
    <span className={cn('block h-1 w-8 overflow-hidden rounded-full bg-surface-strong', className)}>
      {p !== null && <span className={cn('block h-full rounded-full', fill(p))} style={{ width: `${Math.max(p, 4)}%` }} />}
    </span>
  );
}

function BigMeter({ p, faded }: { p: number; faded?: boolean }) {
  return (
    <span className="block h-1.5 overflow-hidden rounded-full bg-surface-strong">
      <span className={cn('block h-full rounded-full', fill(p), faded && 'opacity-40')} style={{ width: `${Math.max(p, 2)}%` }} />
    </span>
  );
}

function Panel({ title, width = 'w-80', children }: { title: string; width?: string; children: ReactNode }) {
  return (
    <div className="grid content-start gap-1">
      <span className="text-[10.5px] font-medium uppercase tracking-wider text-faint">{title}</span>
      <div className={cn('grid gap-3 rounded-xl border border-line-strong bg-overlay p-3 shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)]', width)}>
        {children}
      </div>
    </div>
  );
}

function Btn({ kind = 'secondary', children }: { kind?: 'secondary' | 'danger' | 'primary' | 'ghost' | 'destructive'; children: ReactNode }) {
  return (
    <span
      className={cn(
        'inline-flex h-8 items-center gap-1.5 whitespace-nowrap rounded-lg px-3 text-[13px] font-medium [&_svg]:size-4',
        kind === 'secondary' && 'border border-line bg-surface-raised text-primary',
        kind === 'danger' && 'text-rose-300 ring-1 ring-inset ring-rose-400/25',
        kind === 'primary' && 'bg-gradient-to-b from-brand-500 to-indigo-600 text-white',
        kind === 'ghost' && 'text-muted',
        kind === 'destructive' && 'bg-rose-600 text-white',
      )}
    >
      {children}
    </span>
  );
}

function Crumbs() {
  return (
    <nav className="flex min-w-0 items-center gap-1.5 text-[13px]">
      <span className="text-muted">agentbox</span>
      <ChevronRight className="size-3.5 shrink-0 text-faint" />
      <span className="truncate font-medium text-primary">Top bar redesign previews</span>
    </nav>
  );
}

function BellButton({ count }: { count: number }) {
  return (
    <span className="relative rounded-lg p-2 text-muted">
      <Bell className="size-4" />
      {count > 0 && <span className="absolute right-1 top-1 size-2 rounded-full bg-brand-400 ring-2 ring-[var(--color-ink)]" />}
    </span>
  );
}

// ClaudePopover is the same in every option: which account, both windows
// with words, what happens at 100%, and the other accounts.
function ClaudePopover({ s }: { s: Scene }) {
  const stale = s.daemon !== 'connected';
  return (
    <Panel title="Claude usage, open">
      <div className="grid gap-0.5">
        <span className="flex items-center gap-2 text-[13px] font-medium text-primary">
          <ClaudeMark className="size-4" />
          Claude plan limits · personal
        </span>
        <span className="text-[11.5px] text-muted">The account this project's agents use. Shared by every agent and chat on it.</span>
      </div>
      <LimitRow label="5-hour window" p={s.five.pct} left={`resets in ${s.five.left}`} faded={stale} />
      <LimitRow label="This week" p={s.week.pct} left={`resets in ${s.week.left}`} faded={stale} />
      {s.five.pct > 85 && (
        <span className="rounded-lg bg-rose-400/10 px-2.5 py-2 text-[11.5px] leading-relaxed text-rose-200 ring-1 ring-inset ring-rose-400/25">
          At 100% Claude stops answering every agent on this account until the window resets in {s.five.left}.
        </span>
      )}
      <div className="grid gap-1 border-t border-line pt-2.5 text-[11.5px]">
        <span className="font-medium text-secondary">Other accounts</span>
        <span className="flex justify-between text-muted">
          <span>work</span>
          <span className="font-mono tabular-nums text-faint">5h 12% · week 40%</span>
        </span>
      </div>
      <span className="flex items-center justify-between text-[11px] text-faint">
        <span className={cn(stale && 'text-amber-300')}>As of {s.asOf}, from the last reply</span>
        <span className="flex items-center gap-1 text-brand-300">
          Accounts <ArrowUpRight className="size-3" />
        </span>
      </span>
    </Panel>
  );
}

function LimitRow({ label, p, left, faded }: { label: string; p: number; left: string; faded?: boolean }) {
  return (
    <div className="grid gap-1">
      <div className="flex items-baseline justify-between text-[12px]">
        <span className="text-secondary">{label}</span>
        <span className="font-mono text-[11px] tabular-nums">
          <span className={cn('font-semibold', text(p))}>{p}% used</span>
          <span className="text-faint"> · {left}</span>
        </span>
      </div>
      <BigMeter p={p} faded={faded} />
    </div>
  );
}

// StopPanel is what "Free resources" becomes, opened: what it stops, who is
// cut off, what comes back, and that one click undoes it.
function StopPanel({ s, name }: { s: Scene; name: string }) {
  return (
    <Panel title={`"${name}", clicked`} width="w-[22rem]">
      <div className="grid gap-1">
        <span className="text-[14px] font-semibold text-primary">
          Stop {s.agents.running} agents and turn the {s.machine} off?
        </span>
        <span className="text-[12px] leading-relaxed text-secondary">
          Gives all {gib(s.mem.total)} of the {s.machine}'s memory and its {s.cpu.cores} CPUs back to your computer. Nothing is deleted:
          <span className="font-medium text-primary"> Start</span> brings the {s.machine} and these agents back.
        </span>
      </div>
      {s.agents.working + s.agents.asking > 0 && (
        <span className="rounded-lg bg-amber-400/10 px-2.5 py-2 text-[11.5px] text-amber-200 ring-1 ring-inset ring-amber-400/25">
          {s.agents.working} working and {s.agents.asking} waiting on you lose what they were in the middle of.
        </span>
      )}
      <div className="flex justify-end gap-2">
        <Btn kind="ghost">Cancel</Btn>
        <Btn kind="destructive">
          <Power /> {name.replace('…', '')}
        </Btn>
      </div>
    </Panel>
  );
}

function Busiest() {
  return (
    <div className="grid gap-1 text-[11.5px]">
      <span className="font-medium text-secondary">Using the most</span>
      {busiest.map((a) => (
        <span key={a.name} className="flex min-w-0 items-center justify-between gap-3 text-muted">
          <span className="min-w-0 truncate">{a.name}</span>
          <span className="flex shrink-0 items-center gap-1.5 font-mono tabular-nums text-faint">
            {gib(a.mem)} · {a.cpu}%
            <Square className="size-3" />
          </span>
        </span>
      ))}
    </div>
  );
}

function ResourceRow({ icon, label, value, p }: { icon: ReactNode; label: string; value: string; p: number }) {
  return (
    <div className="grid gap-1">
      <div className="flex items-center justify-between text-[12px]">
        <span className="flex items-center gap-1.5 text-secondary">
          {icon}
          {label}
        </span>
        <span className={cn('font-mono text-[11px] tabular-nums', text(p))}>{value}</span>
      </div>
      <BigMeter p={p} />
    </div>
  );
}

// MachineBody is the VM's state, its daemon, and its memory, CPU and disk:
// the content of the one machine popover options A and B open.
function MachineBody({ s }: { s: Scene }) {
  const h = healthOf(s);
  const up = s.vm === 'running';
  return (
    <>
      <div className="flex items-center justify-between">
        <span className="flex items-center gap-2 text-[13px] font-medium text-primary">
          <MonitorCog className="size-4 text-subtle" />
          {s.machine === 'WSL' ? "AgentBox's WSL distro" : "AgentBox's VM"}
        </span>
        <span className="flex items-center gap-1.5 text-[12px] text-muted">
          <span className={cn('size-1.5 rounded-full', dotOf[h.tone])} />
          {s.vm === 'running' ? 'Running' : s.vm === 'starting' ? 'Starting…' : 'Off'}
        </span>
      </div>
      <span className="text-[11.5px] leading-relaxed text-muted">
        Where every agent runs. {s.machine === 'WSL' ? 'Windows gives it memory as it needs it, up to WSL’s limit.' : 'Its memory grows as agents need it, up to its cap.'}
      </span>
      <DaemonLine s={s} />
      {up && (
        <>
          <ResourceRow
            icon={<MemoryStick className="size-3.5 text-subtle" />}
            label="Memory"
            p={pct(s.mem.used, s.mem.total)}
            value={`${gib(s.mem.used)} of ${gib(s.mem.total)}${s.mem.cap ? ` (cap ${s.mem.cap})` : ''}`}
          />
          <ResourceRow icon={<Cpu className="size-3.5 text-subtle" />} label="CPU" p={s.cpu.pct} value={`${s.cpu.pct}% of ${s.cpu.cores} CPUs`} />
          {s.machine === 'VM' && (
            <ResourceRow icon={<HardDrive className="size-3.5 text-subtle" />} label="Disk" p={pct(s.disk.used, s.disk.size)} value={`${gib(s.disk.used)} of ${s.disk.size} GiB`} />
          )}
          {s.daemon === 'connected' && <Busiest />}
        </>
      )}
      {s.vm === 'off' && (
        <span className="text-[12px] leading-relaxed text-secondary">
          Stopped from the top bar to give its memory back. Start turns it on and brings back the {s.restorable} agents that were running.
        </span>
      )}
    </>
  );
}

function DaemonLine({ s }: { s: Scene }) {
  if (s.vm === 'off') return null;
  const ok = s.daemon === 'connected';
  return (
    <span
      className={cn(
        'flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-[11.5px]',
        ok ? 'bg-surface-faint text-muted' : s.daemon === 'offline' ? 'bg-rose-400/10 text-rose-200' : 'bg-brand-400/10 text-brand-200',
      )}
    >
      {ok ? <span className="size-1.5 rounded-full bg-emerald-400" /> : s.daemon === 'offline' ? <Unplug className="size-3.5" /> : <LoaderCircle className="size-3.5 animate-spin" />}
      {ok
        ? 'App connected to AgentBox in the VM'
        : s.daemon === 'offline'
          ? 'Lost the connection to AgentBox in the VM. Retrying in 3 s…'
          : 'Waiting for AgentBox in the VM to answer…'}
      {s.daemon === 'offline' && (
        <span className="ml-auto flex items-center gap-1 font-medium text-rose-100">
          <RefreshCw className="size-3" /> Retry
        </span>
      )}
    </span>
  );
}

function MachineActions({ s, stopName }: { s: Scene; stopName: string }) {
  return (
    <div className="grid gap-2 border-t border-line pt-3">
      <div className="flex flex-wrap items-center gap-2">
        {s.vm === 'off' ? (
          <Btn kind="primary">
            <Play /> Start {s.machine} and {s.restorable} agents
          </Btn>
        ) : s.vm === 'starting' ? (
          <Btn>
            <LoaderCircle className="animate-spin" /> Starting…
          </Btn>
        ) : (
          <>
            {s.machine === 'VM' && (
              <Btn>
                <Pause /> Pause
              </Btn>
            )}
            <Btn kind="danger">
              <Power /> {stopName}
            </Btn>
          </>
        )}
      </div>
      {s.vm === 'running' && (
        <span className="text-[11px] leading-relaxed text-faint">
          {s.machine === 'VM' && 'Pause freezes it and keeps its memory. '}
          {stopName.replace('…', '')} stops the {s.agents.running} running agents and turns the {s.machine} off, giving all its memory back to your computer.
        </span>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Option A: two labelled bubbles — Claude, and the machine.

function ABar({ s }: { s: Scene }) {
  const h = healthOf(s);
  const m = pct(s.mem.used, s.mem.total);
  const c = s.five.pct;
  return (
    <>
      <span
        className={cn(
          'flex items-center gap-2 rounded-full border py-1 pl-2 pr-2.5',
          tone(c) === 'high' ? 'border-rose-400/40 bg-rose-400/10' : 'border-line bg-surface-faint',
          s.daemon !== 'connected' && 'opacity-60',
        )}
      >
        <ClaudeMark />
        <span className="text-[12px] font-medium text-secondary">Claude</span>
        <Meter p={c} className="w-10" />
        <span className={cn('whitespace-nowrap font-mono text-[11px] tabular-nums', text(c))}>{c}%</span>
        {tone(c) !== 'ok' && <span className="whitespace-nowrap text-[11px] text-muted">· resets {s.five.left}</span>}
      </span>
      <span
        className={cn(
          'flex items-center gap-2 rounded-full border py-1 pl-2.5 pr-2.5',
          h.tone === 'bad' ? 'border-rose-400/40 bg-rose-400/10' : h.tone === 'warn' ? 'border-amber-400/40 bg-amber-400/10' : 'border-line bg-surface-faint',
        )}
      >
        <span className={cn('size-1.5 rounded-full', dotOf[h.tone])} />
        <span className="text-[12px] font-medium text-secondary">{s.machine}</span>
        {h.tone === 'ok' || h.tone === 'warn' ? (
          <>
            <MemoryStick className="size-3.5 text-subtle" />
            <span className={cn('whitespace-nowrap font-mono text-[11px] tabular-nums', text(m))}>
              {s.mem.used.toFixed(1)}/{s.mem.total} GiB
            </span>
            <Meter p={m} />
          </>
        ) : (
          <span className={cn('whitespace-nowrap text-[11.5px]', h.tone === 'bad' ? 'text-rose-200' : 'text-muted')}>{h.word}</span>
        )}
      </span>
      {s.vm === 'off' && (
        <span className="flex items-center gap-1.5 rounded-full bg-emerald-400/10 px-2.5 py-1 text-xs font-medium text-emerald-200 ring-1 ring-inset ring-emerald-400/30">
          <Play className="size-3.5" /> Start
        </span>
      )}
    </>
  );
}

function APopovers({ s }: { s: Scene }) {
  return (
    <>
      {s.open.includes('claude') && <ClaudePopover s={s} />}
      {s.open.includes('machine') && (
        <Panel title={`${s.machine} bubble, open`}>
          <MachineBody s={s} />
          <MachineActions s={s} stopName={`Stop agents & ${s.machine}…`} />
        </Panel>
      )}
      {s.open.includes('stop') && <StopPanel s={s} name={`Stop agents & ${s.machine}…`} />}
    </>
  );
}

// ---------------------------------------------------------------------------
// Option B: words, not numbers. A Claude ring, one status sentence, and the
// action the moment calls for, only when it calls for one.

function Ring({ p, size = 18 }: { p: number; size?: number }) {
  const r = size / 2 - 2;
  const len = 2 * Math.PI * r;
  const color = p > 85 ? 'var(--color-rose-400)' : p > 65 ? 'var(--color-amber-400)' : '#d97757';
  return (
    <svg width={size} height={size} className="-rotate-90 shrink-0" aria-hidden>
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--color-surface-strong)" strokeWidth="2.5" />
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke={color} strokeWidth="2.5" strokeDasharray={`${(p / 100) * len} ${len}`} strokeLinecap="round" />
    </svg>
  );
}

function BBar({ s }: { s: Scene }) {
  const h = healthOf(s);
  const c = s.five.pct;
  const sentence =
    h.tone === 'ok' ? `${s.machine} running · ${s.agents.running} agents` : h.tone === 'warn' ? `${s.machine} memory almost full` : h.tone === 'bad' ? 'Reconnecting to AgentBox…' : h.word;
  return (
    <>
      <span className={cn('flex items-center gap-2 rounded-full border border-line bg-surface-faint py-1 pl-1.5 pr-3', s.daemon !== 'connected' && 'opacity-60')}>
        <Ring p={c} />
        <span className="whitespace-nowrap text-[12px] text-secondary">
          Claude <span className={cn('font-medium', c > 65 ? text(c) : 'text-primary')}>{c > 85 ? `${c}% · ${s.five.left} to reset` : `${c}% used`}</span>
        </span>
      </span>
      <span
        className={cn(
          'flex items-center gap-2 rounded-full border py-1 pl-2.5 pr-3',
          h.tone === 'bad' ? 'border-rose-400/40 bg-rose-400/10' : h.tone === 'warn' ? 'border-amber-400/40 bg-amber-400/10' : 'border-line bg-surface-faint',
        )}
      >
        {h.tone === 'bad' ? (
          <Unplug className="size-3.5 text-rose-300" />
        ) : h.tone === 'busy' ? (
          <LoaderCircle className="size-3.5 animate-spin text-brand-300" />
        ) : h.tone === 'warn' ? (
          <TriangleAlert className="size-3.5 text-amber-300" />
        ) : (
          <span className={cn('size-1.5 rounded-full', dotOf[h.tone])} />
        )}
        <span className={cn('whitespace-nowrap text-[12px]', h.tone === 'bad' ? 'text-rose-200' : h.tone === 'warn' ? 'text-amber-200' : 'text-secondary')}>{sentence}</span>
      </span>
      {h.tone === 'warn' && (
        <span className="flex items-center gap-1.5 rounded-full bg-amber-400/15 px-2.5 py-1 text-xs font-medium text-amber-100 ring-1 ring-inset ring-amber-400/35">
          <Power className="size-3.5" /> Free up memory…
        </span>
      )}
      {s.vm === 'off' && (
        <span className="flex items-center gap-1.5 rounded-full bg-emerald-400/10 px-2.5 py-1 text-xs font-medium text-emerald-200 ring-1 ring-inset ring-emerald-400/30">
          <Play className="size-3.5" /> Start {s.machine} + {s.restorable} agents
        </span>
      )}
    </>
  );
}

function BPopovers({ s }: { s: Scene }) {
  const h = healthOf(s);
  const up = s.vm === 'running';
  const m = pct(s.mem.used, s.mem.total);
  const dk = pct(s.disk.used, s.disk.size);
  return (
    <>
      {s.open.includes('claude') && <ClaudePopover s={s} />}
      {s.open.includes('machine') && (
        <Panel title="Status, open">
          <span className="text-[13px] font-medium text-primary">{h.tone === 'ok' ? 'Everything is running' : h.word}</span>
          <div className="grid gap-1.5 text-[12px]">
            <Check ok={up} busy={s.vm === 'starting'} label={s.machine === 'WSL' ? "AgentBox's WSL distro" : "AgentBox's VM"} value={s.vm === 'running' ? 'Running' : s.vm === 'starting' ? 'Starting…' : 'Off'} />
            <Check
              ok={s.daemon === 'connected'}
              busy={s.daemon === 'connecting'}
              bad={s.daemon === 'offline' && s.vm === 'running'}
              label="Connection to it"
              value={s.daemon === 'connected' ? 'Connected' : s.daemon === 'connecting' ? 'Waiting…' : s.vm === 'off' ? '—' : 'Lost, retrying'}
            />
            {up && (
              <>
                <Check ok={m <= 85} warn={m > 85} label="Memory" value={`${gib(s.mem.used)} of ${gib(s.mem.total)}`} />
                <Check ok={s.cpu.pct <= 85} warn={s.cpu.pct > 85} label="CPU" value={`${s.cpu.pct}% of ${s.cpu.cores}`} />
                {s.machine === 'VM' && <Check ok={dk <= 85} label="Disk" value={`${Math.round(s.disk.size - s.disk.used)} GiB free`} />}
                <Check ok label="Agents" value={`${s.agents.running} running${s.agents.working ? `, ${s.agents.working} working` : ''}`} />
              </>
            )}
          </div>
          {up && s.daemon === 'connected' && <Busiest />}
          <MachineActions s={s} stopName="Free up memory…" />
        </Panel>
      )}
      {s.open.includes('stop') && <StopPanel s={s} name="Free up memory…" />}
    </>
  );
}

function Check({ ok, warn, bad, busy, label, value }: { ok?: boolean; warn?: boolean; bad?: boolean; busy?: boolean; label: string; value: string }) {
  return (
    <span className="flex items-center gap-2">
      <span
        className={cn(
          'size-1.5 shrink-0 rounded-full',
          busy ? 'bg-brand-400 animate-pulse' : bad ? 'bg-rose-400' : warn ? 'bg-amber-400' : ok ? 'bg-emerald-400' : 'bg-faint',
        )}
      />
      <span className="text-secondary">{label}</span>
      <span className={cn('ml-auto font-mono text-[11px] tabular-nums', bad ? 'text-rose-300' : warn ? 'text-amber-300' : 'text-tertiary')}>{value}</span>
    </span>
  );
}

// ---------------------------------------------------------------------------
// Option C: one labelled strip, every number in words' company, and the
// action spelled out beside it.

function CBar({ s }: { s: Scene }) {
  const h = healthOf(s);
  const c = s.five.pct;
  const m = pct(s.mem.used, s.mem.total);
  const up = s.vm === 'running' && s.daemon === 'connected';
  const seg = 'flex items-center gap-1.5 px-2.5 py-1';
  return (
    <>
      <span
        className={cn(
          'flex items-center gap-1.5 rounded-full py-1 pl-2 pr-2.5 ring-1 ring-inset',
          c > 85 ? 'bg-rose-400/10 ring-rose-400/40' : 'bg-[#d97757]/10 ring-[#d97757]/30',
          s.daemon !== 'connected' && 'opacity-60',
        )}
      >
        <ClaudeMark />
        <span className="text-[12px] font-medium text-primary">Claude</span>
        <span className="text-[11px] text-muted">5h</span>
        <span className={cn('font-mono text-[11px] font-semibold tabular-nums', text(c))}>{c}%</span>
        <span className="text-[11px] text-muted">week</span>
        <span className={cn('font-mono text-[11px] tabular-nums', text(s.week.pct))}>{s.week.pct}%</span>
      </span>
      <span
        className={cn(
          'flex items-center divide-x divide-line overflow-hidden rounded-full border bg-surface-faint',
          h.tone === 'bad' ? 'border-rose-400/40' : h.tone === 'warn' ? 'border-amber-400/40' : 'border-line',
        )}
      >
        <span className={seg}>
          <span className={cn('size-1.5 rounded-full', dotOf[h.tone])} />
          <span className="text-[12px] font-medium text-secondary">{s.machine}</span>
          {!up && <span className={cn('whitespace-nowrap text-[11.5px]', h.tone === 'bad' ? 'text-rose-200' : 'text-muted')}>{h.word}</span>}
        </span>
        {up && (
          <>
            <span className={cn(seg, m > 85 && 'bg-amber-400/10')}>
              <span className="text-[11px] text-muted">Memory</span>
              <span className={cn('whitespace-nowrap font-mono text-[11px] tabular-nums', text(m))}>
                {s.mem.used.toFixed(1)}/{s.mem.total} GiB
              </span>
            </span>
            <span className={seg}>
              <span className="text-[11px] text-muted">CPU</span>
              <span className={cn('font-mono text-[11px] tabular-nums', text(s.cpu.pct))}>{s.cpu.pct}%</span>
            </span>
            {s.machine === 'VM' && (
              <span className={seg}>
                <span className="text-[11px] text-muted">Disk</span>
                <span className="whitespace-nowrap font-mono text-[11px] tabular-nums text-tertiary">
                  {Math.round(s.disk.used)}/{s.disk.size} GiB
                </span>
              </span>
            )}
          </>
        )}
      </span>
      {s.vm === 'off' ? (
        <span className="flex items-center gap-1.5 rounded-full bg-emerald-400/10 px-2.5 py-1 text-xs font-medium text-emerald-200 ring-1 ring-inset ring-emerald-400/30">
          <Play className="size-3.5" /> Start
        </span>
      ) : (
        s.vm === 'running' && (
          <span className="flex items-center gap-1.5 rounded-full border border-line px-2.5 py-1 text-xs font-medium text-secondary">
            <Power className="size-3.5" /> Shut down
          </span>
        )
      )}
    </>
  );
}

function CPopovers({ s }: { s: Scene }) {
  const m = pct(s.mem.used, s.mem.total);
  return (
    <>
      {s.open.includes('claude') && <ClaudePopover s={s} />}
      {s.open.includes('machine') && s.id === 'memory' ? (
        <Panel title="Memory segment, open">
          <ResourceRow
            icon={<MemoryStick className="size-3.5 text-subtle" />}
            label="VM memory"
            p={m}
            value={`${gib(s.mem.used)} of ${gib(s.mem.total)}`}
          />
          <span className="text-[11.5px] text-muted">When it's full, agents slow down and builds fail. Stop agents you don't need, or shut everything down.</span>
          <Busiest />
        </Panel>
      ) : (
        s.open.includes('machine') && (
          <Panel title={`${s.machine} segment, open`}>
            <MachineBody s={s} />
            <MachineActions s={s} stopName="Shut down…" />
          </Panel>
        )
      )}
      {s.open.includes('stop') && <StopPanel s={s} name="Shut down…" />}
      {s.id === 'normal' && (
        <Panel title='Hovering "Shut down"' width="w-64">
          <span className="text-[12px] leading-relaxed text-secondary">
            Stop the 4 running agents and turn the VM off, giving its 12 GiB of memory back to your computer. Start brings it all back.
          </span>
        </Panel>
      )}
    </>
  );
}

// ---------------------------------------------------------------------------

const options = {
  a: {
    name: 'Option A — Two labelled bubbles',
    pitch:
      '"Claude" and "VM" are written on the two items. Claude shows its 5-hour % (and the reset once it matters); the VM bubble merges the VM pill, Daemon, CPU, Disk, Disk low and Free resources, showing its memory, or its state in words when it isn’t simply running. Free resources becomes "Stop agents & VM…" inside the VM popover.',
    Bar: ABar,
    Popovers: APopovers,
  },
  b: {
    name: 'Option B — Words, not numbers',
    pitch:
      'A Claude ring with "Claude 38% used", and one status sentence for the machine ("VM running · 4 agents", "Reconnecting to AgentBox…"). No machine numbers in the bar: they live in a checklist popover. The action shows up only when it is needed, named for what it is for: "Free up memory…" when memory is tight, "Start VM + 4 agents" when off.',
    Bar: BBar,
    Popovers: BPopovers,
  },
  c: {
    name: 'Option C — One labelled strip',
    pitch:
      'For people who want every number: Claude in its own orange chip with both windows (5h and week), and one strip for the machine whose segments say what they are (VM · Memory · CPU · Disk); the daemon is the VM’s dot. Free resources becomes a plain "Shut down" button beside it, "Start" when off.',
    Bar: CBar,
    Popovers: CPopovers,
  },
} as const;

export function TopbarDesigns({ option }: { option: string }) {
  const o = options[option as keyof typeof options] ?? options.a;
  const { Bar, Popovers } = o;
  return (
    <div className="grid gap-8 p-8" style={{ minHeight: '100vh', background: 'var(--color-ink)', font: '13px var(--font-sans)', width: 1340 }} data-topbar-option={option}>
      <div className="grid max-w-[1200px] gap-1.5">
        <h1 className="text-[20px] font-semibold text-title">{o.name}</h1>
        <p className="text-[13px] leading-relaxed text-muted">{o.pitch}</p>
      </div>
      {scenes.map((s) => (
        <section key={s.id} className="grid gap-2" data-scene={s.id}>
          <div className="flex items-baseline gap-3">
            <span className="text-[13px] font-medium text-secondary">{s.title}</span>
            <span className="rounded-full bg-surface-raised px-2 py-0.5 text-[10.5px] text-muted">{s.platform}</span>
          </div>
          <div className="w-[1260px] overflow-hidden rounded-xl border border-line bg-[var(--color-ink)]">
            <header className="flex h-14 items-center gap-4 border-b border-line px-5">
              <Crumbs />
              <div className="ml-auto flex items-center gap-2">
                <Bar s={s} />
                <BellButton count={s.bell} />
              </div>
            </header>
            <div className="flex min-h-10 items-start justify-end gap-4 p-4">
              <Popovers s={s} />
            </div>
          </div>
        </section>
      ))}
    </div>
  );
}
