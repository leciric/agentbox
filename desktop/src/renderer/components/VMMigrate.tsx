import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ArrowRightLeft, CheckCircle2, LoaderCircle, Trash2 } from 'lucide-react';
import { type ReactNode, useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { VMMigration } from '../../preload';
import { errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { appendOutput, CommandBox, SetupLog } from './SettingsView';
import { Button } from './ui/button';
import { Notice, Panel } from './ui/card';

// VMMigrate moves a Linux machine that runs AgentBox itself into AgentBox's
// VM, in one step: `agentbox vm migrate`, run by the main process, which
// makes the VM, carries every project, setting, account, chat and agent into
// it, and checks that all of it arrived. The old machines stay in this
// machine's Incus until a second step the user confirms once they've looked
// at the VM: `agentbox vm migrate --remove-old`, which removes AgentBox's own
// machines by name and nothing else there.
//
// embedded is inside Settings' "Move to a VM" (RunInVM.tsx), which says why
// already: it leaves out its own panel and title.
export function VMMigrate({ migration, kvm, embedded }: { migration: VMMigration; kvm: boolean; embedded?: boolean }) {
  const queryClient = useQueryClient();
  const [lines, setLines] = useState<string[]>([]);
  const [confirming, setConfirming] = useState<'move' | 'remove' | null>(null);
  useEffect(() => window.agentbox.vmMigrate.onOutput((text) => setLines((prev) => appendOutput(prev, text))), []);

  const move = useMutation({
    mutationFn: () => window.agentbox.vmMigrate.run(),
    onMutate: () => setLines([]),
    onSuccess: async () => {
      toast('AgentBox runs in its VM now', {
        description: 'Every project, chat and agent came along. Remove the old machines once you have checked them.',
      });
      // Every page's data is the VM's daemon's now.
      await queryClient.invalidateQueries();
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['vm-migration'] }),
  });
  const removeOld = useMutation({
    mutationFn: () => window.agentbox.vmMigrate.removeOld(),
    onMutate: () => setLines([]),
    onSuccess: () => toast("AgentBox's old machines are gone", { description: 'Everything else in Incus stays as it was.' }),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['vm-migration'] }),
  });
  const busy = move.isPending || removeOld.isPending;
  const projects = migration.projects?.length ?? 0;
  const agents = migration.agents?.length ?? 0;
  const old = migration.oldMachines ?? [];
  // What the page polls says "started" while a move runs: this one's.
  const started = migration.state === 'started' && !move.isPending;
  const moved = (migration.state === 'verified' || migration.state === 'removed') && !move.isPending;

  return (
    <Box embedded={embedded} data-vm-migrate={migration.state}>
      {moved ? (
        <div className="grid gap-2">
          <div className="flex items-center gap-2 text-[13px] font-medium text-primary">
            <CheckCircle2 className="size-4 text-emerald-400" />
            Moved into AgentBox's VM, and checked there
          </div>
          {migration.state === 'removed' && (
            <p className="text-[12px] leading-relaxed text-subtle" data-vm-migrate-removed>
              Its old machines are gone from this machine's Incus. Everything else there stays as it was, and the state.db from before the move is
              kept{migration.backup ? ` at ${migration.backup}` : ''}.
            </p>
          )}
          {migration.found && migration.found.length > 0 && (
            <ul className="grid gap-0.5 text-[12px] text-subtle" data-vm-migrate-found>
              {migration.found.map((line) => (
                <li key={line} className="min-w-0 break-words">
                  {line}
                </li>
              ))}
            </ul>
          )}
          {migration.state === 'verified' && old.length > 0 && (
            <>
              <p className="text-[12px] leading-relaxed text-subtle">
                The agents' old machines are still in this machine's Incus, stopped, as they were before the move. Once you've checked your agents in
                the VM, remove them. Only AgentBox's own machines go: your other containers and VMs, the Incus network, its storage pool and Incus
                itself stay.
              </p>
              <div className="flex flex-wrap items-center gap-2">
                <Button variant="secondary" size="sm" disabled={busy} onClick={() => setConfirming('remove')} data-vm-migrate-remove>
                  {removeOld.isPending ? <LoaderCircle className="animate-spin" /> : <Trash2 />}
                  Remove {old.length} old machine{old.length === 1 ? '' : 's'}
                </Button>
              </div>
            </>
          )}
        </div>
      ) : (
        <div className="grid gap-2">
          <div className={embedded && !started && !move.isPending ? 'hidden' : 'text-[13px] font-medium text-primary'}>
            {move.isPending ? 'Moving into the VM…' : started ? 'A move into the VM is half-way' : 'Run AgentBox in a VM instead'}
          </div>
          <p className="text-[12px] leading-relaxed text-subtle">
            {started
              ? 'It stopped before it was done. Nothing of this machine was removed: carry on and it picks up where it was.'
              : `${embedded ? '' : 'Moves the daemon, Incus and every agent into one Cloud Hypervisor VM, with your home folder shared into it. '}Everything comes along: ${projects} project${projects === 1 ? '' : 's'}, ${agents} agent${agents === 1 ? '' : 's'} with their branches, worktrees and uncommitted changes, titles, models and limits, and your settings, accounts, notes, memory, chats and media. Each agent gets a new machine, from the VM's base image.`}
          </p>
          <p className="text-[12px] leading-relaxed text-subtle">
            What doesn't come along: anything installed inside an agent's old machine, and its home folder outside the worktree. Agents stop while
            they move; the ones running now start again in the VM. It takes a few minutes, needs no password, and keeps this machine's copy until
            you remove it.
          </p>
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="primary" size="sm" disabled={busy || !kvm} onClick={() => setConfirming('move')} data-vm-migrate-run>
              {move.isPending ? <LoaderCircle className="animate-spin" /> : <ArrowRightLeft />}
              {started ? 'Carry on moving' : 'Move into a VM'}
            </Button>
            <span className="text-xs text-subtle">
              {move.isPending
                ? 'Moving. Building the VM and its base image takes a few minutes.'
                : kvm
                  ? 'No password needed'
                  : 'This machine has no /dev/kvm you can use'}
            </span>
          </div>
        </div>
      )}

      {(lines.length > 0 || busy) && <SetupLog lines={lines} label="Move log" />}
      {move.error && (
        <>
          <Notice>{errorMessage(move.error)}</Notice>
          <p className="text-[12px] text-subtle">Nothing of this machine's was removed. Carry on from here, or in a terminal:</p>
          <CommandBox command="agentbox vm migrate" />
        </>
      )}
      {removeOld.error && <Notice>{errorMessage(removeOld.error)}</Notice>}

      <ConfirmDialog
        open={confirming === 'move'}
        onOpenChange={(open) => setConfirming(open ? 'move' : null)}
        title="Move AgentBox into a VM?"
        description={
          <>
            Every agent stops while it moves, with its terminal, dev servers and any turn in progress; their worktrees, branches and chats come along.
            The app can't reach the daemon until the VM's is up. This machine's copy stays until you remove it.
          </>
        }
        confirmLabel="Move into a VM"
        onConfirm={async () => move.mutate()}
      />
      <ConfirmDialog
        open={confirming === 'remove'}
        onOpenChange={(open) => setConfirming(open ? 'remove' : null)}
        title="Remove AgentBox's old machines?"
        description={
          <>
            These go from this machine's Incus, for good: <span className="font-mono text-primary">{old.join(', ')}</span>. Nothing else in Incus is
            touched.
          </>
        }
        confirmLabel="Remove them"
        destructive
        onConfirm={async () => removeOld.mutate()}
      />
    </Box>
  );
}

function Box({ embedded, children, ...rest }: { embedded?: boolean; children: ReactNode; 'data-vm-migrate': string }) {
  return embedded ? (
    <div className="grid gap-3" {...rest}>
      {children}
    </div>
  ) : (
    <Panel className="grid gap-3 p-4" {...rest}>
      {children}
    </Panel>
  );
}
