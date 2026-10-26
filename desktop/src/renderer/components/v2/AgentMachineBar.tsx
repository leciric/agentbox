// AgentMachineBar is the strip under an agent's header in UI v2 that says it
// has a computer of its own: its desktop small, live or as last seen, what
// the machine is doing, and its four views, each opening the right-hand
// panel on it. It is the panel folded: while the panel shows this agent the
// bar steps aside, rather than repeat its switch.
import { FolderTree, Images, Monitor, SquareTerminal, type LucideIcon } from 'lucide-react';
import type * as T from '../../../shared/api';
import { useT, type MessageKey } from '../../lib/i18n';
import { chatPanel, fitsBeside, hasView, openPanelOn, panelTarget, machineBarViews, usePanel, viewOf, type PanelView } from '../../lib/rightPanel';
import { cn } from '../../lib/utils';
import { deskLine, DesktopPeek, DeskGlyph, useDesk, useDeskUp } from './DesktopPeek';

const icon: Partial<Record<PanelView, LucideIcon>> = { desktop: Monitor, terminal: SquareTerminal, files: FolderTree, evidence: Images };
const label: Partial<Record<PanelView, MessageKey>> = { desktop: 'panel.view.desktop', terminal: 'panel.view.terminal', files: 'panel.view.files', evidence: 'panel.view.evidence' };

export function AgentMachineBar({ agent }: { agent: T.Agent }) {
  const t = useT();
  const desk = useDesk(agent);
  const up = useDeskUp(agent);
  const state = usePanel();
  const panel = chatPanel(state, agent.ref);
  const whose = { kind: 'agent', agent } as const;
  // Shown in the panel beside the chat (always there where it fits), or in its drawer.
  if ((fitsBeside() || panel.open) && !panel.wide && panelTarget(state, agent.ref) === agent.ref) return null;
  const last = viewOf(state, whose);
  return (
    <div className="mx-2 mb-2 flex min-w-0 items-center gap-3 rounded-xl border border-line bg-surface-faint py-1.5 pl-1.5 pr-2 md:mx-6" data-agent-machine={desk}>
      <button type="button" onClick={() => openPanelOn(agent.ref, 'desktop', agent.ref)} className="w-[104px] shrink-0 rounded-lg transition hover:ring-2 hover:ring-brand-500/50" aria-label={t('panel.view.desktop')} data-agent-machine-peek>
        <DesktopPeek agent={agent} desk={desk} caption={false} />
      </button>
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="flex min-w-0 items-center gap-1.5 text-[12.5px] font-medium text-title">
          <DeskGlyph desk={desk} />
          <span className="truncate">{t('desk.ownComputer', { name: agent.name })}</span>
        </span>
        <span className="truncate pl-5 text-[11.5px] text-subtle">{t(deskLine(desk, up))}</span>
      </span>
      <span className="flex shrink-0 items-center gap-1">
        {machineBarViews.map((v) => {
          const Icon = icon[v]!;
          return (
            <button
              key={v}
              type="button"
              disabled={!hasView(whose, v)}
              onClick={() => openPanelOn(agent.ref, v, agent.ref)}
              className={cn('flex h-7 items-center gap-1.5 rounded-md border px-2 text-[12px] font-medium transition-colors disabled:opacity-40', last === v ? 'border-brand-500/50 bg-brand-500/10 text-title' : 'border-line text-secondary', 'hover:border-brand-500/60 hover:text-title')}
              data-agent-machine-view={v}
            >
              <Icon className="size-3.5" />
              <span className="hidden xl:inline">{t(label[v]!)}</span>
            </button>
          );
        })}
      </span>
    </div>
  );
}
