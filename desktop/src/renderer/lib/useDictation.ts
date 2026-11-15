// The hook behind the phone composer's mic button: it drives the browser's
// SpeechRecognition (see dictation.ts for the rules). Tap to start; it ends
// on a tap or when the speaker pauses (one utterance, not continuous). The
// interim guess goes to onText as it comes, so the composer shows it live;
// nothing is ever sent from here.
import { createContext, useContext, useEffect, useRef, useState } from 'react';
import { dictationSupport, draftAt, heard, micMissing, micRefused, recognitionLang, speak, type Support } from './dictation';

// The composer is the phone's only where PhoneApp says so: the desktop app
// shares it and has no button.
export const PhoneMode = createContext(false);

type Recognition = {
  lang: string;
  interimResults: boolean;
  continuous: boolean;
  onresult: ((e: { results: ArrayLike<{ isFinal: boolean; [i: number]: { transcript: string } }> }) => void) | null;
  onerror: ((e: { error: string }) => void) | null;
  onend: (() => void) | null;
  start(): void;
  stop(): void;
  abort(): void;
};

export type Dictation = { support: Support | 'off'; listening: boolean; toggle: () => void; stop: () => void };

export function useDictation({
  language,
  current,
  onText,
  onRefused,
  onNoMic,
}: {
  language: string;
  // current is the composer's text and cursor (selection) right now.
  current: () => { text: string; start: number; end: number };
  onText: (text: string, cursor: number) => void;
  onRefused: () => void;
  onNoMic: () => void;
}): Dictation {
  const phone = useContext(PhoneMode);
  const [listening, setListening] = useState(false);
  const rec = useRef<Recognition | null>(null);
  const latest = useRef({ current, onText, onRefused, onNoMic });
  latest.current = { current, onText, onRefused, onNoMic };
  const support: Support = dictationSupport(window as unknown as Parameters<typeof dictationSupport>[0]);

  // Leaving the screen stops the mic.
  useEffect(() => () => rec.current?.abort(), []);

  // stop ends the listening now and drops what it hasn't delivered: the
  // composer was sent or cleared, or the agent changed.
  const stop = () => {
    const r = rec.current;
    rec.current = null;
    r?.abort();
    setListening(false);
  };

  const toggle = () => {
    if (rec.current) {
      rec.current.stop();
      return;
    }
    const w = window as unknown as { SpeechRecognition?: new () => Recognition; webkitSpeechRecognition?: new () => Recognition };
    const Ctor = w.SpeechRecognition ?? w.webkitSpeechRecognition;
    if (!Ctor) return;
    const here = latest.current.current();
    const draft = draftAt(here.text, here.start, here.end);
    let shown = here.text;
    const r = new Ctor();
    r.lang = recognitionLang(language);
    r.interimResults = true;
    r.continuous = false;
    r.onresult = (e) => {
      if (rec.current !== r) return;
      const { final, interim } = heard(e.results);
      const next = speak(draft, shown, latest.current.current().text, final + interim);
      if (!next) return stop();
      shown = next.text;
      latest.current.onText(next.text, next.cursor);
    };
    r.onerror = (e) => {
      if (micRefused(e.error)) latest.current.onRefused();
      else if (micMissing(e.error)) latest.current.onNoMic();
    };
    r.onend = () => {
      if (rec.current === r) rec.current = null;
      setListening(false);
    };
    rec.current = r;
    try {
      r.start();
      setListening(true);
    } catch {
      rec.current = null;
    }
  };

  return { support: phone ? support : 'off', listening, toggle, stop };
}
