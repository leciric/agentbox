import { useMutation, useQueryClient } from '@tanstack/react-query';
import { MemoryStick } from 'lucide-react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { cn, humanBytes } from '../lib/utils';
import { Button } from './ui/button';

// MemoryShortageWarning is what an agent thrashing at its memory limit shows,
// on its rail row and in its info card: held at its limit, it keeps dropping
// its files from memory and reading them back from disk, and that I/O slows
// the whole computer down, not only the agent (agent.ThrashWatch). The fix is
// more memory, so the warning carries it — a one-click raise to what the
// daemon offers, within the shared budget or what the host can spare, applied
// live. An agent with nothing to offer gets the reason instead.
export function MemoryShortageWarning({ agent, className }: { agent: T.Agent; className?: string }) {
  const short = agent.memoryShortage;
  const queryClient = useQueryClient();
  const raise = useMutation({
    mutationFn: (memory: string) => api.updateAgent(agent.ref, { memory }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['agents'] });
      void queryClient.invalidateQueries({ queryKey: ['fleet', agent.project] });
    },
  });
  if (!short) return null;

  const held = short.limit > 0 ? `its ${humanBytes(short.limit)} limit` : "the shared budget's memory";
  const why = `Held at ${held}, it's re-reading ${humanBytes(short.refaultRate)}/s from disk, and waiting on memory ${Math.round(short.pressure)}% of the time.`;
  const raised = raise.isSuccess;
  return (
    <div
      className={cn('grid min-w-0 gap-1.5 rounded-lg border border-amber-400/25 bg-amber-400/[0.08] px-2.5 py-2 text-[11.5px]', className)}
      data-memory-shortage={agent.ref}
      role="alert"
    >
      <span className="flex min-w-0 items-center gap-1.5 font-medium text-amber-300">
        <MemoryStick className="size-3.5 shrink-0" />
        <span className="min-w-0">Short of memory, and slowing the whole computer down</span>
      </span>
      <span className="leading-snug text-secondary">{why}</span>
      {short.raiseTo ? (
        <span className="flex min-w-0 items-center gap-2">
          <Button
            variant="default"
            className="h-7 shrink-0 px-2.5 text-[12px]"
            data-memory-raise={short.raiseTo}
            disabled={raise.isPending || raised}
            onClick={(event) => {
              // The warning sits inside rows that open the agent on a click.
              event.stopPropagation();
              raise.mutate(short.raiseTo as string);
            }}
          >
            {raised ? `Raised to ${readable(short.raiseTo)}` : raise.isPending ? 'Raising…' : `Raise to ${readable(short.raiseTo)}`}
          </Button>
          {raise.isError && <span className="min-w-0 truncate text-rose-300">{raise.error.message}</span>}
        </span>
      ) : (
        <span className="leading-snug text-subtle">
          {short.limit > 0 ? 'There is no room to raise it further: stop another agent, or give it less to run.' : 'Give the shared budget more memory in Settings, or stop another agent.'}
        </span>
      )}
    </div>
  );
}

// readable is a limit the way Incus takes it ("8GiB") the way the app shows
// sizes everywhere else ("8 GiB").
function readable(size: string): string {
  return size.replace(/^(\d+(?:\.\d+)?)([KMGT]i?B)$/, '$1 $2');
}
