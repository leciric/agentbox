import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, Check, KeyRound, LoaderCircle, Plus, X } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import * as A from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { projectLabel } from '../lib/projectName';
import { cn, errorMessage } from '../lib/utils';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from './ui/dialog';
import { Input } from './ui/input';
import { Select, SelectOption } from './ui/select';

// The AI tools a server can come from, by their own names, which aren't
// translated.
const toolNames: Record<string, string> = {
  claude: 'Claude Code',
  'claude-desktop': 'Claude Desktop',
  codex: 'Codex',
  opencode: 'OpenCode',
  cursor: 'Cursor',
  gemini: 'Gemini CLI',
};

// ConnectorImportDialog lists the MCP servers the user's own AI tools have
// (the daemon reads their files: internal/connectors/found.go), lets them
// tick the ones to import, and adds them where it was opened: AgentBox-wide
// from AgentBox's Settings, or a project from its tab, which can send them
// AgentBox-wide instead. Those that sign in with OAuth then open their
// sign-in in the browser, as adding one by hand does. A header the tool's file
// sends goes from that file into a secret without passing through here; one
// it reads from the user's shell is typed in.
export function ConnectorImportDialog({ project, onClose }: { project?: string; onClose: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  // From a project's tab it imports into that project unless the user says
  // AgentBox-wide; from AgentBox's Settings, AgentBox-wide.
  const [scope, setScope] = useState(project ?? '');
  const scan = useQuery({ queryKey: ['connectorScan', scope], queryFn: () => api.scanConnectors(scope), retry: false, staleTime: 0, gcTime: 0 });
  const found = useMemo(() => scan.data?.candidates ?? [], [scan.data]);
  const importable = found.filter((c) => !c.problem && !c.existing);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [values, setValues] = useState<Record<string, string>>({});
  useEffect(() => {
    // What can be imported as it is starts picked; one that needs a value is
    // picked by typing it.
    setPicked(new Set(found.filter((c) => !c.problem && !c.existing && !c.needsValue).map((c) => c.url!)));
  }, [found]);

  const toggle = (url: string) =>
    setPicked((p) => {
      const next = new Set(p);
      if (next.has(url)) next.delete(url);
      else next.add(url);
      return next;
    });
  const missing = found.some((c) => c.needsValue && picked.has(c.url!) && !values[c.url!]?.trim());

  const run = useMutation({
    mutationFn: () =>
      api.importConnectors({
        project: scope,
        servers: found.filter((c) => picked.has(c.url!)).map((c) => ({ url: c.url!, ...(c.needsValue ? { value: values[c.url!]?.trim() } : {}) })),
      }),
    onSuccess: async (res) => {
      await queryClient.invalidateQueries({ queryKey: ['connectors'] });
      for (const f of res.failed) toast.error(t('project.connectors.importFailed', { name: f.name || f.url, error: f.error }));
      if (res.added.length === 0) return;
      const oauth = res.added.filter((c) => c.auth === A.ConnectorOAuth);
      toast.success(t('project.connectors.imported', { count: res.added.length }), { description: oauth.length > 0 ? t('project.connectors.importedSignIn') : undefined });
      onClose();
      for (const c of oauth) {
        try {
          const started = await api.connectConnector(scope, c.name);
          await window.agentbox.openExternal(started.authorizationUrl);
        } catch (err) {
          toast.error(t('project.connectors.notConnected', { name: c.name }), { description: errorMessage(err) });
        }
      }
      await queryClient.invalidateQueries({ queryKey: ['connectors'] });
    },
  });

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="flex h-[82vh] max-w-3xl flex-col gap-0 overflow-hidden p-0" data-connectors-import-dialog>
        <div className="shrink-0 px-6 pb-4 pt-5">
          <DialogTitle>{t('project.connectors.importTitle')}</DialogTitle>
          <DialogDescription className="mt-1">{t('project.connectors.importDescription')}</DialogDescription>
          <div className="mt-4 flex items-center gap-3">
            <label htmlFor="connector-import-scope" className="shrink-0 text-[12.5px] text-subtle">
              {t('project.connectors.importScope')}
            </label>
            {project ? (
              <Select id="connector-import-scope" className="max-w-sm" value={scope} onChange={setScope}>
                <SelectOption value={project}>{t('project.connectors.importScopeProject', { project: projectLabel(project, projects.data) })}</SelectOption>
                <SelectOption value="">{t('project.connectors.importScopeWide')}</SelectOption>
              </Select>
            ) : (
              <span id="connector-import-scope" className="text-[13px] text-primary">
                {t('project.connectors.importScopeWide')}
              </span>
            )}
          </div>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto border-t border-line-faint px-6 py-4">
          {scan.isFetching && (
            <p className="flex items-center justify-center gap-2 py-10 text-[13px] text-subtle">
              <LoaderCircle className="size-4 animate-spin" /> {t('project.connectors.importLooking')}
            </p>
          )}
          {scan.error && !scan.isFetching && <Notice>{errorMessage(scan.error)}</Notice>}
          {!scan.isFetching &&
            scan.data?.unreadable.map((u) => (
              <Notice key={u.path} tone="warning">
                {t('project.connectors.importUnreadable', { tool: toolNames[u.tool] ?? u.tool, path: u.path, error: u.error })}
              </Notice>
            ))}
          {!scan.isFetching && !scan.error && found.length === 0 && <p className="py-10 text-center text-[13px] text-subtle">{t('project.connectors.importNone')}</p>}
          {!scan.isFetching && (
            <ul className="grid gap-1.5" aria-label={t('project.connectors.importTitle')}>
              {found.map((c) => (
                <CandidateRow
                  key={c.url || `${c.name}:${c.command ?? ''}`}
                  candidate={c}
                  checked={!!c.url && picked.has(c.url)}
                  value={values[c.url ?? ''] ?? ''}
                  onToggle={() => toggle(c.url!)}
                  onValue={(v) => {
                    setValues((all) => ({ ...all, [c.url!]: v }));
                    if (v.trim()) setPicked((p) => new Set(p).add(c.url!));
                  }}
                />
              ))}
            </ul>
          )}
        </div>

        <div className="flex shrink-0 items-center gap-3 border-t border-line px-6 py-3">
          {run.error ? (
            <p className="flex min-w-0 items-center gap-2 text-[12.5px] text-rose-300">
              <AlertTriangle className="size-4 shrink-0" /> <span className="truncate">{errorMessage(run.error)}</span>
            </p>
          ) : importable.length > 0 ? (
            <div className="flex items-center gap-3 text-[12.5px]">
              <button className="text-brand-300 hover:underline" onClick={() => setPicked(new Set(importable.map((c) => c.url!)))}>
                {t('project.connectors.importSelectAll')}
              </button>
              <button className="text-subtle hover:text-primary" onClick={() => setPicked(new Set())}>
                {t('project.connectors.importSelectNone')}
              </button>
            </div>
          ) : null}
          <div className="ml-auto flex gap-2">
            <Button variant="ghost" onClick={onClose}>
              <X /> {t('common.cancel')}
            </Button>
            <Button variant="primary" disabled={picked.size === 0 || missing || run.isPending || scan.isFetching} onClick={() => run.mutate()} data-connectors-import-run>
              {run.isPending ? <LoaderCircle className="animate-spin" /> : <Plus />} {t('project.connectors.importCount', { count: picked.size })}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

// CandidateRow is one server found: where it is from, how it would sign in,
// and why it can't be imported when it can't, greyed out.
function CandidateRow({
  candidate: c,
  checked,
  value,
  onToggle,
  onValue,
}: {
  candidate: T.ConnectorCandidate;
  checked: boolean;
  value: string;
  onToggle: () => void;
  onValue: (value: string) => void;
}) {
  const t = useT();
  const blocked = !!c.problem || !!c.existing;
  const problem =
    c.problem === A.ConnectorProblemStdio
      ? t('project.connectors.importProblemStdio', { command: c.command ?? '' })
      : c.problem === A.ConnectorProblemSSE
        ? t('project.connectors.importProblemSse')
        : c.problem === A.ConnectorProblemVariable
          ? t('project.connectors.importProblemVariable')
          : c.problem
            ? t('project.connectors.importProblemUrl')
            : undefined;
  const tools = [...new Set(c.sources.map((s) => toolNames[s.tool] ?? s.tool))];
  const inProject = c.sources.find((s) => s.project);

  return (
    <li
      data-connector-candidate={c.name}
      data-connector-candidate-problem={c.problem || undefined}
      className={cn('rounded-xl border px-3 py-2.5 transition-colors', checked ? 'border-brand-400/40 bg-brand-500/[0.06]' : 'border-line bg-surface', blocked && 'opacity-60')}
    >
      <div className="flex items-start gap-3">
        <button
          role="checkbox"
          aria-checked={checked}
          aria-label={c.name}
          disabled={blocked}
          onClick={onToggle}
          className={cn(
            'mt-0.5 flex size-[18px] shrink-0 items-center justify-center rounded-md border transition-colors disabled:cursor-default',
            checked ? 'border-transparent bg-brand-500 text-white' : 'border-line-vivid bg-sunken',
          )}
        >
          {checked && <Check className="size-3" strokeWidth={3} />}
        </button>
        <div className="grid min-w-0 flex-1 gap-1">
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <span className="font-mono text-[13px] font-medium text-title">{c.name}</span>
            {tools.map((tool) => (
              <Badge key={tool}>{tool}</Badge>
            ))}
            {c.existing && <Badge variant="brand">{t('project.connectors.importExisting', { name: c.existing })}</Badge>}
          </div>
          {(c.url || c.command) && (
            <span className="min-w-0 truncate font-mono text-[11.5px] text-subtle" title={c.url}>
              {c.url || c.command}
            </span>
          )}
          {inProject && tools.length === 1 && (
            <span className="min-w-0 truncate text-[11.5px] text-faint" title={inProject.project}>
              {t('project.connectors.importInProject', { tool: toolNames[inProject.tool] ?? inProject.tool, project: inProject.project ?? '' })}
            </span>
          )}
          {problem ? (
            <p className="flex items-center gap-1.5 text-[12px] text-subtle">
              <AlertTriangle className="size-3.5 shrink-0" /> {problem}
            </p>
          ) : c.existing ? null : c.needsValue ? (
            <div className="grid gap-1.5">
              <p className="flex items-center gap-1.5 text-[12px] text-muted">
                <KeyRound className="size-3.5 shrink-0" /> {t('project.connectors.importNeedsValue', { header: c.header ?? '', variable: c.variable ?? '' })}
              </p>
              <Input
                type="password"
                className="h-8 max-w-sm font-mono text-[12.5px]"
                placeholder={t('project.connectors.importValuePlaceholder', { variable: c.variable ?? '' })}
                aria-label={t('project.connectors.importValuePlaceholder', { variable: c.variable ?? '' })}
                autoComplete="off"
                spellCheck={false}
                value={value}
                onChange={(event) => onValue(event.target.value)}
              />
            </div>
          ) : (
            <p className="flex items-center gap-1.5 text-[12px] text-muted">
              {c.header ? <KeyRound className="size-3.5 shrink-0" /> : null}
              {c.header ? t('project.connectors.importHeader', { header: c.header, secret: c.secret ?? '' }) : t('project.connectors.importOAuth')}
            </p>
          )}
        </div>
      </div>
    </li>
  );
}
