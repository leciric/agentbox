import { useQuery } from '@tanstack/react-query';
import { Check, Copy, RefreshCw } from 'lucide-react';
import { Fragment, useState, type ReactNode } from 'react';
import * as T from '../../shared/api';
import { api } from '../lib/api';
import {
  attrText,
  buckets,
  formatMs,
  periods,
  resourceWindows,
  spanAgent,
  spanDetails,
  type Bucket,
  type Period,
  type ResourceWindow,
} from '../lib/diagnostics';
import { formatNumber, useT } from '../lib/i18n';
import { cn, errorMessage, humanBytes, timeAgo } from '../lib/utils';
import { useVMPower } from '../lib/vm';
import { HostFootprint } from './ResourceControls';
import { Button } from './ui/button';
import { Notice, Panel } from './ui/card';
import { Tip } from './ui/tooltip';

// Settings → Diagnostics, after T3Code's: what the daemon (and the app, its
// spans named "app …") has been slow at or failed at over a period, when it
// stalled, and what the VM and each agent took of CPU and memory. Read only:
// nothing here acts on a process. A span's details copy as text for a bug
// report.

// Lists show this many rows until "Show all".
const folded = 10;

export function DiagnosticsPanel({ version }: { version?: string }) {
  const t = useT();
  const [period, setPeriod] = useState<Period>('30m');
  const spans = useQuery({
    queryKey: ['diagnostics', 'spans', period],
    queryFn: () => api.diagnosticSpans(period),
    refetchInterval: 30_000,
  });
  const stalls = useQuery({
    queryKey: ['diagnostics', 'stalls', period],
    queryFn: () => api.diagnosticStalls(period),
    refetchInterval: 30_000,
  });
  const refresh = () => {
    void spans.refetch();
    void stalls.refetch();
  };
  const stats = spans.data?.stats ?? [];
  const stallList = stalls.data?.stalls ?? [];
  const total = stats.reduce((n, s) => n + s.count, 0);
  const errors = stats.reduce((n, s) => n + s.errors, 0);

  return (
    <div className="grid min-w-0 gap-4" data-diagnostics>
      <div className="flex flex-wrap items-center gap-2">
        <Segmented
          label={t('diagnostics.period')}
          options={periods.map((p) => ({ id: p, label: t(`diagnostics.period.${p}`) }))}
          value={period}
          onChange={setPeriod}
        />
        <Tip label={t('diagnostics.refresh')}>
          <Button size="icon-sm" variant="ghost" aria-label={t('diagnostics.refresh')} onClick={refresh}>
            <RefreshCw className={cn(spans.isFetching && 'animate-spin')} />
          </Button>
        </Tip>
      </div>
      {spans.error && <Notice>{errorMessage(spans.error)}</Notice>}
      {(spans.data?.dropped ?? 0) > 0 && (
        <Notice tone="warning">{t('diagnostics.dropped', { count: spans.data!.dropped })}</Notice>
      )}

      <Panel className="grid grid-cols-2 divide-x divide-line-faint overflow-hidden sm:grid-cols-4">
        <Stat label={t('diagnostics.stat.spans')} value={spans.data ? formatNumber(total) : '…'} />
        <Stat label={t('diagnostics.stat.failures')} value={spans.data ? formatNumber(errors) : '…'} tone={errors > 0 ? 'danger' : undefined} />
        <Stat label={t('diagnostics.stat.operations')} value={spans.data ? formatNumber(stats.length) : '…'} />
        <Stat
          label={t('diagnostics.stat.stalls')}
          value={stalls.data ? formatNumber(stallList.length) : '…'}
          tone={stallList.length > 0 ? 'warning' : undefined}
        />
      </Panel>

      <Section title={t('diagnostics.slowest.title')} description={t('diagnostics.slowest.description')}>
        <SpanTable spans={spans.data?.slowest} loading={spans.isPending} empty={t('diagnostics.slowest.empty')} version={version} />
      </Section>

      <Section title={t('diagnostics.top.title')} description={t('diagnostics.top.description')}>
        {stats.length > 0 ? (
          <Table
            headers={[
              t('diagnostics.col.operation'),
              t('diagnostics.col.count'),
              t('diagnostics.col.errors'),
              t('diagnostics.col.p50'),
              t('diagnostics.col.p95'),
              t('diagnostics.col.max'),
              t('diagnostics.col.total'),
            ]}
            numeric={[1, 2, 3, 4, 5, 6]}
          >
            {/* In the daemon's order: the busiest, by total time, first. */}
            {stats.map((s) => (
                <tr key={s.name} data-diagnostics-op={s.name}>
                  <Cell name>{s.name}</Cell>
                  <Cell num>{formatNumber(s.count)}</Cell>
                  <Cell num className={s.errors > 0 ? 'text-rose-400' : 'text-subtle'}>
                    {formatNumber(s.errors)}
                  </Cell>
                  <Cell num>{formatMs(s.p50Ms)}</Cell>
                  <Cell num>{formatMs(s.p95Ms)}</Cell>
                  <Cell num className="text-muted">
                    {formatMs(s.maxMs)}
                  </Cell>
                  <Cell num className="text-muted">
                    {formatMs(s.totalMs)}
                  </Cell>
                </tr>
              ))}
          </Table>
        ) : (
          <Empty>{spans.isPending ? t('common.loading') : t('diagnostics.top.empty')}</Empty>
        )}
      </Section>

      <Section title={t('diagnostics.failures.title')} description={t('diagnostics.failures.description')}>
        <SpanTable spans={spans.data?.failures} loading={spans.isPending} empty={t('diagnostics.failures.empty')} version={version} errors />
      </Section>

      <Section title={t('diagnostics.stalls.title')} description={t('diagnostics.stalls.description')}>
        {stalls.error ? (
          <Empty>{errorMessage(stalls.error)}</Empty>
        ) : stallList.length > 0 ? (
          <Table
            headers={[t('diagnostics.col.kind'), t('diagnostics.col.operation'), t('diagnostics.col.detail'), t('diagnostics.col.duration'), t('diagnostics.col.when'), '']}
            numeric={[3]}
          >
            {stallList.map((s, i) => (
              <tr key={`${s.id ?? ''}:${s.start}:${i}`}>
                <Cell className="whitespace-nowrap text-amber-300/90">{s.attrs?.stall === undefined ? '' : attrText(s.attrs.stall)}</Cell>
                <Cell name>{s.name}</Cell>
                <Cell className="break-words text-muted">{stallDetail(s)}</Cell>
                <Cell num>{formatMs(s.durationMs)}</Cell>
                <Cell className="whitespace-nowrap text-subtle">{timeAgo(s.start)}</Cell>
                <td className="w-px px-2 py-1 align-top">
                  <CopySpan span={s} version={version} />
                </td>
              </tr>
            ))}
          </Table>
        ) : (
          <Empty>{stalls.isPending ? t('common.loading') : t('diagnostics.stalls.empty')}</Empty>
        )}
      </Section>

      <PackageCacheSize />

      <OpenFiles />
      <ResourceHistory />
    </div>
  );
}

// OpenFiles is virtiofsd's open files against its limit (the shared home on a
// Linux host, internal/hostvm/chv/share.go), and the daemon's and the VM's.
function OpenFiles() {
  const t = useT();
  const files = useQuery({ queryKey: ['diagnostics', 'files'], queryFn: () => api.diagnosticFiles(), refetchInterval: 5_000 });
  const d = files.data;
  const of = (c?: T.FileCount) => (!c ? '…' : c.limit > 0 ? t('diagnostics.files.of', { open: formatNumber(c.open), limit: formatNumber(c.limit) }) : formatNumber(c.open));
  const share = d?.share;
  const used = share && share.limit > 0 ? share.open / share.limit : 0;
  return (
    <section className="grid min-w-0 gap-2" data-diagnostics-files>
      <header className="min-w-0">
        <h3 className="text-[13px] font-semibold text-title">{t('diagnostics.files.title')}</h3>
        <p className="text-[12px] text-muted">{t('diagnostics.files.description')}</p>
      </header>
      {files.error && <Notice>{errorMessage(files.error)}</Notice>}
      {d?.waiting && <Notice tone="warning">{t('diagnostics.files.waiting')}</Notice>}
      <Panel className="grid grid-cols-1 divide-y divide-line-faint overflow-hidden sm:grid-cols-3 sm:divide-x sm:divide-y-0">
        <Stat
          label={t('diagnostics.files.share')}
          value={!d ? '…' : share ? of(share) : t('diagnostics.files.none')}
          tone={used >= 0.7 ? 'danger' : used >= 0.4 ? 'warning' : undefined}
        />
        <Stat label={t('diagnostics.files.daemon')} value={of(d?.daemon)} />
        <Stat label={t('diagnostics.files.system')} value={of(d?.system)} />
      </Panel>
      {share && (
        <p className="text-[12px] text-muted" data-diagnostics-trims>
          {share.trims > 0 && share.lastTrim
            ? t('diagnostics.files.trims', { count: formatNumber(share.trims), when: timeAgo(share.lastTrim), pinned: formatNumber(share.pinned) })
            : t('diagnostics.files.noTrims')}
        </p>
      )}
    </section>
  );
}

// stallDetail is a stall's attributes but its kind, as "key value" pairs.
function stallDetail(s: T.TraceSpan): string {
  return Object.entries(s.attrs ?? {})
    .filter(([k]) => k !== 'stall')
    .map(([k, v]) => `${k} ${attrText(v)}`)
    .join(' · ');
}

function Segmented<Id extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: { id: Id; label: string }[];
  value: Id;
  onChange: (id: Id) => void;
}) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex rounded-lg border border-line bg-surface-faint p-0.5">
      {options.map((o) => (
        <button
          key={o.id}
          type="button"
          role="radio"
          aria-checked={o.id === value}
          data-segment={o.id}
          onClick={() => onChange(o.id)}
          className={cn(
            'rounded-md px-2.5 py-1 text-[12px] font-medium tabular-nums transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50',
            o.id === value ? 'bg-surface-strong text-title shadow-sm' : 'text-muted hover:text-primary',
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: 'warning' | 'danger' }) {
  return (
    <div className="min-w-0 px-4 py-3">
      <div className="truncate text-[11px] font-medium uppercase tracking-wider text-subtle">{label}</div>
      <div
        className={cn(
          'mt-1 truncate font-mono text-lg font-semibold tabular-nums text-title',
          tone === 'warning' && 'text-amber-400',
          tone === 'danger' && 'text-rose-400',
        )}
      >
        {value}
      </div>
    </div>
  );
}

function Section({ title, description, action, children }: { title: string; description?: string; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="grid min-w-0 gap-2">
      <header className="flex flex-wrap items-end justify-between gap-2">
        <div className="min-w-0">
          <h3 className="text-[13px] font-semibold text-title">{title}</h3>
          {description && <p className="text-[12px] text-muted">{description}</p>}
        </div>
        {action}
      </header>
      <Panel className="min-w-0 overflow-hidden">{children}</Panel>
    </section>
  );
}

// PackageCacheSize is what the shared package caches (pnpm, npm, Go, Playwright…)
// hold on the VM's disk, by tool, against the cap set under Settings → Agents.
function PackageCacheSize() {
  const t = useT();
  const cache = useQuery({ queryKey: ['diagnostics', 'package-cache'], queryFn: api.diagnosticPackageCache, refetchInterval: 60_000 });
  const data = cache.data;
  return (
    <Section
      title={t('diagnostics.pkgcache.title')}
      description={data?.on === false ? t('diagnostics.pkgcache.off') : t('diagnostics.pkgcache.description', { used: humanBytes(data?.bytes ?? 0), max: humanBytes(data?.maxBytes ?? 0) })}
    >
      {cache.error ? (
        <Empty>{errorMessage(cache.error)}</Empty>
      ) : data && data.parts.length > 0 ? (
        <Table headers={[t('diagnostics.pkgcache.tool'), t('diagnostics.pkgcache.size')]} numeric={[1]}>
          {data.parts.map((p) => (
            <tr key={p.name}>
              <Cell name>{p.name}</Cell>
              <Cell num>{humanBytes(p.bytes)}</Cell>
            </tr>
          ))}
        </Table>
      ) : (
        <Empty>{cache.isPending ? t('common.loading') : t('diagnostics.pkgcache.empty')}</Empty>
      )}
    </Section>
  );
}

function Table({ headers, numeric = [], children }: { headers: string[]; numeric?: number[]; children: ReactNode }) {
  return (
    <div className="min-w-0 overflow-x-auto">
      <table className="w-full text-left text-[12px]">
        <thead className="border-b border-line-faint text-[11px] uppercase tracking-wider text-subtle">
          <tr>
            {headers.map((h, i) => (
              <th key={h} className={cn('whitespace-nowrap px-3 py-2 first:pl-4 last:pr-4 font-medium', numeric.includes(i) && 'text-right')}>
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-line-faint">{children}</tbody>
      </table>
    </div>
  );
}

function Cell({ name, num, className, children }: { name?: boolean; num?: boolean; className?: string; children: ReactNode }) {
  return (
    <td
      className={cn(
        'px-3 py-2 align-top first:pl-4 last:pr-4',
        // A route name breaks inside a path only when it must, so the table
        // fits its card rather than scrolling sideways.
        name && '[overflow-wrap:anywhere] font-mono text-[12px] text-primary',
        num && 'whitespace-nowrap text-right font-mono tabular-nums',
        className,
      )}
    >
      {name && typeof children === 'string' ? breakable(children) : children}
    </td>
  );
}

// breakable lets a route name wrap after each slash, so a word is split only
// when even one of its segments can't fit.
function breakable(name: string): ReactNode {
  return name.split('/').map((part, i) => (
    <Fragment key={i}>
      {i > 0 && '/'}
      {i > 0 && <wbr />}
      {part}
    </Fragment>
  ));
}

function Empty({ children }: { children: ReactNode }) {
  return <div className="px-4 py-4 text-[12px] text-muted">{children}</div>;
}

// SpanTable lists spans, the slowest or the latest failures, folded to the
// first few, each with a button that copies its details.
function SpanTable({ spans, loading, empty, version, errors }: { spans?: T.TraceSpan[]; loading: boolean; empty: string; version?: string; errors?: boolean }) {
  const t = useT();
  const [all, setAll] = useState(false);
  if (!spans || spans.length === 0) return <Empty>{loading ? t('common.loading') : empty}</Empty>;
  const shown = all ? spans : spans.slice(0, folded);
  return (
    <>
      <Table
        headers={[
          t('diagnostics.col.operation'),
          ...(errors ? [t('diagnostics.col.error')] : [t('diagnostics.col.agent')]),
          t('diagnostics.col.duration'),
          t('diagnostics.col.when'),
          '',
        ]}
        numeric={[2]}
      >
        {shown.map((s, i) => (
          <tr key={`${s.id ?? ''}:${s.start}:${i}`} className="group">
            <Cell name>
              {s.source === T.SpanSourceApp && <span className="mr-1.5 rounded bg-surface-strong px-1 py-px font-sans text-[10px] uppercase text-subtle">{t('diagnostics.source.app')}</span>}
              {breakable(s.name)}
            </Cell>
            {errors ? (
              <Cell className="max-w-[28rem] break-words text-rose-300/90">{s.error}</Cell>
            ) : (
              <Cell className="whitespace-nowrap text-muted">{spanAgent(s)}</Cell>
            )}
            <Cell num>{formatMs(s.durationMs)}</Cell>
            <Cell className="whitespace-nowrap text-subtle">{timeAgo(s.start)}</Cell>
            <td className="w-px px-2 py-1 align-top">
              <CopySpan span={s} version={version} />
            </td>
          </tr>
        ))}
      </Table>
      {spans.length > folded && (
        <button
          type="button"
          onClick={() => setAll(!all)}
          className="w-full border-t border-line-faint px-4 py-2 text-left text-[12px] text-muted hover:bg-surface-raised hover:text-primary"
        >
          {all ? t('diagnostics.showFewer') : t('diagnostics.showAll', { count: spans.length })}
        </button>
      )}
    </>
  );
}

function CopySpan({ span, version }: { span: T.TraceSpan; version?: string }) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  const label = copied ? t('common.copied') : t('diagnostics.copy');
  return (
    <Tip label={label} side="left">
      <Button
        size="icon-sm"
        variant="ghost"
        aria-label={label}
        data-copy-span
        onClick={() => {
          window.agentbox.copyText(spanDetails(span, version));
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        }}
      >
        {copied ? <Check className="text-emerald-400" /> : <Copy />}
      </Button>
    </Tip>
  );
}

// ResourceHistory charts CPU and memory for the VM and each agent over a
// window, in bars of a stretch each: the average filled, the peak behind it.
// The daemon samples through Incus every 30 seconds and keeps an hour; a
// machine without Incus (an agent's own) has nothing to chart.
function ResourceHistory() {
  const t = useT();
  const [win, setWin] = useState<ResourceWindow>('15m');
  const resources = useQuery({
    queryKey: ['diagnostics', 'resources', win],
    queryFn: () => api.diagnosticResources(win),
    refetchInterval: 30_000,
  });
  const spec = resourceWindows.find((w) => w.id === win)!;
  const bucketMs = Math.max(spec.bucketMs, (resources.data?.intervalSeconds ?? 0) * 1000);
  const vm = resources.data?.vm ?? [];
  const agents = (resources.data?.agents ?? []).filter((a) => a.samples.length > 0);
  const lastVM = vm.at(-1);
  const power = useVMPower().data;
  return (
    <Section
      title={t('diagnostics.resources.title')}
      description={t('diagnostics.resources.description')}
      action={
        <Segmented
          label={t('diagnostics.resources.window')}
          options={resourceWindows.map((w) => ({ id: w.id, label: t(`diagnostics.period.${w.id}`) }))}
          value={win}
          onChange={setWin}
        />
      }
    >
      {resources.error ? (
        <Empty>{errorMessage(resources.error)}</Empty>
      ) : vm.length === 0 && agents.length === 0 ? (
        resources.isPending ? (
          <Empty>{t('common.loading')}</Empty>
        ) : (
          <div className="grid gap-1 px-4 py-5 text-center" data-diagnostics-resources-empty>
            <span className="text-[13px] font-medium text-title">{t('diagnostics.resources.empty')}</span>
            <span className="text-[12px] text-muted">{t('diagnostics.resources.emptyHint')}</span>
          </div>
        )
      ) : (
        <div className="divide-y divide-line-faint">
          {vm.length > 0 && (
            <SeriesRow
              id="vm"
              name={t('diagnostics.resources.vm')}
              samples={vm}
              cpu={(s) => s.cpu}
              memory={(s) => s.memUsed}
              cpuMax={100}
              memMax={lastVM!.memTotal}
              windowMs={spec.ms}
              bucketMs={bucketMs}
            />
          )}
          {vm.length > 0 && power && <HostFootprint vm={power} className="block px-4 py-2" />}
          {agents.map((a) => (
            <SeriesRow
              key={a.ref}
              id={a.ref}
              name={a.ref}
              mono
              samples={a.samples}
              cpu={(s) => s.cpu}
              memory={(s) => s.memory}
              memMax={lastVM?.memTotal}
              windowMs={spec.ms}
              bucketMs={bucketMs}
            />
          ))}
        </div>
      )}
    </Section>
  );
}

// SeriesRow is one machine's two charts. The VM's CPU is a percentage of all
// its cores; an agent's is 100 to a core, so its scale grows past 100.
function SeriesRow<S extends { at: string }>({
  id,
  name,
  mono,
  samples,
  cpu,
  memory,
  cpuMax,
  memMax,
  windowMs,
  bucketMs,
}: {
  id: string;
  name: string;
  mono?: boolean;
  samples: S[];
  cpu: (s: S) => number;
  memory: (s: S) => number;
  cpuMax?: number;
  memMax?: number;
  windowMs: number;
  bucketMs: number;
}) {
  const t = useT();
  const last = samples.at(-1);
  const cpuBars = buckets(samples, cpu, windowMs, bucketMs);
  const memBars = buckets(samples, memory, windowMs, bucketMs);
  return (
    <div className="grid min-w-0 gap-2 px-4 py-3" data-diagnostics-series={id}>
      <div className="flex min-w-0 items-baseline justify-between gap-3">
        <span className={cn('truncate text-[13px] font-medium text-title', mono && 'font-mono text-[12px]')}>{name}</span>
        {last && (
          <span className="shrink-0 font-mono text-[11px] tabular-nums text-subtle">
            {t('diagnostics.resources.now', { cpu: `${Math.round(cpu(last))}%`, memory: humanBytes(memory(last)) })}
          </span>
        )}
      </div>
      <div className="grid min-w-0 gap-3 md:grid-cols-2">
        <Chart
          label={t('diagnostics.resources.cpu')}
          data={cpuBars}
          max={cpuMax ?? Math.max(100, ...cpuBars.map((b) => b.peak))}
          format={(v) => `${Math.round(v)}%`}
          tone="bg-sky-400"
        />
        <Chart
          label={t('diagnostics.resources.memory')}
          data={memBars}
          max={memMax || Math.max(1, ...memBars.map((b) => b.peak))}
          format={humanBytes}
          tone="bg-violet-400"
        />
      </div>
    </div>
  );
}

function Chart({ label, data, max, format, tone }: { label: string; data: Bucket[]; max: number; format: (v: number) => string; tone: string }) {
  const t = useT();
  const peak = Math.max(0, ...data.map((b) => b.peak));
  return (
    <div className="min-w-0">
      <div className="mb-1 flex items-baseline justify-between text-[11px] text-subtle">
        <span>{label}</span>
        <span className="font-mono tabular-nums">{t('diagnostics.resources.peak', { value: format(peak) })}</span>
      </div>
      <div className="flex h-16 items-end gap-px overflow-hidden rounded-md bg-surface-faint p-1" role="img" aria-label={label}>
        {data.map((b) => (
          <div
            key={b.start}
            className="relative h-full min-w-px flex-1"
            title={b.samples ? t('diagnostics.resources.bar', { avg: format(b.avg), peak: format(b.peak), time: new Date(b.start).toLocaleTimeString() }) : undefined}
          >
            {b.samples > 0 && (
              <>
                <div className={cn('absolute inset-x-0 bottom-0 rounded-t-[1px] opacity-25', tone)} style={{ height: `${Math.max(2, (b.peak / max) * 100)}%` }} />
                <div className={cn('absolute inset-x-0 bottom-0 rounded-t-[1px] opacity-80', tone)} style={{ height: `${Math.max(2, (b.avg / max) * 100)}%` }} />
              </>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
