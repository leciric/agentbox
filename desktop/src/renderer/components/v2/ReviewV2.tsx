// UI v2's review: #301's carousel (ReadyToReview.tsx) with the decision under
// its header — Merge as the one primary button, green, or disabled with the
// reason; Request changes beside it, a box that sends the message straight to
// the agent and then says "changes requested — agent working" until it
// finishes again. Opening an agent's work is reviewing it (#313), so there is
// no Mark reviewed. The carousel opens from the inbox, as in v1, and from an
// agent's own view, whose card (AgentReviewCard) says what it made.
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowRight, CircleCheck, Film, GitMerge, GitPullRequest, Globe, Images, LoaderCircle, PencilLine, Send } from 'lucide-react';
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { agentReviewPrefix, refreshInbox, useReviewPages } from '../../lib/inboxPages';
import { changesPending } from '../../lib/leadFirst';
import { useMediaCounts } from '../../lib/mediaPages';
import { cn, errorMessage } from '../../lib/utils';
import { ReviewDialog, type ReviewSession } from '../ReadyToReview';
import { Button } from '../ui/button';
import { Kbd } from '../ui/card';
import { ReviewDecisionContext, type DecisionProps } from './reviewDecision';

type OpenReview = (ref: string) => void;
const OpenReviewContext = createContext<OpenReview | null>(null);

// useOpenReview opens the carousel on an agent, under ReviewV2.
export const useOpenReview = () => useContext(OpenReviewContext);

// agentReviewKey is one agent's review item, wherever it is: ready, or
// reviewed on the work as it still is. It is refetched as the lists are
// (lib/inboxPages.ts refreshInbox, lib/events.ts), but kept out of ['review'],
// whose entries are all pages.
const agentReviewKey = (ref: string) => [agentReviewPrefix, ref];

async function agentReview(ref: string): Promise<T.ReviewItem | null> {
  const project = ref.split('/')[0];
  const q = new URLSearchParams({ limit: '200' });
  const [ready, reviewed] = await Promise.all([api.readyToReviewPage(q, project), api.reviewedPage(q, project)]);
  return ready.items.find((i) => i.ref === ref) ?? reviewed.items.find((i) => i.ref === ref) ?? null;
}

// useAgentReview is an agent's review item, or null while it has none: it
// hasn't finished, or is at work again.
export function useAgentReview(ref: string) {
  return useQuery({ queryKey: agentReviewKey(ref), queryFn: () => agentReview(ref) });
}

// ReviewV2 gives what's under it a way to open the carousel on any agent,
// and every carousel under it (the inbox's too) the v2 decision.
export function ReviewV2({ onSelect, children }: { onSelect: (view: View) => void; children: ReactNode }) {
  const queryClient = useQueryClient();
  const [session, setSession] = useState<ReviewSession | null>(null);
  const pages = useReviewPages(false, session !== null);
  const open: OpenReview = (ref) => {
    void queryClient.fetchQuery({ queryKey: agentReviewKey(ref), queryFn: () => agentReview(ref), staleTime: 0 }).then((item) => {
      if (item) setSession({ item, order: null });
    });
  };
  return (
    <ReviewDecisionContext.Provider value={DecisionBar}>
      <OpenReviewContext.Provider value={open}>
        {children}
        <ReviewDialog session={session} pages={pages} onSession={setSession} onSelect={onSelect} />
      </OpenReviewContext.Provider>
    </ReviewDecisionContext.Provider>
  );
}

// useChangesState is where a request for changes stands for an agent: none,
// the agent working on it ("asked"), or done with it since this app sent it.
function useChangesState(ref: string, sent: string | null) {
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const agent = agents.data?.find((a) => a.ref === ref);
  const pending = !!agent && changesPending(agent);
  // The agent's list says what was asked from the moment it's refetched;
  // until then, what this app just sent.
  const [sawWork, setSawWork] = useState(false);
  const working = agent?.chat === 'running' || agent?.chat === 'starting';
  useEffect(() => {
    if (sent && working) setSawWork(true);
  }, [sent, working]);
  const asked = pending || (!!sent && !sawWork);
  return {
    agent,
    asked,
    text: agent?.changesRequested || sent || '',
    updated: !!sent && sawWork && !pending && !working,
  };
}

// DecisionBar is the carousel's decision, under its header: where a request
// for changes stands, then Request changes and Merge.
function DecisionBar({ item, pr, canMerge, mergeReason, merging, onMerge }: DecisionProps) {
  const t = useT();
  const queryClient = useQueryClient();
  const [composing, setComposing] = useState(false);
  const [draft, setDraft] = useState('');
  const [sending, setSending] = useState(false);
  const [sent, setSent] = useState<string | null>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  const changes = useChangesState(item.ref, sent);
  const name = item.title || item.agent;
  useEffect(() => {
    if (composing) box.current?.focus();
  }, [composing]);
  // C opens the box, as the carousel's other keys do their part.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      if (e.metaKey || e.ctrlKey || e.altKey || target?.closest('input,textarea,[contenteditable=true]')) return;
      if (e.key.toLowerCase() === 'c' && !changes.asked) {
        e.preventDefault();
        setComposing(true);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [changes.asked]);

  const send = async () => {
    const text = draft.trim();
    if (!text || sending) return;
    setSending(true);
    try {
      await api.requestChanges(item.ref, { text, fingerprint: item.fingerprint });
      setSent(text);
      setDraft('');
      setComposing(false);
      toast(t('v2.review.sent', { agent: item.agent }));
    } catch (err) {
      toast.error(t('v2.review.sendFailed', { error: errorMessage(err) }));
    } finally {
      setSending(false);
      void queryClient.invalidateQueries({ queryKey: ['agents'] });
      refreshInbox(queryClient);
    }
  };

  const merged = pr?.state === 'merged';
  const blocked = !pr
    ? t('v2.review.noPr')
    : changes.asked
      ? t('v2.review.waitingFor', { agent: item.agent })
      : !canMerge
        ? undefined
        : mergeReason;

  return (
    <div className="border-b border-line" data-review-decision>
      {changes.asked ? (
        <Banner tone="amber" icon={<LoaderCircle className="size-4 shrink-0 animate-spin text-amber-300" />} data="requested">
          <span className="shrink-0 font-medium text-title">{t('v2.review.requested', { agent: item.agent })}</span>
          {changes.text && <span className="min-w-0 flex-1 truncate text-subtle">“{changes.text}” · {t('v2.review.backWhenDone')}</span>}
        </Banner>
      ) : changes.updated ? (
        <Banner tone="brand" icon={<CircleCheck className="size-4 shrink-0 text-brand-300" />} data="updated">
          <span className="font-medium text-title">{t('v2.review.updated', { agent: item.agent })}</span>
        </Banner>
      ) : null}
      <div className="flex flex-wrap items-center justify-end gap-2 px-5 py-2 max-sm:px-3">
        {blocked && !merged && (
          <span className="mr-auto min-w-0 text-[12px] text-subtle" data-merge-blocked>
            {t('v2.review.cantMerge', { reason: blocked })}
          </span>
        )}
        <Button
          size="sm"
          variant="ghost"
          disabled={changes.asked || merged}
          aria-expanded={composing}
          onClick={() => setComposing((c) => !c)}
          className={cn(composing && 'bg-surface-strong text-title')}
          data-review-request
        >
          <PencilLine />
          {t('v2.review.requestChanges')}
          <span className="max-sm:hidden">
            <Kbd>C</Kbd>
          </span>
        </Button>
        {merged ? (
          <span className="inline-flex h-8 items-center gap-1.5 rounded-lg px-2 text-[12.5px] font-medium text-violet-300" data-review-merged>
            <GitMerge className="size-4" />
            {t('v2.review.merged', { number: pr!.number })}
          </span>
        ) : (
          <button
            type="button"
            onClick={onMerge}
            disabled={!!blocked || !canMerge || merging}
            title={blocked}
            className="inline-flex h-8 items-center gap-1.5 rounded-lg bg-emerald-600 px-3.5 text-[13px] font-semibold text-white shadow-sm transition hover:bg-emerald-500 disabled:cursor-not-allowed disabled:bg-surface disabled:font-medium disabled:text-faint disabled:shadow-none disabled:ring-1 disabled:ring-inset disabled:ring-line-strong"
            data-review-merge={blocked ? 'blocked' : 'ready'}
          >
            {merging ? <LoaderCircle className="size-4 animate-spin" /> : <GitMerge className="size-4" />}
            {pr ? t('v2.review.mergeNumber', { number: pr.number }) : t('memory.pulls.merge')}
          </button>
        )}
      </div>
      {composing && (
        <div className="border-t border-line-faint bg-surface-faint px-5 py-3 max-sm:px-3" data-request-box>
          <div className="mb-1.5 flex items-center gap-1.5 text-[12px] font-medium text-title">
            <PencilLine className="size-3.5 text-subtle" />
            {t('v2.review.requestFrom', { agent: item.agent })}
            <span className="font-normal text-subtle max-sm:hidden">· {t('v2.review.requestHint')}</span>
          </div>
          <div className="flex items-end gap-2 max-sm:flex-col max-sm:items-stretch">
            <textarea
              ref={box}
              value={draft}
              rows={2}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                  e.preventDefault();
                  void send();
                }
              }}
              placeholder={t('v2.review.requestPlaceholder', { agent: name })}
              aria-label={t('v2.review.requestFrom', { agent: item.agent })}
              className="min-h-[56px] min-w-0 flex-1 resize-none rounded-lg border border-line bg-composer px-3 py-2 text-[13px] text-primary outline-none placeholder:text-faint focus:border-brand-400/60"
              data-request-text
            />
            <div className="flex shrink-0 items-center gap-1.5">
              <Button size="sm" variant="ghost" onClick={() => setComposing(false)}>
                {t('common.cancel')}
              </Button>
              <Button size="sm" variant="primary" disabled={!draft.trim() || sending} onClick={() => void send()} data-request-send>
                {sending ? <LoaderCircle className="animate-spin" /> : <Send />}
                {t('v2.review.send', { agent: item.agent })}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function Banner({ tone, icon, data, children }: { tone: 'amber' | 'brand'; icon: ReactNode; data: string; children: ReactNode }) {
  return (
    <div
      className={cn('flex min-w-0 items-center gap-x-3 gap-y-1 px-5 py-2 text-[12.5px] max-sm:flex-wrap max-sm:px-3', tone === 'amber' ? 'bg-amber-400/10' : 'bg-brand-500/10')}
      data-review-status={data}
    >
      {icon}
      {children}
    </div>
  );
}

// AgentReviewCard sits above a finished agent's composer: what it made, its
// pull request, and Review changes, which opens the carousel on it. While the
// agent works on changes asked for, it says so instead.
export function AgentReviewCard({ agent }: { agent: T.Agent }) {
  const t = useT();
  const open = useOpenReview();
  const review = useAgentReview(agent.ref);
  const counts = useMediaCounts({ project: agent.project, agent: agent.name });
  const item = review.data;
  const asked = changesPending(agent);
  if (!open || (!item && !asked)) return null;
  const kinds = counts.data?.kinds ?? {};
  const made = [
    kinds.screenshot ? { icon: Images, text: t('v2.card.screenshots', { count: kinds.screenshot }) } : null,
    kinds.recording ? { icon: Film, text: t('v2.card.recordings', { count: kinds.recording }) } : null,
    kinds.page ? { icon: Globe, text: t('v2.card.pages', { count: kinds.page }) } : null,
  ].filter((x) => x !== null);
  const pr = item?.pr;
  const prTone = !pr ? '' : pr.state === 'merged' ? 'text-violet-300' : pr.checks === 'failing' || pr.conflict ? 'text-rose-300' : 'text-secondary';
  return (
    <div className="mb-2 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 rounded-xl border border-line-strong bg-overlay px-3 py-2 shadow-lg backdrop-blur-xl" data-review-card={agent.ref}>
      <span className="flex min-w-0 items-center gap-2">
        {asked ? <LoaderCircle className="size-4 shrink-0 animate-spin text-subtle" /> : <CircleCheck className="size-4 shrink-0 text-subtle" />}
        <span className="shrink-0 text-[12.5px] font-medium text-title">
          {asked ? t('v2.card.requested') : item?.verdict ? t('v2.card.reviewed') : t('v2.card.ready')}
        </span>
      </span>
      <span className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2.5 gap-y-1 text-[11.5px] text-subtle">
        {made.map(({ icon: Icon, text }) => (
          <span key={text} className="inline-flex items-center gap-1">
            <Icon className="size-3.5 text-faint" />
            {text}
          </span>
        ))}
        {pr && (
          <span className={cn('inline-flex items-center gap-1', prTone)} data-review-card-pr={pr.number}>
            {pr.state === 'merged' ? <GitMerge className="size-3.5" /> : <GitPullRequest className="size-3.5" />}#{pr.number} {prLabel(t, pr)}
          </span>
        )}
      </span>
      {item && (
        <Button size="sm" variant={asked ? 'secondary' : 'primary'} className="ml-auto h-7" onClick={() => open(agent.ref)} data-review-open>
          {t('v2.card.review')}
          <ArrowRight />
        </Button>
      )}
    </div>
  );
}

function prLabel(t: ReturnType<typeof useT>, pr: T.PullRequest): string {
  if (pr.state === 'merged') return t('v2.card.prMerged');
  if (pr.state === 'closed') return t('v2.card.prClosed');
  if (pr.draft) return t('memory.pulls.draft');
  if (pr.conflict) return t('memory.pulls.conflicts');
  if (pr.checks === 'failing') return t('v2.card.checksFailing');
  if (pr.checks === 'passing') return t('v2.card.checksPass');
  return t('v2.card.prOpen');
}

