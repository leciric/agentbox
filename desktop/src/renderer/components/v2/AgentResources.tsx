// AgentResources is the panel's Resources view of an agent: its machine's
// CPU, memory and disk, live, each with the last ten minutes as a sparkline.
// The daemon samples the agent only while a window shows this view: the view
// puts it in the window's presence lease (lib/presence.ts), and the samples
// come as events (lib/events.ts) onto the history fetched when it opened. A
// sample taken while nobody watched doesn't exist, so a gap in the line is
// time the view was closed.
import { useQuery } from '@tanstack/react-query';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { usePresence } from '../../lib/presence';
import { runs } from '../../lib/resourceLine';
import { cn, humanBytes, humanRate } from '../../lib/utils';

export function AgentResources({ agent }: { agent: T.Agent }) {
  const t = useT();
  usePresence({ project: agent.project, agent: agent.ref, resources: true });
  const history = useQuery({ queryKey: ['agentResources', agent.ref], queryFn: () => api.agentResources(agent.ref) });
  const h = history.data;
  const points = h?.points ?? [];
  const last = points[points.length - 1];
  const windowMs = (h?.windowSeconds ?? 600) * 1000;
  const end = last ? new Date(last.at).getTime() : Date.now();
  const minutes = Math.round(windowMs / 60_000);
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-3" data-agent-resources={agent.ref}>
      <Metric
        label={t('resources.cpu')}
        value={last ? `${last.cpu.toFixed(0)}%` : '—'}
        series={[{ values: points.map((p) => [p.at, p.cpu]), className: 'text-brand-300' }]}
        floor={100}
        end={end}
        windowMs={windowMs}
        data="cpu"
      />
      <Metric
        label={t('resources.memory')}
        value={last ? humanBytes(last.memory) : '—'}
        detail={last && last.swap > 0 ? t('resources.swap', { size: humanBytes(last.swap) }) : undefined}
        series={[{ values: points.map((p) => [p.at, p.memory]), className: 'text-emerald-300' }]}
        floor={512 * 1024 * 1024}
        end={end}
        windowMs={windowMs}
        data="memory"
      />
      <Metric
        label={t('resources.disk')}
        value={last ? `${t('resources.read')} ${humanRate(last.diskRead)} · ${t('resources.write')} ${humanRate(last.diskWrite)}` : '—'}
        series={[
          { values: points.map((p) => [p.at, p.diskRead]), className: 'text-brand-300' },
          { values: points.map((p) => [p.at, p.diskWrite]), className: 'text-amber-300' },
        ]}
        legend={[
          { label: t('resources.read'), className: 'bg-brand-300' },
          { label: t('resources.write'), className: 'bg-amber-300' },
        ]}
        floor={1024 * 1024}
        end={end}
        windowMs={windowMs}
        data="disk"
      />
      <p className="px-1 text-caption text-subtle">{points.length === 0 && !history.isError ? t('resources.waiting') : t('resources.note', { minutes })}</p>
    </div>
  );
}

type Series = { values: [string, number][]; className: string };

function Metric({ label, value, detail, series, legend, floor, end, windowMs, data }: { label: string; value: string; detail?: string; series: Series[]; legend?: { label: string; className: string }[]; floor: number; end: number; windowMs: number; data: string }) {
  return (
    <section className="rounded-xl border border-line bg-surface-faint px-3 py-2.5" data-resource={data}>
      <div className="flex min-w-0 items-baseline gap-2">
        <span className="text-ui font-medium text-title">{label}</span>
        {legend && (
          <span className="flex items-center gap-2 text-caption text-subtle">
            {legend.map((l) => (
              <span key={l.label} className="flex items-center gap-1">
                <span className={cn('inline-block size-1.5 rounded-full', l.className)} />
                {l.label}
              </span>
            ))}
          </span>
        )}
        <span className="ml-auto truncate font-mono text-ui tabular-nums text-secondary" data-resource-value>
          {value}
        </span>
      </div>
      {detail && <div className="text-right text-caption text-subtle">{detail}</div>}
      <TrendLine series={series} floor={floor} end={end} windowMs={windowMs} />
    </section>
  );
}

// TrendLine draws each series over the window ending at end, placed by time,
// so a stretch nobody watched shows as a break rather than being squeezed out.
// Its scale is the largest value shown, never under floor, so an idle agent's
// line lies flat at the bottom instead of magnifying noise.
export function TrendLine({ series, floor, end, windowMs, height = 44 }: { series: Series[]; floor: number; end: number; windowMs: number; height?: number }) {
  const w = 300;
  const start = end - windowMs;
  const max = Math.max(floor, ...series.flatMap((s) => s.values.map(([, v]) => v)));
  return (
    <svg viewBox={`0 0 ${w} ${height}`} preserveAspectRatio="none" className="mt-2 block h-11 w-full" aria-hidden>
      <line x1="0" x2={w} y1={height - 0.5} y2={height - 0.5} className="stroke-line" strokeWidth="1" vectorEffect="non-scaling-stroke" />
      {series.map((s, i) => (
        <g key={i} className={s.className}>
          {runs(s.values, start, windowMs).map((run, j) => {
            const pts = run.map(([at, v]) => [((at - start) / windowMs) * w, height - 1 - (Math.max(0, v) / max) * (height - 3)] as const);
            const line = pts.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join(' ');
            const area = `${pts[0][0].toFixed(1)},${height} ${line} ${pts[pts.length - 1][0].toFixed(1)},${height}`;
            return (
              <g key={j}>
                {series.length === 1 && <polygon points={area} fill="currentColor" opacity="0.12" />}
                <polyline points={line} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />
              </g>
            );
          })}
        </g>
      ))}
    </svg>
  );
}
