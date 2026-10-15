import { useQuery } from '@tanstack/react-query';
import { BellOff, CheckCheck, Images, Inbox as InboxIcon, Settings } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { chatLabel } from '../lib/agentStatus';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { needsYou } from '../lib/inbox';
import { useMarkSeen } from '../lib/notifications';
import { projectLabel } from '../lib/projectName';
import { useDnd } from '../lib/notifyPrefs';
import { countLabel } from '../lib/review';
import { isUnseen, markAllSeen, useReviewing, useUnseen } from '../lib/reviewAlert';
import { cn } from '../lib/utils';
import { NoticeHistory, NoticeRow, useProjectNames } from './Notifications';
import { ReviewDialog, ReviewSection, useReadyToReview, type ReviewSession } from './ReadyToReview';
import { openSettingsSection } from './SettingsPage';
import { AIIcon } from './state';
import { Button } from './ui/button';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';

// Inbox is the top bar's one way in to what needs you, across projects
// (lib/inbox.ts): a button whose badge counts agents ready to review,
// questions, permission requests and errors, and a dot for news that waits on
// nobody. Its panel has them in sections — Needs you, Ready to review (whose
// carousel opens from here), then the Activity the bell used to hold.
// Do not disturb is in Settings → Notifications; the panel says when it's on.
export function Inbox({ onOpen, onSelect, defaultOpen = false }: { onOpen: (n: T.Notification) => void; onSelect: (view: View) => void; defaultOpen?: boolean }) {
  const t = useT();
  const [open, setOpen] = useState(defaultOpen);
  const reviewing = useReviewing();
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const notices = useQuery({ queryKey: ['notifications'], queryFn: api.notifications });
  const ready = useReadyToReview(reviewing);
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const projectName = useProjectNames();
  const markSeen = useMarkSeen();
  const { on: quiet } = useDnd();
  const unseen = useUnseen();
  // What was new in review when the panel opened stays marked while it's open.
  const [shown, setShown] = useState<ReadonlySet<string>>(new Set());
  const [session, setSession] = useState<ReviewSession | null>(null);
  const items = reviewing ? (ready.data ?? []) : [];
  const needs = needsYou({ agents: agents.data ?? [], notices: notices.data ?? [], review: items });
  const fresh = items.filter((i) => isUnseen(unseen, i)).length;
  const unread = (notices.data ?? []).filter((n) => !n.seen).length;
  const go = (view: View) => {
    setOpen(false);
    onSelect(view);
  };
  const label = needs.count ? t('inbox.buttonCount', { count: needs.count }) : needs.news ? t('inbox.buttonNews') : t('inbox.title');

  return (
    <>
      <Popover
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          // Opening it is looking at what's new in review.
          setShown(next ? unseen : new Set());
          if (next) markAllSeen();
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
            {needs.count > 0 ? (
              <span
                data-inbox-badge
                className={cn(
                  'absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-brand-500 px-1 text-[10px] font-semibold tabular-nums text-white ring-2 ring-[var(--color-ink)]',
                  fresh > 0 && 'animate-pulse',
                )}
              >
                {countLabel(needs.count)}
              </span>
            ) : (
              needs.news > 0 && <span data-inbox-news className="absolute right-0 top-0 size-2.5 rounded-full bg-brand-400 ring-2 ring-[var(--color-ink)]" />
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
                  openSettingsSection('general');
                }}
              >
                <BellOff className="size-3" />
                {t('notify.dnd.on')}
              </button>
            )}
            <Button size="sm" variant="ghost" className="ml-auto" disabled={unread === 0} onClick={() => void markSeen({ all: true })}>
              <CheckCheck />
              {t('shell.notice.markAllRead')}
            </Button>
          </div>
          <div className="max-h-[min(640px,calc(var(--radix-popover-content-available-height)-100px))] overflow-y-auto p-1.5">
            {needs.permissions.length + needs.questions.length + needs.errors.length > 0 && (
              <Section title={t('inbox.needsYou')} data="needs">
                {needs.permissions.map((agent) => (
                  <AgentRow key={agent.ref} agent={agent} project={projectLabel(agent.project, projects.data)} text={t('inbox.permission')} tone="urgent" onOpen={() => go({ kind: 'agent', ref: agent.ref, tab: 'chat' })} />
                ))}
                {needs.questions.map((n) => (
                  <NoticeRow
                    key={n.id}
                    notice={n}
                    projectName={projectName(n.project)}
                    onOpen={() => {
                      setOpen(false);
                      onOpen(n);
                    }}
                  />
                ))}
                {needs.errors.map((agent) => (
                  <AgentRow key={agent.ref} agent={agent} project={projectLabel(agent.project, projects.data)} text={chatLabel(agent).text} tone="error" onOpen={() => go({ kind: 'agent', ref: agent.ref, tab: 'chat' })} />
                ))}
              </Section>
            )}
            {reviewing && (
              <ReviewSection
                items={items}
                pending={ready.isPending}
                shown={shown}
                open={open}
                onBegin={(next) => {
                  setOpen(false);
                  setSession(next);
                }}
              />
            )}
            <Section title={t('inbox.activity')} data="activity">
              <NoticeHistory
                onOpen={(n) => {
                  setOpen(false);
                  onOpen(n);
                }}
              />
            </Section>
          </div>
          <div className="flex items-center gap-1 border-t border-line px-4 py-2 text-[12px] text-subtle">
            <span className="min-w-0 truncate">{t('shell.notice.kept')}</span>
            <Button size="sm" variant="ghost" className="ml-auto" onClick={() => go({ kind: 'media' })}>
              <Images />
              {t('shell.notice.allMedia')}
            </Button>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={t('inbox.settings')}
              title={t('inbox.settings')}
              data-inbox-settings
              onClick={() => {
                setOpen(false);
                openSettingsSection('general');
              }}
            >
              <Settings />
            </Button>
          </div>
        </PopoverContent>
      </Popover>
      <ReviewDialog session={session} items={items} onSession={setSession} onSelect={onSelect} />
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

// AgentRow is an agent waiting on you that isn't a notification: a
// permission its AI tool asks for, or an error it stopped on.
function AgentRow({ agent, project, text, tone, onOpen }: { agent: T.Agent; project: string; text: string; tone: 'urgent' | 'error'; onOpen: () => void }) {
  return (
    <button type="button" onClick={onOpen} data-inbox-agent={agent.ref} className="flex w-full min-w-0 items-start gap-3 rounded-lg px-3 py-2 text-left transition hover:bg-surface-raised">
      <AIIcon ai={agent.ai || 'claude'} className="mt-0.5 size-4 shrink-0" />
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span className={cn('truncate text-[13px] font-medium text-primary', !agent.title && 'font-mono')}>{agent.title || agent.name}</span>
        <span className="flex min-w-0 items-center gap-1.5 text-[11.5px]">
          <span className={cn('size-1.5 shrink-0 rounded-full', tone === 'urgent' ? 'bg-amber-400' : 'bg-rose-400')} />
          <span className={cn('shrink-0', tone === 'urgent' ? 'text-amber-300' : 'text-rose-300')}>{text}</span>
          <span aria-hidden className="text-faint">
            ·
          </span>
          <span className="min-w-0 truncate text-muted">{project}</span>
        </span>
      </span>
    </button>
  );
}
