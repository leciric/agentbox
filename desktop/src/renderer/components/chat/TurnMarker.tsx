import { useQueryClient } from '@tanstack/react-query';
import { Ellipsis, GitFork, RotateCcw } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { ConfirmDialog } from '../ConfirmDialog';
import { ForkDialog } from '../SnapshotsTab';
import { Menu, MenuContent, MenuItem, MenuTrigger } from '../ui/menu';

// TurnMarker ends a settled turn that has a checkpoint: the agent's files as
// they were when it ended. Its menu rolls the agent back to there or forks a
// new agent from it.
export function TurnMarker({ agent, checkpoint, latest, onOpenAgent }: { agent: T.Agent; checkpoint: T.Checkpoint; latest: boolean; onOpenAgent?: (ref: string) => void }) {
  const t = useT();
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
          aria-label={t('chat.turn.menu', { n })}
          data-chat-turn-menu
        >
          {t('chat.turn.label', { n })}
          <Ellipsis className="size-3.5" />
        </MenuTrigger>
        <MenuContent>
          <MenuItem icon={RotateCcw} disabled={latest} hint={latest ? t('chat.turn.latest') : undefined} onSelect={() => setRollingBack(true)}>
            {t('chat.turn.rollBackHere')}
          </MenuItem>
          <MenuItem icon={GitFork} onSelect={() => setForking(true)}>
            {t('chat.turn.forkHere')}
          </MenuItem>
        </MenuContent>
      </Menu>
      <ConfirmDialog
        open={rollingBack}
        onOpenChange={setRollingBack}
        title={t('chat.turn.rollBackTitle', { name: agent.name, n })}
        description={t('chat.turn.rollBackDescription', { name: agent.name })}
        confirmLabel={t('chat.turn.rollBack')}
        destructive
        onConfirm={async () => {
          const res = await api.rollback(agent.ref, checkpoint.id);
          await queryClient.invalidateQueries({ queryKey: ['checkpoints', agent.ref] });
          toast(t('chat.turn.rolledBack', { n }), { description: t('chat.turn.rolledBackSaved', { name: agent.name, id: res.saved.id }) });
        }}
      />
      <ForkDialog
        agent={agent}
        from={
          forking
            ? {
                title: t('chat.turn.forkTitle', { name: agent.name, n }),
                description: t('chat.turn.forkDescription'),
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
