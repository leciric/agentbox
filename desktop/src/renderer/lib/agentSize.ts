// An agent's size: what it reserves of the VM's memory in its heavy phases
// (tests, builds, the browser, a recording), which wait for room in the VM's
// burst pool (internal/agent/admission.go, which has the final say). A
// reservation, not a cap: an agent may use more while the VM has memory to
// spare.

import { t } from '../../shared/i18n/index.ts';

export interface AgentSize {
  value: string; // "" is auto
  label: string;
  tip: string;
}

// agentSizes are the sizes in the order a picker offers them. leadDecides
// words auto for the project setting, where it lets the chat choose.
export function agentSizes(leadDecides = false): AgentSize[] {
  return [
    {
      value: '',
      label: t('agent.size.auto'),
      tip: leadDecides ? t('agent.size.autoLead') : t('agent.size.autoNormal'),
    },
    { value: 'light', label: t('agent.size.light'), tip: t('agent.size.lightTip') },
    { value: 'normal', label: t('agent.size.normal'), tip: t('agent.size.normalTip') },
    { value: 'heavy', label: t('agent.size.heavy'), tip: t('agent.size.heavyTip') },
  ];
}

// waitingLine turns the daemon's reason a queued agent waits ("queued: 6
// agents in 2 projects reserve 15 of 18 GB; starts when ~7 GB is free") into
// what follows "Queued #N — ", or "" when there is none.
export function waitingLine(waiting: string | undefined): string {
  return (waiting ?? '').replace(/^queued:\s*/, '');
}
