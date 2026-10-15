// TalkToProject is v2's way into #310's cross-project messaging from a
// lead's chat: a quiet button in the project's header that opens a picker of
// the other projects, each with its icon. Picking one puts a message to this
// lead in the composer, mentioning the other project, to ask its lead
// something or to align with it on a topic; the user finishes the sentence
// and sends it, and the lead uses message_project or align_with_project.
import { useQuery } from '@tanstack/react-query';
import { ArrowLeftRight, Handshake, MessageCircleQuestion } from 'lucide-react';
import { useState } from 'react';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { composeIn } from '../../lib/drafts';
import { useT } from '../../lib/i18n';
import { projectLabel } from '../../lib/projectName';
import { ProjectIcon } from '../ProjectIcon';
import { Popover, PopoverContent, PopoverTrigger } from '../ui/popover';

export function TalkToProject({ project, leadRef }: { project: string; leadRef: string }) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const [open, setOpen] = useState(false);
  const others = (projects.data ?? []).filter((p) => p.name !== project);
  if (others.length === 0) return null;
  const write = (other: T.Project, how: 'ask' | 'align') => {
    setOpen(false);
    composeIn(leadRef, t(how === 'ask' ? 'v2.talk.askText' : 'v2.talk.alignText', { project: `@${other.name}` }));
  };
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="inline-flex h-7 items-center gap-1.5 rounded-full border border-line px-2.5 text-[12px] text-subtle transition hover:border-line-heavy hover:bg-surface hover:text-primary data-[state=open]:border-line-heavy data-[state=open]:text-title"
          data-v2-talk
        >
          <ArrowLeftRight className="size-3.5" />
          {t('v2.talk.button')}
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-[340px] max-w-[calc(100vw-1rem)] p-1.5" data-v2-talk-picker>
        <div className="px-2 pb-1.5 pt-1">
          <div className="text-[13px] font-medium text-title">{t('v2.talk.title')}</div>
          <p className="mt-0.5 text-[11.5px] leading-snug text-subtle">{t('v2.talk.hint')}</p>
        </div>
        <div className="grid max-h-72 gap-0.5 overflow-y-auto">
          {others.map((p) => (
            <div key={p.name} className="group flex min-w-0 items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-surface-raised" data-v2-talk-project={p.name}>
              <ProjectIcon project={p} />
              <span className="min-w-0 flex-1 truncate text-[13px] text-secondary">{projectLabel(p)}</span>
              <button type="button" onClick={() => write(p, 'ask')} className="inline-flex shrink-0 items-center gap-1 rounded-md px-1.5 py-1 text-[11.5px] text-subtle hover:bg-surface-strong hover:text-title" data-v2-talk-ask={p.name}>
                <MessageCircleQuestion className="size-3.5" />
                {t('v2.talk.ask')}
              </button>
              <button type="button" onClick={() => write(p, 'align')} className="inline-flex shrink-0 items-center gap-1 rounded-md px-1.5 py-1 text-[11.5px] text-subtle hover:bg-surface-strong hover:text-title" data-v2-talk-align={p.name}>
                <Handshake className="size-3.5" />
                {t('v2.talk.align')}
              </button>
            </div>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
}
