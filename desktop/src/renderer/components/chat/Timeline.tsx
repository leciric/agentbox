import { Archive, Brain, Check, ChevronRight, CircleAlert, Copy, Info, LoaderCircle } from 'lucide-react';
import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type * as T from '../../../shared/api';
import { formatDuration, timelineRows, type Row } from '../../lib/chat';
import { cn } from '../../lib/utils';
import { aiLabel } from '../state';
import { Tip } from '../ui/tooltip';
import { ChangedFiles } from './ChangedFiles';
import { SentImages } from './Images';
import { Markdown } from './Markdown';
import { SubagentCard, WorkGroup } from './Work';

export function Timeline({ agent, thread }: { agent: T.Agent; thread: T.ChatThread }) {
  const [openTurns, setOpenTurns] = useState<ReadonlySet<string>>(() => new Set());
  const rows = useMemo(() => timelineRows(thread, openTurns), [thread, openTurns]);
  const toggleTurn = useCallback(
    (turn: string) =>
      setOpenTurns((open) => {
        const next = new Set(open);
        if (!next.delete(turn)) next.add(turn);
        return next;
      }),
    [],
  );
  const starting = thread.session.state === 'starting' ? thread.session.detail || `Starting ${aiLabel(agent.ai)}` : undefined;
  return (
    <div className="flex flex-col" data-chat-timeline>
      {rows.map((row) => (
        <TimelineRow key={row.key} row={row} chatRef={agent.ref} root={agent.worktree} starting={row.type === 'thinking' ? starting : undefined} onToggleTurn={toggleTurn} />
      ))}
    </div>
  );
}

const TimelineRow = memo(
  function TimelineRow({ row, chatRef, root, starting, onToggleTurn }: { row: Row; chatRef: string; root: string; starting?: string; onToggleTurn: (turn: string) => void }) {
    switch (row.type) {
      case 'user':
        return (
          <div className="group flex flex-col items-end gap-1 pb-4 pt-1" data-chat-item={row.item.kind === 'aside' ? 'aside' : 'user'}>
            {!!row.item.images?.length && <SentImages chatRef={chatRef} images={row.item.images} />}
            {row.item.text && (
              <div className="max-w-[85%] whitespace-pre-wrap break-words rounded-2xl bg-surface-raised px-3.5 py-2.5 text-sm leading-relaxed text-primary shadow-[inset_0_1px_0_rgb(255_255_255/0.04)]">
                {row.item.text}
              </div>
            )}
            {row.item.kind === 'aside' && <Delivered item={row.item} />}
            <Meta item={row.item} className="justify-end pr-1" />
          </div>
        );
      case 'assistant':
        return (
          <div className={cn('group min-w-0 px-1', row.final ? 'pb-4' : 'pb-2.5')} data-chat-item="assistant">
            <Markdown text={row.item.text ?? ''} streaming={row.item.streaming} className="text-sm leading-relaxed text-tertiary" />
            {row.final && <Meta item={row.item} className="mt-1.5" />}
          </div>
        );
      case 'work':
        return <WorkGroup items={row.items} live={row.live} root={root} />;
      case 'subagent':
        return <SubagentCard item={row.item} children={row.children} root={root} />;
      case 'compaction':
        return <CompactionCard item={row.item} />;
      case 'fold':
        return (
          <div className="mb-3 border-b border-line pb-2 pt-1">
            <button
              className="flex items-center gap-1 rounded-md px-1 text-sm tabular-nums text-subtle transition-colors hover:text-tertiary"
              aria-expanded={row.open}
              data-chat-fold
              onClick={() => onToggleTurn(row.turn)}
            >
              {row.label}
              <ChevronRight className={cn('size-3.5 transition-transform', row.open && 'rotate-90')} />
            </button>
          </div>
        );
      case 'working':
        return (
          <div className="mb-3 border-b border-line pb-2 pt-1" data-chat-working>
            <div className="flex h-6 items-baseline px-1 text-sm tabular-nums text-subtle">
              Working for&nbsp;
              <Elapsed since={row.since} />
            </div>
          </div>
        );
      case 'thinking':
        return (
          <div className="flex min-h-7 items-center gap-1.5 px-0.5 pb-2 text-sm" data-chat-thinking>
            <span className="flex size-6 items-center justify-center text-subtle">
              <Brain className="size-4" strokeWidth={1.8} />
            </span>
            <span className="chat-shine">{starting ?? 'Thinking'}</span>
          </div>
        );
      case 'note':
        // What an agent reported no longer reaches here — that is a thread in
        // the rail, not a grey box in the middle of the conversation. What is
        // left is an error, and the mark a rollover leaves where the session
        // changed.
        return row.item.kind === 'error' ? (
          <div className="mb-4 flex items-start gap-2.5 rounded-xl border border-rose-500/20 bg-rose-500/[0.06] px-3.5 py-2.5 text-[13px] text-rose-100" role="alert" data-chat-item="error">
            <CircleAlert className="mt-0.5 size-4 shrink-0 text-rose-300" />
            <p className="min-w-0 break-words leading-relaxed">{row.item.text}</p>
          </div>
        ) : (
          <div className="mb-4 flex items-start gap-2.5 rounded-xl border border-line bg-surface-faint px-3.5 py-2.5 text-[13px] text-muted" data-chat-item="notice">
            <Info className="mt-0.5 size-4 shrink-0 text-subtle" />
            <p className="min-w-0 break-words leading-relaxed">{row.item.text}</p>
          </div>
        );
      case 'changes':
        return <ChangedFiles files={row.files} root={root} />;
    }
  },
  (a, b) => a.chatRef === b.chatRef && a.root === b.root && a.starting === b.starting && a.onToggleTurn === b.onToggleTurn && sameRow(a.row, b.row),
);

function sameRow(a: Row, b: Row): boolean {
  if (a.type !== b.type || a.key !== b.key) return false;
  switch (a.type) {
    case 'user':
    case 'note':
    case 'compaction':
      return a.item === (b as typeof a).item;
    case 'assistant':
      return a.item === (b as typeof a).item && a.final === (b as typeof a).final;
    case 'work': {
      const other = b as typeof a;
      return a.live === other.live && a.items.length === other.items.length && a.items.every((it, i) => it === other.items[i]);
    }
    case 'subagent': {
      const other = b as typeof a;
      return a.item === other.item && a.children.length === other.children.length && a.children.every((it, i) => it === other.children[i]);
    }
    case 'fold':
      return a.label === (b as typeof a).label && a.open === (b as typeof a).open;
    case 'working':
      return a.since === (b as typeof a).since;
    case 'thinking':
      return true;
    case 'changes': {
      const other = b as typeof a;
      return a.files.length === other.files.length && a.files.every((f, i) => f.path === other.files[i].path && f.added === other.files[i].added && f.removed === other.files[i].removed);
    }
  }
}

// Meta is a message's time and a copy button, shown on hover.
// Delivered says what became of a message sent while the tool was working. The
// interesting one is "sent": the tool read it mid-work and it was the model,
// not AgentBox, that decided what to do about it.
const deliveries: Record<string, { label: string; className: string }> = {
  waiting: { label: 'Sending…', className: 'text-subtle' },
  sent: { label: 'Sent while working', className: 'text-subtle' },
  deferred: { label: 'Waiting for this turn to end', className: 'text-amber-300/70' },
  lost: { label: "Never reached the tool", className: 'text-rose-300/80' },
  held: { label: 'Waiting for the compaction to finish', className: 'text-amber-300/70' },
};

// CompactionCard is the chat saving its conversation to the project's memory
// and carrying on in a fresh session ([D73]). It is up while that runs, so a
// message that isn't answered yet has a reason on screen, and then says how it
// went. The card's text is the daemon's, once it has ended.
function CompactionCard({ item }: { item: T.ChatItem }) {
  const c = item.compaction!;
  const waiting = c.waiting ?? 0;
  if (c.state === 'running') {
    return (
      <div className="mb-4 flex items-start gap-2.5 rounded-xl border border-line bg-surface-faint px-3.5 py-2.5 text-[13px]" role="status" data-chat-item="compaction" data-chat-compaction="running">
        <LoaderCircle className="mt-0.5 size-4 shrink-0 animate-spin text-subtle" />
        <div className="min-w-0 leading-relaxed">
          <p className="chat-shine">Compacting context — saving to project memory…</p>
          {waiting > 0 && (
            <p className="break-words text-subtle">
              {waiting === 1 ? 'Your message waits' : `Your ${waiting} messages wait`} for the fresh session, and goes in as soon as it starts.
            </p>
          )}
        </div>
      </div>
    );
  }
  const failed = c.state === 'failed';
  return (
    <div
      className={cn(
        'mb-4 flex items-start gap-2.5 rounded-xl border px-3.5 py-2.5 text-[13px]',
        failed ? 'border-amber-500/20 bg-amber-500/[0.06] text-amber-100' : 'border-line bg-surface-faint text-muted',
      )}
      data-chat-item="compaction"
      data-chat-compaction={c.state}
    >
      {failed ? <CircleAlert className="mt-0.5 size-4 shrink-0 text-amber-300" /> : <Archive className="mt-0.5 size-4 shrink-0 text-subtle" />}
      <div className="min-w-0 leading-relaxed">
        <p className="break-words">{item.text}</p>
        {failed && c.error && <p className="break-words text-[12.5px] text-amber-200/70">{c.error}</p>}
      </div>
    </div>
  );
}

function Delivered({ item }: { item: T.ChatItem }) {
  const delivery = item.delivery ? deliveries[item.delivery] : undefined;
  if (!delivery) return null;
  return (
    <span className={cn('pr-1 text-[11.5px]', delivery.className)} data-chat-delivery={item.delivery}>
      {delivery.label}
    </span>
  );
}

function Meta({ item, className }: { item: T.ChatItem; className?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className={cn('flex items-center gap-1 text-xs tabular-nums text-faint opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100', className)}>
      <Tip label={new Date(item.createdAt).toLocaleString()}>
        <span>{new Date(item.updatedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
      </Tip>
      <button
        aria-label="Copy the message"
        className="flex size-6 items-center justify-center rounded-md transition hover:bg-surface-raised hover:text-tertiary"
        onClick={() => {
          window.agentbox.copyText(item.text ?? '');
          setCopied(true);
          setTimeout(() => setCopied(false), 1200);
        }}
      >
        {copied ? <Check className="size-3.5 text-emerald-400" /> : <Copy className="size-3.5" />}
      </button>
    </div>
  );
}

// Elapsed counts the seconds since a time, rewriting its own text rather than re-rendering.
export function Elapsed({ since }: { since: string }) {
  const ref = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    const start = Date.parse(since);
    const tick = () => {
      if (ref.current) ref.current.textContent = formatDuration((Date.now() - start) / 1000);
    };
    tick();
    const timer = setInterval(tick, 1000);
    return () => clearInterval(timer);
  }, [since]);
  return <span ref={ref} />;
}
