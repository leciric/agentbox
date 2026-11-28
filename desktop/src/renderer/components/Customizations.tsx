// Customizations is Settings' view of the user's customization of the app's
// look (lib/customization.ts): what is set, said in a line each, how to change
// it — by asking a chat, since that is what changes it — and Reset to default,
// which takes it all away. It doesn't edit the customization itself: a chat
// does that better than a form of every colour role would, and a form beside
// it would be a second way to say the same thing.
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { RotateCcw } from 'lucide-react';
import { useState } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { isCustomized, useCustomizationQuery } from '../lib/customization';
import { useT, type MessageKey } from '../lib/i18n';
import { errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { toast } from 'sonner';
import { Button } from './ui/button';
import { SettingNote, SettingRow } from './ui/settings';

// The sidebar's items by what they are called where they appear.
const itemLabels: Record<string, MessageKey> = {
  'new-task': 'v2.sidebar.newTask',
  home: 'shell.nav.home',
  office: 'office.title',
  filters: 'settings.customization.filters',
  settings: 'common.settings',
  work: 'work.title',
  media: 'shell.nav.media',
  jobs: 'shell.nav.jobs',
  running: 'running.title',
  update: 'settings.customization.update',
};

export function Customizations() {
  const t = useT();
  const queryClient = useQueryClient();
  const status = useCustomizationQuery();
  const [confirming, setConfirming] = useState(false);
  const reset = useMutation({
    mutationFn: api.resetCustomization,
    onSuccess: (updated) => {
      queryClient.setQueryData(['customization'], updated);
      toast.success(t('settings.customization.resetDone'));
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const c = status.data?.customization;
  const lines = c ? describe(c, t) : [];
  const customized = isCustomized(c);

  return (
    <SettingRow
      label={t('settings.entry.customization.label')}
      description={t('settings.customization.description')}
      details={t('settings.customization.details')}
      control={
        <Button variant="outline" size="sm" disabled={(!customized && !status.data?.problem) || reset.isPending} onClick={() => setConfirming(true)} data-customization-reset>
          <RotateCcw className="size-3.5" />
          {t('settings.customization.reset')}
        </Button>
      }
      data-customization
    >
      {status.data?.problem && <SettingNote tone="warning">{t('settings.customization.problem', { problem: status.data.problem })}</SettingNote>}
      {c && !status.data?.problem && (
        <div className="grid gap-1.5" data-customization-summary>
          {lines.length === 0 ? (
            <SettingNote>{t('settings.customization.none')}</SettingNote>
          ) : (
            <ul className="flex flex-wrap gap-1.5">
              {lines.map(({ key, text, swatch }) => (
                <li key={key} className="inline-flex min-w-0 items-center gap-1.5 rounded-full bg-surface-raised px-2 py-0.5 text-caption text-secondary" data-customized={key}>
                  {swatch && <span className="size-2.5 shrink-0 rounded-full border border-line-vivid" style={{ background: swatch }} aria-hidden />}
                  <span className="min-w-0 truncate">{text}</span>
                </li>
              ))}
            </ul>
          )}
          {status.data && <SettingNote className="break-all">{t('settings.customization.file', { path: status.data.path })}</SettingNote>}
        </div>
      )}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('settings.customization.resetTitle')}
        description={t('settings.customization.resetDescription')}
        confirmLabel={t('settings.customization.reset')}
        destructive
        onConfirm={() => reset.mutateAsync()}
      />
    </SettingRow>
  );
}

type Line = { key: string; text: string; swatch?: string };

// describe says what a customization changes, a line each.
function describe(c: T.Customization, t: ReturnType<typeof useT>): Line[] {
  const out: Line[] = [];
  if (c.theme.accent) out.push({ key: 'accent', text: t('settings.customization.accent'), swatch: c.theme.accent });
  for (const mode of ['dark', 'light'] as const) {
    const set = Object.values(c.theme[mode]).filter(Boolean) as string[];
    if (set.length) out.push({ key: mode, text: t('settings.customization.colors', { mode, count: set.length }), swatch: c.theme[mode].background ?? set[0] });
  }
  if (c.theme.radius) out.push({ key: 'radius', text: t('settings.customization.radius', { radius: c.theme.radius }) });
  if (c.fonts.ui) out.push({ key: 'font', text: t('settings.customization.font', { font: c.fonts.ui }) });
  if (c.fonts.mono) out.push({ key: 'mono', text: t('settings.customization.monoFont', { font: c.fonts.mono }) });
  if (c.fonts.size) out.push({ key: 'size', text: t('settings.customization.size', { size: c.fonts.size }) });
  if (c.density) out.push({ key: 'density', text: t('settings.customization.density', { density: c.density }) });
  if (c.sidebar.top?.length || c.sidebar.foot?.length) out.push({ key: 'order', text: t('settings.customization.order') });
  if (c.sidebar.hidden?.length) {
    out.push({ key: 'hidden', text: t('settings.customization.hidden', { items: c.sidebar.hidden.map((id) => (itemLabels[id] ? t(itemLabels[id]) : id)).join(', ') }) });
  }
  if (c.css?.trim()) out.push({ key: 'css', text: t('settings.customization.css', { count: c.css.trim().split('\n').length }) });
  return out;
}
