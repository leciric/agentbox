import { useQuery } from '@tanstack/react-query';
import { ChevronRight, CircleAlert, FolderGit2, MessageCircleQuestion } from 'lucide-react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { noticeVerb } from '../lib/notifications';
import { projectLabel } from '../lib/projectName';
import { useT } from '../lib/i18n';
import { cn, timeAgo } from '../lib/utils';

// NoticeArt is a notification's picture: a question, or an error.
function NoticeArt({ notice }: { notice: T.Notification }) {
  const error = notice.kind === 'error';
  const Icon = error ? CircleAlert : MessageCircleQuestion;
  return (
    <span className={cn('flex h-9 w-14 shrink-0 items-center justify-center rounded-md', error ? 'bg-rose-400/10 text-rose-300' : 'bg-amber-400/10 text-amber-300')}>
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
