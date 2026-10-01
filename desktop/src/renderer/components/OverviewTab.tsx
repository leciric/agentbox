import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Copy, ExternalLink, FileDiff, FolderOpen } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { cn, errorMessage, humanBytes, humanRate, shortCommit, shortRate } from '../lib/utils';
import { aiLabel, StateBadge } from './state';
import { AgentTokensCard } from './TokensPanel';
import { Button } from './ui/button';
import { Panel, Row } from './ui/card';
import { Select, SelectOption } from './ui/select';
import { Tip } from './ui/tooltip';

// OverviewTab is what an agent's Settings tab says about its machine, its code
// and its AI tool: one section of it at a time.
export function OverviewTab({ agent, section = 'machine' }: { agent: T.Agent; section?: 'machine' | 'code' | 'ai' }) {
  const usage = useQuery({ queryKey: ['usage'], queryFn: api.usage });
  const diff = useQuery({ queryKey: ['diff', agent.ref], queryFn: () => api.diffStat(agent.ref), refetchInterval: 10_000 });
  const events = useQuery({ queryKey: ['agentEvents', agent.project], queryFn: () => api.agentEvents(agent.project) });
  const idleStop = events.data?.find((ev) => ev.ref === agent.ref && ev.kind === 'idle_stopped');
  const mine = usage.data?.agents.find((a) => a.ref === agent.ref);
  const cpu = mine?.cpu ?? 0;
  const cores = usage.data?.host.cores ?? 0;
  const memoryCap = usage.data?.host.memTotal;
  const memoryFraction = memoryCap ? Math.min((mine?.memory ?? 0) / memoryCap, 1) : undefined;

  return (
    <div className="h-full overflow-y-auto p-3 md:p-5">
      <div className="mx-auto max-w-3xl">
        {section === 'machine' && (
          <>
            <Panel className="p-5">
              <Row label="State">
                <StateBadge state={agent.state} />
                {agent.state === 'stopped' && idleStop && <span className="text-tertiary ml-2 text-sm">{idleStop.summary}</span>}
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
              <div className="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
                <Metric
                  label="CPU"
                  value={mine ? `${cpu.toFixed(0)}%` : '—'}
                  fraction={cores ? Math.min(cpu / (cores * 100), 1) : undefined}
                  hint={cores ? `100% is one core; it may use all ${cores} of the VM's` : '100% is one core'}
                />
                <Metric
                  label="Memory"
                  value={mine ? humanBytes(mine.memory) : '—'}
                  fraction={memoryFraction}
                  hint="It may take all the VM's memory"
                />
                <Metric
                  label="Disk IO"
                  value={mine ? humanRate(mine.diskRead + mine.diskWrite) : '—'}
                  detail={
                    mine && (
                      <>
                        <span className="block truncate">{shortRate(mine.diskRead)} read</span>
                        <span className="block truncate">{shortRate(mine.diskWrite)} write</span>
                      </>
                    )
                  }
                  hint="What its machine reads from and writes to the host's disks"
                />
                <Metric label="Processes" value={mine ? String(mine.processes) : '—'} />
              </div>
            </Panel>
          </>
        )}

        {section === 'code' && (
          <>
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
          </>
        )}

        {section === 'ai' && (
          <>
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
          </>
        )}
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

function Metric({ label, value, detail, fraction, hint }: { label: string; value: string; detail?: ReactNode; fraction?: number; hint?: string }) {
  return (
    <div className="min-w-0 rounded-xl border border-line-faint bg-surface-faint p-3" title={hint}>
      <div className="text-[11px] uppercase tracking-wider text-subtle">{label}</div>
      <div className="mt-1 font-mono text-lg tabular-nums text-primary">{value}</div>
      {detail && <div className="mt-1 font-mono text-[10.5px] tabular-nums text-subtle">{detail}</div>}
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
