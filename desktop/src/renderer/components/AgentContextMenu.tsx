import { useMutation, useQueryClient } from '@tanstack/react-query';
import { CircleX, GitBranch, GitPullRequest, MessageSquare, Moon, Pause, Play, Square, SquareTerminal } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import type { View } from '../App';
import { lifecycleActions, usesChat } from '../lib/agentActions';
import { api, type AgentAction } from '../lib/api';
import { disposeTerminal } from '../lib/terminals';
import { ConfirmDialog } from './ConfirmDialog';
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from './ui/context-menu';
import { Code } from './ui/card';
import { Label } from './ui/input';
import { Switch } from './ui/switch';

// The actions an agent already offers, wherever it's listed: the rail, the
// all-agents list, and a project's fleet. One component, so an agent has the
// same menu everywhere it appears, and every action goes through exactly the
// API call its own view already uses (AgentView.tsx, FleetPanel.tsx).
export function AgentContextMenu({
  agent,
  pr,
  onSelect,
  children,
}: {
  agent: T.Agent;
  pr?: T.PullRequest;
  onSelect: (view: View) => void;
  children: ReactNode;
}) {
  const queryClient = useQueryClient();
  const [destroying, setDestroying] = useState(false);
  const [force, setForce] = useState(false);
  const [deleteBranch, setDeleteBranch] = useState(false);
  const [deleteMedia, setDeleteMedia] = useState(false);

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

  const actions = lifecycleActions(agent.state);
  const lifecycleIcon = { pause: Pause, resume: Play, start: Play, stop: Square };
  const lifecycleLabel = { pause: 'Pause', resume: 'Resume', start: 'Start', stop: 'Stop' };

  return (
    <>
      <ContextMenu>
        <ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
        <ContextMenuContent>
          {usesChat(agent) && (
            <ContextMenuItem icon={MessageSquare} onSelect={() => onSelect({ kind: 'agent', ref: agent.ref, tab: 'chat' })}>
              Open chat
            </ContextMenuItem>
          )}
          <ContextMenuItem icon={SquareTerminal} onSelect={() => onSelect({ kind: 'agent', ref: agent.ref, tab: 'terminal' })}>
            Open terminal
          </ContextMenuItem>
          <ContextMenuSeparator />
          {actions.map((name) => (
            <ContextMenuItem key={name} icon={lifecycleIcon[name]} onSelect={() => action.mutate(name)}>
              {lifecycleLabel[name]}
            </ContextMenuItem>
          ))}
          <ContextMenuItem icon={Moon} onSelect={() => retire.mutate()}>
            Retire
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem
            icon={GitBranch}
            onSelect={() => {
              window.agentbox.copyText(agent.branch);
              toast('Copied the branch name');
            }}
          >
            Copy branch name
          </ContextMenuItem>
          {pr && (
            <ContextMenuItem icon={GitPullRequest} onSelect={() => void window.agentbox.openExternal(pr.url)}>
              Open pull request #{pr.number}
            </ContextMenuItem>
          )}
          <ContextMenuSeparator />
          <ContextMenuItem
            icon={CircleX}
            destructive
            onSelect={() => {
              setForce(agent.state === 'incomplete' || agent.state === 'missing');
              setDeleteBranch(false);
              setDeleteMedia(false);
              setDestroying(true);
            }}
          >
            Destroy agent…
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>

      <ConfirmDialog
        open={destroying}
        onOpenChange={setDestroying}
        title={`Destroy ${agent.title || agent.ref}?`}
        description="Deletes the machine, its snapshots and the worktree. Commits stay on the branch, and media stays in the project's media view, unless you delete them too."
        confirmLabel="Destroy"
        destructive
        onConfirm={async () => {
          await api.destroyAgent(agent.ref, force, deleteBranch, deleteMedia);
          disposeTerminal(agent.ref);
          await invalidate();
        }}
      >
        <div className="grid gap-3 rounded-xl border border-line bg-surface-faint p-3.5">
          <div className="flex items-center gap-3">
            <Switch id={`destroy-force-${agent.ref}`} checked={force} onCheckedChange={setForce} />
            <Label htmlFor={`destroy-force-${agent.ref}`} className="font-normal">
              Discard uncommitted changes
            </Label>
          </div>
          <div className="flex items-center gap-3">
            <Switch id={`destroy-branch-${agent.ref}`} checked={deleteBranch} onCheckedChange={setDeleteBranch} />
            <Label htmlFor={`destroy-branch-${agent.ref}`} className="font-normal">
              Also delete the branch <Code>{agent.branch}</Code>
            </Label>
          </div>
          <div className="flex items-center gap-3">
            <Switch id={`destroy-media-${agent.ref}`} checked={deleteMedia} onCheckedChange={setDeleteMedia} />
            <Label htmlFor={`destroy-media-${agent.ref}`} className="font-normal">
              Also delete its media, instead of keeping it in the project's media view
            </Label>
          </div>
        </div>
      </ConfirmDialog>
    </>
  );
}
