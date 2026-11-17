import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { errorMessage } from '../lib/utils';
import { Select, SelectOption } from './ui/select';
import { SettingRow } from './ui/settings';
import { Switch } from './ui/switch';

const freezeTimes = [2 * 60, 5 * 60, 10 * 60, 15 * 60, 30 * 60];
const stopTimes = [5 * 60, 10 * 60, 30 * 60, 60 * 60, 2 * 60 * 60];
const defaultFreeze = 5 * 60;
const defaultStop = 10 * 60;

function minutesWords(seconds: number): string {
  const hours = seconds / 3600;
  if (hours >= 1 && hours === Math.trunc(hours)) return `${hours}h`;
  return `${Math.round(seconds / 60)}m`;
}

// IdleReclaim is "reclaim idle agents' memory", an experiment and off by
// default (internal/daemon/idlereclaim.go): an agent whose chat has been idle
// a few minutes has its dev servers, desktop, watchers and Docker frozen and
// pushed out to swap, woken as soon as anything touches it, and a finished one
// has its machine stopped, started again by the next message. Off, AgentBox
// behaves as it did before the setting existed.
export function IdleReclaim() {
  const t = useT();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings });
  const queryClient = useQueryClient();
  const save = useMutation({
    mutationFn: (req: T.UpdateSettingsRequest) => api.updateSettings(req),
    onSuccess: (next) => queryClient.setQueryData(['settings'], next),
    onError: (err) => toast.error(errorMessage(err)),
  });
  const on = settings.data?.idleReclaim ?? false;
  const disabled = save.isPending || settings.data === undefined;
  const wait = (label: string, value: number, choices: number[], fallback: number, set: (n: number) => T.UpdateSettingsRequest) => (
    <label className="flex min-w-0 flex-col gap-1 text-xs text-subtle">
      {label}
      <div className="max-w-40">
        <Select aria-label={label} disabled={disabled} value={String(value)} onChange={(next) => save.mutate(set(Number(next)))}>
          {choices.map((n) => (
            <SelectOption key={n} value={String(n)}>
              {n === fallback ? t('defaults.newAgent.withDefault', { label: minutesWords(n) }) : minutesWords(n)}
            </SelectOption>
          ))}
          {!choices.includes(value) && <SelectOption value={String(value)}>{minutesWords(value)}</SelectOption>}
        </Select>
      </div>
    </label>
  );
  return (
    <SettingRow
      label={t('settings.idleReclaim.label')}
      description={t('settings.idleReclaim.description')}
      details={t('settings.idleReclaim.details')}
      control={
        <Switch
          data-idle-reclaim
          aria-label={t('settings.idleReclaim.label')}
          disabled={disabled}
          checked={on}
          onCheckedChange={(idleReclaim) => save.mutate({ idleReclaim })}
        />
      }
    >
      {on && (
        <div className="flex flex-wrap gap-4">
          {wait(t('settings.idleReclaim.freezeAfter'), settings.data?.idleFreezeSeconds ?? defaultFreeze, freezeTimes, defaultFreeze, (idleFreezeSeconds) => ({ idleFreezeSeconds }))}
          {wait(t('settings.idleReclaim.stopAfter'), settings.data?.idleStopSeconds ?? defaultStop, stopTimes, defaultStop, (idleStopSeconds) => ({ idleStopSeconds }))}
        </div>
      )}
    </SettingRow>
  );
}
