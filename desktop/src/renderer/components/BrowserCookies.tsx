import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Cookie, FileUp, LoaderCircle, Trash2, Upload } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { errorMessage, timeAgo } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Card, Code, Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input, Textarea } from './ui/input';

// BrowserCookies, in a project's Secrets: cookies the user exported from their
// own browser (a cookies.txt, or a cookie extension's JSON), kept to the
// domains they pick and sealed like a secret, so the agents made afterwards
// start their Chromium signed in to those sites. AgentBox never reads a
// browser's own files, and no page reads a cookie back: what shows is the
// domains and how many.

export function BrowserCookiesCard({ project }: { project: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  const info = useQuery({ queryKey: ['browserCookies', project], queryFn: () => api.browserCookies(project) });
  const [importing, setImporting] = useState(false);
  const [removing, setRemoving] = useState(false);
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['browserCookies', project] });
  const imported = info.data?.imported ? info.data : undefined;

  return (
    <Card
      title={t('agent.cookies.title')}
      icon={Cookie}
      description={t('agent.cookies.description')}
    >
      {info.error && <Notice>{errorMessage(info.error)}</Notice>}
      {imported ? (
        <div className="grid gap-3" data-browser-cookies="imported">
          <div className="flex flex-wrap items-center gap-2">
            {imported.domains.map((d) => (
              <Badge key={d} variant="info">
                {d}
              </Badge>
            ))}
          </div>
          <p className="text-[12.5px] leading-relaxed text-subtle">
            {imported.importedAt
              ? t('agent.cookies.summary', { count: imported.cookies, when: timeAgo(imported.importedAt), format: imported.format })
              : t('agent.cookies.summaryNoTime', { count: imported.cookies, format: imported.format })}
          </p>
          <div className="flex gap-2">
            <Button variant="secondary" onClick={() => setImporting(true)}>
              <Upload />
              {t('agent.cookies.importAgain')}
            </Button>
            <Button variant="danger" onClick={() => setRemoving(true)}>
              <Trash2 />
              {t('common.remove')}
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-3" data-browser-cookies="none">
          <p className="text-[12.5px] leading-relaxed text-subtle">
            {t('agent.cookies.none')}
          </p>
          <Button variant="secondary" className="ml-auto" onClick={() => setImporting(true)} disabled={info.isPending}>
            <Upload />
            {t('agent.cookies.import')}
          </Button>
        </div>
      )}

      <ImportCookiesDialog project={project} open={importing} onOpenChange={setImporting} onImported={refresh} />
      <ConfirmDialog
        open={removing}
        onOpenChange={setRemoving}
        title={t('agent.cookies.removeTitle')}
        description={t('agent.cookies.removeDescription')}
        confirmLabel={t('common.remove')}
        destructive
        onConfirm={async () => {
          await api.removeBrowserCookies(project);
          toast(t('agent.cookies.removed'));
          await refresh();
        }}
      />
    </Card>
  );
}

// ImportCookiesDialog takes an export, pasted or picked as a file, shows the
// sites it has cookies for, and imports the ones ticked (or typed).
function ImportCookiesDialog({
  project,
  open,
  onOpenChange,
  onImported,
}: {
  project: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onImported: () => Promise<unknown>;
}) {
  const t = useT();
  const [exported, setExported] = useState('');
  const [fileName, setFileName] = useState('');
  const [preview, setPreview] = useState<T.BrowserCookiesPreview | null>(null);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [typed, setTyped] = useState('');
  const fileInput = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (open) return;
    // Nothing of an export outlives the dialog.
    setExported('');
    setFileName('');
    setPreview(null);
    setPicked(new Set());
    setTyped('');
  }, [open]);

  const read = useMutation({
    mutationFn: (text: string) => api.previewBrowserCookies(project, text),
    onSuccess: (p) => {
      setPreview(p);
      setPicked(new Set());
    },
    onError: () => setPreview(null),
  });
  const domains = [...picked, ...typed.split(/[\s,]+/).filter(Boolean)];
  const save = useMutation({
    mutationFn: () => api.importBrowserCookies(project, exported, domains),
    onSuccess: async (info) => {
      toast(t('agent.cookies.imported', { count: info.cookies }), { description: info.domains.join(', ') });
      onOpenChange(false);
      await onImported();
    },
  });

  const take = (text: string, name = '') => {
    setExported(text);
    setFileName(name);
    if (text.trim()) read.mutate(text);
    else setPreview(null);
  };
  const toggle = (domain: string) =>
    setPicked((now) => {
      const next = new Set(now);
      if (next.has(domain)) next.delete(domain);
      else next.add(domain);
      return next;
    });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl" data-import-cookies>
        <DialogHeader>
          <DialogTitle>{t('agent.cookies.dialogTitle')}</DialogTitle>
          <DialogDescription>
            {t.rich('agent.cookies.dialogDescription', { code: (c) => <Code>{c}</Code> })}
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-4">
          <Field label={t('agent.cookies.export')} htmlFor="cookie-export">
            <div className="grid gap-2">
              <Textarea
                id="cookie-export"
                rows={4}
                className="font-mono text-[12px]"
                placeholder={'# Netscape HTTP Cookie File\n.github.com\tTRUE\t/\tTRUE\t1799999999\t_octo\t…'}
                spellCheck={false}
                autoComplete="off"
                value={fileName ? `(${fileName})` : exported}
                readOnly={Boolean(fileName)}
                onChange={(event) => take(event.target.value)}
              />
              <div className="flex items-center gap-2">
                <input
                  ref={fileInput}
                  type="file"
                  accept=".txt,.json,text/plain,application/json"
                  className="hidden"
                  onChange={async (event) => {
                    const file = event.target.files?.[0];
                    event.target.value = '';
                    if (file) take(await file.text(), file.name);
                  }}
                />
                <Button type="button" variant="secondary" size="sm" onClick={() => fileInput.current?.click()}>
                  <FileUp />
                  {t('agent.cookies.chooseFile')}
                </Button>
                {fileName && (
                  <Button type="button" variant="ghost" size="sm" onClick={() => take('')}>
                    {t('agent.cookies.clear')}
                  </Button>
                )}
                {read.isPending && <LoaderCircle className="size-4 animate-spin text-subtle" />}
              </div>
            </div>
          </Field>

          {read.error && <Notice>{errorMessage(read.error)}</Notice>}

          {preview && (
            <div className="grid gap-2">
              <p className="text-[12.5px] text-subtle">
                {t('agent.cookies.preview', { count: preview.cookies, format: preview.format, sites: preview.domains.length })}
              </p>
              <ul className="grid max-h-56 gap-1 overflow-y-auto rounded-xl border border-line-faint bg-surface-faint p-2" aria-label={t('agent.cookies.sites')}>
                {preview.domains.map((d) => (
                  <li key={d.domain}>
                    <label className="flex min-w-0 cursor-pointer items-center gap-2.5 rounded-lg px-2 py-1.5 text-[13px] hover:bg-surface-raised">
                      <input type="checkbox" checked={picked.has(d.domain)} onChange={() => toggle(d.domain)} />
                      <span className="min-w-0 truncate font-mono text-primary">{d.domain}</span>
                      <span className="ml-auto shrink-0 text-[11.5px] text-subtle">{d.cookies}</span>
                    </label>
                  </li>
                ))}
              </ul>
            </div>
          )}

          <Field label={t('agent.cookies.typeDomains')} htmlFor="cookie-domains" hint={t('agent.cookies.typeDomainsHint')}>
            <Input id="cookie-domains" placeholder="github.com, linear.app" value={typed} onChange={(event) => setTyped(event.target.value)} spellCheck={false} />
          </Field>

          {save.error && <Notice>{errorMessage(save.error)}</Notice>}
          <p className="text-[12px] leading-relaxed text-subtle">
            {t('agent.cookies.warning')}
          </p>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t('common.cancel')}
          </Button>
          <Button variant="primary" disabled={!preview || domains.length === 0 || save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? <LoaderCircle className="animate-spin" /> : <Cookie />}
            {t('agent.cookies.importSites', { count: domains.length })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
