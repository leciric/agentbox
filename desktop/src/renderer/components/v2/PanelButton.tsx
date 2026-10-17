// The light half of UI v2's right-hand panel (RightPanel.tsx): the chat
// header's button that opens it, and how a view hands it its ⋯ menu. Kept
// apart so the views that draw the button don't load the panel's tabs, and
// the desktop view with them.
import { PanelRight } from 'lucide-react';
import * as A from '../../../shared/api';
import type { View } from '../../App';
import { useT } from '../../lib/i18n';
import { isNew, livePages } from '../../lib/pages';
import { setPanelOpen, useChatPanel } from '../../lib/rightPanel';
import { cn, shortcut } from '../../lib/utils';
import { useProjectPages } from '../pages/usePages';
import { Button } from '../ui/button';
import { Tip } from '../ui/tooltip';

// chatOf is the chat a view has in the middle, and its project: the lead's
// for a project, the agent's for an agent.
export function chatOf(view: View): { project: string; chat: string } | null {
  if (view.kind === 'project' || view.kind === 'today') return { project: view.project, chat: `${view.project}/${A.LeadName}` };
  if (view.kind === 'agent') return { project: view.ref.split('/')[0], chat: view.ref };
  return null;
}

// PanelButton is the chat header's one button that opens and closes the
// chat's panel, an icon whose tooltip names it and the launcher's letters,
// with a dot while it's closed and the chat's project has pages you
// haven't seen.
export function PanelButton({ project, chat }: { project: string; chat: string }) {
  const t = useT();
  const { open } = useChatPanel(chat);
  const pages = useProjectPages(project);
  const fresh = livePages(pages.data).some(isNew);
  return (
    <Tip
      label={
        <span className="grid gap-1">
          <span>{t('panel.toggleTip', { keys: shortcut('D') })}</span>
          <span className="text-subtle">{t('panel.toggleKeys')}</span>
        </span>
      }
    >
      <Button
        size="icon"
        variant="ghost"
        aria-label={t('panel.toggle')}
        aria-pressed={open}
        className={cn('relative', open && 'bg-surface-raised text-primary')}
        onClick={() => setPanelOpen(chat, !open)}
        data-panel-toggle
      >
        <PanelRight />
        {!open && fresh && <span className="absolute right-1 top-1 size-2 rounded-full bg-brand-500 ring-2 ring-[var(--ab-chat)]" data-panel-fresh />}
      </Button>
    </Tip>
  );
}

