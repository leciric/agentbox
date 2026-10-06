import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Copy, ExternalLink, FileDiff, FolderOpen } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatDateTime, useT } from '../lib/i18n';
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
  const t = useT();
  const usage = useQuery({ queryKey: ['usage'], queryFn: api.usage });
  const diff = useQuery({ queryKey: ['diff', agent.ref], queryFn: () => api.diffStat(agent.ref), refetchInterval: 10_000 });
  const events = useQuery({ queryKey: ['agentEvents', agent.project], queryFn: () => api.agentEvents(agent.project) });
  const idleStop = events.data?.find((ev) => ev.ref === agent.ref && ev.kind === 'idle_stopped');
  const dockerPruned = events.data?.find((ev) => ev.ref === agent.ref && ev.kind === 'docker_pruned');
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
              <Row label={t('agent.overview.state')}>
                <StateBadge state={agent.state} />
                {agent.state === 'stopped' && idleStop && <span className="text-tertiary ml-2 text-sm">{idleStop.summary}</span>}
                {agent.state === 'stopped' && dockerPruned && (
                  <span data-docker-pruned className="text-tertiary ml-2 text-sm">
                    {dockerPruned.summary}
                  </span>
                )}
              </Row>
              <Row label={t('agent.overview.ip')} mono>
                {agent.ip || '—'}
              </Row>
              <Row label={t('agent.overview.preview')} mono>
                <PreviewLink agent={agent} />
              </Row>
              <Row label={t('agent.overview.instance')} mono>
                {agent.instance}
              </Row>
              <Row label={t('agent.overview.startedFrom')} mono>
                {agent.source || '—'}
              </Row>
              <Row label={t('agent.overview.created')}>{formatDateTime(agent.createdAt)}</Row>
              <div className="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
                <Metric
                  label="CPU"
                  value={mine ? `${cpu.toFixed(0)}%` : '—'}
                  fraction={cores ? Math.min(cpu / (cores * 100), 1) : undefined}
                  hint={cores ? t('agent.overview.cpuHintCores', { cores }) : t('agent.overview.cpuHint')}
                />
                <Metric
                  label={t('agent.overview.memory')}
                  value={mine ? humanBytes(mine.memory) : '—'}
                  fraction={memoryFraction}
                  hint={t('agent.overview.memoryHint')}
                />
                <Metric
                  label={t('agent.overview.diskIo')}
                  value={mine ? humanRate(mine.diskRead + mine.diskWrite) : '—'}
                  detail={
                    mine && (
                      <>
                        <span className="block truncate">{t('agent.overview.read', { rate: shortRate(mine.diskRead) })}</span>
                        <span className="block truncate">{t('agent.overview.write', { rate: shortRate(mine.diskWrite) })}</span>
                      </>
                    )
                  }
                  hint={t('agent.overview.diskIoHint')}
                />
                <Metric label={t('agent.overview.processes')} value={mine ? String(mine.processes) : '—'} />
              </div>
            </Panel>
          </>
        )}

        {section === 'code' && (
          <>
            <Panel className="p-5">
              <Row label={t('agent.view.branch')} mono>
                {agent.branch}
              </Row>
              <Row label={t('agent.overview.basedOn')} mono>
                {agent.baseRef} @ {shortCommit(agent.baseCommit)}
              </Row>
              <Row label={t('agent.overview.worktree')} mono>
                <span className="truncate" title={agent.worktree}>
                  {agent.worktree}
                </span>
                <Tip label={t('agent.overview.copyPath')}>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t('agent.view.copyWorktree')}
                    onClick={() => {
                      window.agentbox.copyText(agent.worktree);
                      toast(t('agent.view.copiedWorktree'));
                    }}
                  >
                    <Copy />
                  </Button>
                </Tip>
                <Tip label={t('agent.overview.openFolder')}>
                  <Button variant="ghost" size="icon-sm" aria-label={t('agent.view.openWorktree')} onClick={() => void window.agentbox.openPath(agent.worktree)}>
                    <FolderOpen />
                  </Button>
                </Tip>
              </Row>
              <div className="mt-4">
                <div className="mb-2 flex items-center gap-1.5 text-[11px] uppercase tracking-wider text-subtle">
                  <FileDiff className="size-3.5" />
                  {t('agent.overview.changesSince', { ref: agent.baseRef })}
                </div>
                <pre className="overflow-x-auto rounded-xl border border-line-faint bg-sunken p-3.5 font-mono text-[12px] leading-relaxed text-tertiary" aria-label={t('agent.overview.diffSummary')}>
                  {diff.isPending ? t('common.loading') : diff.data?.trim() || t('agent.overview.noChanges')}
                </pre>
              </div>
            </Panel>
          </>
        )}

        {section === 'ai' && (
          <>
            <Panel className="p-5">
              <Row label={t('agent.overview.tool')}>{aiLabel(agent.ai)}</Row>
              {agent.ai !== 'none' && (
                <Row label={t('agent.overview.interface')}>
                  <InterfacePicker agent={agent} />
                </Row>
              )}
              <Row label={t('agent.overview.permissions')}>{agent.ai === 'none' ? '—' : agent.autonomous ? t('agent.overview.bypassed') : t('agent.overview.asks')}</Row>
              {agent.ai === 'claude' && (
                <Row label={t('agent.overview.account')}>
                  <ClaudeAccountPicker agent={agent} />
                </Row>
              )}
              <Row label={t('agent.overview.shell')} mono>
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
  const t = useT();
  const queryClient = useQueryClient();
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth });
  const accounts = auth.data?.claudeAccounts ?? [];
  const pick = useMutation({
    mutationFn: (claudeAccount: string) => api.updateAgent(agent.ref, { claudeAccount }),
    onSuccess: async (updated) => {
      toast(t('agent.overview.accountChanged', { ref: updated.ref, account: updated.claudeAccount }), {
        description: t('agent.overview.accountChangedNote'),
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
        aria-label={t('agent.overview.claudeAccount')}
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
      {agent.state !== 'running' && <span className="text-xs text-subtle">{t('agent.overview.startToChange')}</span>}
      {pick.error && <span className="text-xs text-rose-300">{errorMessage(pick.error)}</span>}
    </span>
  );
}

// InterfacePicker switches between the chat and the AI tool's own command line.
function InterfacePicker({ agent }: { agent: T.Agent }) {
  const t = useT();
  const queryClient = useQueryClient();
  const change = useMutation({
    mutationFn: (value: string) => api.updateAgent(agent.ref, { interface: value }),
    onSuccess: async (updated) => {
      toast(updated.interface === 'chat' ? t('agent.overview.usesChat', { ref: updated.ref }) : t('agent.overview.usesCli', { ref: updated.ref, tool: aiLabel(updated.ai) }), {
        description: updated.interface === 'chat' ? t('agent.overview.usesChatNote') : t('agent.overview.usesCliNote'),
      });
      await queryClient.invalidateQueries({ queryKey: ['agents'] });
    },
  });
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-2">
      <span className="inline-flex rounded-lg border border-line bg-rail p-0.5" role="radiogroup" aria-label={t('agent.overview.interface')}>
        {[
          ['chat', t('agent.tab.chat')],
          ['cli', t('agent.tab.terminal')],
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
  const t = useT();
  const preview = useQuery({ queryKey: ['preview'], queryFn: api.preview, staleTime: Infinity });
  const [port, setPort] = useState('3000');
  const addr = preview.data?.addr;
  if (!addr) return <span className="text-subtle">{preview.isPending ? '…' : t('agent.overview.previewOff')}</span>;
  const url = `http://${port || '3000'}.${agent.name}.${agent.project}.localhost:${addr.split(':').pop()}`;
  return (
    <>
      <input
        aria-label={t('agent.overview.previewPort')}
        className="w-14 rounded-md border border-line-strong bg-well px-1.5 py-0.5 text-[12px] text-primary outline-none focus:border-brand-400/50"
        value={port}
        onChange={(event) => setPort(event.target.value.replace(/\D/g, '').slice(0, 5))}
      />
      <span className="truncate text-muted" title={url} data-preview-url={url}>
        {url}
      </span>
      <Tip label={t('agent.overview.openInBrowser')}>
        <Button variant="ghost" size="icon-sm" aria-label={t('agent.overview.openPreview')} onClick={() => void window.agentbox.openExternal(url)}>
          <ExternalLink />
        </Button>
      </Tip>
    </>
  );
}
