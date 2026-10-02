import { useMutation, useQueryClient } from '@tanstack/react-query';
import { HardDrive, LoaderCircle } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { errorMessage } from '../lib/utils';
import { appendOutput, SetupLog } from './SettingsView';
import { Button } from './ui/button';
import { Notice, Panel } from './ui/card';
import { Field, Input } from './ui/input';
import { Switch } from './ui/switch';

const GiB = 1024 ** 3;
// The swapfile the switch starts at (api.VMSwapDefault), and the least
// `agentbox vm swap on` makes (hostvm.MinSwap).
const defaultGiB = 8;
const minGiB = 0.5;

function gib(bytes: number): string {
  return String(Math.round((bytes / GiB) * 10) / 10);
}

// VMSwap is the swap of AgentBox's VM, next to its size: none by default, as
// the VM always had, or a swapfile on the VM's own disk, made and turned on
// while the VM runs and kept across its restarts (`agentbox vm swap`, run by
// the main process). The command refuses a swapfile that would leave the
// disk under its floor, and says so here. While the VM is off there's
// nothing to change it in.
export function VMSwap({ swap, running }: { swap?: T.VMSwap; running: boolean }) {
  const queryClient = useQueryClient();
  const size = swap?.size ?? 0;
  const [on, setOn] = useState(size > 0);
  const [form, setForm] = useState(size > 0 ? gib(size) : String(defaultGiB));
  const [lines, setLines] = useState<string[]>([]);
  useEffect(() => window.agentbox.vm.onSwapOutput((text) => setLines((prev) => appendOutput(prev, text))), []);

  // The switch and the size follow the VM until you change them.
  const [edited, setEdited] = useState(false);
  useEffect(() => {
    if (edited) return;
    setOn(size > 0);
    setForm(size > 0 ? gib(size) : String(defaultGiB));
  }, [size, edited]);

  const apply = useMutation({
    mutationFn: (gibs: number | null) => window.agentbox.vm.swap(gibs === null ? null : `${gibs}GiB`),
    onMutate: () => setLines([]),
    onSuccess: async (_, gibs) => {
      setEdited(false);
      toast(gibs === null ? "AgentBox's VM has no swap now" : `AgentBox's VM has ${gibs} GiB of swap now`, {
        description: 'Every agent kept running.',
      });
      await queryClient.invalidateQueries({ queryKey: ['host-setup'] });
    },
    // A refused size stays in the form to fix; a failed off puts the switch
    // back on.
    onError: (_, gibs) => gibs === null && setEdited(false),
  });

  const gibs = Number(form);
  const sizeOk = form.trim() !== '' && Number.isFinite(gibs) && gibs >= minGiB;
  const changed = on ? Math.abs(gibs * GiB - size) >= GiB / 20 : size > 0;
  const busy = apply.isPending;
  const inUse = swap && swap.total > 0 ? `${gib(swap.used)} of ${gib(swap.total)} GiB in use` : null;

  return (
    <Panel className="mt-3 grid gap-3 p-4" data-vm-swap>
      <div className="flex items-start gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <label htmlFor="vm-swap" className="text-[13px] font-medium text-primary">
              Swap
            </label>
            <div className="text-[12px] text-subtle" data-vm-swap-current>
              {size > 0 ? `${gib(size)} GiB swapfile` : 'Off'}
              {running && inUse ? ` · ${inUse}` : ''}
            </div>
          </div>
          <p className="mt-0.5 text-[12px] leading-relaxed text-subtle">
            A swapfile on the VM's disk, for agents that briefly need more memory than its cap. It's made while the VM runs, every agent
            keeps running, and the VM keeps it across restarts. It takes its size of the disk, and must leave the disk floor free.
          </p>
        </div>
        <Switch
          id="vm-swap"
          data-vm-swap-switch
          aria-label="Give the VM swap"
          disabled={busy || !running}
          checked={on}
          onCheckedChange={(next) => {
            setOn(next);
            setEdited(true);
            apply.reset();
            // Off takes the swapfile away at once; on waits for a size.
            if (!next && size > 0) apply.mutate(null);
            if (!next && size === 0) setEdited(false);
          }}
        />
      </div>

      {!running && <p className="text-[12px] text-subtle">The VM is off: start it to change its swap.</p>}

      {running && on && (
        <form
          className="grid gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            if (sizeOk && changed && !busy) apply.mutate(gibs);
          }}
        >
          <Field
            label="Size, in GiB"
            htmlFor="vm-swap-size"
            hint={sizeOk || form === '' ? undefined : <span className="text-rose-300">{`At least ${minGiB} GiB.`}</span>}
          >
            <div className="flex flex-wrap items-center gap-3">
              <div className="relative w-36">
                <HardDrive className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
                <Input
                  id="vm-swap-size"
                  type="number"
                  inputMode="decimal"
                  min={minGiB}
                  // Steps count from min: 1 would make 8 invalid, and the form
                  // wouldn't submit.
                  step={minGiB}
                  className="h-8 pl-8 font-mono text-[12.5px]"
                  disabled={busy}
                  value={form}
                  onChange={(e) => {
                    setEdited(true);
                    setForm(e.target.value);
                  }}
                />
              </div>
              <Button type="submit" variant="primary" size="sm" disabled={busy || !changed || !sizeOk} data-vm-swap-apply>
                {busy && <LoaderCircle className="animate-spin" />}
                {busy ? 'Making it…' : size > 0 ? 'Resize the swapfile' : 'Make the swapfile'}
              </Button>
            </div>
          </Field>
        </form>
      )}

      {(lines.length > 0 || busy) && <SetupLog lines={lines} label="VM swap log" />}
      {apply.error && <Notice>{errorMessage(apply.error)}</Notice>}
    </Panel>
  );
}
