import { useT, type MessageKey } from '../lib/i18n';
import { kinds, modeOf, modes, setKind, setMode, usePrefs, type Channel, type Mode } from '../lib/notifyPrefs';
import { playReadySound, setReviewVolume, useReviewVolume } from '../lib/reviewAlert';
import { cn } from '../lib/utils';
import { DoNotDisturbControl } from './DoNotDisturb';
import { Button } from './ui/button';
import { SettingRow } from './ui/settings';
import { Switch } from './ui/switch';

const channels: readonly Channel[] = ['system', 'sound'];

// NotificationKinds is one row for each kind of notice with a switch for the
// system notification and one for the sound (lib/notifyPrefs.ts). Do not
// disturb overrides them all, which the row says while it's on.
export function NotificationKinds() {
  const t = useT();
  const prefs = usePrefs();
  return (
    <div className="grid gap-3 px-5 py-4" data-notify-kinds>
      <div className="grid grid-cols-[minmax(0,1fr)_5rem_5rem] items-end gap-3 text-[11px] uppercase tracking-wider text-faint">
        <span />
        {channels.map((c) => (
          <span key={c} className="text-center">
            {t(`notify.channel.${c}`)}
          </span>
        ))}
      </div>
      {kinds.map((k) => (
        <div key={k} className="grid grid-cols-[minmax(0,1fr)_5rem_5rem] items-center gap-3" data-notify-kind={k}>
          <div className="min-w-0">
            <div className="text-[13px] font-medium text-primary">{t(`notify.kind.${k}.label` as MessageKey)}</div>
            <div className="text-[12px] leading-relaxed text-subtle">{t(`notify.kind.${k}.description` as MessageKey)}</div>
          </div>
          {channels.map((c) => (
            <span key={c} className="flex justify-center">
              <Switch
                data-notify-switch={`${k}.${c}`}
                aria-label={`${t(`notify.kind.${k}.label` as MessageKey)}: ${t(`notify.channel.${c}`)}`}
                checked={prefs.kinds[k][c]}
                onCheckedChange={(on) => setKind(k, c, on)}
              />
            </span>
          ))}
        </div>
      ))}
    </div>
  );
}

// ChimeVolume is the loudness of the chime and a way to hear it.
export function ChimeVolume() {
  const t = useT();
  const volume = useReviewVolume();
  const prefs = usePrefs();
  const anySound = kinds.some((k) => prefs.kinds[k].sound);
  return (
    <SettingRow label={t('settings.reviewSound.volume')} description={t('notify.volume.description')}>
      <div className="flex items-center gap-3">
        <input
          type="range"
          min={0}
          max={100}
          value={volume}
          disabled={!anySound}
          aria-label={t('settings.reviewSound.volume')}
          data-review-sound-volume
          className="w-40 accent-brand-500"
          onChange={(e) => setReviewVolume(Number(e.target.value))}
        />
        <Button size="sm" variant="secondary" disabled={!anySound} data-review-sound-test onClick={() => playReadySound()}>
          {t('settings.reviewSound.test')}
        </Button>
      </div>
    </SettingRow>
  );
}

export function DoNotDisturbSetting() {
  const t = useT();
  return (
    <SettingRow label={t('notify.dnd.title')} description={t('notify.dnd.description')}>
      <DoNotDisturbControl titled={false} />
    </SettingRow>
  );
}

// NotificationModeChoice is the first run's question: notifications on, sound
// only, or silent. It sets every kind at once; Settings → Notifications has
// them one by one.
export function NotificationModeChoice() {
  const t = useT();
  const mode = modeOf(usePrefs());
  return (
    <div className="grid gap-2" role="radiogroup" aria-label={t('notify.setup.title')} data-notify-mode-choice>
      {modes.map((m: Mode) => (
        <button
          key={m}
          role="radio"
          aria-checked={mode === m}
          data-notify-mode={m}
          onClick={() => setMode(m)}
          className={cn(
            'grid gap-0.5 rounded-xl border px-4 py-3 text-left transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/50',
            mode === m ? 'border-brand-500/60 bg-brand-500/10' : 'border-line bg-surface-faint hover:bg-surface-raised',
          )}
        >
          <span className="text-[13px] font-medium text-title">{t(`notify.mode.${m}`)}</span>
          <span className="text-[12px] text-muted">{t(`notify.mode.${m}.hint`)}</span>
        </button>
      ))}
    </div>
  );
}
