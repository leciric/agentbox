import { History, LoaderCircle, Plus, X } from 'lucide-react';
import { useRef, useState } from 'react';
import type * as T from '../../../shared/api';
import { useT } from '../../lib/i18n';
import { isUnread, isWorking, sessionColor, useSeenAt } from '../../lib/sessions';
import { keysHint, sessionKeys } from '../../lib/shortcuts';
import { cn } from '../../lib/utils';
import { Button } from '../ui/button';
import { Tip } from '../ui/tooltip';

// SessionTabs is the row above a project's chat: one tab per session, with a
// spinner while its lead works and a dot when it has said something since you
// last looked, after the dot in its colour (sessionColor) that tints the
// agents it made; + opens a new one at once, untitled until its first turn
// names it (ProjectChatPanel). A tab's ×, and its shortcut
// (lib/shortcuts.ts), close the session into the chat's history, which the
// History button at the end of the row opens (SessionHistory): nothing is
// lost, so nothing asks first. Double-clicking a tab renames it.
export function SessionTabs({
  sessions,
  current,
  adding,
  closedCount,
  onChoose,
  onAdd,
  onClose,
  onHistory,
  onRename,
}: {
  sessions: T.LeadSession[];
  current?: string;
  adding: boolean;
  closedCount: number;
  onChoose: (id: string) => void;
  onAdd: () => void;
  onClose: (id: string) => void;
  onHistory: () => void;
  onRename: (id: string, topic: string) => void;
}) {
  const t = useT();
  const seen = useSeenAt();
  const [renaming, setRenaming] = useState<string>();
  return (
    <div role="tablist" aria-label={t('sessions.label')} data-session-tabs className="flex min-w-0 shrink-0 items-center gap-1 overflow-x-auto border-b border-line px-2 py-1.5">
      {sessions.map((s) => {
        const on = s.id === current;
        const unread = !on && isUnread(s, seen[s.ref]);
        const color = sessionColor(sessions, s);
        if (renaming === s.id) {
          return (
            <TopicInput
              key={s.id}
              initial={s.topic}
              label={t('sessions.rename')}
              onDone={(topic) => {
                setRenaming(undefined);
                if (topic && topic !== s.topic) onRename(s.id, topic);
              }}
            />
          );
        }
        return (
          <div
            key={s.id}
            role="tab"
            aria-selected={on}
            tabIndex={on ? 0 : -1}
            data-session={s.id}
            data-session-color={color}
            onClick={() => onChoose(s.id)}
            onDoubleClick={() => setRenaming(s.id)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') onChoose(s.id);
              if (e.key === 'F2') setRenaming(s.id);
            }}
            className={cn(
              'group flex h-7 max-w-56 shrink-0 cursor-pointer select-none items-center gap-1.5 rounded-lg pl-2.5 pr-1 text-ui transition-colors',
              on ? 'bg-surface-strong text-primary' : 'text-muted hover:bg-surface-raised hover:text-secondary',
            )}
          >
            {color && <span aria-hidden className="session-dot size-2 shrink-0 rounded-full" data-session-dot />}
            {isWorking(s) ? (
              <LoaderCircle aria-label={t('sessions.working')} className="size-3 shrink-0 animate-spin text-brand-300" />
            ) : unread ? (
              <span aria-label={t('sessions.unread')} className="size-1.5 shrink-0 rounded-full bg-brand-400" />
            ) : null}
            <span className="min-w-0 truncate">{s.topic}</span>
            {s.agents > 0 && <span className="shrink-0 text-caption tabular-nums text-faint">{s.agents}</span>}
            <Tip label={on ? `${t('sessions.close')} · ${keysHint(sessionKeys.close)}` : t('sessions.close')}>
              <button
                type="button"
                aria-label={t('sessions.closeNamed', { topic: s.topic })}
                data-session-close={s.id}
                onClick={(e) => {
                  e.stopPropagation();
                  onClose(s.id);
                }}
                className={cn('flex size-5 shrink-0 items-center justify-center rounded-md text-faint hover:bg-surface-strong hover:text-primary', !on && 'opacity-0 group-hover:opacity-100')}
              >
                <X className="size-3" />
              </button>
            </Tip>
          </div>
        );
      })}
      <Tip label={`${t('sessions.new')} · ${keysHint(sessionKeys.new)}`}>
        <Button variant="ghost" size="sm" className="h-7 shrink-0 gap-1 px-2 text-ui" onClick={onAdd} disabled={adding} data-session-new>
          <Plus className="size-3.5" />
          {sessions.length === 0 && t('sessions.new')}
        </Button>
      </Tip>
      <span className="min-w-2 flex-1" />
      <Tip label={closedCount > 0 ? t('sessions.historyCount', { count: closedCount }) : t('sessions.history')}>
        <Button variant="ghost" size="sm" className="h-7 shrink-0 gap-1 px-2 text-ui" onClick={onHistory} aria-label={t('sessions.history')} data-session-history-open>
          <History className="size-3.5" />
          {closedCount > 0 && <span className="tabular-nums text-caption text-faint">{closedCount}</span>}
        </Button>
      </Tip>
    </div>
  );
}

// TopicInput is a topic typed in place: Enter keeps it, Escape or leaving
// the field empty gives up.
export function TopicInput({ initial = '', label, placeholder, onDone }: { initial?: string; label: string; placeholder?: string; onDone: (topic: string) => void }) {
  const [value, setValue] = useState(initial);
  const done = useRef(false);
  const finish = (topic: string) => {
    if (done.current) return;
    done.current = true;
    onDone(topic.trim());
  };
  return (
    <input
      autoFocus
      aria-label={label}
      placeholder={placeholder}
      value={value}
      maxLength={200}
      onChange={(e) => setValue(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === 'Enter') finish(value);
        if (e.key === 'Escape') finish('');
      }}
      onBlur={() => finish(value === initial ? '' : value)}
      className="h-7 w-56 shrink-0 rounded-lg border border-line-strong bg-surface px-2.5 text-ui text-primary outline-none placeholder:text-faint focus:border-brand-400"
    />
  );
}
