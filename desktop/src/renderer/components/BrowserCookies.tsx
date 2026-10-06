import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Cookie, FileUp, Globe, LoaderCircle, Trash2, Upload } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { errorMessage, timeAgo } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { Button } from './ui/button';
import { Card, Code, Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input, Textarea } from './ui/input';
import { Select, SelectOption } from './ui/select';

// BrowserCookies, in a project's Secrets: cookies the user signs their agents'
// browser in with, so an agent made afterwards starts their Chromium already
// signed in to the sites they use. They come either straight from one of the
// user's own installed browsers, read and decrypted on import
// (internal/cookieimport), or, as a fallback, from an export they made (a
// cookies.txt or a cookie extension's JSON). Either way they're kept sealed
// like a secret, and no page reads a cookie back: what shows is the sites and
// how many.

// SiteList shows how many cookies an import holds per site: the "count by
// site" the user sees after importing, and on the card afterwards.
function SiteList({ sites, label }: { sites: T.CookieDomain[]; label: string }) {
  return (
    <ul className="grid max-h-56 gap-1 overflow-y-auto rounded-xl border border-line-faint bg-surface-faint p-2" aria-label={label}>
      {sites.map((s) => (
        <li key={s.domain} className="flex min-w-0 items-center gap-2.5 px-2 py-1 text-[13px]">
          <span className="min-w-0 truncate font-mono text-primary">{s.domain}</span>
          <span className="ml-auto shrink-0 text-[11.5px] text-subtle">{s.cookies}</span>
        </li>
      ))}
    </ul>
  );
}

export function BrowserCookiesCard({ project }: { project: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  const info = useQuery({ queryKey: ['browserCookies', project], queryFn: () => api.browserCookies(project) });
  const [importing, setImporting] = useState(false);
  const [removing, setRemoving] = useState(false);
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['browserCookies', project] });
  const imported = info.data?.imported ? info.data : undefined;

  return (
    <Card title={t('agent.cookies.title')} icon={Cookie} description={t('agent.cookies.description')}>
      {info.error && <Notice>{errorMessage(info.error)}</Notice>}
      {imported ? (
        <div className="grid gap-3" data-browser-cookies="imported">
          <p className="text-[12.5px] leading-relaxed text-subtle">
            {imported.importedAt
              ? t('agent.cookies.summary', { count: imported.cookies, when: timeAgo(imported.importedAt), source: imported.source || imported.format })
              : t('agent.cookies.summaryNoTime', { count: imported.cookies, source: imported.source || imported.format })}
          </p>
          {imported.sites && imported.sites.length > 0 && <SiteList sites={imported.sites} label={t('agent.cookies.sites')} />}
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
          <p className="text-[12.5px] leading-relaxed text-subtle">{t('agent.cookies.none')}</p>
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

// ImportCookiesDialog imports cookies either straight from an installed
// browser profile (the default), or from an export file (the fallback below).
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
  const profiles = useQuery({
    queryKey: ['browserProfiles', project],
    queryFn: () => api.browserProfiles(project),
    enabled: open,
  });
  const [picked, setPicked] = useState('');

  useEffect(() => {
    if (!open) setPicked('');
  }, [open]);

  const list = profiles.data?.profiles ?? [];
  const chosen = list.find((p) => p.id === picked);
  const importBrowser = useMutation({
    mutationFn: () => {
      if (!chosen) throw new Error(t('agent.cookies.pickBrowser'));
      return api.importFromBrowser(project, chosen);
    },
    onSuccess: async (result) => {
      toast(t('agent.cookies.imported', { count: result.cookies }), { description: result.source });
      onOpenChange(false);
      await onImported();
    },
  });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl" data-import-cookies>
        <DialogHeader>
          <DialogTitle>{t('agent.cookies.dialogTitle')}</DialogTitle>
          <DialogDescription>{t('agent.cookies.dialogDescription')}</DialogDescription>
        </DialogHeader>

        <div className="grid gap-4">
          <Field label={t('agent.cookies.fromBrowser')} htmlFor="cookie-browser" hint={t('agent.cookies.fromBrowserHint')}>
            {profiles.isPending ? (
              <div className="flex items-center gap-2 text-[13px] text-subtle">
                <LoaderCircle className="size-4 animate-spin" />
                {t('agent.cookies.findingBrowsers')}
              </div>
            ) : list.length === 0 ? (
              <p className="text-[12.5px] leading-relaxed text-subtle">{t('agent.cookies.noBrowsers')}</p>
            ) : (
              <div className="flex items-center gap-2">
                <Select
                  id="cookie-browser"
                  value={picked}
                  onChange={setPicked}
                  placeholder={t('agent.cookies.pickBrowser')}
                  className="flex-1"
                  data-browser-select
                >
                  {list.map((p) => (
                    <SelectOption key={p.id} value={p.id}>
                      {p.name && p.name !== p.browserName ? `${p.browserName} — ${p.name}` : p.browserName}
                    </SelectOption>
                  ))}
                </Select>
                <Button
                  variant="primary"
                  disabled={!chosen || importBrowser.isPending}
                  onClick={() => importBrowser.mutate()}
                  data-import-browser
                >
                  {importBrowser.isPending ? <LoaderCircle className="animate-spin" /> : <Globe />}
                  {t('agent.cookies.import')}
                </Button>
              </div>
            )}
            {profiles.error && <Notice>{errorMessage(profiles.error)}</Notice>}
            {importBrowser.error && <Notice>{errorMessage(importBrowser.error)}</Notice>}
          </Field>

          <FileImport project={project} onImported={onImported} onClose={() => onOpenChange(false)} />
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t('common.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// FileImport is the fallback: an export the user made, pasted or picked as a
// file, with the sites it holds ticked (or typed) before importing. It stays
// for browsers AgentBox can't read (Chrome on Windows), and for exports of a
// browser not installed here.
function FileImport({ project, onImported, onClose }: { project: string; onImported: () => Promise<unknown>; onClose: () => void }) {
  const t = useT();
  const [exported, setExported] = useState('');
  const [fileName, setFileName] = useState('');
  const [open, setOpen] = useState(false);
  const [preview, setPreview] = useState<T.BrowserCookiesPreview | null>(null);
  const [pickedSites, setPickedSites] = useState<Set<string>>(new Set());
  const [typed, setTyped] = useState('');
  const fileInput = useRef<HTMLInputElement>(null);

  const read = useMutation({
    mutationFn: (text: string) => api.previewBrowserCookies(project, text),
    onSuccess: (p) => {
      setPreview(p);
      setPickedSites(new Set());
    },
    onError: () => setPreview(null),
  });
  const domains = [...pickedSites, ...typed.split(/[\s,]+/).filter(Boolean)];
  const save = useMutation({
    mutationFn: () => api.importBrowserCookies(project, exported, domains),
    onSuccess: async (info) => {
      toast(t('agent.cookies.imported', { count: info.cookies }), { description: info.domains.join(', ') });
      onClose();
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
    setPickedSites((now) => {
      const next = new Set(now);
      if (next.has(domain)) next.delete(domain);
      else next.add(domain);
      return next;
    });

  if (!open) {
    return (
      <button type="button" className="justify-self-start text-[12.5px] text-subtle underline-offset-2 hover:underline" onClick={() => setOpen(true)} data-file-import-toggle>
        {t('agent.cookies.orFromFile')}
      </button>
    );
  }

  return (
    <div className="grid gap-4 border-t border-line-faint pt-4" data-file-import>
      <Field label={t('agent.cookies.export')} htmlFor="cookie-export" hint={t.rich('agent.cookies.exportHint', { code: (c) => <Code>{c}</Code> })}>
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
                  <input type="checkbox" checked={pickedSites.has(d.domain)} onChange={() => toggle(d.domain)} />
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
      <p className="text-[12px] leading-relaxed text-subtle">{t('agent.cookies.warning')}</p>

      <Button
        variant="primary"
        className="justify-self-start"
        disabled={!preview || domains.length === 0 || save.isPending}
        onClick={() => save.mutate()}
      >
        {save.isPending ? <LoaderCircle className="animate-spin" /> : <Cookie />}
        {t('agent.cookies.importSites', { count: domains.length })}
      </Button>
    </div>
  );
}
