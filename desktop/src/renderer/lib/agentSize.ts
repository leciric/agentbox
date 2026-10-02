// An agent's size: what it reserves of the VM's memory while it runs, which
// the daemon's admission across projects waits for before starting it
// (internal/agent/admission.go, which has the final say). A reservation, not a
// cap: an agent may use more while the VM has memory to spare.

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
      label: 'Auto',
      tip: leadDecides ? 'The chat picks a size for each agent it creates.' : "Normal: reserves what this project's agents have needed at most.",
    },
    { value: 'light', label: 'Light', tip: 'Reserves ~2 GB: reading, reviews and small edits.' },
    { value: 'normal', label: 'Normal', tip: "Reserves what this project's agents have needed at most." },
    { value: 'heavy', label: 'Heavy', tip: 'Reserves ~8 GB: recordings, Android, big builds.' },
  ];
}

// waitingLine turns the daemon's reason a queued agent waits ("queued: 6
// agents in 2 projects reserve 15 of 18 GB; starts when ~7 GB is free") into
// what follows "Queued #N — ", or "" when there is none.
export function waitingLine(waiting: string | undefined): string {
  return (waiting ?? '').replace(/^queued:\s*/, '');
}
