import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Download, LoaderCircle, Trash2 } from 'lucide-react';
import { useEffect } from 'react';
import { toast } from 'sonner';
import type { SettingGroup } from '../lib/settingsSearch';
import { errorMessage } from '../lib/utils';
import { cachedSizes, clearCached } from '../lib/voice/cache';
import { autoModel, formatMB, pickModel, voiceModels } from '../lib/voice/models';
import { defaultVoiceSettings, setVoiceSettings, useVoiceSettings, type VoiceSettings } from '../lib/voice/settings';
import { forgetWhisper, preloadWhisper, probeWhisper, useWhisper, whisperState } from '../lib/voice/whisper';
import { Button } from './ui/button';
import { Select, SelectOption } from './ui/select';
import { SettingRow } from './ui/settings';

// Push-to-talk's settings, under Settings → Voice. They're this app's, kept in
// localStorage (lib/voice/settings), not the daemon's.
export function pushToTalkGroup(s: VoiceSettings): SettingGroup {
  return {
    id: 'push-to-talk',
    title: 'Push-to-talk',
    description: 'Hold the microphone in a chat, or Ctrl+Space, and talk. Whisper transcribes it on this machine; nothing you say leaves it.',
    entries: [
      {
        id: 'voice-model',
        label: 'Speech-to-text model',
        keywords: 'whisper voice speech microphone mic dictation transcribe gpu webgpu cpu turbo small base',
        modified: s.model !== defaultVoiceSettings.model,
        render: () => <ModelRow />,
      },
      {
        id: 'voice-after',
        label: 'After you talk',
        keywords: 'voice send edit dictation composer push to talk',
        modified: s.after !== defaultVoiceSettings.after,
        render: () => <AfterRow />,
      },
      {
        id: 'voice-language',
        label: 'Language you speak',
        keywords: 'voice language portuguese português english auto detect',
        modified: s.language !== defaultVoiceSettings.language,
        render: () => <LanguageRow />,
      },
      {
        id: 'voice-downloads',
        label: 'Downloaded models',
        keywords: 'voice whisper download cache clear delete disk size',
        render: () => <DownloadsRow />,
      },
    ],
  };
}

function ModelRow() {
  const settings = useVoiceSettings();
  const whisper = useWhisper();
  useEffect(() => probeWhisper(), []);
  const device = whisper.device;
  const auto = device ? pickModel(autoModel, device).label : undefined;
  return (
    <SettingRow
      label="Speech-to-text model"
      description={
        device === 'webgpu' ? (
          <>Runs on the GPU with WebGPU{whisper.adapter ? ` (${whisper.adapter})` : ''}.</>
        ) : device === 'wasm' ? (
          <span className="text-amber-300">No WebGPU here, so Whisper runs on the CPU: slower, and Automatic picks a smaller model. {whisper.reason}</span>
        ) : (
          'Looking for a GPU…'
        )
      }
      details={pickModel(settings.model, device ?? 'webgpu').note}
      control={
        <Select aria-label="Speech-to-text model" value={settings.model} onChange={(model) => setVoiceSettings({ model })}>
          <SelectOption value={autoModel}>Automatic{auto ? ` (${auto})` : ''}</SelectOption>
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

function AfterRow() {
  const settings = useVoiceSettings();
  return (
    <SettingRow
      label="After you talk"
      description="Whether what you said waits in the message box for you to check, or is sent as soon as it's transcribed."
      control={
        <Select aria-label="After you talk" value={settings.after} onChange={(after) => setVoiceSettings({ after: after as 'send' | 'edit' })}>
          <SelectOption value="edit">Let me edit it first</SelectOption>
          <SelectOption value="send">Send it at once</SelectOption>
        </Select>
      }
    />
  );
}

function LanguageRow() {
  const settings = useVoiceSettings();
  return (
    <SettingRow
      label="Language you speak"
      description="Automatic tells Portuguese from English each time you talk."
      control={
        <Select aria-label="Language you speak" value={settings.language} onChange={(language) => setVoiceSettings({ language: language as 'auto' | 'pt' | 'en' })}>
          <SelectOption value="auto">Automatic</SelectOption>
          <SelectOption value="pt">Português</SelectOption>
          <SelectOption value="en">English</SelectOption>
        </Select>
      }
    />
  );
}

function DownloadsRow() {
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
    <SettingRow label="Downloaded models" description="Each model downloads from Hugging Face the first time it's used, and stays here for the next.">
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
                  ? `Downloading${percent !== undefined ? ` · ${percent}%` : '…'}`
                  : have > 0
                    ? `${formatMB(have / 1e6)} downloaded`
                    : `${formatMB(m.size[device])} to download`}
              </span>
              {loading ? (
                <LoaderCircle className="size-4 shrink-0 animate-spin text-subtle" />
              ) : have > 0 ? (
                <Button variant="ghost" size="icon-sm" aria-label={`Clear ${m.label}`} onClick={() => void clear(m.id)}>
                  <Trash2 />
                </Button>
              ) : (
                <Button variant="ghost" size="icon-sm" aria-label={`Download ${m.label}`} onClick={() => preloadWhisper(m.id)}>
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
