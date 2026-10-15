import { useQuery } from '@tanstack/react-query';
import { ChevronRight, CircleAlert, CircleCheck, CircleDashed, FolderGit2, MessageCircleQuestion, Video } from 'lucide-react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { clock, kindInfo, mediaUrl } from '../lib/media';
import { byDay, noticeVerb } from '../lib/notifications';
import { projectLabel } from '../lib/projectName';
import { useT } from '../lib/i18n';
import { cn, timeAgo } from '../lib/utils';

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

export function useProjectNames(): (project: string) => string {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  return (project) => projectLabel(project, projects.data);
}

export function NoticeRow({ notice, projectName, onOpen }: { notice: T.Notification; projectName: string; onOpen: () => void }) {
  const t = useT();
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
      <span className={cn('mt-1.5 size-2 shrink-0 rounded-full', notice.seen ? 'bg-transparent' : 'bg-brand-400')} aria-label={notice.seen ? undefined : t('shell.notice.unseen')} />
    </button>
  );
}

// NoticeHistory is the inbox's Activity (components/Inbox.tsx): what agents
// did, in every project, newest first, by day. A row goes where its
// notification went (App's openNotice).
export function NoticeHistory({ onOpen }: { onOpen: (n: T.Notification) => void }) {
  const t = useT();
  const notices = useQuery({ queryKey: ['notifications'], queryFn: api.notifications });
  const projectName = useProjectNames();
  const list = notices.data ?? [];
  if (list.length === 0) {
    return <div className="px-3 py-3 text-[12.5px] text-muted">{notices.isPending ? t('common.loading') : t('shell.notice.empty')}</div>;
  }
  return (
    <>
      {byDay(list, (n) => n.at).map(([day, group]) => (
        <div key={day}>
          <div className="px-2.5 pb-1 pt-2 text-[11px] text-subtle">{day}</div>
          {group.map((n) => (
            <NoticeRow key={n.id} notice={n} projectName={projectName(n.project)} onOpen={() => onOpen(n)} />
          ))}
        </div>
      ))}
    </>
  );
}

// NoticeToast is a notification's in-app toast, the whole of which is the
// link (sonner's toast.custom).
export function NoticeToast({ notice, projectName, onOpen }: { notice: T.Notification; projectName: string; onOpen: () => void }) {
  const t = useT();
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
      <span className="shrink-0 self-center text-[11.5px] font-medium text-brand-300 [:root[data-appearance=light]_&]:text-brand-600">{t('shell.app.view')}</span>
    </button>
  );
}
