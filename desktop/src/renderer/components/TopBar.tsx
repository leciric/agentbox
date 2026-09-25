import { useQuery } from '@tanstack/react-query';
import { ChevronRight, Cpu, Gauge, HardDrive, Menu as MenuIcon, MemoryStick, TriangleAlert } from 'lucide-react';
import type { ComponentType } from 'react';
import type { View } from '../App';
import { api } from '../lib/api';
import { useConnection } from '../lib/events';
import type * as T from '../../shared/api';
import { limitTone, windowNow } from '../lib/tokens';
import { pickMeter } from '../lib/usageMeter';
import { cn, humanBytes, timeAgo, timeUntil } from '../lib/utils';
import { AgentSwitcher } from './AgentSwitcher';
import { Tip } from './ui/tooltip';

export function TopBar({
  view,
  onSelect,
  onOpenNav,
  onNewAgent,
}: {
  view: View;
  onSelect: (view: View) => void;
  onOpenNav: () => void;
  onNewAgent: (project: string) => void;
}) {
  const usage = useQuery({ queryKey: ['usage'], queryFn: api.usage });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setup, refetchInterval: 15_000 });
  const connection = useConnection();
  const host = usage.data?.host;

  const crumbs: { label: string; mono?: boolean; view?: View }[] = [];
  switch (view.kind) {
    case 'home':
      crumbs.push({ label: 'Home' });
      break;
    case 'jobs':
      crumbs.push({ label: 'Jobs' });
      break;
    case 'settings':
      crumbs.push({ label: 'Settings' });
      break;
    case 'project':
      crumbs.push({ label: view.project });
      break;
    case 'agent': {
      const [project, name] = view.ref.split('/');
      const agent = agents.data?.find((a) => a.ref === view.ref);
      crumbs.push({ label: project, view: { kind: 'project', project } }, { label: agent?.title || name, mono: !agent?.title });
    }
  }

  return (
    <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line px-3 md:gap-4 md:px-5">
      <button
        aria-label="Open the menu"
        className="-ml-1 rounded-lg p-2 text-muted transition hover:bg-surface-raised hover:text-primary md:hidden"
        onClick={onOpenNav}
      >
        <MenuIcon className="size-5" />
      </button>
      <nav className="flex min-w-0 items-center gap-1.5 text-[13px]" aria-label="Breadcrumb">
        {crumbs.map((crumb, i) => (
          <span key={i} className="flex min-w-0 items-center gap-1.5">
            {i > 0 && <ChevronRight className="size-3.5 shrink-0 text-faint" />}
            {crumb.view ? (
              <button className="-mx-1 truncate rounded-md px-1 text-muted transition hover:bg-surface-raised hover:text-primary" onClick={() => onSelect(crumb.view!)}>
                {crumb.label}
              </button>
            ) : (
              <span className={cn('truncate font-medium text-primary', crumb.mono && 'font-mono text-[12.5px]')}>{crumb.label}</span>
            )}
          </span>
        ))}
      </nav>

      <div className="lg:hidden">
        <AgentSwitcher view={view} onSelect={onSelect} onNewAgent={onNewAgent} />
      </div>

      <div className="ml-auto flex items-center gap-2">
        {setup.data && !setup.data.ready && view.kind !== 'settings' && (
          <button
            className="flex items-center gap-1.5 rounded-full bg-amber-400/10 px-2.5 py-1 text-xs font-medium text-amber-200 ring-1 ring-inset ring-amber-400/25 transition hover:bg-amber-400/15"
            onClick={() => onSelect({ kind: 'settings' })}
          >
            <TriangleAlert className="size-3.5" />
            <span className="hidden sm:inline">Finish setup</span>
          </button>
        )}
        <UsageMeter view={view} agents={agents.data ?? []} />
        {host && (
          <>
            <Meter className="hidden sm:flex" icon={Cpu} label="Host CPU" text={`${host.cpu.toFixed(0)}%`} detail={`${host.cores} cores`} fraction={host.cpu / 100} />
            <Meter
              className="hidden lg:flex"
              icon={MemoryStick}
              label="Host memory"
              text={humanBytes(host.memUsed)}
              detail={`of ${humanBytes(host.memTotal)}`}
              fraction={host.memUsed / host.memTotal}
            />
            {host.poolTotal > 0 && (
              <Meter
                className="hidden lg:flex"
                icon={HardDrive}
                label="Storage pool"
                text={humanBytes(host.poolUsed)}
                detail={`of ${humanBytes(host.poolTotal)}`}
                fraction={host.poolUsed / host.poolTotal}
              />
            )}
          </>
        )}
        <Tip label={connection.error ?? (connection.state === 'connected' ? 'Connected to the AgentBox daemon' : 'Connecting to the daemon…')}>
          <span
            className="flex items-center gap-2 rounded-full border border-line bg-surface-faint px-2.5 py-1 text-xs text-muted"
            data-connection={connection.state}
          >
            <span
              className={cn(
                'size-1.5 rounded-full',
                connection.state === 'connected' ? 'bg-emerald-400 animate-glow' : connection.state === 'connecting' ? 'bg-amber-400' : 'bg-rose-400',
              )}
            />
            <span className="hidden sm:inline">{connection.state === 'connected' ? 'Daemon' : connection.state === 'connecting' ? 'Connecting' : 'Offline'}</span>
          </span>
        </Tip>
      </div>
    </header>
  );
}

function Meter({
  icon: Icon,
  label,
  text,
  detail,
  fraction,
  className,
}: {
  icon: ComponentType<{ className?: string }>;
  label: string;
  text: string;
  detail: string;
  fraction: number;
  className?: string;
}) {
  const percent = Math.max(0, Math.min(1, fraction)) * 100;
  return (
    <Tip label={`${label}: ${text} ${detail}`}>
      <span
        className={cn('flex items-center gap-2 rounded-full border border-line bg-surface-faint py-1 pl-2 pr-2.5', className)}
        aria-label={`${label}: ${text} ${detail}`}
      >
        <Icon className="size-3.5 text-subtle" />
        <span className="font-mono text-[11px] tabular-nums text-tertiary">{text}</span>
        <span className="h-1 w-8 overflow-hidden rounded-full bg-surface-strong">
          <span
            className={cn('block h-full rounded-full', percent > 85 ? 'bg-rose-400' : percent > 65 ? 'bg-amber-400' : 'bg-gradient-to-r from-brand-400 to-sky-400')}
            style={{ width: `${Math.max(percent, 4)}%` }}
          />
        </span>
      </span>
    </Tip>
  );
}

// UsageMeter is how much of a Claude account's five-hour window is used (D85),
// beside the host's own meters: the limit every agent on the account shares.
// The account is the one what's open spends — the agent's, the project's, or
// on Home the machine's default (pickMeter) — and the tooltip names it above
// every account's readings. A reading is what the last chat on that account
// was told, so the tooltip says when that was, and a window that has reset
// since shows no number rather than one that describes a window that is over.
function UsageMeter({ view, agents }: { view: View; agents: T.Agent[] }) {
  const limits = useQuery({ queryKey: ['claudeLimits'], queryFn: api.claudeLimits, refetchInterval: 30_000 });
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const agent = view.kind === 'agent' ? agents.find((a) => a.ref === view.ref) : undefined;
  const projectName = view.kind === 'project' ? view.project : view.kind === 'agent' ? view.ref.split('/')[0] : undefined;
  const project = projectName ? projects.data?.find((p) => p.name === projectName) : undefined;
  const pick = pickMeter({ limits: limits.data ?? [], project, agent });
  const account = pick.reading;
  const five = account?.windows.find((w) => w.name === 'five_hour') ?? account?.windows[0];
  if (!account || !five) return null;
  const now = windowNow(five);
  const tone = limitTone(now);
  const percent = now === null ? 0 : Math.max(0, Math.min(1, now)) * 100;
  return (
    <Tip
      label={
        <span className="grid gap-2">
          <span className="text-muted">
            Showing <span className="font-medium text-primary">{account.account}</span>, {pick.whose}
          </span>
          <LimitsTip limits={limits.data ?? []} shown={account.account} />
        </span>
      }
    >
      <span
        className="hidden items-center gap-2 rounded-full border border-line bg-surface-faint py-1 pl-2 pr-2.5 md:flex"
        aria-label={`Claude account ${account.account}, ${five.label} window: ${now === null ? 'reset since the last reading' : `${Math.round(percent)}% used`}`}
        data-claude-meter={now === null ? 'reset' : Math.round(percent)}
        data-claude-account={account.account}
      >
        <Gauge className="size-3.5 text-subtle" />
        <span className="font-mono text-[11px] tabular-nums text-tertiary">{now === null ? '5h —' : `5h ${Math.round(percent)}%`}</span>
        <span className="h-1 w-8 overflow-hidden rounded-full bg-surface-strong">
          <span
            className={cn('block h-full rounded-full', tone === 'high' ? 'bg-rose-400' : tone === 'warn' ? 'bg-amber-400' : 'bg-gradient-to-r from-brand-400 to-sky-400')}
            style={{ width: `${Math.max(percent, 4)}%` }}
          />
        </span>
      </span>
    </Tip>
  );
}

function LimitsTip({ limits, shown }: { limits: T.ClaudeLimit[]; shown: string }) {
  return (
    <span className="grid gap-2">
      {limits.map((l) => (
        <span key={l.account} className="grid gap-0.5">
          <span className="font-medium text-primary">
            {l.account === shown ? '▸ ' : ''}Claude · {l.account}
            {l.default ? ' (default)' : ''}
          </span>
          {l.windows.map((w) => {
            const now = windowNow(w);
            return (
              <span key={w.name} className="tabular-nums text-muted">
                {w.label}: {now === null ? `reset ${timeAgo(w.resetsAt)}` : `${Math.round(now * 100)}% used, resets in ${timeUntil(w.resetsAt)}`}
              </span>
            );
          })}
          <span className="text-faint">As of its last chat, {timeAgo(l.at)}</span>
        </span>
      ))}
    </span>
  );
}
