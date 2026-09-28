import { Play } from 'lucide-react';
import type { SettingGroup } from '../lib/settingsSearch';
import { speak, stop } from '../lib/voice/reader';
import { setReadAloud, speeds, useReadAloudSettings, voices } from '../lib/voice/settings';
import { Button } from './ui/button';
import { Select, SelectOption } from './ui/select';
import { SettingRow } from './ui/settings';
import { Switch } from './ui/switch';

// useReadAloudGroup is Settings → Voice's reading aloud: the chat's speaker,
// and the voice it reads in. Kept apart from SettingsView so the Voice section
// only lists it.
export function useReadAloudGroup(): SettingGroup {
  const s = useReadAloudSettings();
  return {
    id: 'read-aloud',
    title: 'Read aloud',
    description: 'The speaker in every chat reads the agent’s replies aloud as they come in, on this computer, with Kokoro.',
    entries: [
      { id: 'read-aloud-on', label: 'Read replies aloud', keywords: 'speaker speech tts kokoro audio voice sound', modified: s.on, render: () => <ReadAloudOn /> },
      { id: 'read-aloud-voice', label: 'Voice', keywords: 'kokoro voice language english portuguese português', render: () => <ReadAloudVoice /> },
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
      control={<Switch aria-label="Read replies aloud" checked={on} onCheckedChange={(checked) => setReadAloud({ on: checked })} />}
    />
  );
}

function ReadAloudVoice() {
  const { voice } = useReadAloudSettings();
  return (
    <SettingRow
      label="Voice"
      description="English or Brazilian Portuguese: the voice reads everything in its own language."
      control={
        <div className="flex w-full items-center gap-1.5">
          <Select aria-label="Voice" value={voice} onChange={(v) => setReadAloud({ voice: v })} className="min-w-0 flex-1">
            {voices.map((v) => (
              <SelectOption key={v.id} value={v.id}>
                {v.label}
              </SelectOption>
            ))}
          </Select>
          <Button
            variant="ghost"
            className="h-9 shrink-0 px-2.5"
            aria-label="Try this voice"
            data-voice-try
            onClick={() => {
              stop();
              speak(voices.find((v) => v.id === voice)?.language === 'pt' ? 'Olá! Eu leio as respostas do agente em voz alta.' : 'Hello! I read the agent’s replies aloud.');
            }}
          >
            <Play />
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
