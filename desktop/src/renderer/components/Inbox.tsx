import { useQuery } from '@tanstack/react-query';
import { BellOff, CheckCheck, CircleAlert, Inbox as InboxIcon, MessageCircleQuestion, Settings, ShieldQuestion } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { needRows, needsYou, type NeedRow } from '../lib/inbox';
import { pageItems, pageTotal, useQuestions, useReviewHead, useReviewPages } from '../lib/inboxPages';
import { useMarkSeen } from '../lib/notifications';
import { requestReveal } from '../lib/reveal';
import { projectLabel } from '../lib/projectName';
import { useDnd } from '../lib/notifyPrefs';
import { countLabel } from '../lib/review';
import { isUnseen, markAllSeen, useReviewing, useSeenReady, useUnseen } from '../lib/reviewAlert';
import { cn, timeAgo } from '../lib/utils';
import { MoreMedia } from './MediaTab';
import { useProjectNames } from './Notifications';
import { ReviewDialog, ReviewSection, type ReviewSession } from './ReadyToReview';
import { openSettingsSection } from './SettingsPage';
import { Button } from './ui/button';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';

// The card an agent's chat shows while it waits on the user: a permission
// request or a secret.
const waitingCard = '[data-chat-permission], [data-chat-credential]';

// Inbox is the top bar's one way in to what needs you, across projects
// (lib/inbox.ts): a button whose badge counts agents ready to review,
// questions, permission requests and errors (agents ready to review count
// until opened). Its panel has them in sections —
// Needs you, then Ready to review (whose carousel opens from here) with the
// Reviewed ones folded below. What agents merely did, such as finishing or
// keeping a screenshot, is not here: finished work is in the review list.
// Questions and review load a page at a time as the panel scrolls.
// Do not disturb is in Settings → Notifications; the panel says when it's on.
export function Inbox({ onOpen, onSelect, defaultOpen = false }: { onOpen: (n: T.Notification) => void; onSelect: (view: View) => void; defaultOpen?: boolean }) {
  const t = useT();
  const [open, setOpen] = useState(defaultOpen);
  const reviewing = useReviewing();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const questions = useQuestions();
  const [session, setSession] = useState<ReviewSession | null>(null);
  // The head says how many wait whether the panel is open or not; the pages
  // are read for the panel, and the carousel walking them.
  const head = useReviewHead(reviewing);
  const pages = useReviewPages(false, reviewing && (open || session !== null));
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const projectName = useProjectNames();
  const markSeen = useMarkSeen();
  const { on: quiet } = useDnd();
  const unseen = useUnseen();
  const seen = useSeenReady();
  const [filter, setFilter] = useState<'all' | 'needs' | 'review'>('all');
  // What was new in review when the panel opened stays marked while it's open.
  const [shown, setShown] = useState<ReadonlySet<string>>(new Set());
  const waiting = reviewing ? (head.data?.items ?? []) : [];
  const asked = pageItems(questions.data);
  const askedTotal = pageTotal(questions.data);
  const needs = needsYou({
    agents: agents.data ?? [],
    questions: asked,
    review: waiting,
    seen,
    totals: {
      questions: askedTotal,
      review: reviewing ? (head.data?.total ?? 0) : 0,
    },
  });
  const fresh = waiting.filter((i) => isUnseen(unseen, i)).length;
  const go = (view: View) => {
    setOpen(false);
    onSelect(view);
  };
  const label = needs.count ? t('inbox.buttonCount', { count: needs.count }) : t('inbox.title');
  const rows = needRows(needs, agents.data ?? []);
  const hasNeeds = needs.permissions.length + needs.questions.length + needs.errors.length > 0;

  const showNeeds = hasNeeds && filter !== 'review';
  const showReview = reviewing && filter !== 'needs';

  return (
    <>
      <Popover
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          // Opening it is looking at what's new in review.
          setShown(next ? unseen : new Set());
          if (next) markAllSeen();
          if (next) setFilter('all');
        }}
      >
        <PopoverTrigger asChild>
          <button
            type="button"
            aria-label={label}
            title={label}
            data-inbox
            data-inbox-count={needs.count || undefined}
            data-ready-new={fresh > 0 ? fresh : undefined}
            className={cn(
              'relative flex size-8 shrink-0 items-center justify-center rounded-full border border-line bg-surface-faint text-muted transition hover:bg-surface-raised hover:text-primary',
              open && 'bg-surface-raised text-primary',
            )}
          >
            <InboxIcon className="size-4" />
            {needs.count > 0 && (
              <span
                data-inbox-badge
                className={cn(
                  'absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-brand-500 px-1 text-[10px] font-semibold tabular-nums text-white ring-2 ring-[var(--color-ink)]',
                  fresh > 0 && 'animate-pulse',
                )}
              >
                {countLabel(needs.count)}
              </span>
            )}
          </button>
        </PopoverTrigger>
        {/* No focus on a button when it opens: Enter would press it. */}
        <PopoverContent className="w-[min(440px,calc(100vw-2rem))] p-0" data-inbox-panel onOpenAutoFocus={(e) => e.preventDefault()}>
          <div className="flex items-center gap-2 border-b border-line px-4 py-2.5">
            <span className="text-[14px] font-medium text-title">{t('inbox.title')}</span>
            {quiet && (
              <button
                type="button"
                className="flex items-center gap-1 rounded-full bg-amber-400/10 px-2 py-0.5 text-[11px] text-amber-200 transition hover:bg-amber-400/15 [:root[data-appearance=light]_&]:text-amber-700"
                data-inbox-dnd
                onClick={() => {
                  setOpen(false);
                  openSettingsSection('notifications');
                }}
              >
                <BellOff className="size-3" />
                {t('notify.dnd.on')}
              </button>
            )}
            <Button size="sm" variant="ghost" className="ml-auto" disabled={askedTotal === 0} onClick={() => void markSeen({ kind: 'question' })}>
              <CheckCheck />
              {t('shell.notice.markAllRead')}
            </Button>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={t('inbox.settings')}
              title={t('inbox.settings')}
              data-inbox-settings
              onClick={() => {
                setOpen(false);
                openSettingsSection('notifications');
              }}
            >
              <Settings />
            </Button>
          </div>
          {reviewing && (
            <div className="flex gap-1 border-b border-line-faint px-3 py-1.5" data-inbox-filter>
              {(['all', 'needs', 'review'] as const).map((f) => (
                <button
                  key={f}
                  type="button"
                  data-inbox-filter-option={f}
                  aria-pressed={filter === f}
                  onClick={() => setFilter(f)}
                  className={cn('rounded-full px-2.5 py-0.5 text-[11.5px] transition', filter === f ? 'bg-surface-raised text-primary' : 'text-muted hover:text-primary')}
                >
                  {t(`inbox.filter.${f}`)}
                </button>
              ))}
            </div>
          )}
          <div className="max-h-[min(640px,calc(var(--radix-popover-content-available-height)-60px))] overflow-y-auto p-1.5">
            {showNeeds && (
              <Section title={t('inbox.needsYou')} data="needs">
                {rows.map((row) => (
                  <NeedRowView
                    key={row.key}
                    row={row}
                    projectName={row.notice ? projectName(row.project) : projectLabel(row.project, projects.data)}
                    onOpen={() => {
                      if (row.notice) {
                        setOpen(false);
                        onOpen(row.notice);
                      } else {
                        go({ kind: 'agent', ref: row.ref, tab: 'chat' });
                        // A request waits as a card in the chat: scroll to it and flash it.
                        if (row.kind === 'permission') requestReveal({ selector: waitingCard });
                      }
                    }}
                  />
                ))}
                <MoreMedia pages={questions} />
              </Section>
            )}
            {showReview ? (
              <ReviewSection
                pages={pages}
                shown={shown}
                open={open}
                onBegin={(next) => {
                  setOpen(false);
                  setSession(next);
                }}
              />
            ) : (
              !showNeeds && <div className="px-3 py-6 text-center text-[12.5px] text-muted">{t('inbox.empty')}</div>
            )}
          </div>
        </PopoverContent>
      </Popover>
      <ReviewDialog session={session} pages={pages} onSession={setSession} onSelect={onSelect} />
    </>
  );
}

function Section({ title, data, children }: { title: string; data: string; children: ReactNode }) {
  return (
    <section className="border-b border-line-faint pb-1 last:border-b-0" data-inbox-section={data}>
      <div className="px-2.5 pb-1 pt-2 text-[11px] font-medium uppercase tracking-wide text-subtle">{title}</div>
      {children}
    </section>
  );
}

// NeedRowView is one thing that needs you, whatever it is: who and which
// project, one line of what it needs, how long ago, and one way in, to the
// agent's chat. Only an error is coloured.
function NeedRowView({ row, projectName, onOpen }: { row: NeedRow; projectName: string; onOpen: () => void }) {
  const t = useT();
  const Icon = row.kind === 'error' ? CircleAlert : row.kind === 'permission' ? ShieldQuestion : MessageCircleQuestion;
  const text =
    row.kind === 'permission'
      ? t('inbox.permission')
      : row.kind === 'error'
        ? t('inbox.error', { text: row.detail ?? '' })
        : t('inbox.asks', {
            text: row.detail || t('shell.notice.verb.question'),
          });
  return (
    <button
      type="button"
      onClick={onOpen}
      data-need={row.kind}
      data-inbox-agent={row.ref}
      className="group flex w-full min-w-0 items-start gap-3 rounded-lg px-3 py-2 text-left transition hover:bg-surface-raised"
    >
      <Icon className={cn('mt-0.5 size-4 shrink-0', row.kind === 'error' ? 'text-rose-300' : 'text-muted')} />
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span className="flex min-w-0 items-baseline gap-1.5 text-[13px]">
          <span className={cn('truncate font-medium text-primary', row.title.startsWith('agent-') && 'font-mono')}>{row.title}</span>
          <span aria-hidden className="text-faint">
            ·
          </span>
          <span className="min-w-0 truncate text-[11.5px] text-muted">{projectName}</span>
        </span>
        <span className={cn('truncate text-[12px]', row.kind === 'error' ? 'text-rose-300' : 'text-secondary')}>{text}</span>
      </span>
      <span className="flex shrink-0 flex-col items-end gap-0.5 text-[11px]">
        {row.at && <span className="tabular-nums text-subtle">{timeAgo(row.at)}</span>}
        <span className="text-brand-300 [:root[data-appearance=light]_&]:text-brand-600">{t('inbox.open')}</span>
      </span>
    </button>
  );
}
