import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Download, LoaderCircle, Play, Square, Trash2 } from 'lucide-react';
import { useEffect } from 'react';
import { toast } from 'sonner';
import { t, useT, type MessageKey } from '../lib/i18n';
import type { SettingGroup } from '../lib/settingsSearch';
import { errorMessage } from '../lib/utils';
import { cachedSizes, clearCached } from '../lib/voice/cache';
import { autoModel, formatMB, pickModel, voiceModels } from '../lib/voice/models';
import { speak, stop, unlock, useReader } from '../lib/voice/reader';
import { defaultVoiceSettings, setReadAloud, setVoiceSettings, speeds, useReadAloudSettings, useVoiceSettings, voices, type VoiceSettings } from '../lib/voice/settings';
import type { VoiceLanguage } from '../lib/voice/speakable';
import { deviceReason, forgetWhisper, preloadWhisper, probeWhisper, useWhisper, whisperState } from '../lib/voice/whisper';
import { Button } from './ui/button';
import { Select, SelectOption } from './ui/select';
import { SettingRow } from './ui/settings';
import { Switch } from './ui/switch';

// Push-to-talk's settings, under Settings → Voice. They're this app's, kept in
// localStorage (lib/voice/settings), not the daemon's.
export function pushToTalkGroup(s: VoiceSettings): SettingGroup {
  return {
    id: 'push-to-talk',
    title: t('defaults.voice.pttTitle'),
    description: t('defaults.voice.pttDescription'),
    entries: [
      {
        id: 'voice-model',
        label: t('defaults.voice.modelLabel'),
        keywords: t('defaults.voice.modelKeywords'),
        modified: s.model !== defaultVoiceSettings.model,
        render: () => <ModelRow />,
      },
      {
        id: 'voice-after',
        label: t('defaults.voice.afterLabel'),
        keywords: t('defaults.voice.afterKeywords'),
        modified: s.after !== defaultVoiceSettings.after,
        render: () => <AfterRow />,
      },
      {
        id: 'voice-language',
        label: t('defaults.voice.languageLabel'),
        keywords: t('defaults.voice.languageKeywords'),
        modified: s.language !== defaultVoiceSettings.language,
        render: () => <LanguageRow />,
      },
      {
        id: 'voice-gpu',
        label: t('defaults.voice.gpuLabel'),
        keywords: t('defaults.voice.gpuKeywords'),
        render: () => <VulkanRow />,
      },
      {
        id: 'voice-downloads',
        label: t('defaults.voice.downloadsLabel'),
        keywords: t('defaults.voice.downloadsKeywords'),
        render: () => <DownloadsRow />,
      },
    ],
  };
}

function ModelRow() {
  const t = useT();
  const settings = useVoiceSettings();
  const whisper = useWhisper();
  useEffect(() => probeWhisper(), []);
  const device = whisper.device;
  const auto = device ? pickModel(autoModel, device).label : undefined;
  return (
    <SettingRow
      label={t('defaults.voice.modelLabel')}
      description={
        device === 'webgpu' ? (
          <>{whisper.adapter ? t('defaults.voice.gpuAdapter', { adapter: whisper.adapter }) : t('defaults.voice.gpu')}</>
        ) : device === 'wasm' ? (
          <span className="text-amber-300">{t('defaults.voice.cpu', { reason: deviceReason(whisper.reason) })}</span>
        ) : (
          t('defaults.voice.lookingForGpu')
        )
      }
      details={t(pickModel(settings.model, device ?? 'webgpu').note)}
      control={
        <Select aria-label={t('defaults.voice.modelLabel')} value={settings.model} onChange={(model) => setVoiceSettings({ model })}>
          <SelectOption value={autoModel}>{auto ? t('defaults.voice.automaticModel', { model: auto }) : t('defaults.voice.automatic')}</SelectOption>
          {voiceModels.map((m) => (
            <SelectOption key={m.id} value={m.id}>
              {m.label}
            </SelectOption>
          ))}
        </Select>
      }
    />
  );
}

// VulkanRow is the one push-to-talk setting the main process keeps rather
// than localStorage, since it decides Chromium's switches before any page
// loads (main/voicegpu.ts): it takes effect when AgentBox next starts.
function VulkanRow() {
  const t = useT();
  const queryClient = useQueryClient();
  const gpu = useQuery({ queryKey: ['voice-gpu'], queryFn: () => window.agentbox.voiceGPU() });
  const linux = gpu.data?.platform === 'linux';
  const pending = gpu.data && gpu.data.saved.vulkan !== gpu.data.running.vulkan;
  const set = async (vulkan: boolean) => {
    try {
      await window.agentbox.setVoiceGPU({ vulkan });
      await queryClient.invalidateQueries({ queryKey: ['voice-gpu'] });
    } catch (err) {
      toast.error(errorMessage(err));
    }
  };
  return (
    <SettingRow
      label={t('defaults.voice.gpuLabel')}
      description={
        !gpu.data ? null : gpu.data.platform === 'web' ? (
          t('defaults.voice.vulkanWeb')
        ) : !linux ? (
          t('defaults.voice.vulkanNotLinux')
        ) : pending ? (
          <span className="text-amber-300">{t('defaults.voice.vulkanPending')}</span>
        ) : (
          t('defaults.voice.vulkanDescription')
        )
      }
      details={t('defaults.voice.vulkanDetails')}
      control={<Switch aria-label={t('defaults.voice.gpuLabel')} disabled={!linux} checked={gpu.data?.saved.vulkan ?? false} onCheckedChange={(checked) => void set(checked)} />}
    />
  );
}

function AfterRow() {
  const t = useT();
  const settings = useVoiceSettings();
  return (
    <SettingRow
      label={t('defaults.voice.afterLabel')}
      description={t('defaults.voice.afterDescription')}
      control={
        <Select aria-label={t('defaults.voice.afterLabel')} value={settings.after} onChange={(after) => setVoiceSettings({ after: after as 'send' | 'edit' })}>
          <SelectOption value="edit">{t('defaults.voice.afterEdit')}</SelectOption>
          <SelectOption value="send">{t('defaults.voice.afterSend')}</SelectOption>
        </Select>
      }
    />
  );
}

function LanguageRow() {
  const t = useT();
  const settings = useVoiceSettings();
  return (
    <SettingRow
      label={t('defaults.voice.languageLabel')}
      description={t('defaults.voice.languageDescription')}
      control={
        <Select aria-label={t('defaults.voice.languageLabel')} value={settings.language} onChange={(language) => setVoiceSettings({ language: language as 'auto' | 'pt' | 'en' })}>
          <SelectOption value="auto">{t('defaults.voice.automatic')}</SelectOption>
          <SelectOption value="pt">Português</SelectOption>
          <SelectOption value="en">English</SelectOption>
        </Select>
      }
    />
  );
}

function DownloadsRow() {
  const t = useT();
  const whisper = useWhisper();
  const queryClient = useQueryClient();
  // Refreshed whenever a model finishes loading, which is when it's cached.
  const sizes = useQuery({ queryKey: ['voice-cache', whisper.ready, !!whisper.loading], queryFn: cachedSizes });
  const device = whisper.device ?? 'webgpu';
  const clear = async (id: string) => {
    try {
      if (whisperState().ready === id || whisperState().loading?.model === id) forgetWhisper();
      await clearCached(id);
      await queryClient.invalidateQueries({ queryKey: ['voice-cache'] });
    } catch (err) {
      toast.error(errorMessage(err));
    }
  };
  return (
    <SettingRow label={t('defaults.voice.downloadsLabel')} description={t('defaults.voice.downloadsDescription')}>
      <div className="grid gap-1.5">
        {voiceModels.map((m) => {
          const have = sizes.data?.[m.id] ?? 0;
          const loading = whisper.loading?.model === m.id ? whisper.loading : undefined;
          const percent = loading?.total ? Math.round((loading.loaded / loading.total) * 100) : undefined;
          return (
            <div key={m.id} className="flex items-center gap-3 text-[13px]" data-voice-model={m.id}>
              <span className="min-w-0 flex-1 truncate text-primary">{m.label}</span>
              <span className="shrink-0 tabular-nums text-subtle">
                {loading
                  ? percent !== undefined
                    ? t('defaults.voice.downloadingPercent', { percent })
                    : t('defaults.voice.downloading')
                  : have > 0
                    ? t('defaults.voice.downloaded', { size: formatMB(have / 1e6) })
                    : t('defaults.voice.toDownload', { size: formatMB(m.size[device]) })}
              </span>
              {loading ? (
                <LoaderCircle className="size-4 shrink-0 animate-spin text-subtle" />
              ) : have > 0 ? (
                <Button variant="ghost" size="icon-sm" aria-label={t('defaults.voice.clearModel', { model: m.label })} onClick={() => void clear(m.id)}>
                  <Trash2 />
                </Button>
              ) : (
                <Button variant="ghost" size="icon-sm" aria-label={t('defaults.voice.downloadModel', { model: m.label })} onClick={() => preloadWhisper(m.id)}>
                  <Download />
                </Button>
              )}
            </div>
          );
        })}
      </div>
    </SettingRow>
  );
}

// useReadAloudGroup is Settings → Voice's reading aloud: the chat's speaker,
// and the voices it reads in. Kept apart from SettingsView so the Voice section
// only lists it.
export function useReadAloudGroup(): SettingGroup {
  const t = useT();
  const s = useReadAloudSettings();
  return {
    id: 'read-aloud',
    title: t('defaults.voice.readAloudTitle'),
    description: t('defaults.voice.readAloudDescription'),
    entries: [
      { id: 'read-aloud-on', label: t('defaults.voice.readAloudOn'), keywords: t('defaults.voice.readAloudKeywords'), modified: s.on, render: () => <ReadAloudOn /> },
      { id: 'read-aloud-voice-en', label: t('defaults.voice.voiceEn'), keywords: t('defaults.voice.voiceEnKeywords'), modified: s.voices.en !== 'af_heart', render: () => <ReadAloudVoice language="en" /> },
      { id: 'read-aloud-voice-pt', label: t('defaults.voice.voicePt'), keywords: t('defaults.voice.voicePtKeywords'), modified: s.voices.pt !== 'pf_dora', render: () => <ReadAloudVoice language="pt" /> },
      { id: 'read-aloud-speed', label: t('defaults.voice.speed'), keywords: t('defaults.voice.speedKeywords'), modified: s.speed !== 1, render: () => <ReadAloudSpeed /> },
    ],
  };
}

function ReadAloudOn() {
  const t = useT();
  const { on } = useReadAloudSettings();
  return (
    <SettingRow
      label={t('defaults.voice.readAloudOn')}
      description={t('defaults.voice.readAloudOnDescription')}
      details={
        <>
          <p>{t('defaults.voice.readAloudDetails1')}</p>
          <p>{t('defaults.voice.readAloudDetails2')}</p>
        </>
      }
      control={<Switch aria-label={t('defaults.voice.readAloudOn')} checked={on} onCheckedChange={(checked) => {
            if (checked) unlock();
            setReadAloud({ on: checked });
          }} />}
    />
  );
}

// The samples stay in the language the voice speaks, whatever the app's.
const voiceRows: Record<VoiceLanguage, { label: MessageKey; description: MessageKey; sample: string }> = {
  en: { label: 'defaults.voice.voiceEn', description: 'defaults.voice.voiceEnDescription', sample: 'Hello! I read the agent’s replies aloud.' },
  pt: {
    label: 'defaults.voice.voicePt',
    description: 'defaults.voice.voicePtDescription',
    sample: 'Olá! Eu leio as respostas do agente em voz alta.',
  },
};

function ReadAloudVoice({ language }: { language: VoiceLanguage }) {
  const t = useT();
  const { voices: chosen } = useReadAloudSettings();
  const reader = useReader();
  const row = voiceRows[language];
  return (
    <SettingRow
      label={t(row.label)}
      description={t(row.description)}
      control={
        <div className="flex w-full items-center gap-1.5">
          <Select aria-label={t(row.label)} value={chosen[language]} onChange={(v) => setReadAloud({ voices: { ...chosen, [language]: v } })} className="min-w-0 flex-1">
            {voices
              .filter((v) => v.language === language)
              .map((v) => (
                <SelectOption key={v.id} value={v.id}>
                  {t(v.label)}
                </SelectOption>
              ))}
          </Select>
          <Button
            variant="ghost"
            className="h-9 shrink-0 px-2.5"
            aria-label={reader.status === 'idle' ? t('defaults.voice.tryVoice', { voice: t(row.label).toLowerCase() }) : t('common.stop')}
            title={reader.error ? t('defaults.voice.voiceFailed', { error: reader.error }) : undefined}
            data-voice-try={reader.status}
            onClick={() => {
              stop();
              if (reader.status === 'idle') speak(row.sample, language);
            }}
          >
            {reader.status === 'loading' ? <LoaderCircle className="animate-spin" /> : reader.status === 'speaking' ? <Square /> : <Play />}
            {reader.status === 'loading' && reader.progress !== undefined && <span className="text-[11.5px] tabular-nums">{Math.round(reader.progress * 100)}%</span>}
          </Button>
        </div>
      }
    />
  );
}

function ReadAloudSpeed() {
  const t = useT();
  const { speed } = useReadAloudSettings();
  return (
    <SettingRow
      label={t('defaults.voice.speed')}
      description={t('defaults.voice.speedDescription')}
      control={
        <Select aria-label={t('defaults.voice.speed')} value={String(speed)} onChange={(v) => setReadAloud({ speed: Number(v) })}>
          {speeds.map((v) => (
            <SelectOption key={v} value={String(v)}>
              {v === 1 ? t('defaults.voice.speedNormal') : `${v}×`}
            </SelectOption>
          ))}
        </Select>
      }
    />
  );
}
