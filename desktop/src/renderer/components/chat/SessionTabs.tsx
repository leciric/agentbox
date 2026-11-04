import { AlarmClock, History, Plus, X } from 'lucide-react';
import { useRef, useState } from 'react';
import type * as T from '../../../shared/api';
import { useT } from '../../lib/i18n';
import { isUnread, sessionColor, sessionStatus, useSeenAt } from '../../lib/sessions';
import { SessionStatusMark } from './SessionStatusMark';
import { keysHint, sessionKeys } from '../../lib/shortcuts';
import { cn } from '../../lib/utils';
import { Button } from '../ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '../ui/popover';
import { wakeTime } from './SessionHeader';
import { Tip } from '../ui/tooltip';

// SessionTabs is the row above a project's chat: one tab per session, with
// where it stands before its name (SessionStatusMark: what waits on you, a
// stop, a spinner while its lead works, or a dot when it has said something
// since you last looked, here or on the phone); each is a folder tab on the box of the chat below it, in its
// colour (sessionColor) once there are several, the box and the bracket of the
// agents it made having the same; + opens a new one at once, untitled until its first turn
// names it (ProjectChatPanel). A tab's ×, and its shortcut
// (lib/shortcuts.ts), close the session into the chat's history, which the
// History button at the end of the row opens (SessionHistory): nothing is
// lost, so nothing asks first. Double-clicking a tab renames it.
export function SessionTabs({
  sessions,
  current,
  adding,
  closedCount,
  snoozed = [],
  onWake,
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
  // snoozed are the sessions hidden until a time (lib/sessions.ts isSnoozed),
  // listed apart at the end of the row; onWake brings one back now.
  snoozed?: T.LeadSession[];
  onWake?: (id: string) => void;
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
    <div role="tablist" aria-label={t('sessions.label')} data-session-tabs className="relative z-10 flex min-w-0 shrink-0 items-end gap-1 overflow-x-auto px-5 pt-1">
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
            data-session-state={sessionStatus(s)}
            data-session-color={color}
            onClick={() => onChoose(s.id)}
            onDoubleClick={() => setRenaming(s.id)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') onChoose(s.id);
              if (e.key === 'F2') setRenaming(s.id);
            }}
            className={cn(
              'group flex h-8 max-w-56 shrink-0 cursor-pointer select-none items-center gap-1.5 rounded-t-lg border pl-2.5 pr-1 text-ui transition-colors',
              color ? (on ? 'session-line' : 'session-line-quiet') : on ? 'border-line' : 'border-line-faint',
              on ? 'border-b-0 bg-ink text-primary' : 'text-muted hover:bg-surface-raised hover:text-secondary',
            )}
          >
            <SessionStatusMark session={s} unread={unread} />
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
        <Button variant="ghost" size="sm" className="mb-0.5 h-7 shrink-0 gap-1 px-2 text-ui" onClick={onAdd} disabled={adding} data-session-new>
          <Plus className="size-3.5" />
          {sessions.length === 0 && t('sessions.new')}
        </Button>
      </Tip>
      <span className="min-w-2 flex-1" />
      {snoozed.length > 0 && <SnoozedSessions sessions={snoozed} onWake={(id) => onWake?.(id)} />}
      <Tip label={closedCount > 0 ? t('sessions.historyCount', { count: closedCount }) : t('sessions.history')}>
        <Button variant="ghost" size="sm" className="mb-0.5 h-7 shrink-0 gap-1 px-2 text-ui" onClick={onHistory} aria-label={t('sessions.history')} data-session-history-open>
          <History className="size-3.5" />
          {closedCount > 0 && <span className="tabular-nums text-caption text-faint">{closedCount}</span>}
        </Button>
      </Tip>
    </div>
  );
}

// SnoozedSessions is the row's Snoozed button and its list: each snoozed
// session with when it comes back, and Wake now.
function SnoozedSessions({ sessions, onWake }: { sessions: T.LeadSession[]; onWake: (id: string) => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <Tip label={t('sessions.snoozed.count', { count: sessions.length })}>
        <PopoverTrigger asChild>
          <Button variant="ghost" size="sm" className="mb-0.5 h-7 shrink-0 gap-1 px-2 text-ui" aria-label={t('sessions.snoozed.title')} data-session-snoozed-open>
            <AlarmClock className="size-3.5" />
            <span className="tabular-nums text-caption text-faint">{sessions.length}</span>
          </Button>
        </PopoverTrigger>
      </Tip>
      <PopoverContent className="w-80 p-1.5" data-session-snoozed>
        <div className="px-2 pb-1.5 pt-1 text-caption font-medium text-subtle">{t('sessions.snoozed.title')}</div>
        {sessions.map((s) => (
          <div key={s.id} className="flex min-w-0 items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-surface-raised" data-session-snoozed-row={s.id}>
            <span className="min-w-0 flex-1">
              <span className="block truncate text-ui text-primary">{s.topic}</span>
              <span className="block text-caption text-subtle">{t('sessions.snoozed.back', { when: wakeTime(s.snoozedUntil!) })}</span>
            </span>
            <Button
              variant="secondary"
              size="sm"
              className="h-7 shrink-0 px-2 text-caption"
              onClick={() => {
                setOpen(false);
                onWake(s.id);
              }}
              data-session-wake={s.id}
            >
              {t('sessions.snoozed.wake')}
            </Button>
          </div>
        ))}
      </PopoverContent>
    </Popover>
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
