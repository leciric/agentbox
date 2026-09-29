import { LoaderCircle, Play, Square } from 'lucide-react';
import type { SettingGroup } from '../lib/settingsSearch';
import { speak, stop, unlock, useReader } from '../lib/voice/reader';
import { setReadAloud, speeds, useReadAloudSettings, voices } from '../lib/voice/settings';
import type { VoiceLanguage } from '../lib/voice/speakable';
import { Button } from './ui/button';
import { Select, SelectOption } from './ui/select';
import { SettingRow } from './ui/settings';
import { Switch } from './ui/switch';

// useReadAloudGroup is Settings → Voice's reading aloud: the chat's speaker,
// and the voices it reads in. Kept apart from SettingsView so the Voice section
// only lists it.
export function useReadAloudGroup(): SettingGroup {
  const s = useReadAloudSettings();
  return {
    id: 'read-aloud',
    title: 'Read aloud',
    description: 'The speaker in every chat reads the agent’s replies aloud as they come in, on this computer, with Kokoro.',
    entries: [
      { id: 'read-aloud-on', label: 'Read replies aloud', keywords: 'speaker speech tts kokoro audio voice sound', modified: s.on, render: () => <ReadAloudOn /> },
      { id: 'read-aloud-voice-en', label: 'English voice', keywords: 'kokoro voice language english', modified: s.voices.en !== 'af_heart', render: () => <ReadAloudVoice language="en" /> },
      { id: 'read-aloud-voice-pt', label: 'Portuguese voice', keywords: 'kokoro voice language portuguese português brasil', modified: s.voices.pt !== 'pf_dora', render: () => <ReadAloudVoice language="pt" /> },
      { id: 'read-aloud-speed', label: 'Speed', keywords: 'rate fast slow', modified: s.speed !== 1, render: () => <ReadAloudSpeed /> },
    ],
  };
}

function ReadAloudOn() {
  const { on } = useReadAloudSettings();
  return (
    <SettingRow
      label="Read replies aloud"
      description="The same as the speaker at the top of a chat: what the agent says to you, never its thinking or its tools."
      details={
        <>
          <p>
            Replies are read a sentence at a time as they stream in, with code blocks read as “the code is in the chat”. Stop and skip sit next to the speaker
            while it reads, and cancelling a turn or leaving the chat stops it.
          </p>
          <p>
            The voice is Kokoro-82M, run inside AgentBox: nothing you read leaves this computer. The first time, it downloads its weights from Hugging Face
            (about 90 MB, or 330 MB when it can use your GPU) and eSpeak NG from jsDelivr (25 MB), and keeps them.
          </p>
        </>
      }
      control={<Switch aria-label="Read replies aloud" checked={on} onCheckedChange={(checked) => {
            if (checked) unlock();
            setReadAloud({ on: checked });
          }} />}
    />
  );
}

const voiceRows: Record<VoiceLanguage, { label: string; description: string; sample: string }> = {
  en: { label: 'English voice', description: 'Reads the replies written in English.', sample: 'Hello! I read the agent’s replies aloud.' },
  pt: {
    label: 'Portuguese voice',
    description: 'Reads the replies written in Portuguese, in Brazilian Portuguese.',
    sample: 'Olá! Eu leio as respostas do agente em voz alta.',
  },
};

function ReadAloudVoice({ language }: { language: VoiceLanguage }) {
  const { voices: chosen } = useReadAloudSettings();
  const reader = useReader();
  const row = voiceRows[language];
  return (
    <SettingRow
      label={row.label}
      description={row.description}
      control={
        <div className="flex w-full items-center gap-1.5">
          <Select aria-label={row.label} value={chosen[language]} onChange={(v) => setReadAloud({ voices: { ...chosen, [language]: v } })} className="min-w-0 flex-1">
            {voices
              .filter((v) => v.language === language)
              .map((v) => (
                <SelectOption key={v.id} value={v.id}>
                  {v.label}
                </SelectOption>
              ))}
          </Select>
          <Button
            variant="ghost"
            className="h-9 shrink-0 px-2.5"
            aria-label={reader.status === 'idle' ? `Try the ${row.label.toLowerCase()}` : 'Stop'}
            title={reader.error ? `The voice failed: ${reader.error}` : undefined}
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
  const { speed } = useReadAloudSettings();
  return (
    <SettingRow
      label="Speed"
      description="How fast the voice reads."
      control={
        <Select aria-label="Speed" value={String(speed)} onChange={(v) => setReadAloud({ speed: Number(v) })}>
          {speeds.map((v) => (
            <SelectOption key={v} value={String(v)}>
              {v === 1 ? 'Normal' : `${v}×`}
            </SelectOption>
          ))}
        </Select>
      }
    />
  );
}
