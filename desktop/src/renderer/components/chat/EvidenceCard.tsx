import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, CircleAlert, CircleCheck, GitMerge, GitPullRequest, Play } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { refreshInbox } from '../../lib/inboxPages';
import { mediaUrl } from '../../lib/media';
import { pagesFor } from '../../lib/pages';
import { chapterAt, mergeBlock, proofOf, readableName } from '../../lib/review';
import { cn } from '../../lib/utils';
import { usePullMerge } from '../PullRequestsPanel';
import { markItemReviewed } from '../ReadyToReview';
import { PageIcon } from '../pages/PageThumb';
import { useProjectPages } from '../pages/usePages';
import { clock } from '../ReviewProof';
import { AIIcon } from '../state';
import { Button } from '../ui/button';
import { useAgentReview, useOpenReview } from '../v2/ReviewV2';

// What an agent finished with, in its lead's chat, where the lead tells of it
// (api.ChatItem.Finished on the notice the daemon writes the lead): its latest
// recording playing muted, the check it's on named over it; what it checked;
// three screenshots; its pull request with its checks; and Review proof and
// Merge. Review proof, the video or a screenshot opens the review on the
// agent (ReviewV2), Proof first. Merge asks first, as the Pull requests list
// does, and marks the agent reviewed. With no review (UI v1) or nothing to
// show, there's no card, as there was no notice.
export function FinishedCards({ item }: { item: T.ChatItem }) {
  return (
    <div className="mb-4 grid gap-3" data-chat-item="finished" data-chat-id={item.id}>
      {(item.finished ?? []).map((ref) => (
        <EvidenceCard key={ref} agentRef={ref} />
      ))}
    </div>
  );
}

function EvidenceCard({ agentRef }: { agentRef: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  const open = useOpenReview();
  const [project, name] = agentRef.split('/');
  const review = useAgentReview(agentRef);
  const media = useQuery({ queryKey: ['media', agentRef], queryFn: () => api.media(agentRef) });
  const pages = useProjectPages(project);
  const item = review.data;
  const pulls = useQuery({ queryKey: ['pulls', project], queryFn: () => api.projectPullRequests(project), enabled: !!item?.pr });
  const pr = (item?.pr && pulls.data?.pullRequests.find((p) => p.number === item.pr?.number)) || item?.pr;
  const merge = usePullMerge(
    project,
    (merged) => {
      toast.success(t('review.merged', { number: merged.number, agent: name, base: merged.baseBranch || pr?.baseBranch || '' }), { description: merged.title });
      if (item && !item.verdict) void markItemReviewed(queryClient, item, t, true);
      refreshInbox(queryClient);
    },
    { quiet: true },
  );
  const [time, setTime] = useState(0);
  const proof = proofOf(media.data ?? [], pagesFor(pages.data, agentRef));
  const rec = proof.recordings[0];
  if (!open || (!item && !rec && proof.screenshots.length === 0)) return null;

  const chapters = rec?.chapters ?? [];
  const at = chapterAt(chapters, time);
  const length = rec?.media.meta.duration;
  const shots = proof.screenshots.slice(0, 3);
  const more = proof.screenshots.length - shots.length;
  const blocked = pr ? mergeBlock(pr, pulls.data) : null;
  const canMerge = !!pr && pr.state === 'open' && !blocked && !merge.running.has(pr.number);
  const title = item?.title || rec?.media.agentTitle || name;
  const review_ = () => item && open({ item });
  const changes = item?.changes;

  return (
    <div className="@container overflow-hidden rounded-xl border border-line-strong bg-surface" data-evidence-card={agentRef}>
      <div className="flex flex-col @xl:flex-row">
        {rec && (
          <button type="button" onClick={review_} className="group relative aspect-[16/10] shrink-0 overflow-hidden bg-black @xl:w-[46%]" aria-label={t('evidence.review')} data-evidence-video>
            <video
              src={mediaUrl(rec.media)}
              autoPlay
              muted
              loop
              playsInline
              preload="metadata"
              onTimeUpdate={(e) => setTime(e.currentTarget.currentTime)}
              className="size-full object-cover object-top transition group-hover:scale-[1.02]"
            />
            {chapters.length > 0 && (
              <span className="absolute inset-x-2 top-2 flex gap-1" aria-hidden>
                {chapters.map((c, i) => {
                  const end = chapters[i + 1]?.at ?? length ?? c.at + 1;
                  const p = Math.max(0, Math.min(1, (time - c.at) / Math.max(end - c.at, 0.001)));
                  return (
                    <span key={`${c.at}-${i}`} className="h-0.5 flex-1 overflow-hidden rounded-full bg-white/25">
                      <span className="block h-full bg-white" style={{ width: `${p * 100}%` }} />
                    </span>
                  );
                })}
              </span>
            )}
            <span className="absolute inset-x-0 bottom-0 flex items-end gap-2 bg-gradient-to-t from-black/85 to-transparent px-3 pb-2 pt-8 text-left text-[11.5px] text-white">
              <span className="min-w-0 flex-1 truncate">{chapters[at] ? t('evidence.chapter', { title: chapters[at].title }) : readableName(rec.media.name)}</span>
              {length ? <span className="shrink-0 tabular-nums text-white/70">{clock(length)}</span> : null}
            </span>
            <span className="absolute left-1/2 top-1/2 flex size-11 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full bg-black/55 text-white opacity-0 backdrop-blur transition group-hover:opacity-100">
              <Play className="ml-0.5 size-5 fill-current" />
            </span>
          </button>
        )}
        <div className="flex min-w-0 flex-1 flex-col gap-2.5 p-3.5">
          <div className="flex min-w-0 items-center gap-2">
            <AIIcon ai={item?.ai || 'claude'} className="size-4 shrink-0" />
            <span className="min-w-0 flex-1 truncate font-medium text-title" title={title}>
              {title}
            </span>
            <span className="shrink-0 text-[11.5px] text-subtle">{t('evidence.finished', { agent: name })}</span>
          </div>
          {chapters.length > 0 ? (
            <ul className="grid gap-1 text-[12.5px]" data-evidence-chapters>
              {chapters.slice(0, 5).map((c, i) => (
                <li key={`${c.at}-${i}`} className={cn('flex min-w-0 items-center gap-1.5', i === at ? 'text-title' : 'text-secondary')}>
                  <Check className="size-3.5 shrink-0 text-emerald-400" />
                  <span className="truncate">{c.title}</span>
                </li>
              ))}
            </ul>
          ) : (
            item?.summary && <p className="line-clamp-3 text-[12.5px] leading-relaxed text-secondary">{item.summary.replace(/[*_`#>]/g, '')}</p>
          )}
          {proof.pages[0] && (
            <span className="inline-flex min-w-0 items-center gap-1.5 text-[12px] text-secondary">
              <PageIcon className="size-3.5 shrink-0 text-faint" />
              <span className="truncate">{proof.pages[0].title}</span>
            </span>
          )}
          <div className="mt-auto flex flex-wrap items-center gap-x-2.5 gap-y-1 text-[11.5px] text-subtle">
            {pr && (
              <span className={cn('inline-flex items-center gap-1', pr.state === 'merged' ? 'text-violet-300' : 'text-secondary')}>
                {pr.state === 'merged' ? <GitMerge className="size-3.5" /> : <GitPullRequest className="size-3.5" />}#{pr.number}
              </span>
            )}
            {changes && changes.files > 0 && (
              <span className="font-mono">
                <span className="text-emerald-300">+{changes.insertions}</span> <span className="text-rose-300">−{changes.deletions}</span>
              </span>
            )}
            {pr?.state === 'open' &&
              (pr.checks === 'failing' ? (
                <span className="inline-flex items-center gap-1 text-rose-300">
                  <CircleAlert className="size-3.5" />
                  {t('v2.card.checksFailing')}
                </span>
              ) : pr.checks === 'passing' ? (
                <span className="inline-flex items-center gap-1">
                  <CircleCheck className="size-3.5 text-emerald-400" />
                  {t('v2.card.checksPass')}
                </span>
              ) : null)}
          </div>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2 border-t border-line p-2.5">
        <div className="flex min-w-0 flex-1 gap-1.5">
          {shots.map((s) => (
            <button
              key={s.id}
              type="button"
              title={s.name}
              onClick={review_}
              className="h-12 w-20 shrink-0 overflow-hidden rounded-md border border-line-strong bg-black transition hover:border-line-heavy"
              data-evidence-shot
            >
              <img src={mediaUrl(s)} alt={s.name} draggable={false} className="size-full object-cover object-top" />
            </button>
          ))}
          {more > 0 && (
            <button type="button" onClick={review_} className="flex h-12 w-12 shrink-0 items-center justify-center rounded-md border border-line text-[12px] text-subtle hover:text-title">
              {t('evidence.moreShots', { count: more })}
            </button>
          )}
        </div>
        {item && (
          <Button size="sm" onClick={review_} data-evidence-review>
            {t('evidence.review')}
          </Button>
        )}
        {pr?.state === 'merged' ? (
          <span className="inline-flex h-8 items-center gap-1.5 px-2 text-[12.5px] text-violet-300" data-evidence-merged>
            <GitMerge className="size-4" />
            {t('evidence.merged')}
          </span>
        ) : (
          pr &&
          pr.state === 'open' && (
            <Button size="sm" variant="primary" disabled={!canMerge} onClick={() => merge.request(pr)} data-evidence-merge>
              <GitMerge />
              {t('evidence.merge')}
            </Button>
          )
        )}
      </div>
      {merge.dialog}
    </div>
  );
}
