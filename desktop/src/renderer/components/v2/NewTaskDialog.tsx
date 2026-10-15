import * as DialogPrimitive from '@radix-ui/react-dialog';
import { useQueries, useQuery } from '@tanstack/react-query';
import { CornerDownLeft, House, Search } from 'lucide-react';
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import * as A from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { useFocusReturn } from '../../lib/focusReturn';
import { useT } from '../../lib/i18n';
import { closeNewTask, digitPick, requestComposerFocus, taskTargets, useNewTaskOpen, type TaskTarget } from '../../lib/newTask';
import { projectLabel } from '../../lib/projectName';
import { buildLists } from '../../lib/sidebar';
import { cn, timeAgo } from '../../lib/utils';
import { useNow } from '../../lib/useNow';
import { ProjectIcon } from '../ProjectIcon';

// NewTaskDialog is UI v2's New task: where a task goes, picked from the
// keyboard — type to search, ↑↓ and Enter, or a digit for one of the first
// nine — and then that chat, a project's or the Home chat for none, opens
// with its composer focused for the message. It makes nothing: the project's
// chat is the one its page has.
export function NewTaskDialog({ onSelect }: { onSelect: (view: View) => void }) {
  const t = useT();
  const open = useNewTaskOpen();
  const focusReturn = useFocusReturn();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const sections = useQuery({ queryKey: ['sections'], queryFn: api.sections });
  const ordered = useMemo(() => buildLists(projects.data ?? [], sections.data ?? []).flatMap((l) => l.projects), [projects.data, sections.data]);
  // Each project's chat, read for the sidebar already: when it was last
  // written in puts the recent ones first.
  const chats = useQueries({ queries: ordered.map((p) => ({ queryKey: ['projectChat', p.name], queryFn: () => api.projectChat(p.name) })) });
  const byName = useMemo(() => new Map(ordered.map((p) => [p.name, p])), [ordered]);
  const now = useNow(60_000);
  const [query, setQuery] = useState('');
  const [highlighted, setHighlighted] = useState(0);
  const list = useRef<HTMLDivElement>(null);

  const home: TaskTarget = { name: '', label: t('v2.newTask.home'), home: true };
  const targets = taskTargets(
    ordered.map((p, i) => ({ name: p.name, label: projectLabel(p), lastAt: chats[i]?.data?.last?.at })),
    home,
    query,
  );
  useEffect(() => {
    if (open) setQuery('');
  }, [open]);
  useEffect(() => setHighlighted(0), [query, open]);
  useEffect(() => {
    list.current?.querySelector(`[data-task-index="${highlighted}"]`)?.scrollIntoView({ block: 'nearest' });
  }, [highlighted]);

  const choose = (target: TaskTarget) => {
    closeNewTask();
    if (target.home) {
      requestComposerFocus(`${A.HomeProject}/${A.LeadName}`);
      onSelect({ kind: 'homeChat' });
    } else {
      requestComposerFocus(`${target.name}/${A.LeadName}`);
      onSelect({ kind: 'project', project: target.name, tab: 'chat' });
    }
  };
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    const digit = digitPick(targets, e.key, query, e.altKey);
    if (digit) {
      e.preventDefault();
      choose(digit);
      return;
    }
    if (targets.length === 0) return;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      setHighlighted((i) => (i + (e.key === 'ArrowDown' ? 1 : targets.length - 1)) % targets.length);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      choose(targets[Math.min(highlighted, targets.length - 1)]);
    }
  };

  return (
    <DialogPrimitive.Root open={open} onOpenChange={(next) => !next && closeNewTask()}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 animate-fade-in bg-scrim backdrop-blur-[3px]" />
        <DialogPrimitive.Content
          {...focusReturn}
          aria-describedby={undefined}
          className="fixed left-1/2 top-[18vh] z-50 w-[calc(100vw-2rem)] max-w-xl -translate-x-1/2 animate-pop-in overflow-hidden rounded-2xl border border-line-strong bg-modal shadow-[0_40px_120px_-30px_var(--ab-shadow-deep)] focus:outline-none"
          data-new-task
        >
          <DialogPrimitive.Title className="sr-only">{t('v2.newTask.title')}</DialogPrimitive.Title>
          <div className="flex items-center gap-2.5 border-b border-line px-4 py-3">
            <Search className="size-4 shrink-0 text-faint" />
            <input
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={onKeyDown}
              placeholder={t('v2.newTask.search')}
              aria-label={t('v2.newTask.search')}
              className="w-full bg-transparent text-[15px] text-primary placeholder:text-faint focus:outline-none"
              data-new-task-search
            />
          </div>
          <div ref={list} className="max-h-[50vh] overflow-y-auto p-1.5" role="listbox" aria-label={t('v2.newTask.title')}>
            {targets.map((target, i) => (
              <button
                key={target.name || '_home'}
                type="button"
                role="option"
                aria-selected={i === highlighted}
                data-task-index={i}
                data-new-task-option={target.name || A.HomeProject}
                onMouseMove={() => setHighlighted(i)}
                onClick={() => choose(target)}
                className={cn('flex w-full min-w-0 items-center gap-3 rounded-lg px-3 py-2 text-left text-[13.5px]', i === highlighted ? 'bg-surface-raised text-title' : 'text-secondary', target.home && 'mt-1')}
              >
                {target.home || !byName.get(target.name) ? (
                  <span className="flex size-6 shrink-0 items-center justify-center rounded-md bg-surface-strong text-secondary">
                    <House className="size-3.5" />
                  </span>
                ) : (
                  <ProjectIcon project={byName.get(target.name)!} />
                )}
                <span className="min-w-0 flex-1 truncate font-medium">{target.label}</span>
                {target.lastAt && <span className="shrink-0 text-[11.5px] text-faint">{timeAgo(target.lastAt, now)}</span>}
                {i === highlighted && <CornerDownLeft className="size-3.5 shrink-0 text-faint" />}
                {i < 9 && <kbd className="w-5 shrink-0 rounded bg-surface-strong text-center font-mono text-[11px] text-subtle">{i + 1}</kbd>}
              </button>
            ))}
            {targets.length === 0 && <div className="px-3 py-4 text-[13px] text-subtle">{t('v2.newTask.none')}</div>}
          </div>
          <div className="flex items-center gap-3 border-t border-line px-4 py-2 text-[11.5px] text-faint">
            <span>{t('v2.newTask.hintMove')}</span>
            <span>{t('v2.newTask.hintOpen')}</span>
            <span>{query ? t('v2.newTask.hintAltDigit') : t('v2.newTask.hintDigit')}</span>
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
