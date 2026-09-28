// AgentBox's VM in VM mode, from the renderer: its power and memory, polled
// through the bridge (main/vmpower.ts), and bringing it up when something
// needs the daemon inside it.
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import type { VMPower, VMPowerAction } from '../../preload';
import { vmTransitions } from './freeResources.ts';

// changed tells every useVMPower that the VM was just told to do something,
// so each shows the transition now rather than at its next poll.
const listeners = new Set<() => void>();
const changed = () => listeners.forEach((fn) => fn());

// useVMPower is the VM's power and memory, or null outside VM mode. It is
// asked once when null (the mode doesn't change while the app runs), every
// second while the VM is on its way up or down, and every five otherwise.
export function useVMPower() {
  const queryClient = useQueryClient();
  useEffect(() => {
    const refetch = () => void queryClient.invalidateQueries({ queryKey: ['vmPower'] });
    listeners.add(refetch);
    return () => void listeners.delete(refetch);
  }, [queryClient]);
  return useQuery({
    queryKey: ['vmPower'],
    queryFn: () => window.agentbox.vm.power(),
    refetchInterval: (query) => {
      const vm = query.state.data;
      if (!vm) return false;
      return vmTransitions.includes(vm.state) ? 1_000 : 5_000;
    },
  });
}

// actOnVM starts, pauses, resumes or stops the VM, resolving once it's done.
export async function actOnVM(action: VMPowerAction): Promise<VMPower> {
  const done = window.agentbox.vm.act(action);
  // The main process reports the transition as soon as the action starts.
  setTimeout(changed, 50);
  try {
    return await done;
  } finally {
    changed();
  }
}

// ensureVMRunning brings the VM up, if there is one and it isn't, before
// something that needs the daemon inside it: starting or making an agent. A
// paused VM is resumed, an off one started, and one already on its way up
// waited for. Outside VM mode it does nothing.
export async function ensureVMRunning(): Promise<void> {
  const bridge = typeof window === 'undefined' ? undefined : window.agentbox?.vm;
  if (!bridge?.power) return;
  let vm = await bridge.power();
  for (const deadline = Date.now() + 5 * 60_000; vm && Date.now() < deadline; vm = await bridge.power()) {
    switch (vm.state) {
      case 'running':
        return;
      case 'paused':
        await actOnVM('resume');
        return;
      case 'off':
        await actOnVM('start');
        return;
      default:
        // starting, resuming, or on its way down: wait for it to settle.
        await new Promise((resolve) => setTimeout(resolve, 1_000));
    }
  }
}
