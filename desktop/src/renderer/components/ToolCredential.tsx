import { useQuery } from '@tanstack/react-query';
import { ChevronRight, TriangleAlert } from 'lucide-react';
import { useState } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { aiToolsReading, credentialTone, summarizeCredentials } from '../lib/toolCredentials';
import { cn } from '../lib/utils';
import { aiLabel } from './state';

// What an agent's AI tool really runs on, as the daemon read it from the
// tool's running process (GET .../credential), not what the settings say it
// should: an ANTHROPIC_API_KEY that reached Claude Code shows here, in amber.

export function useToolCredential(ref: string, enabled = true) {
  return useQuery({ queryKey: ['toolCredential', ref], queryFn: () => api.agentCredential(ref), staleTime: 15_000, enabled });
}

// ToolCredentialText is one tool's credential in a few words.
export function ToolCredentialText({ cred, className }: { cred: T.ToolCredential | undefined; className?: string }) {
  const t = useT();
  if (!cred) return <span className={className}>…</span>;
  const tone = credentialTone(cred);
  let text: string;
  if (cred.error) text = t('agent.credential.error');
  else if (!cred.running) text = t('agent.credential.notRunning');
  else if (cred.variable === 'CLAUDE_CODE_OAUTH_TOKEN')
    text = cred.account ? t('agent.credential.account', { account: cred.account }) : t('agent.credential.unknownToken');
  else if (cred.variable) text = cred.account ? t('agent.credential.variableOver', { variable: cred.variable, account: cred.account }) : t('agent.credential.variable', { variable: cred.variable });
  else text = t('agent.credential.ownLogin');
  const others = cred.others?.length ? t('agent.credential.others', { names: cred.others.join(', ') }) : '';
  return (
    <span
      className={cn('min-w-0', tone === 'warning' ? 'text-amber-300' : tone === 'idle' ? 'text-subtle' : undefined, className)}
      title={[cred.error, others].filter(Boolean).join('\n') || undefined}
      data-tool-credential={cred.agent}
      data-tool-credential-variable={cred.variable ?? ''}
    >
      {tone === 'warning' && <TriangleAlert className="mr-1 inline size-3.5 align-[-2px]" />}
      {text}
      {others && <span className="text-subtle"> · {others}</span>}
    </span>
  );
}

// AgentToolCredential is ToolCredentialText fetched for one agent.
export function AgentToolCredential({ agentRef, className }: { agentRef: string; className?: string }) {
  const cred = useToolCredential(agentRef);
  return <ToolCredentialText cred={cred.data} className={className} />;
}

// ProjectToolCredentials is what every agent of a project with an AI tool
// runs on, for its AI accounts: one line ("6 agents on work", or a warning
// when any bills a variable instead), and every agent once you open it.
export function ProjectToolCredentials({ project }: { project: string }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const creds = useQuery({ queryKey: ['toolCredentials', project], queryFn: () => api.projectCredentials(project), staleTime: 15_000 });
  if (creds.isPending) return <p className="text-[12px] text-subtle">{t('common.loading')}</p>;
  const list = creds.data ?? [];
  if (list.length === 0) return <p className="text-[12px] text-subtle">{t('project.accounts.runningOnNone')}</p>;
  const summary = summarizeCredentials(list);
  const groups = summary.groups.map(({ on, count }) =>
    on === 'idle'
      ? t('project.accounts.runningOnIdle', { count })
      : t('project.accounts.runningOnGroup', { count, on: on === 'own' ? t('project.accounts.runningOnOwn') : on === 'token' ? t('project.accounts.runningOnToken') : on }),
  );
  return (
    <div className="grid min-w-0 gap-2" data-project-tool-credentials={project}>
      <button
        type="button"
        className="flex min-w-0 items-baseline gap-1.5 text-left text-[12px] text-secondary transition hover:text-primary"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        data-tool-credentials-summary
      >
        <ChevronRight className={cn('size-3.5 shrink-0 self-center transition-transform', open && 'rotate-90')} />
        <span className="min-w-0 break-words">
          {summary.off > 0 && (
            <span className="text-amber-300">
              <TriangleAlert className="mr-1 inline size-3.5 align-[-2px]" />
              {t('project.accounts.runningOnOff', { count: summary.off, variables: summary.variables.join(', ') })}
              {groups.length > 0 && ' · '}
            </span>
          )}
          {groups.join(' · ')}
        </span>
      </button>
      {open && (
        <ul className="grid min-w-0 gap-1 pl-5">
          {list.map((cred) => (
            <li key={cred.agent} className="flex min-w-0 items-baseline justify-between gap-3 text-[12px]">
              <span className="shrink-0 font-mono text-[11.5px] text-secondary">
                {cred.agent.split('/')[1]} <span className="font-sans text-faint">· {aiLabel(cred.tool)}</span>
              </span>
              <ToolCredentialText cred={cred} className="truncate text-right" />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// AIToolSecretWarning says, for a secret named after a variable an AI tool
// picks its credential by, that the tool won't use it: it's for the agent's
// code. Nothing for any other name.
export function AIToolSecretWarning({ name, className }: { name: string; className?: string }) {
  const t = useT();
  const tools = aiToolsReading(name);
  if (tools.length === 0) return null;
  return (
    <p className={cn('flex min-w-0 gap-1.5 text-[12px] leading-relaxed text-amber-300', className)} data-ai-tool-secret-warning={name.trim()}>
      <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
      <span className="min-w-0 break-words">{t('agent.secrets.aiToolWarning', { name: name.trim(), tools: tools.map(aiLabel).join(t('agent.secrets.toolsAnd')) })}</span>
    </p>
  );
}
