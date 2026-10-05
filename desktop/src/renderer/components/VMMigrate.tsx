import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ArrowRightLeft, CheckCircle2, LoaderCircle, Trash2 } from 'lucide-react';
import { type ReactNode, useEffect, useState } from 'react';
import { toast } from 'sonner';
import type { VMMigration } from '../../preload';
import { useT } from '../lib/i18n';
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
  const t = useT();
  const queryClient = useQueryClient();
  const [lines, setLines] = useState<string[]>([]);
  const [confirming, setConfirming] = useState<'move' | 'remove' | null>(null);
  useEffect(() => window.agentbox.vmMigrate.onOutput((text) => setLines((prev) => appendOutput(prev, text))), []);

  const move = useMutation({
    mutationFn: () => window.agentbox.vmMigrate.run(),
    onMutate: () => setLines([]),
    onSuccess: async () => {
      toast(t('vm.migrate.toast'), { description: t('vm.migrate.toastDescription') });
      // Every page's data is the VM's daemon's now.
      await queryClient.invalidateQueries();
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['vm-migration'] }),
  });
  const removeOld = useMutation({
    mutationFn: () => window.agentbox.vmMigrate.removeOld(),
    onMutate: () => setLines([]),
    onSuccess: () => toast(t('vm.migrate.removedToast'), { description: t('vm.migrate.removedToastDescription') }),
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
            {t('vm.migrate.moved')}
          </div>
          {migration.state === 'removed' && (
            <p className="text-[12px] leading-relaxed text-subtle" data-vm-migrate-removed>
              {t('vm.migrate.removedNote', { hasBackup: migration.backup ? 'yes' : 'no', backup: migration.backup ?? '' })}
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
                {t('vm.migrate.checkNote')}
              </p>
              <div className="flex flex-wrap items-center gap-2">
                <Button variant="secondary" size="sm" disabled={busy} onClick={() => setConfirming('remove')} data-vm-migrate-remove>
                  {removeOld.isPending ? <LoaderCircle className="animate-spin" /> : <Trash2 />}
                  {t('vm.migrate.removeOld', { count: old.length })}
                </Button>
              </div>
            </>
          )}
        </div>
      ) : (
        <div className="grid gap-2">
          <div className={embedded && !started && !move.isPending ? 'hidden' : 'text-[13px] font-medium text-primary'}>
            {move.isPending ? t('vm.migrate.moving') : started ? t('vm.migrate.halfway') : t('vm.migrate.title')}
          </div>
          <p className="text-[12px] leading-relaxed text-subtle">
            {started
              ? t('vm.migrate.carryOnNote')
              : `${embedded ? '' : t('vm.migrate.intro')}${t('vm.migrate.comesAlong', { projects, agents })}`}
          </p>
          <p className="text-[12px] leading-relaxed text-subtle">
            {t('vm.migrate.notAlong')}
          </p>
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="primary" size="sm" disabled={busy || !kvm} onClick={() => setConfirming('move')} data-vm-migrate-run>
              {move.isPending ? <LoaderCircle className="animate-spin" /> : <ArrowRightLeft />}
              {started ? t('vm.migrate.carryOn') : t('vm.migrate.move')}
            </Button>
            <span className="text-xs text-subtle">
              {move.isPending
                ? t('vm.migrate.busy')
                : kvm
                  ? t('vm.migrate.noPassword')
                  : t('vm.migrate.noKVM')}
            </span>
          </div>
        </div>
      )}

      {(lines.length > 0 || busy) && <SetupLog lines={lines} label={t('vm.migrate.log')} />}
      {move.error && (
        <>
          <Notice>{errorMessage(move.error)}</Notice>
          <p className="text-[12px] text-subtle">{t('vm.migrate.failedNote')}</p>
          <CommandBox command="agentbox vm migrate" />
        </>
      )}
      {removeOld.error && <Notice>{errorMessage(removeOld.error)}</Notice>}

      <ConfirmDialog
        open={confirming === 'move'}
        onOpenChange={(open) => setConfirming(open ? 'move' : null)}
        title={t('vm.migrate.confirmMoveTitle')}
        description={t('vm.migrate.confirmMove')}
        confirmLabel={t('vm.migrate.move')}
        onConfirm={async () => move.mutate()}
      />
      <ConfirmDialog
        open={confirming === 'remove'}
        onOpenChange={(open) => setConfirming(open ? 'remove' : null)}
        title={t('vm.migrate.confirmRemoveTitle')}
        description={t.rich('vm.migrate.confirmRemove', {
          machines: old.join(', '),
          mono: (c) => <span className="font-mono text-primary">{c}</span>,
        })}
        confirmLabel={t('vm.migrate.removeThem')}
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
