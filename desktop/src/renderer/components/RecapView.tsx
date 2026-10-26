// Your week with AgentBox (GET /v1/recap), opened from the Work page: what
// agents did in the last seven days, and a card of it to post, wide or
// square, saved or copied as a PNG (lib/recapCard.ts). The page names the
// projects, being the user's own; the card leaves them off unless ticked in.
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { ArrowLeft, Check, ChevronLeft, ChevronRight, Copy, Download, Flame, GitMerge, GitPullRequest, Moon, Sparkles, Timer, Bot, FileDiff } from 'lucide-react';
import { useEffect, useRef, useState, type ComponentType, type ReactNode } from 'react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { useLanguage, useT } from '../lib/i18n';
import { awayShare, cardFileName, compact, dayName, duration, isEmpty, modelLabel, peakLabel, weekRange, type CardFormat } from '../lib/recap';
import { cardPNG, drawCard } from '../lib/recapCard';
import { usd } from '../lib/tokens';
import { cn, errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Notice, Panel } from './ui/card';
import { Skeleton } from './ui/skeleton';
import { Switch } from './ui/switch';

export function RecapView({ onSelect }: { onSelect: (view: View) => void }) {
  const t = useT();
  const [weeks, setWeeks] = useState(0);
  const recap = useQuery({ queryKey: ['recap', weeks], queryFn: () => api.recap(weeks), placeholderData: keepPreviousData });
  const r = recap.data;
  return (
    <div className="h-full overflow-y-auto" data-recap-page>
      <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-7">
        <button type="button" onClick={() => onSelect({ kind: 'work' })} className="mb-3 inline-flex items-center gap-1.5 text-[13px] text-muted hover:text-primary">
          <ArrowLeft className="size-3.5" />
          {t('work.title')}
        </button>
        <div className="mb-6 flex flex-wrap items-end gap-3">
          <div className="min-w-0">
            <h1 className="text-2xl font-semibold tracking-tight text-title">{t('recap.title')}</h1>
            <p className="mt-1 text-sm text-muted">{t('recap.description')}</p>
          </div>
          <div className="ml-auto flex items-center gap-1 rounded-xl border border-line bg-surface-raised p-1">
            <Button variant="ghost" size="sm" className="size-8 px-0" aria-label={t('recap.previous')} title={t('recap.previous')} onClick={() => setWeeks(weeks + 1)}>
              <ChevronLeft className="size-4" />
            </Button>
            <span className="min-w-36 px-1 text-center text-[13px] font-medium tabular-nums text-secondary">{r ? weekRange(r) : '…'}</span>
            <Button variant="ghost" size="sm" className="size-8 px-0" aria-label={t('recap.next')} title={t('recap.next')} disabled={weeks === 0} onClick={() => setWeeks(weeks - 1)}>
              <ChevronRight className="size-4" />
            </Button>
          </div>
        </div>
        {recap.error && <Notice className="mb-4">{errorMessage(recap.error)}</Notice>}
        {!r ? (
          <div className="grid gap-4">
            <Skeleton className="h-48 rounded-3xl" />
            <Skeleton className="h-72 rounded-2xl" />
          </div>
        ) : (
          <Week recap={r} />
        )}
      </div>
    </div>
  );
}

function Week({ recap }: { recap: T.Recap }) {
  const t = useT();
  if (isEmpty(recap))
    return (
      <Panel className="grid place-items-center gap-2 px-6 py-16 text-center">
        <Moon className="size-6 text-brand-400" />
        <p className="text-sm text-muted">{t('recap.empty')}</p>
      </Panel>
    );
  return (
    <div className="grid gap-4">
      <Hero recap={recap} />
      <div className="grid gap-4 lg:grid-cols-[minmax(0,1.25fr)_minmax(0,1fr)]">
        <Panel className="min-w-0 p-5">
          <Days recap={recap} />
          <Hours recap={recap} />
        </Panel>
        <Panel className="min-w-0 p-5">
          <Models recap={recap} />
        </Panel>
      </div>
      <Share recap={recap} />
    </div>
  );
}

// Hero is the week's headline: hours of agent work, how much of it the user
// missed, and the counts.
function Hero({ recap }: { recap: T.Recap }) {
  const t = useT();
  const longest = recap.longest;
  const busiest = recap.busiest;
  return (
    <div className="relative overflow-hidden rounded-3xl border border-white/10 bg-[#0f0c24] p-6 text-[#f5f3ff] shadow-[0_30px_80px_-40px_rgb(124_58_237/0.6)] md:p-8">
      <div className="pointer-events-none absolute -top-40 -right-24 size-[34rem] rounded-full bg-[radial-gradient(closest-side,rgb(124_58_237/0.45),transparent)]" />
      <div className="pointer-events-none absolute -bottom-48 -left-24 size-[30rem] rounded-full bg-[radial-gradient(closest-side,rgb(79_70_229/0.35),transparent)]" />
      <div className="relative grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.35fr)] lg:items-end">
        <div className="min-w-0">
          <div className="text-[11.5px] font-bold uppercase tracking-[0.18em] text-[#c4b5fd]">{t('recap.title')}</div>
          <div className="mt-2 flex flex-wrap items-baseline gap-x-3">
            <span className="bg-gradient-to-b from-white to-[#c4b5fd] bg-clip-text text-7xl font-extrabold tracking-[-0.04em] text-transparent tabular-nums md:text-8xl">{duration(recap.workSeconds)}</span>
            <span className="text-lg font-semibold">{t('recap.card.agentWork')}</span>
          </div>
          <div className="mt-2 flex items-center gap-2 text-[15px] text-[#c4b5fd]" title={t('recap.awayHint')}>
            <Moon className="size-4 fill-[#fde68a] text-[#fde68a]" />
            {t('recap.awayYou', { hours: duration(recap.awaySeconds), share: Math.round(awayShare(recap) * 100) })}
          </div>
        </div>
        <div className="grid min-w-0 grid-cols-2 gap-2.5 sm:grid-cols-3">
          <Stat icon={Bot} label={t('recap.agents')} value={compact(recap.agents)} sub={t('recap.projects', { count: recap.projects })} />
          <Stat icon={GitPullRequest} label={t('recap.prs')} value={compact(recap.prsOpened)} sub={t('recap.merged', { count: recap.prsMerged })} />
          <Stat
            icon={FileDiff}
            label={t('recap.lines')}
            value={compact(recap.insertions + recap.deletions)}
            sub={
              <>
                <span className="text-[#6ee7b7]">+{compact(recap.insertions)}</span> <span className="text-[#fda4af]">−{compact(recap.deletions)}</span>
              </>
            }
          />
          <Stat icon={Flame} label={t('recap.streak')} value={t('recap.days', { count: recap.streak })} sub={t('recap.best', { count: recap.bestStreak })} />
          <Stat icon={Timer} label={t('recap.longest')} value={longest ? duration(longest.workSeconds) : '—'} sub={longest ? (longest.title || longest.agent) : ''} title={longest ? `${longest.project}/${longest.agent}` : undefined} />
          <Stat icon={GitMerge} label={t('recap.busiest')} value={busiest?.project ?? '—'} sub={busiest ? t('recap.busiestSub', { hours: duration(busiest.workSeconds), count: busiest.agents }) : ''} />
        </div>
      </div>
    </div>
  );
}

function Stat({ icon: Icon, label, value, sub, title }: { icon: ComponentType<{ className?: string }>; label: string; value: string; sub: ReactNode; title?: string }) {
  return (
    <div className="min-w-0 rounded-2xl border border-white/[0.09] bg-white/[0.045] px-3.5 py-3" title={title}>
      <div className="flex items-center gap-1.5 text-[11.5px] font-semibold text-[#a8a3c7]">
        <Icon className="size-3.5 shrink-0 text-[#a78bfa]" />
        <span className="truncate">{label}</span>
      </div>
      <div className="mt-1 truncate text-2xl font-bold tracking-tight tabular-nums">{value}</div>
      <div className="truncate text-[12px] text-[#8f8ab0]">{sub}</div>
    </div>
  );
}

// Days is each day's work, the part done while the user was away at its foot.
function Days({ recap }: { recap: T.Recap }) {
  const t = useT();
  const most = Math.max(...recap.days.map((d) => d.workSeconds), 1);
  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center gap-x-4 gap-y-1">
        <h2 className="text-[13px] font-semibold text-title">{t('recap.eachDay')}</h2>
        <div className="ml-auto flex items-center gap-3 text-[11.5px] text-muted">
          <span className="flex items-center gap-1.5">
            <span className="size-2 rounded-[2px] bg-brand-500" />
            {t('recap.whileAway')}
          </span>
          <span className="flex items-center gap-1.5">
            <span className="size-2 rounded-[2px] bg-brand-300" />
            {t('recap.withYou')}
          </span>
        </div>
      </div>
      <div className="grid h-44 grid-cols-7 items-end gap-2">
        {recap.days.map((d, i) => {
          const there = d.workSeconds - d.awaySeconds;
          return (
            <div key={d.date} className="group relative flex h-full flex-col items-center justify-end gap-1.5" title={t('recap.dayTip', { work: duration(d.workSeconds), away: duration(d.awaySeconds), count: d.agents })}>
              <span className="text-[11px] tabular-nums text-subtle opacity-0 transition-opacity group-hover:opacity-100">{d.workSeconds ? duration(d.workSeconds) : ''}</span>
              <div className="flex w-full max-w-11 flex-col gap-[2px] overflow-hidden rounded-t-md rounded-b-[2px]" style={{ height: `${(d.workSeconds / most) * 100}%`, minHeight: 4 }}>
                {d.workSeconds === 0 ? (
                  <div className="h-1 bg-surface-strong" />
                ) : (
                  <>
                    {there > 0 && <div className="bg-brand-300" style={{ flexGrow: there }} />}
                    {d.awaySeconds > 0 && <div className="bg-brand-500" style={{ flexGrow: d.awaySeconds }} />}
                  </>
                )}
              </div>
              <span className={cn('text-[11.5px]', i === recap.days.length - 1 ? 'font-semibold text-primary' : 'text-muted')}>{dayName(d.date)}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// Hours is the week's work by the hour of the day, one cell an hour.
function Hours({ recap }: { recap: T.Recap }) {
  const t = useT();
  const most = Math.max(...recap.hours, 1);
  const peak = peakLabel(recap);
  return (
    <div className="mt-6">
      <div className="mb-2 flex items-center gap-3">
        <h2 className="text-[13px] font-semibold text-title">{t('recap.byHour')}</h2>
        {peak && <span className="ml-auto text-[11.5px] text-muted">{t('recap.peak', { hour: peak })}</span>}
      </div>
      <div className="grid grid-cols-[repeat(24,minmax(0,1fr))] gap-[3px]">
        {recap.hours.map((s, h) => (
          <div key={h} className="h-7 rounded-[4px] bg-brand-500" style={{ opacity: s ? 0.18 + (0.82 * s) / most : 0.06 }} title={`${String(h).padStart(2, '0')}:00 · ${duration(s)}`} />
        ))}
      </div>
      <div className="mt-1 flex justify-between text-[10.5px] tabular-nums text-subtle">
        <span>00</span>
        <span>06</span>
        <span>12</span>
        <span>18</span>
        <span>24</span>
      </div>
    </div>
  );
}

function Models({ recap }: { recap: T.Recap }) {
  const t = useT();
  const most = recap.models[0]?.tokens || 1;
  return (
    <div>
      <div className="mb-3 flex items-baseline gap-3">
        <h2 className="text-[13px] font-semibold text-title">{t('recap.models')}</h2>
        <span className="ml-auto text-[11.5px] tabular-nums text-muted">
          {t('recap.tokens', { tokens: compact(recap.tokens) })}
          {recap.costUsd > 0 && ` · ${t('recap.cost', { cost: usd(recap.costUsd) })}`}
        </span>
      </div>
      {recap.models.length === 0 ? (
        <p className="text-[13px] text-subtle">{t('recap.noTokens')}</p>
      ) : (
        <ul className="grid gap-3">
          {recap.models.slice(0, 7).map((m) => (
            <li key={m.model || 'unnamed'} className="min-w-0">
              <div className="flex items-baseline justify-between gap-2 text-[13px]">
                <span className="truncate font-medium text-primary" title={m.model}>
                  {m.model ? modelLabel(m.model) : t('vm.tokens.unnamedModel')}
                </span>
                <span className="shrink-0 tabular-nums text-muted">{compact(m.tokens)}</span>
              </div>
              <div className="mt-1.5 h-1.5 rounded-full bg-surface-strong">
                <div className="h-full rounded-full bg-gradient-to-r from-brand-600 to-brand-400" style={{ width: `${Math.max(2, (m.tokens / most) * 100)}%` }} />
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// Share is the card, with its shape, whether it names projects, and Save and
// Copy. The card is drawn once its panel comes near the screen, which below
// the week's numbers may be never, and shown as the canvas it was drawn on:
// the PNG is the same pixels, so it is only encoded when saved or copied.
function Share({ recap }: { recap: T.Recap }) {
  const t = useT();
  const lang = useLanguage();
  const [format, setFormat] = useState<CardFormat>('landscape');
  const [names, setNames] = useState(false);
  const panel = useRef<HTMLDivElement>(null);
  const [near, setNear] = useState(false);
  const [card, setCard] = useState<{ canvas: HTMLCanvasElement; format: CardFormat } | null>(null);
  const [done, setDone] = useState<'' | 'saved' | 'copied'>('');
  useEffect(() => {
    if (near || !panel.current) return;
    const io = new IntersectionObserver((entries) => entries.some((e) => e.isIntersecting) && setNear(true), { rootMargin: '200px' });
    io.observe(panel.current);
    return () => io.disconnect();
  }, [near]);
  useEffect(() => {
    if (!near) return;
    let live = true;
    void drawCard(recap, { format, names }).then((canvas) => live && setCard({ canvas, format }));
    return () => {
      live = false;
    };
  }, [near, recap, format, names, lang]);
  useEffect(() => {
    if (!done) return;
    const id = setTimeout(() => setDone(''), 1800);
    return () => clearTimeout(id);
  }, [done]);
  return (
    <Panel ref={panel} className="p-5">
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <div className="min-w-0">
          <h2 className="flex items-center gap-1.5 text-[15px] font-semibold text-title">
            <Sparkles className="size-4 text-brand-400" />
            {t('recap.share.title')}
          </h2>
          <p className="mt-0.5 text-[12.5px] text-muted">{t('recap.share.description')}</p>
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          <div className="flex rounded-lg border border-line bg-surface p-0.5" role="radiogroup" aria-label={t('recap.share.shape')}>
            {(['landscape', 'square'] as const).map((f) => (
              <button
                key={f}
                type="button"
                role="radio"
                aria-checked={format === f}
                onClick={() => setFormat(f)}
                className={cn('rounded-md px-3 py-1 text-[12.5px] font-medium', format === f ? 'bg-surface-strong text-primary shadow-sm' : 'text-muted hover:text-primary')}
              >
                {t(f === 'landscape' ? 'recap.share.landscape' : 'recap.share.square')}
              </button>
            ))}
          </div>
          <label className="flex cursor-pointer items-center gap-2 rounded-lg px-2 py-1 text-[12.5px] text-secondary">
            <Switch checked={names} onCheckedChange={setNames} />
            {t('recap.share.names')}
          </label>
        </div>
      </div>
      <div className="grid place-items-center rounded-2xl border border-line bg-surface p-4 md:p-6">
        {card ? (
          <CardCanvas canvas={card.canvas} label={t('recap.share.alt')} className={cn('h-auto w-full rounded-xl shadow-[0_24px_60px_-28px_rgb(0_0_0/0.8)]', card.format === 'square' ? 'max-w-[34rem]' : 'max-w-[56rem]')} />
        ) : (
          <Skeleton className={cn('w-full rounded-xl', format === 'square' ? 'aspect-square max-w-[34rem]' : 'aspect-[1200/630] max-w-[56rem]')} />
        )}
      </div>
      <div className="mt-4 flex flex-wrap items-center gap-2">
        <p className="mr-auto text-[12px] text-subtle">{names ? t('recap.share.namesOn') : t('recap.share.namesOff')}</p>
        <Button
          size="sm"
          disabled={!card}
          onClick={() => {
            if (!card) return;
            void cardPNG(card.canvas).then((bytes) => {
              window.agentbox.copyImage(bytes);
              setDone('copied');
            });
          }}
        >
          {done === 'copied' ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {done === 'copied' ? t('recap.share.copied') : t('recap.share.copy')}
        </Button>
        <Button
          size="sm"
          variant="primary"
          disabled={!card}
          onClick={() => {
            if (!card) return;
            void cardPNG(card.canvas)
              .then((bytes) => window.agentbox.saveImage(cardFileName(recap, card.format), bytes))
              .then((saved) => saved && setDone('saved'));
          }}
        >
          {done === 'saved' ? <Check className="size-3.5" /> : <Download className="size-3.5" />}
          {done === 'saved' ? t('recap.share.saved') : t('recap.share.save')}
        </Button>
      </div>
    </Panel>
  );
}

// CardCanvas shows a drawn card, copied as it is onto a canvas of its own.
function CardCanvas({ canvas, label, className }: { canvas: HTMLCanvasElement; label: string; className: string }) {
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    ref.current?.getContext('2d')?.drawImage(canvas, 0, 0);
  }, [canvas]);
  return <canvas ref={ref} width={canvas.width} height={canvas.height} role="img" aria-label={label} className={className} />;
}
