import { useQuery } from '@tanstack/react-query';
import { Bell, CheckCheck, ChevronRight, CircleAlert, CircleCheck, CircleDashed, FolderGit2, Images, MessageCircleQuestion, Video } from 'lucide-react';
import { useState } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { clock, kindInfo, mediaUrl } from '../lib/media';
import { byDay, noticeVerb, useMarkSeen } from '../lib/notifications';
import { projectLabel } from '../lib/projectName';
import { cn, timeAgo } from '../lib/utils';
import { Button } from './ui/button';
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover';

const statusArt: Record<string, { icon: typeof CircleCheck; tone: string }> = {
  done: { icon: CircleCheck, tone: 'text-emerald-400 bg-emerald-400/10' },
  partial: { icon: CircleDashed, tone: 'text-amber-300 bg-amber-400/10' },
  blocked: { icon: CircleAlert, tone: 'text-rose-300 bg-rose-400/10' },
  failed: { icon: CircleAlert, tone: 'text-rose-300 bg-rose-400/10' },
};

// NoticeArt is a notification's picture: the screenshot itself, a
// recording's length, or what kind of news it is.
function NoticeArt({ notice }: { notice: T.Notification }) {
  const media = notice.media;
  if (media && !media.removed) {
    return (
      <span className="relative flex h-9 w-14 shrink-0 items-center justify-center overflow-hidden rounded-md border border-line bg-well">
        {media.kind === 'screenshot' ? (
          <img src={mediaUrl(media)} alt="" loading="lazy" className="size-full object-cover object-top" />
        ) : (
          <>
            <Video className="size-4 text-rose-300" />
            {media.meta.duration ? (
              <span className="absolute bottom-0.5 right-0.5 rounded bg-black/70 px-1 font-mono text-[9px] text-white">{clock(media.meta.duration)}</span>
            ) : null}
          </>
        )}
      </span>
    );
  }
  const { icon: Icon, tone } =
    notice.kind === 'question'
      ? { icon: MessageCircleQuestion, tone: 'text-amber-300 bg-amber-400/10' }
      : notice.kind === 'media'
        ? { icon: kindInfo(media?.kind ?? '').icon, tone: 'text-faint bg-surface' }
        : (statusArt[notice.status ?? ''] ?? statusArt.done);
  return (
    <span className={cn('flex h-9 w-14 shrink-0 items-center justify-center rounded-md', tone)}>
      <Icon className="size-4" />
    </span>
  );
}

function Headline({ notice }: { notice: T.Notification }) {
  return (
    <>
      <span className="font-mono text-[12.5px]">{notice.agent}</span> {noticeVerb(notice)}
    </>
  );
}

function useProjectNames(): (project: string) => string {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  return (project) => projectLabel(project, projects.data);
}

function NoticeRow({ notice, projectName, onOpen }: { notice: T.Notification; projectName: string; onOpen: () => void }) {
  return (
    <button
      onClick={onOpen}
      data-notice={notice.kind}
      className={cn('group flex w-full items-start gap-3 rounded-lg px-2.5 py-2 text-left transition hover:bg-surface', !notice.seen && 'bg-brand-500/[0.06]')}
    >
      <NoticeArt notice={notice} />
      <span className="min-w-0 flex-1">
        <span className={cn('flex items-center gap-1.5 text-[13px]', notice.seen ? 'text-secondary' : 'font-medium text-title')}>
          <span className="truncate">
            <Headline notice={notice} />
          </span>
          <span className="ml-auto shrink-0 text-[11px] font-normal text-subtle">{timeAgo(notice.at)}</span>
        </span>
        {notice.text && <span className="mt-0.5 block truncate text-[12px] text-muted">{notice.text}</span>}
        <span className="mt-1 flex min-w-0 items-center gap-1 text-[11px] text-subtle">
          <FolderGit2 className="size-3 shrink-0" />
          <span className="max-w-[45%] shrink-0 truncate">{projectName}</span>
          <ChevronRight className="size-3 shrink-0 text-faint" />
          <span className="truncate">{notice.title || notice.agent}</span>
        </span>
      </span>
      <span className={cn('mt-1.5 size-2 shrink-0 rounded-full', notice.seen ? 'bg-transparent' : 'bg-brand-400')} aria-label={notice.seen ? undefined : 'Unseen'} />
    </button>
  );
}

// NotificationBell is the top bar's history of what agents did, in every
// project, newest first, with a count of those you haven't seen. A row goes
// where its notification went (App's openNotice).
export function NotificationBell({ onOpen, onAllMedia, defaultOpen = false }: { onOpen: (n: T.Notification) => void; onAllMedia: () => void; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  const notices = useQuery({ queryKey: ['notifications'], queryFn: api.notifications });
  const projectName = useProjectNames();
  const markSeen = useMarkSeen();
  const list = notices.data ?? [];
  const unseen = list.filter((n) => !n.seen).length;
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          aria-label={unseen ? `Notifications, ${unseen} new` : 'Notifications'}
          data-bell
          className={cn(
            'relative flex size-8 shrink-0 items-center justify-center rounded-full border border-line bg-surface-faint text-muted transition hover:bg-surface-raised hover:text-primary',
            open && 'bg-surface-raised text-primary',
          )}
        >
          <Bell className="size-4" />
          {unseen > 0 && (
            <span className="absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-brand-500 px-1 text-[10px] font-semibold tabular-nums text-white ring-2 ring-[var(--color-ink)]">
              {unseen > 99 ? '99+' : unseen}
            </span>
          )}
        </button>
      </PopoverTrigger>
      {/* No focus on Mark all read when it opens: Enter would mark them all. */}
      <PopoverContent className="w-[min(420px,calc(100vw-2rem))] p-0" data-bell-history onOpenAutoFocus={(e) => e.preventDefault()}>
        <div className="flex items-center gap-2 border-b border-line px-4 py-3">
          <span className="text-[14px] font-medium text-title">Notifications</span>
          {unseen > 0 && <span className="rounded-full bg-brand-500/15 px-2 py-0.5 text-[11px] font-medium text-brand-300 [:root[data-appearance=light]_&]:text-brand-600">{unseen} new</span>}
          <Button size="sm" variant="ghost" className="ml-auto" disabled={unseen === 0} onClick={() => void markSeen({ all: true })}>
            <CheckCheck />
            Mark all read
          </Button>
        </div>
        <div className="max-h-[min(560px,70vh)] overflow-y-auto p-1.5">
          {list.length === 0 && (
            <div className="px-4 py-10 text-center text-[13px] text-muted">
              {notices.isPending ? 'Loading…' : 'When an agent finishes, asks you something or keeps a screenshot or recording, it shows here.'}
            </div>
          )}
          {byDay(list, (n) => n.at).map(([day, group]) => (
            <div key={day}>
              <div className="px-2.5 pb-1 pt-2.5 text-[11px] font-medium uppercase tracking-wide text-subtle">{day}</div>
              {group.map((n) => (
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
            </div>
          ))}
        </div>
        <div className="flex items-center border-t border-line px-4 py-2 text-[12px] text-subtle">
          Kept for 30 days
          <Button
            size="sm"
            variant="ghost"
            className="ml-auto"
            onClick={() => {
              setOpen(false);
              onAllMedia();
            }}
          >
            <Images />
            All media
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}

// NoticeToast is a notification's in-app toast, the whole of which is the
// link (sonner's toast.custom).
export function NoticeToast({ notice, projectName, onOpen }: { notice: T.Notification; projectName: string; onOpen: () => void }) {
  return (
    <button
      onClick={onOpen}
      data-toast-notice={notice.kind}
      className="flex w-[356px] max-w-[calc(100vw-2rem)] items-start gap-3 rounded-xl border border-line-strong bg-overlay p-3 text-left shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur transition hover:border-line-vivid"
    >
      <NoticeArt notice={notice} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[13px] font-medium text-title">
          <Headline notice={notice} />
        </span>
        {notice.text && <span className="mt-0.5 block truncate text-[12px] text-muted">{notice.text}</span>}
        <span className="mt-1 block truncate text-[11px] text-subtle">{[projectName, notice.title].filter(Boolean).join(' · ')}</span>
      </span>
      <span className="shrink-0 self-center text-[11.5px] font-medium text-brand-300 [:root[data-appearance=light]_&]:text-brand-600">View</span>
    </button>
  );
}
