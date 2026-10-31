// The light half of UI v2's right-hand panel (RightPanel.tsx): the chat
// header's button that opens it, and how a view hands it its ⋯ menu. Kept
// apart so the views that draw the button don't load the panel's tabs, and
// the desktop view with them.
import { PanelRight } from 'lucide-react';
import * as A from '../../../shared/api';
import type { View } from '../../App';
import { useT } from '../../lib/i18n';
import { isNew, livePages } from '../../lib/pages';
import { chatPanel, fitsBeside, onList, panelToggle, usePanel } from '../../lib/rightPanel';
import { cn, shortcut } from '../../lib/utils';
import { useProjectPages } from '../pages/usePages';
import { dotLabel, FeatureDot, useFeatureDot } from '../FeatureDot';
import { Button } from '../ui/button';
import { Tip } from '../ui/tooltip';

// chatOf is the chat a view has in the middle, and its project: the lead's
// for a project, the agent's for an agent.
export function chatOf(view: View): { project: string; chat: string } | null {
  if (view.kind === 'project') return { project: view.project, chat: `${view.project}/${A.LeadName}` };
  if (view.kind === 'work' && view.project) return { project: view.project, chat: `${view.project}/${A.LeadName}` };
  if (view.kind === 'agent') return { project: view.ref.split('/')[0], chat: view.ref };
  return null;
}

// PanelButton is the chat header's one button that folds the chat's panel
// to its strip of agents and shows it again (panelToggle; it never closes),
// an icon whose tooltip names it and the launcher's letters, with a dot
// while it's folded and the chat's project has pages you haven't seen.
export function PanelButton({ project, chat }: { project: string; chat: string }) {
  const t = useT();
  const state = usePanel();
  const open = fitsBeside() ? !onList(state, chat) || !state.listFolded : chatPanel(state, chat).open;
  const pages = useProjectPages(project);
  const fresh = livePages(pages.data).some(isNew);
  // New features inside the panel are marked here while it's folded: those
  // of a lead's panel on a lead's chat, an agent's on an agent's.
  const dot = useFeatureDot(chat.endsWith(`/${A.LeadName}`) ? 'panel-toggle:lead' : 'panel-toggle:agent');
  return (
    <Tip
      label={dotLabel(
        <span className="grid gap-1">
          <span>{t('panel.toggleTip', { keys: shortcut('D') })}</span>
          <span className="text-subtle">{t('panel.toggleKeys')}</span>
        </span>,
        dot,
      )}
    >
      <Button
        size="icon"
        variant="ghost"
        aria-label={t('panel.toggle')}
        aria-pressed={open}
        className={cn('relative', open && 'bg-surface-raised text-primary')}
        onClick={() => panelToggle(chat)}
        data-panel-toggle
      >
        <PanelRight />
        {!open && fresh && <span className="absolute right-1 top-1 size-2 rounded-full bg-brand-500 ring-2 ring-[var(--ab-chat)]" data-panel-fresh />}
        {!open && !fresh && <FeatureDot dot={dot} className="right-1 top-1" />}
      </Button>
    </Tip>
  );
}

