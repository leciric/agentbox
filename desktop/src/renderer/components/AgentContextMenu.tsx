import { useMutation, useQueryClient } from '@tanstack/react-query';
import * as TooltipPrimitive from '@radix-ui/react-tooltip';
import { CircleX, GitBranch, GitPullRequest, Info, MessageSquare, Moon, Pause, Play, Square, SquareTerminal } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import * as T from '../../shared/api';
import type { View } from '../App';
import { lifecycleActions, usesChat } from '../lib/agentActions';
import { api, type AgentAction } from '../lib/api';
import { countFeature, type AppFeature } from '../lib/usageStats';
import { AgentInfoCard } from './AgentInfoCard';
import { DestroyAgentDialog } from './DestroyAgentDialog';
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from './ui/context-menu';
import { Dialog, DialogContent, DialogTitle } from './ui/dialog';

// The actions an agent already offers, wherever it's listed: the rail, the
// all-agents list, and a project's fleet. One component, so an agent has the
// same menu everywhere it appears, and every action goes through exactly the
// API call its own view already uses (AgentView.tsx, FleetPanel.tsx). active
// says the agent's own view is the one open, which destroying it has to leave.
//
// Hovering children shows the same info the "Info" item opens, as a tooltip
// (infoSide says which way it opens). The tooltip's own trigger is nested
// inside ContextMenuTrigger's asChild slot, both wrapping children directly,
// so a real right-click and a real hover each reach the same DOM node: a
// plain component in between (like this file's own Tip) would swallow
// ContextMenuTrigger's cloned props instead of forwarding them to children.
export function AgentContextMenu({
  agent,
  pr,
  active,
  infoSide = 'right',
  onSelect,
  children,
}: {
  agent: T.Agent;
  pr?: T.PullRequest;
  active?: boolean;
  infoSide?: 'top' | 'bottom' | 'left' | 'right';
  onSelect: (view: View) => void;
  children: ReactNode;
}) {
  const queryClient = useQueryClient();
  const [destroying, setDestroying] = useState(false);
  const [showingInfo, setShowingInfo] = useState(false);

  const invalidate = async () => {
    await queryClient.invalidateQueries({ queryKey: ['agents'] });
    await queryClient.invalidateQueries({ queryKey: ['fleet', agent.project] });
  };

  const action = useMutation({
    mutationFn: (name: AgentAction) => api.agentAction(agent.ref, name),
    onSuccess: () => void invalidate(),
    onError: (err) => toast.error(String(err)),
  });

  const retire = useMutation({
    mutationFn: () => api.retire(agent.project, { how: 'stop', agents: [agent.name] }),
    onSuccess: async (result) => {
      const freed = result.retired.length;
      toast(
        freed > 0 ? `Stopped ${agent.title || agent.name}` : `${agent.title || agent.name} wasn't free to stop`,
        { description: freed > 0 ? "Its work stays on its branch. Start it again any time." : result.skipped[0]?.reason },
      );
      await invalidate();
    },
    onError: (err) => toast.error(String(err)),
  });

  // Each item counts itself for the usage stats as it runs.
  const counted = (feature: AppFeature, run: () => void) => () => {
    countFeature(feature);
    run();
  };

  const actions = lifecycleActions(agent.state);
  const lifecycleIcon = { pause: Pause, resume: Play, start: Play, stop: Square };
  const lifecycleLabel = { pause: 'Pause', resume: 'Resume', start: 'Start', stop: 'Stop' };

  return (
    <>
      <ContextMenu>
        <TooltipPrimitive.Root>
          <ContextMenuTrigger asChild>
            <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
          </ContextMenuTrigger>
          <TooltipPrimitive.Portal>
            <TooltipPrimitive.Content side={infoSide} sideOffset={6} className="z-[60] max-w-none animate-fade-in rounded-lg border border-line-strong bg-overlay p-3 text-xs text-secondary shadow-xl backdrop-blur">
              <AgentInfoCard agent={agent} pr={pr} />
            </TooltipPrimitive.Content>
          </TooltipPrimitive.Portal>
        </TooltipPrimitive.Root>
        <ContextMenuContent>
          {usesChat(agent) && (
            <ContextMenuItem icon={MessageSquare} onSelect={counted(T.FeatureMenuOpenChat, () => onSelect({ kind: 'agent', ref: agent.ref, tab: 'chat' }))}>
              Open chat
            </ContextMenuItem>
          )}
          <ContextMenuItem icon={SquareTerminal} onSelect={counted(T.FeatureMenuOpenTerminal, () => onSelect({ kind: 'agent', ref: agent.ref, tab: 'terminal' }))}>
            Open terminal
          </ContextMenuItem>
          <ContextMenuItem icon={Info} onSelect={counted(T.FeatureMenuInfo, () => setShowingInfo(true))}>
            Info
          </ContextMenuItem>
          <ContextMenuSeparator />
          {actions.map((name) => (
            <ContextMenuItem key={name} icon={lifecycleIcon[name]} onSelect={counted(T.FeatureMenuLifecycle, () => action.mutate(name))}>
              {lifecycleLabel[name]}
            </ContextMenuItem>
          ))}
          <ContextMenuItem icon={Moon} onSelect={counted(T.FeatureMenuRetire, () => retire.mutate())}>
            Retire
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem
            icon={GitBranch}
            onSelect={counted(T.FeatureMenuCopyBranch, () => {
              window.agentbox.copyText(agent.branch);
              toast('Copied the branch name');
            })}
          >
            Copy branch name
          </ContextMenuItem>
          {pr && (
            <ContextMenuItem icon={GitPullRequest} onSelect={counted(T.FeatureMenuOpenPullRequest, () => void window.agentbox.openExternal(pr.url))}>
              Open pull request #{pr.number}
            </ContextMenuItem>
          )}
          <ContextMenuSeparator />
          <ContextMenuItem
            icon={CircleX}
            destructive
            onSelect={counted(T.FeatureMenuDestroy, () => setDestroying(true))}
          >
            Destroy agent…
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>

      <DestroyAgentDialog
        agent={agent}
        open={destroying}
        onOpenChange={setDestroying}
        onDestroyed={() => active && onSelect({ kind: 'project', project: agent.project })}
      />

      <Dialog open={showingInfo} onOpenChange={setShowingInfo}>
        <DialogContent className="max-w-sm">
          <DialogTitle className="sr-only">{agent.title || agent.name}, agent info</DialogTitle>
          <AgentInfoCard agent={agent} pr={pr} />
        </DialogContent>
      </Dialog>
    </>
  );
}
