import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { disposeTerminal } from '../lib/terminals';
import { ConfirmDialog } from './ConfirmDialog';
import { Code } from './ui/card';
import { Label } from './ui/input';
import { Switch } from './ui/switch';

// The confirmation for destroying an agent, wherever it's asked for: the
// agent's own view and its right-click menu (AgentContextMenu). One component,
// so what it says happens to the branch and the media can't drift between them.
export function DestroyAgentDialog({
  agent,
  open,
  onOpenChange,
  onDestroyed,
}: {
  agent: T.Agent;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDestroyed?: () => void;
}) {
  const queryClient = useQueryClient();
  const [force, setForce] = useState(false);
  const [deleteBranch, setDeleteBranch] = useState(false);
  const [deleteMedia, setDeleteMedia] = useState(false);
  // Every time it opens, it starts from the safe choices, except that an agent
  // whose machine is broken or gone has nothing left to keep uncommitted. Only
  // on opening: the agent's state can change under an open dialog, and that
  // mustn't undo what the user switched.
  useEffect(() => {
    if (!open) return;
    setForce(agent.state === 'incomplete' || agent.state === 'missing');
    setDeleteBranch(false);
    setDeleteMedia(false);
  }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Destroy ${agent.title || agent.ref}?`}
      description="Deletes the machine, its snapshots and the worktree. The branch is deleted too once it's merged or pushed; otherwise its commits stay on it. Media stays in the project's media view for as long as Settings keeps it, unless you delete it now."
      confirmLabel="Destroy"
      destructive
      onConfirm={async () => {
        await api.destroyAgent(agent.ref, force, deleteBranch, deleteMedia);
        disposeTerminal(agent.ref);
        await queryClient.invalidateQueries({ queryKey: ['agents'] });
        await queryClient.invalidateQueries({ queryKey: ['fleet', agent.project] });
        onDestroyed?.();
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
            Delete the branch <Code>{agent.branch}</Code> even if it isn't merged or pushed
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
  );
}
