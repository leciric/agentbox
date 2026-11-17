// Speak to type, on the phone's composer: the browser's own Web Speech API
// (SpeechRecognition, or webkitSpeechRecognition in Chrome and Safari). No
// model of ours and no package: the browser, or the OS behind it, does the
// listening. These are the pure parts; the hook that drives the browser's
// recogniser is useDictation (useDictation.ts).

// Support is what the button does: absent when the browser has no API
// (Firefox), disabled when it has one it won't let this page use (the mic
// needs https, and the LAN page is plain http), ready otherwise.
export type Support = 'missing' | 'insecure' | 'ready';

export type DictationHost = { SpeechRecognition?: unknown; webkitSpeechRecognition?: unknown; isSecureContext?: boolean };

export function dictationSupport(host: DictationHost): Support {
  if (!host.SpeechRecognition && !host.webkitSpeechRecognition) return 'missing';
  return host.isSecureContext ? 'ready' : 'insecure';
}

// recognitionLang is the BCP 47 tag the recogniser listens for: the app's.
export function recognitionLang(language: string): string {
  return language.toLowerCase().startsWith('pt') ? 'pt-BR' : 'en-US';
}

// What the recogniser has heard so far: the settled text and, still moving,
// the interim guess after it.
export type Heard = { final: string; interim: string };

type Result = { isFinal: boolean; [i: number]: { transcript: string } };

// heard reads a recogniser's results list.
export function heard(results: ArrayLike<Result>): Heard {
  let final = '';
  let interim = '';
  for (let i = 0; i < results.length; i++) {
    const text = results[i][0]?.transcript ?? '';
    if (results[i].isFinal) final += text;
    else interim += text;
  }
  return { final, interim };
}

// A Draft is the composer's text with a dictation going into it: the text
// before and after where the cursor was when it began.
export type Draft = { before: string; after: string };

export function draftAt(text: string, start: number, end = start): Draft {
  const a = Math.max(0, Math.min(start, text.length));
  const b = Math.max(a, Math.min(end, text.length));
  return { before: text.slice(0, a), after: text.slice(b) };
}

// spoken puts speech between the draft's two halves, with a space where it
// would run into a word, and says where the cursor goes: after the speech.
export function spoken(draft: Draft, speech: string): { text: string; cursor: number } {
  const said = speech.trim();
  if (!said) return { text: draft.before + draft.after, cursor: draft.before.length };
  const lead = draft.before !== '' && !/\s$/.test(draft.before) ? ' ' : '';
  const tail = draft.after !== '' && !/^\s/.test(draft.after) ? ' ' : '';
  const before = draft.before + lead + said + tail;
  return { text: before + draft.after, cursor: before.length - tail.length };
}

// The recogniser's error codes that mean the person said no, or the phone has
// no mic to give.
export function micRefused(error: string): boolean {
  return error === 'not-allowed' || error === 'service-not-allowed';
}

// A recogniser's error that means the phone has no mic to listen with.
export function micMissing(error: string): boolean {
  return error === 'audio-capture';
}

// speak is what a result puts in the composer: the draft with the speech in
// it, unless the person has typed (or the composer was cleared) since the last
// thing dictation put there. Then it's null, and dictation should stop rather
// than overwrite them.
export function speak(draft: Draft, shown: string, now: string, speech: string): { text: string; cursor: number } | null {
  return now === shown ? spoken(draft, speech) : null;
}
