import { ArrowDownLeft, ArrowUpRight, ExternalLink, Handshake } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { cn } from '../../lib/utils';
import { Markdown } from './Markdown';

// A link from a chat to another project's (openProject): App opens that
// project's view, the way Settings' links open a section of Settings.
export const openProjectEvent = 'agentbox:open-project';

export function openProject(project: string) {
  window.dispatchEvent(new CustomEvent(openProjectEvent, { detail: project }));
}

// ProjectMessageCard is a message between this project's lead and another
// project's (message_project, reply_project), or an alignment between them
// settled: the same message shows in both chats, each from its own side, with
// the other project's name and a link there.
export function ProjectMessageCard({ item }: { item: T.ChatItem }) {
  const t = useT();
  const pm = item.projectMessage!;
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const name = projects.data?.find((p) => p.name === pm.project)?.displayName || pm.project;
  const incoming = pm.direction === 'in';
  const settled = pm.kind === 'settled';
  const title = settled
    ? t('chat.project.settled', { project: name })
    : pm.kind === 'reply'
      ? t(incoming ? 'chat.project.replyFrom' : 'chat.project.replyTo', { project: name })
      : t(incoming ? 'chat.project.from' : 'chat.project.to', { project: name });
  const Icon = settled ? Handshake : incoming ? ArrowDownLeft : ArrowUpRight;
  return (
    <div
      className={cn(
        'mb-4 min-w-0 rounded-xl border px-3.5 py-2.5 text-[13px]',
        settled ? 'border-emerald-500/30 bg-emerald-500/[0.06]' : 'border-violet-500/30 bg-violet-500/[0.06]',
        !incoming && !settled && 'ml-8',
      )}
      data-chat-item="project-message"
      data-direction={pm.direction}
      data-chat-id={item.id}
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <Icon className={cn('size-4 shrink-0', settled ? 'text-emerald-300' : 'text-violet-300')} />
        <span className="min-w-0 truncate font-medium text-primary" title={t('chat.project.hop', { n: pm.hop + 1 })}>
          {title}
        </span>
        {pm.topic && !settled && <span className="min-w-0 truncate rounded-md bg-violet-500/15 px-1.5 py-0.5 text-[11.5px] text-violet-200">{t('chat.project.aligning', { topic: pm.topic })}</span>}
        {pm.expectsReply && !pm.topic && <span className="rounded-md bg-surface-strong px-1.5 py-0.5 text-[11.5px] text-tertiary">{t('chat.project.expectsReply')}</span>}
        <button
          type="button"
          className="ml-auto flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-[12px] text-subtle transition hover:bg-surface-strong hover:text-primary"
          onClick={() => openProject(pm.project)}
          data-open-project={pm.project}
        >
          {t('chat.project.open', { project: name })}
          <ExternalLink className="size-3.5" />
        </button>
      </div>
      {settled && <p className="mt-1 text-[12px] text-emerald-200/80">{pm.topic ? `${pm.topic} · ${t('chat.project.agreed')}` : t('chat.project.agreed')}</p>}
      <div className="mt-1.5 min-w-0 break-words text-secondary" data-chat-text>
        <Markdown text={item.text ?? ''} />
      </div>
    </div>
  );
}
