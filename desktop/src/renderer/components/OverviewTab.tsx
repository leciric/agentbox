import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Box, Copy, ExternalLink, FileDiff, FolderOpen, GitBranch, LoaderCircle, SlidersHorizontal, SquareTerminal } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { cn, errorMessage, humanBytes, shortCommit } from '../lib/utils';
import { aiLabel, StateBadge } from './state';
import { AgentTokensCard } from './TokensPanel';
import { Button } from './ui/button';
import { Panel, Row } from './ui/card';
import { Field, Input } from './ui/input';
import { Select, SelectOption } from './ui/select';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';
import { Tip } from './ui/tooltip';

type OverviewSection = 'machine' | 'code' | 'ai';

export function OverviewTab({ agent }: { agent: T.Agent }) {
  const usage = useQuery({ queryKey: ['usage'], queryFn: api.usage });
  const diff = useQuery({ queryKey: ['diff', agent.ref], queryFn: () => api.diffStat(agent.ref), refetchInterval: 10_000 });
  const mine = usage.data?.agents.find((a) => a.ref === agent.ref);
  const cpu = mine?.cpu ?? 0;
  const cores = mine?.cores ?? 0;
  // Against the ceiling it actually has, and against the host when it has none.
  const memoryCap = agent.limits.memory ? parseBytes(agent.limits.memory) : usage.data?.host.memTotal;
  const memoryFraction = memoryCap ? Math.min((mine?.memory ?? 0) / memoryCap, 1) : undefined;
  const [section, setSection] = useState<OverviewSection>('machine');

  return (
    <div className="h-full overflow-y-auto p-3 md:p-5">
      <div className="mx-auto max-w-3xl">
        <Tabs value={section} onValueChange={(value) => setSection(value as OverviewSection)}>
          <TabsList className="mb-4">
            <TabsTrigger value="machine">
              <Box />
              Machine
            </TabsTrigger>
            <TabsTrigger value="code">
              <GitBranch />
              Code
            </TabsTrigger>
            <TabsTrigger value="ai">
              <SquareTerminal />
              AI tool
            </TabsTrigger>
          </TabsList>

          <TabsContent value="machine">
            <Panel className="p-5">
              <Row label="State">
                <StateBadge state={agent.state} />
              </Row>
              <Row label="IP address" mono>
                {agent.ip || '—'}
              </Row>
              <Row label="Preview" mono>
                <PreviewLink agent={agent} />
              </Row>
              <Row label="Instance" mono>
                {agent.instance}
              </Row>
              <Row label="Started from" mono>
                {agent.source || '—'}
              </Row>
              <Row label="Created">{new Date(agent.createdAt).toLocaleString()}</Row>
              <div className="mt-4 grid grid-cols-3 gap-3">
                {/* Against its own limit, not a fixed four cores: the question a
                    capped agent raises is whether it is the one at its wall. */}
                <Metric
                  label="CPU"
                  value={mine ? `${cpu.toFixed(0)}%` : '—'}
                  fraction={cores ? Math.min(cpu / (cores * 100), 1) : undefined}
                  hint={cores ? `100% is one core; this agent may use ${cores}` : '100% is one core'}
                />
                <Metric
                  label="Memory"
                  value={mine ? humanBytes(mine.memory) : '—'}
                  fraction={memoryFraction}
                  hint={agent.limits.memory ? `Capped at ${agent.limits.memory}` : 'No limit: it may take all the host has'}
                />
                <Metric label="Processes" value={mine ? String(mine.processes) : '—'} />
              </div>
              <div className="mt-3.5">
                <LimitsEditor agent={agent} />
              </div>
            </Panel>
          </TabsContent>

          <TabsContent value="code">
            <Panel className="p-5">
              <Row label="Branch" mono>
                {agent.branch}
              </Row>
              <Row label="Based on" mono>
                {agent.baseRef} @ {shortCommit(agent.baseCommit)}
              </Row>
              <Row label="Worktree" mono>
                <span className="truncate" title={agent.worktree}>
                  {agent.worktree}
                </span>
                <Tip label="Copy the path">
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label="Copy the worktree path"
                    onClick={() => {
                      window.agentbox.copyText(agent.worktree);
                      toast('Copied the worktree path');
                    }}
                  >
                    <Copy />
                  </Button>
                </Tip>
                <Tip label="Open the folder">
                  <Button variant="ghost" size="icon-sm" aria-label="Open the worktree folder" onClick={() => void window.agentbox.openPath(agent.worktree)}>
                    <FolderOpen />
                  </Button>
                </Tip>
              </Row>
              <div className="mt-4">
                <div className="mb-2 flex items-center gap-1.5 text-[11px] uppercase tracking-wider text-subtle">
                  <FileDiff className="size-3.5" />
                  Changes since {agent.baseRef}
                </div>
                <pre className="overflow-x-auto rounded-xl border border-line-faint bg-sunken p-3.5 font-mono text-[12px] leading-relaxed text-tertiary" aria-label="Diff summary">
                  {diff.isPending ? 'Loading…' : diff.data?.trim() || 'No changes yet.'}
                </pre>
              </div>
            </Panel>
          </TabsContent>

          <TabsContent value="ai">
            <Panel className="p-5">
              <Row label="Tool">{aiLabel(agent.ai)}</Row>
              {agent.ai !== 'none' && (
                <Row label="Interface">
                  <InterfacePicker agent={agent} />
                </Row>
              )}
              <Row label="Permissions">{agent.ai === 'none' ? '—' : agent.autonomous ? 'bypassed (autonomous)' : 'asks before acting'}</Row>
              {agent.ai === 'claude' && (
                <Row label="Account">
                  <ClaudeAccountPicker agent={agent} />
                </Row>
              )}
              <Row label="Shell" mono>
                agentbox shell {agent.ref}
              </Row>
            </Panel>
            {agent.ai !== 'none' && <AgentTokensCard agent={agent} />}
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}

// ClaudeAccountPicker moves a running agent to another stored Claude Code
// account: its token is replaced, and Claude Code uses it the next time it starts.
function ClaudeAccountPicker({ agent }: { agent: T.Agent }) {
  const queryClient = useQueryClient();
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const accounts = auth.data?.claudeAccounts ?? [];
  const pick = useMutation({
    mutationFn: (claudeAccount: string) => api.updateAgent(agent.ref, { claudeAccount }),
    onSuccess: async (updated) => {
      toast(`${updated.ref} uses the Claude Code account "${updated.claudeAccount}"`, {
        description: 'Claude Code picks the new token up the next time it starts.',
      });
      await queryClient.invalidateQueries({ queryKey: ['agents'] });
    },
  });

  if (accounts.length < 2) {
    return <span className="font-mono text-[12.5px]">{agent.claudeAccount || '—'}</span>;
  }
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-2">
      <Select
        aria-label="Claude Code account"
        className="h-8 max-w-52"
        value={agent.claudeAccount}
        disabled={pick.isPending || agent.state !== 'running'}
        onChange={(value) => pick.mutate(value)}
      >
        {accounts.map((account) => (
          <SelectOption key={account.name} value={account.name}>
            {account.name}
          </SelectOption>
        ))}
      </Select>
      {agent.state !== 'running' && <span className="text-xs text-subtle">Start the agent to change it</span>}
      {pick.error && <span className="text-xs text-rose-300">{errorMessage(pick.error)}</span>}
    </span>
  );
}

// InterfacePicker switches between the chat and the AI tool's own command line.
function InterfacePicker({ agent }: { agent: T.Agent }) {
  const queryClient = useQueryClient();
  const change = useMutation({
    mutationFn: (value: string) => api.updateAgent(agent.ref, { interface: value }),
    onSuccess: async (updated) => {
      toast(updated.interface === 'chat' ? `${updated.ref} uses the chat` : `${updated.ref} uses ${aiLabel(updated.ai)}'s command line`, {
        description: updated.interface === 'chat' ? 'Talk to it in the Chat tab.' : 'It runs in window 1 of the terminal.',
      });
      await queryClient.invalidateQueries({ queryKey: ['agents'] });
    },
  });
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-2">
      <span className="inline-flex rounded-lg border border-line bg-rail p-0.5" role="radiogroup" aria-label="Interface">
        {[
          ['chat', 'Chat'],
          ['cli', 'Terminal'],
        ].map(([value, label]) => (
          <button
            key={value}
            role="radio"
            aria-checked={agent.interface === value}
            disabled={change.isPending}
            onClick={() => agent.interface !== value && change.mutate(value)}
            className={cn('h-7 rounded-md px-2.5 text-[12.5px] text-muted transition hover:text-primary', agent.interface === value && 'bg-surface-strong text-title')}
          >
            {label}
          </button>
        ))}
      </span>
      {change.error && <span className="text-xs text-rose-300">{errorMessage(change.error)}</span>}
    </span>
  );
}

// LimitsEditor changes what the machine may take, while it runs: Incus applies
// all three of its keys to a running instance, so nothing restarts. A memory
// ceiling below what the agent is already using comes back refused — the
// daemon won't let the kernel kill the agent's work to enforce it.
function LimitsEditor({ agent }: { agent: T.Agent }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState({ cpu: agent.limits.cpu, cpuAllowance: agent.limits.allowance, memory: agent.limits.memory });
  const save = useMutation({
    mutationFn: () => api.updateAgent(agent.ref, { cpu: form.cpu.trim(), cpuAllowance: form.cpuAllowance.trim(), memory: form.memory.trim() }),
    onSuccess: async (updated) => {
      setOpen(false);
      toast(`${updated.ref}: ${limitWords(updated.limits)}`, {
        description: updated.state === 'running' ? 'Applied to the running machine.' : 'It will have them when it starts.',
      });
      await queryClient.invalidateQueries({ queryKey: ['agents'] });
      await queryClient.invalidateQueries({ queryKey: ['usage'] });
    },
  });

  if (!open) {
    return (
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-t border-line-faint pt-3">
        <span className="text-[13px] text-subtle">Limits</span>
        <span className="text-[13px] text-secondary" data-agent-limits>
          {limitWords(agent.limits)}
        </span>
        <Button
          variant="ghost"
          size="sm"
          className="ml-auto"
          onClick={() => {
            setForm({ cpu: agent.limits.cpu, cpuAllowance: agent.limits.allowance, memory: agent.limits.memory });
            save.reset();
            setOpen(true);
          }}
        >
          <SlidersHorizontal />
          Edit
        </Button>
      </div>
    );
  }
  return (
    <form
      className="grid gap-3 border-t border-line-faint pt-3"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <div className="grid grid-cols-3 gap-3">
        <Field label="CPU cores" htmlFor="limits-cpu" hint="Empty: every core">
          <Input id="limits-cpu" autoFocus className="h-8 font-mono text-[12.5px]" placeholder="every core" value={form.cpu} onChange={(e) => setForm({ ...form, cpu: e.target.value })} />
        </Field>
        <Field label="CPU share" htmlFor="limits-allowance" hint="50%, or 25ms/100ms">
          <Input
            id="limits-allowance"
            className="h-8 font-mono text-[12.5px]"
            placeholder="all of it"
            value={form.cpuAllowance}
            onChange={(e) => setForm({ ...form, cpuAllowance: e.target.value })}
          />
        </Field>
        <Field label="Memory" htmlFor="limits-memory" hint="A hard ceiling">
          <Input id="limits-memory" className="h-8 font-mono text-[12.5px]" placeholder="all of it" value={form.memory} onChange={(e) => setForm({ ...form, memory: e.target.value })} />
        </Field>
      </div>
      {save.error && <span className="text-xs leading-relaxed text-rose-300">{errorMessage(save.error)}</span>}
      <div className="flex items-center gap-2">
        <Button type="submit" variant="primary" size="sm" disabled={save.isPending}>
          {save.isPending && <LoaderCircle className="animate-spin" />}
          Apply
        </Button>
        <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
          Cancel
        </Button>
        <span className="text-xs text-subtle">Applies while the agent runs. Empty removes a limit.</span>
      </div>
    </form>
  );
}

// limitWords says what a set of limits means, naming what is uncapped rather
// than leaving it out: "no memory limit" is the fact worth reading.
function limitWords(limits: T.Limits): string {
  const cores = limits.cpu ? `${limits.cpu} core${limits.cpu === '1' ? '' : 's'}` : 'every core';
  const memory = limits.memory ? `${limits.memory} of memory` : 'no memory limit';
  return [cores, memory, ...(limits.allowance ? [`a CPU share of ${limits.allowance}`] : [])].join(', ');
}

// parseBytes reads the sizes Incus takes, to show memory use as a fraction of
// the agent's own ceiling.
function parseBytes(size: string): number | undefined {
  const match = /^\s*([0-9.]+)\s*(B|kB|MB|GB|TB|KiB|MiB|GiB|TiB)?\s*$/.exec(size);
  if (!match) return undefined;
  const units: Record<string, number> = {
    B: 1,
    kB: 1e3,
    MB: 1e6,
    GB: 1e9,
    TB: 1e12,
    KiB: 1024,
    MiB: 1024 ** 2,
    GiB: 1024 ** 3,
    TiB: 1024 ** 4,
  };
  return Number(match[1]) * (units[match[2] ?? 'B'] ?? 1);
}

function Metric({ label, value, fraction, hint }: { label: string; value: string; fraction?: number; hint?: string }) {
  return (
    <div className="rounded-xl border border-line-faint bg-surface-faint p-3" title={hint}>
      <div className="text-[11px] uppercase tracking-wider text-subtle">{label}</div>
      <div className="mt-1 font-mono text-lg tabular-nums text-primary">{value}</div>
      {fraction !== undefined && (
        <div className="mt-2 h-1 overflow-hidden rounded-full bg-surface-raised">
          <div className="h-full rounded-full bg-gradient-to-r from-brand-400 to-sky-400" style={{ width: `${Math.max(fraction * 100, 3)}%` }} />
        </div>
      )}
    </div>
  );
}

// PreviewLink opens a port of the agent in your own browser, through the
// daemon's preview proxy.
function PreviewLink({ agent }: { agent: T.Agent }) {
  const preview = useQuery({ queryKey: ['preview'], queryFn: api.preview, staleTime: Infinity });
  const [port, setPort] = useState('3000');
  const addr = preview.data?.addr;
  if (!addr) return <span className="text-subtle">{preview.isPending ? '…' : 'off'}</span>;
  const url = `http://${port || '3000'}.${agent.name}.${agent.project}.localhost:${addr.split(':').pop()}`;
  return (
    <>
      <input
        aria-label="Preview port"
        className="w-14 rounded-md border border-line-strong bg-well px-1.5 py-0.5 text-[12px] text-primary outline-none focus:border-brand-400/50"
        value={port}
        onChange={(event) => setPort(event.target.value.replace(/\D/g, '').slice(0, 5))}
      />
      <span className="truncate text-muted" title={url} data-preview-url={url}>
        {url}
      </span>
      <Tip label="Open in your browser">
        <Button variant="ghost" size="icon-sm" aria-label="Open the preview in your browser" onClick={() => void window.agentbox.openExternal(url)}>
          <ExternalLink />
        </Button>
      </Tip>
    </>
  );
}
