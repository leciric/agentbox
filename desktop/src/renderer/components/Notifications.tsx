import { useQuery } from '@tanstack/react-query';
import { CircleAlert, MessageCircleQuestion } from 'lucide-react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { noticeVerb } from '../lib/notifications';
import { projectLabel } from '../lib/projectName';
import { useT } from '../lib/i18n';
import { cn } from '../lib/utils';

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
