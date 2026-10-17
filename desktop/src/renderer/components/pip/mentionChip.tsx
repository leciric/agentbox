import { PanelRight } from 'lucide-react';
import { createContext, use, type ReactNode } from 'react';
import { cn } from '../../lib/utils';

// A lead's chat names its agents ("agent-12 finished the CSV export"). Under
// AgentMentions, each name in a message is a chip (Markdown's links, through
// lib/pip.ts's remark plugin), and a message opens below it on what the
// agent it reports captured: its latest recording or screenshot from just
// before. A chip opens the right-hand panel on the agent's Desktop.
export interface Mentions {
  project: string;
  names: ReadonlySet<string>;
  // live is the agents using their desktops, whose desktop onWatch opens in
  // the panel.
  live: ReadonlySet<string>;
  onWatch: (ref: string) => void;
  // onOpen opens the panel beside the chat on the agent named.
  onOpen: (name: string) => void;
  // onToday opens the project's Today (components/timeline/TodayView.tsx).
  onToday: () => void;
}
export const AgentMentions = createContext<Mentions | null>(null);

export const MessageMentions = createContext<{ open: string | null; toggle: (name: string) => void } | null>(null);

// MentionChip is an agent named in a message: a dot that pulses while it
// works, and the panel opened on its desktop on a click.
export function MentionChip({ name, children }: { name: string; children: ReactNode }) {
  const mentions = use(AgentMentions);
  const scope = use(MessageMentions);
  if (!mentions || !scope) return <>{children}</>;
  const open = scope.open === name;
  return (
    <button
      type="button"
      onClick={() => mentions.onOpen(name)}
      className={cn(
        'mx-px inline-flex items-baseline gap-1 rounded-md px-1.5 py-px align-baseline font-medium no-underline transition',
        open ? 'bg-brand-500/20 text-brand-200' : 'bg-surface-raised text-title hover:bg-surface-strong',
      )}
      data-pip-mention={name}
    >
      <span className={cn('size-1.5 shrink-0 self-center rounded-full', mentions.live.has(name) ? 'animate-pulse bg-emerald-400' : 'bg-faint')} />
      {children}
      <PanelRight className="size-3 self-center text-subtle" />
    </button>
  );
}
