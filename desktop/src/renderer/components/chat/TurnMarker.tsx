import { useQueryClient } from '@tanstack/react-query';
import { Ellipsis, GitFork, RotateCcw } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { ConfirmDialog } from '../ConfirmDialog';
import { ForkDialog } from '../SnapshotsTab';
import { Menu, MenuContent, MenuItem, MenuTrigger } from '../ui/menu';

// TurnMarker ends a settled turn that has a checkpoint: the agent's files as
// they were when it ended. Its menu rolls the agent back to there or forks a
// new agent from it.
export function TurnMarker({ agent, checkpoint, latest, onOpenAgent }: { agent: T.Agent; checkpoint: T.Checkpoint; latest: boolean; onOpenAgent?: (ref: string) => void }) {
  const queryClient = useQueryClient();
  const [rollingBack, setRollingBack] = useState(false);
  const [forking, setForking] = useState(false);
  const n = checkpoint.number;
  return (
    <div className="group/turn -mt-2 mb-3 flex items-center gap-2 px-1" data-chat-turn={n}>
      <div className="h-px flex-1 bg-line opacity-0 transition-opacity group-hover/turn:opacity-100 has-[[data-state=open]]:opacity-100" />
      <Menu>
        <MenuTrigger
          className="flex h-6 items-center gap-1 rounded-md px-1.5 text-[11.5px] tabular-nums text-faint transition hover:bg-surface-raised hover:text-tertiary data-[state=open]:bg-surface-raised data-[state=open]:text-tertiary"
          aria-label={`Turn ${n}: roll back or fork`}
          data-chat-turn-menu
        >
          Turn {n}
          <Ellipsis className="size-3.5" />
        </MenuTrigger>
        <MenuContent>
          <MenuItem icon={RotateCcw} disabled={latest} hint={latest ? 'Latest turn' : undefined} onSelect={() => setRollingBack(true)}>
            Roll back here
          </MenuItem>
          <MenuItem icon={GitFork} onSelect={() => setForking(true)}>
            Fork from here
          </MenuItem>
        </MenuContent>
      </Menu>
      <ConfirmDialog
        open={rollingBack}
        onOpenChange={setRollingBack}
        title={`Roll ${agent.name} back to turn ${n}?`}
        description={
          <>
            The turns after it leave the conversation, and the worktree goes back to its files as this turn ended. {agent.name}'s session starts again,
            told the conversation up to here. What the worktree has now is saved first, so you can still fork from it.
          </>
        }
        confirmLabel="Roll back"
        destructive
        onConfirm={async () => {
          const res = await api.rollback(agent.ref, checkpoint.id);
          await queryClient.invalidateQueries({ queryKey: ['checkpoints', agent.ref] });
          toast(`Rolled back to turn ${n}`, { description: `What ${agent.name} had is saved as ${res.saved.id}.` });
        }}
      />
      <ForkDialog
        agent={agent}
        from={
          forking
            ? {
                title: `Fork ${agent.name} from turn ${n}`,
                description: 'A new agent whose branch starts from the files as this turn ended, and whose chat carries the conversation up to here.',
                request: { checkpoint: checkpoint.id },
              }
            : null
        }
        onClose={() => setForking(false)}
        onOpenAgent={onOpenAgent}
      />
    </div>
  );
}
