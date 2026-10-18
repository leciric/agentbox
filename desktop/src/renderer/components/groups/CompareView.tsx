import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, CircleAlert, CircleCheck, Clock, Coins, FileDiff, FlaskConical, GitPullRequest, Images, LoaderCircle, MessageSquare, Play, Sparkles, Split, Trophy, X } from 'lucide-react';
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { formatTokens } from '../../lib/chat';
import { duration, lowest, memberSt, ready, toolName, tookMs, type MemberSt } from '../../lib/groups';
import { useT } from '../../lib/i18n';
import { mediaUrl } from '../../lib/media';
import { pagesFor } from '../../lib/pages';
import { proofOf } from '../../lib/review';
import { cn, errorMessage } from '../../lib/utils';
import { useNow } from '../../lib/useNow';
import { ConfirmDialog } from '../ConfirmDialog';
import { useProjectPages } from '../pages/usePages';
import { AIIcon } from '../state';
import { ZoomableImage } from '../ZoomableImage';
import { Button } from '../ui/button';
import { Notice } from '../ui/card';
import { Dialog, DialogContent, DialogTitle } from '../ui/dialog';
import { Switch } from '../ui/switch';

// CompareView sets a group's agents side by side (lib/groups.ts), one column
// each: its latest recording, its screenshots, how long it took, what it
// changed, its tests and checks, what it cost, and its report. The recordings
// play in step: playing, pausing or seeking one does the same to the others,
// so the same moment of each run can be watched at once. Keep this one keeps
// it (it goes on to open its pull request, then to review) and retires the
// rest, their branches kept; the lead can be asked to recommend one first.
export function CompareView({ id, onSelect }: { id: string; onSelect: (view: View) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const group = useQuery({ queryKey: ['group', id], queryFn: () => api.group(id), refetchInterval: 15_000 });
  const pages = useProjectPages(group.data?.project ?? '');
  const now = useNow(30_000);
  const [keeping, setKeeping] = useState<T.GroupMember | null>(null);
  const [closing, setClosing] = useState(false);
  const [shot, setShot] = useState<T.MediaItem | null>(null);
  const [sync, setSync] = useState(true);
  const videos = useRef(new Map<string, HTMLVideoElement>());
  // Set while this view moves the other videos, so their own events don't
  // move the rest again.
  const moving = useRef(false);

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['group', id] });
    void queryClient.invalidateQueries({ queryKey: ['groups'] });
    void queryClient.invalidateQueries({ queryKey: ['agents'] });
  };
  // The sidebar's chip and the lead's card read the list: a finish seen here
  // first is news for them too.
  const finished = group.data?.finished;
  useEffect(() => {
    if (finished !== undefined) void queryClient.invalidateQueries({ queryKey: ['groups'] });
  }, [finished, queryClient]);
  const judge = useMutation({ mutationFn: () => api.judgeGroup(id), onSuccess: refresh, onError: (e) => toast.error(errorMessage(e)) });

  const follow = useCallback(
    (from: string, what: 'play' | 'pause' | 'seek') => {
      if (!sync || moving.current) return;
      const source = videos.current.get(from);
      if (!source) return;
      moving.current = true;
      for (const [key, v] of videos.current) {
        if (key === from) continue;
        // Only a video that is somewhere else is moved: the ones already
        // there would answer with events of their own, and drift back.
        const at = Math.min(source.currentTime, v.duration || source.currentTime);
        if (what !== 'pause' && Math.abs(v.currentTime - at) > 0.25) v.currentTime = at;
        if (what === 'play' && v.paused) void v.play().catch(() => {});
        if (what === 'pause' && !v.paused) v.pause();
      }
      // The events these cause arrive after this returns.
      setTimeout(() => (moving.current = false), 300);
    },
    [sync],
  );
  const playAll = () => {
    moving.current = true;
    for (const v of videos.current.values()) {
      v.currentTime = 0;
      void v.play().catch(() => {});
    }
    setTimeout(() => (moving.current = false), 300);
  };

  if (group.isError) {
    return (
      <div className="p-6">
        <Notice>{errorMessage(group.error)}</Notice>
      </div>
    );
  }
  const g = group.data;
  if (!g) {
    return (
      <div className="flex h-full items-center justify-center text-subtle">
        <LoaderCircle className="size-5 animate-spin" />
      </div>
    );
  }
  const settled = !!g.settledAt;
  const took = g.members.map((m) => tookMs(m, now));
  const fastest = lowest(g.members.map((m, i) => (m.finishedAt ? took[i] : undefined)));
  const cheapest = lowest(g.members.map((m) => m.costUsd));
  const smallest = lowest(g.members.map((m) => (m.changes ? m.changes.insertions + m.changes.deletions : undefined)));
  const status = settled
    ? g.kept
      ? t('group.status.kept', { agent: g.kept })
      : t('group.status.closed')
    : ready(g)
      ? t('group.status.ready')
      : t('group.status.working', { finished: g.finished, count: g.members.length });
  const recommended = g.members.find((m) => m.agent && m.agent === g.recommended);

  return (
    <div className="flex h-full min-h-0 flex-col overflow-y-auto" data-compare={g.id}>
      <div className="flex flex-col gap-3 border-b border-line px-5 py-4">
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
          <Split className="size-5 shrink-0 text-brand-300" />
          <h1 className="min-w-0 truncate text-[17px] font-semibold text-title">{g.title}</h1>
          <span className={cn('rounded-full px-2 py-0.5 text-[11.5px] font-medium', ready(g) ? 'bg-brand-500/15 text-brand-200' : settled ? 'bg-surface-strong text-subtle' : 'bg-surface-strong text-secondary')} data-compare-status>
            {status}
          </span>
          <span className="text-[12px] text-faint">{t('group.byWho', { who: g.byLead ? t('group.byLead') : t('group.byYou'), count: g.members.length })}</span>
          <div className="ml-auto flex flex-wrap items-center gap-2">
            <label className="flex items-center gap-2 text-[12.5px] text-secondary">
              <Switch checked={sync} onCheckedChange={setSync} data-compare-sync />
              {t('group.sync')}
            </label>
            <Button size="sm" variant="ghost" onClick={playAll} data-compare-play-all>
              <Play />
              {t('group.playAll')}
            </Button>
            {!settled && (
              <>
                <Button size="sm" disabled={judge.isPending || g.judging} onClick={() => judge.mutate()} data-compare-judge>
                  {judge.isPending || g.judging ? <LoaderCircle className="animate-spin" /> : <Sparkles />}
                  {g.judging ? t('group.judging') : g.recommended ? t('group.judgeAgain') : t('group.judge')}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setClosing(true)} data-compare-close>
                  <X />
                  {t('group.close')}
                </Button>
              </>
            )}
          </div>
        </div>
        <p className="line-clamp-2 max-w-4xl text-[13px] leading-relaxed text-secondary" title={g.task}>
          {g.task}
        </p>
        {g.judging && !settled && (
          <p className="flex max-w-4xl items-center gap-2 text-[12.5px] text-subtle" data-compare-judging>
            <LoaderCircle className="size-3.5 shrink-0 animate-spin" />
            {t('group.judgingHint')}
            <button type="button" className="font-medium text-brand-200 hover:underline" onClick={() => onSelect({ kind: 'project', project: g.project, tab: 'chat' })}>
              {t('group.openLead')}
            </button>
          </p>
        )}
        {recommended && (
          <div className="flex max-w-4xl items-start gap-2.5 rounded-xl border border-brand-400/30 bg-brand-500/[0.07] px-3.5 py-2.5 text-[13px]" data-compare-recommendation={recommended.agent}>
            <Sparkles className="mt-0.5 size-4 shrink-0 text-brand-300" />
            <p className="min-w-0 leading-relaxed text-primary">
              <span className="font-medium text-title">{t('group.recommends', { label: recommended.label, agent: recommended.agent ?? '' })}</span> {g.recommendation}
            </p>
          </div>
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-x-auto p-4">
        <div className="grid gap-3" style={{ gridTemplateColumns: `repeat(${g.members.length}, minmax(270px, 1fr))` }}>
          {g.members.map((m, i) => (
            <MemberColumn
              key={m.label}
              group={g}
              member={m}
              st={memberSt(g, m)}
              took={took[i]}
              fastest={!!m.finishedAt && took[i] === fastest}
              cheapest={!!m.costUsd && m.costUsd === cheapest}
              smallest={!!m.changes && m.changes.insertions + m.changes.deletions === smallest}
              recommended={m === recommended}
              pages={pages.data}
              register={(v) => {
                if (v) videos.current.set(m.label, v);
                else videos.current.delete(m.label);
              }}
              follow={(what) => follow(m.label, what)}
              onShot={setShot}
              onKeep={() => setKeeping(m)}
              onOpen={() => m.ref && onSelect({ kind: 'agent', ref: m.ref })}
            />
          ))}
        </div>
      </div>
      <ConfirmDialog
        open={!!keeping}
        onOpenChange={(open) => !open && setKeeping(null)}
        title={t('group.keep.title', { label: keeping?.label ?? '', agent: keeping?.agent ?? '' })}
        description={t('group.keep.description', { count: g.members.filter((m) => m.agent && m !== keeping).length })}
        confirmLabel={t('group.keep.confirm')}
        onConfirm={async () => {
          await api.keepInGroup(g.id, keeping?.agent ?? '');
          toast.success(t('group.keep.done', { agent: keeping?.agent ?? '' }));
          refresh();
        }}
      />
      <ConfirmDialog
        open={closing}
        onOpenChange={setClosing}
        title={t('group.closeConfirm.title')}
        description={t('group.closeConfirm.description', { count: g.members.filter((m) => m.agent).length })}
        confirmLabel={t('group.close')}
        destructive
        onConfirm={async () => {
          await api.closeGroup(g.id);
          refresh();
        }}
      />
      <Dialog open={!!shot} onOpenChange={(open) => !open && setShot(null)}>
        <DialogContent className="max-w-5xl">
          <DialogTitle className="truncate">{shot?.name}</DialogTitle>
          {shot && <ZoomableImage src={mediaUrl(shot)} alt={shot.name} />}
        </DialogContent>
      </Dialog>
    </div>
  );
}

const stTone: Record<MemberSt, string> = {
  creating: 'bg-surface-strong text-subtle',
  failed: 'bg-rose-500/15 text-rose-200',
  working: 'bg-sky-500/15 text-sky-200',
  finished: 'bg-emerald-500/15 text-emerald-200',
  kept: 'bg-brand-500/20 text-brand-100',
  retired: 'bg-surface-strong text-faint',
};

function MemberColumn({
  group,
  member: m,
  st,
  took,
  fastest,
  cheapest,
  smallest,
  recommended,
  pages,
  register,
  follow,
  onShot,
  onKeep,
  onOpen,
}: {
  group: T.Group;
  member: T.GroupMember;
  st: MemberSt;
  took?: number;
  fastest: boolean;
  cheapest: boolean;
  smallest: boolean;
  recommended: boolean;
  pages?: T.Artifacts;
  register: (v: HTMLVideoElement | null) => void;
  follow: (what: 'play' | 'pause' | 'seek') => void;
  onShot: (item: T.MediaItem) => void;
  onKeep: () => void;
  onOpen: () => void;
}) {
  const t = useT();
  const media = useQuery({ queryKey: ['media', m.ref], queryFn: () => api.media(m.ref!), enabled: !!m.ref, refetchInterval: st === 'working' ? 20_000 : false });
  const proof = proofOf(media.data ?? [], pagesFor(pages, m.ref ?? ''));
  const rec = proof.recordings[0];
  const shots = proof.screenshots.slice(-4).reverse();
  const canKeep = !group.settledAt && !!m.agent && m.state !== 'archived';
  const pr = m.pr;
  const tests = pr?.checks ? pr.checks : m.reportStatus;

  return (
    <div
      className={cn('flex min-w-0 flex-col overflow-hidden rounded-xl border bg-surface', st === 'kept' ? 'border-brand-400/60 shadow-[0_0_0_3px_rgb(139_92_246/0.15)]' : recommended ? 'border-brand-400/40' : 'border-line-strong', st === 'retired' && 'opacity-70')}
      data-compare-member={m.label}
      data-compare-agent={m.agent}
      data-compare-st={st}
    >
      <div className="flex min-w-0 items-center gap-2 border-b border-line px-3 py-2.5">
        <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-surface-strong font-mono text-[13px] font-semibold text-title">{m.label}</span>
        <AIIcon ai={m.ai} className="size-4 shrink-0 text-subtle" />
        <span className="grid min-w-0 flex-1">
          <span className="truncate text-[13px] font-medium text-title" title={m.model || toolName(m.ai)}>
            {toolName(m.ai)}
            {m.model && <span className="text-secondary"> · {m.model}</span>}
          </span>
          <span className="truncate font-mono text-[11px] text-faint">{m.agent ?? t('group.member.notYet')}</span>
        </span>
        {recommended && (
          <span className="inline-flex shrink-0 items-center gap-1 text-[11px] font-medium text-brand-200" title={t('group.recommendedTip')}>
            <Sparkles className="size-3.5" />
          </span>
        )}
        <span className={cn('shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium', stTone[st])} data-compare-member-st={st}>
          {st === 'working' && m.busy === false && !m.finishedAt ? t('group.st.idle') : t(`group.st.${st}`)}
        </span>
      </div>

      <div className="relative aspect-[16/10] w-full bg-black">
        {rec ? (
          <video
            ref={register}
            src={mediaUrl(rec.media)}
            controls
            muted
            playsInline
            preload="metadata"
            onPlay={() => follow('play')}
            onPause={() => follow('pause')}
            onSeeked={() => follow('seek')}
            className="size-full object-contain"
            data-compare-video={m.label}
          />
        ) : shots[0] ? (
          <button type="button" className="size-full" onClick={() => onShot(shots[0])}>
            <img src={mediaUrl(shots[0])} alt={shots[0].name} draggable={false} className="size-full object-contain" />
          </button>
        ) : (
          <div className="flex size-full items-center justify-center px-4 text-center text-[12px] text-faint">
            {st === 'creating' || st === 'working' ? t('group.member.noProofYet') : st === 'failed' ? (m.error ?? '') : t('group.member.noProof')}
          </div>
        )}
      </div>
      {shots.length > (rec ? 0 : 1) && (
        <div className="flex gap-1.5 border-b border-line p-2">
          {(rec ? shots : shots.slice(1)).map((s) => (
            <button key={s.id} type="button" title={s.name} onClick={() => onShot(s)} className="h-11 w-16 shrink-0 overflow-hidden rounded-md border border-line-strong bg-black hover:border-line-heavy" data-compare-shot>
              <img src={mediaUrl(s)} alt={s.name} draggable={false} className="size-full object-cover object-top" />
            </button>
          ))}
        </div>
      )}

      <dl className="grid gap-1.5 px-3 py-2.5 text-[12.5px]">
        <Stat icon={Clock} label={t('group.stat.time')} best={fastest ? t('group.best.fastest') : undefined}>
          {took !== undefined ? (m.finishedAt ? duration(took) : t('group.stat.sofar', { time: duration(took) })) : '—'}
        </Stat>
        <Stat icon={FileDiff} label={t('group.stat.changes')} best={smallest ? t('group.best.smallest') : undefined}>
          {m.changes && m.changes.files > 0 ? (
            <span className="font-mono">
              {t('group.stat.files', { count: m.changes.files })} <span className="text-emerald-300">+{m.changes.insertions}</span> <span className="text-rose-300">−{m.changes.deletions}</span>
              {m.changes.dirty && <span className="text-amber-300"> · {t('group.stat.uncommitted')}</span>}
            </span>
          ) : m.changes ? (
            t('group.stat.noChanges')
          ) : (
            '—'
          )}
        </Stat>
        <Stat icon={FlaskConical} label={t('group.stat.tests')}>
          {tests === 'passing' || tests === 'done' ? (
            <span className="inline-flex items-center gap-1 text-emerald-300">
              <CircleCheck className="size-3.5" />
              {pr?.checks ? t('group.tests.checksPass') : t('group.tests.reportDone')}
            </span>
          ) : tests === 'failing' || tests === 'failed' || tests === 'blocked' ? (
            <span className="inline-flex items-center gap-1 text-rose-300">
              <CircleAlert className="size-3.5" />
              {pr?.checks ? t('group.tests.checksFail') : t(tests === 'blocked' ? 'group.tests.reportBlocked' : 'group.tests.reportFailed')}
            </span>
          ) : tests === 'pending' ? (
            t('group.tests.checksPending')
          ) : tests === 'partial' ? (
            <span className="text-amber-300">{t('group.tests.reportPartial')}</span>
          ) : (
            '—'
          )}
        </Stat>
        {pr && (
          <Stat icon={GitPullRequest} label={t('group.stat.pr')}>
            <a href={pr.url} target="_blank" rel="noreferrer" className="hover:text-title hover:underline">
              #{pr.number} · {pr.state}
            </a>
          </Stat>
        )}
        <Stat icon={Coins} label={t('group.stat.cost')} best={cheapest ? t('group.best.cheapest') : undefined}>
          {m.tokens ? (
            <span className="font-mono">
              {formatTokens(m.tokens)}
              {m.costUsd ? ` · $${m.costUsd.toFixed(2)}` : ''}
            </span>
          ) : (
            '—'
          )}
        </Stat>
        <Stat icon={Images} label={t('group.stat.media')}>
          {m.media ? t('group.stat.mediaCount', { count: m.media }) : '—'}
        </Stat>
      </dl>
      {m.reportSummary && (
        <p className="line-clamp-5 border-t border-line px-3 py-2.5 text-[12px] leading-relaxed text-secondary" title={m.reportSummary} data-compare-report>
          {m.reportSummary.replace(/[*_`#>]/g, '')}
        </p>
      )}
      <div className="mt-auto flex items-center gap-2 border-t border-line p-2.5">
        <Button size="sm" variant="ghost" disabled={!m.ref} onClick={onOpen} data-compare-open={m.label}>
          <MessageSquare />
          {t('group.member.open')}
        </Button>
        {st === 'kept' ? (
          <span className="ml-auto inline-flex h-8 items-center gap-1.5 px-2 text-[12.5px] font-medium text-brand-200">
            <Trophy className="size-4" />
            {t('group.st.kept')}
          </span>
        ) : (
          canKeep && (
            <Button size="sm" variant={recommended ? 'primary' : 'secondary'} className="ml-auto" onClick={onKeep} data-compare-keep={m.label}>
              <Check />
              {t('group.member.keep')}
            </Button>
          )
        )}
      </div>
    </div>
  );
}

function Stat({ icon: Icon, label, best, children }: { icon: typeof Clock; label: string; best?: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 items-center gap-2">
      <Icon className="size-3.5 shrink-0 text-faint" />
      <dt className="w-20 shrink-0 text-subtle">{label}</dt>
      <dd className="flex min-w-0 flex-1 items-center gap-1.5 truncate text-primary">
        <span className="min-w-0 truncate">{children}</span>
        {best && <span className="shrink-0 rounded bg-emerald-500/15 px-1.5 text-[10.5px] font-medium text-emerald-200">{best}</span>}
      </dd>
    </div>
  );
}
