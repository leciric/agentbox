import {
  ArrowRightLeft,
  Bot,
  Brain,
  Check,
  ChevronRight,
  CircleAlert,
  Eye,
  Globe,
  Search,
  ShieldCheck,
  ShieldX,
  SquarePen,
  SquareTerminal,
  Trash2,
  Wrench,
  type LucideIcon,
} from 'lucide-react';
import { memo, useState, type ReactNode } from 'react';
import type * as T from '../../../shared/api';
import { entryLabel, isActive, isWork, liveLabel, workSummary } from '../../lib/chat';
import { cn } from '../../lib/utils';
import { DiffView } from './ChangedFiles';
import { Markdown } from './Markdown';

const rowClass =
  'group/row flex min-h-7 w-full min-w-0 items-center gap-1.5 rounded-md px-0.5 py-0.5 text-left text-sm leading-relaxed transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-brand-400/40';

const toolIcons: Record<string, LucideIcon> = {
  read: Eye,
  edit: SquarePen,
  delete: Trash2,
  move: ArrowRightLeft,
  search: Search,
  execute: SquareTerminal,
  think: Brain,
  fetch: Globe,
};

function iconOf(it: T.ChatItem): LucideIcon {
  if (it.kind === 'thought') return Brain;
  if (it.kind === 'permission') return it.permission?.options.find((o) => o.id === it.permission?.outcome)?.kind.startsWith('allow') ? ShieldCheck : ShieldX;
  if (it.tool?.status === 'failed') return CircleAlert;
  return toolIcons[it.tool?.kind ?? ''] ?? Wrench;
}

// WorkGroup shows consecutive tool calls: the one happening now in a running
// turn, a single call as it is, or several as one summary you can open.
export const WorkGroup = memo(
  function WorkGroup({ items, live, root }: { items: T.ChatItem[]; live: boolean; root: string }) {
    const [open, setOpen] = useState(false);
    const last = items[items.length - 1];

    if (live && isActive(last)) {
      const Icon = iconOf(last);
      return (
        <div className="pb-2" data-chat-work="live">
          <button className={cn(rowClass, 'hover:bg-surface-faint')} aria-expanded={open} onClick={() => setOpen(!open)}>
            <span className="flex size-6 shrink-0 items-center justify-center text-subtle">
              <Icon className="size-4" strokeWidth={1.8} />
            </span>
            <span className="chat-shine min-w-0 truncate">{liveLabel(last)}</span>
            {items.length > 1 && <span className="shrink-0 text-xs tabular-nums text-faint">+{items.length - 1}</span>}
            <ChevronRight className={cn('size-3 shrink-0 text-faint transition-transform', open && 'rotate-90')} />
          </button>
          {open && <EntryList items={items} root={root} />}
        </div>
      );
    }
    if (items.length === 1) {
      return (
        <div className="pb-2" data-chat-work="single">
          <Entry item={items[0]} root={root} />
        </div>
      );
    }
    const failed = items.some((it) => it.tool?.status === 'failed');
    // The group shows what kind of work it is; a failure gets its own mark.
    const firstTool = items.find((it) => it.kind === 'tool')?.tool;
    const Icon = firstTool ? (toolIcons[firstTool.kind] ?? Wrench) : iconOf(items[0]);
    return (
      <div className="pb-2" data-chat-work="group">
        <button className={cn(rowClass, 'hover:bg-surface-faint')} aria-expanded={open} onClick={() => setOpen(!open)}>
          <span className="flex size-6 shrink-0 items-center justify-center text-subtle">
            <Icon className="size-4" strokeWidth={1.8} />
          </span>
          <span className="min-w-0 truncate text-subtle group-hover/row:text-muted">{workSummary(items)}</span>
          {failed && <CircleAlert className="size-3.5 shrink-0 text-rose-400/70" aria-label="A tool call failed" />}
          <ChevronRight className={cn('size-3 shrink-0 text-faint transition-transform', open && 'rotate-90')} />
        </button>
        {open && <EntryList items={items} root={root} />}
      </div>
    );
  },
  (a, b) => a.live === b.live && a.root === b.root && a.items.length === b.items.length && a.items.every((it, i) => it === b.items[i]),
);

function EntryList({ items, root }: { items: T.ChatItem[]; root: string }) {
  return (
    <div className="ml-3 mt-0.5 border-l border-line pl-2.5">
      {items.map((it) => (
        <Entry key={it.id} item={it} root={root} />
      ))}
    </div>
  );
}

function Entry({ item, root }: { item: T.ChatItem; root: string }) {
  const [open, setOpen] = useState(false);
  const detail = detailOf(item, root);
  const Icon = iconOf(item);
  const failed = item.tool?.status === 'failed';
  const active = isActive(item);
  return (
    <div className="min-w-0" data-chat-entry={item.kind}>
      <button
        className={cn(rowClass, detail ? 'hover:bg-surface-faint' : 'cursor-default')}
        aria-expanded={detail ? open : undefined}
        onClick={() => detail && setOpen(!open)}
      >
        <span className={cn('flex size-6 shrink-0 items-center justify-center', failed ? 'text-rose-400/70' : 'text-subtle')}>
          <Icon className="size-4" strokeWidth={1.8} />
        </span>
        <span className={cn('min-w-0 truncate', failed ? 'text-rose-300/80' : 'text-subtle group-hover/row:text-muted', active && 'chat-shine')}>
          {active ? liveLabel(item) : entryLabel(item)}
        </span>
        {item.tool?.status === 'stopped' && <span className="shrink-0 text-xs text-faint">stopped</span>}
        {detail && <ChevronRight className={cn('size-3 shrink-0 text-faint transition-transform', open && 'rotate-90')} />}
      </button>
      {open && detail && <div className="mb-1.5 ml-7 mt-0.5 overflow-hidden rounded-lg border border-line-faint bg-sunken">{detail}</div>}
    </div>
  );
}

function detailOf(it: T.ChatItem, root: string): ReactNode {
  if (it.kind === 'thought') {
    return it.text ? <p className="max-h-72 overflow-auto whitespace-pre-wrap px-3 py-2 text-[12.5px] leading-relaxed text-subtle">{it.text}</p> : null;
  }
  const tool = it.tool;
  if (!tool) return null;
  if (tool.diffs?.length) {
    return tool.diffs.map((diff, i) => (
      <div key={i} className={cn(i > 0 && 'border-t border-line-faint')}>
        <div className="truncate px-3 pt-1.5 font-mono text-[11px] text-subtle">{diff.path.startsWith(`${root}/`) ? diff.path.slice(root.length + 1) : diff.path}</div>
        <DiffView diff={diff} />
      </div>
    ));
  }
  const command = tool.kind === 'execute' ? tool.command : undefined;
  if (!command && !tool.output) return null;
  return (
    <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words px-3 py-2 font-mono text-[11.5px] leading-relaxed text-muted">
      {command && (
        <span className="text-tertiary">
          <span className="select-none text-faint">$ </span>
          {command}
          {tool.output ? '\n' : null}
        </span>
      )}
      {tool.output}
    </pre>
  );
}

// SubagentCard is a subagent the agent started (D86): who it is, what it was
// asked and how it is doing, with what it did nested under it — its tool calls
// and thoughts as the agent's own are shown, and its words, which are its
// report to the agent rather than to you.
export const SubagentCard = memo(
  function SubagentCard({ item, children, root }: { item: T.ChatItem; children: T.ChatItem[]; root: string }) {
    const [open, setOpen] = useState(false);
    const sub = item.subagent!;
    const running = sub.state === 'running';
    const work = children.filter(isWork);
    const current = running ? children.findLast((it) => isActive(it)) : undefined;
    const summary = current ? liveLabel(current) : work.length > 0 ? workSummary(work) : running ? 'Starting' : 'Did nothing';
    return (
      <div className="pb-2" data-chat-subagent={sub.state}>
        <div className="rounded-xl border border-line-faint bg-surface-faint">
          <button className={cn(rowClass, 'items-start gap-2 rounded-xl px-2 py-1.5 hover:bg-surface-faint')} aria-expanded={open} onClick={() => setOpen(!open)}>
            <span className="mt-px flex size-6 shrink-0 items-center justify-center text-subtle">
              <Bot className="size-4" strokeWidth={1.8} />
            </span>
            <span className="min-w-0 flex-1">
              <span className="flex min-w-0 items-baseline gap-1.5">
                <span className="shrink-0 font-medium text-secondary">{sub.name}</span>
                {/* Open, the task is written out in full below instead. */}
                {sub.task && !open && <span className="min-w-0 truncate text-subtle">{sub.task}</span>}
              </span>
              <span className={cn('block min-w-0 truncate text-[12.5px]', running ? 'chat-shine' : 'text-faint')}>{summary}</span>
            </span>
            <SubagentState state={sub.state} />
            <ChevronRight className={cn('mt-1.5 size-3 shrink-0 text-faint transition-transform', open && 'rotate-90')} />
          </button>
          {open && (
            <div className="mx-2 mb-2 border-t border-line-faint pt-1.5">
              {sub.task && <p className="mb-1.5 whitespace-pre-wrap break-words px-1 text-[12.5px] leading-relaxed text-subtle">{sub.task}</p>}
              <div className="ml-3 border-l border-line pl-2.5">
                {children.length === 0 && <p className="py-1 text-[12.5px] text-faint">Nothing yet.</p>}
                {children.map((it) =>
                  it.kind === 'assistant' ? (
                    <div key={it.id} className="py-1 pl-1" data-chat-entry="subagent-message">
                      <Markdown text={it.text ?? ''} streaming={it.streaming} className="text-[13px] leading-relaxed text-muted" />
                    </div>
                  ) : it.kind === 'subagent' ? (
                    <div key={it.id} className={cn(rowClass, 'cursor-default')} data-chat-entry="subagent">
                      <span className="flex size-6 shrink-0 items-center justify-center text-subtle">
                        <Bot className="size-4" strokeWidth={1.8} />
                      </span>
                      <span className="min-w-0 truncate text-subtle">
                        {it.subagent?.name}
                        {it.subagent?.task ? ` · ${it.subagent.task}` : ''}
                      </span>
                      <SubagentState state={it.subagent?.state ?? ''} />
                    </div>
                  ) : isWork(it) ? (
                    <Entry key={it.id} item={it} root={root} />
                  ) : null,
                )}
              </div>
            </div>
          )}
        </div>
      </div>
    );
  },
  (a, b) => a.item === b.item && a.root === b.root && a.children.length === b.children.length && a.children.every((it, i) => it === b.children[i]),
);

function SubagentState({ state }: { state: string }) {
  if (state === 'running') return <span className="mt-1 shrink-0 text-[11.5px] text-subtle">working</span>;
  if (state === 'completed') return <Check className="mt-1 size-3.5 shrink-0 text-emerald-400/80" aria-label="Finished" />;
  if (state === 'failed') return <CircleAlert className="mt-1 size-3.5 shrink-0 text-rose-400/80" aria-label="Failed" />;
  return <span className="mt-1 shrink-0 text-[11.5px] text-faint">stopped</span>;
}
