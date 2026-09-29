// Portuguese phonemes for Kokoro. kokoro-js only reads English; Kokoro itself
// reads Brazilian Portuguese from eSpeak NG's IPA, the way its Python pipeline
// does it (misaki's EspeakG2P): punctuation kept where it was, and each tied
// pair of eSpeak NG's written as the one symbol Kokoro knows it by.

// eSpeak NG's phones as they come out of espeak.ts: a line per clause, with
// "_" between phones.
export type Phones = (text: string) => Promise<string[]>;

// misaki's e2m table, without its "^" ties: a tied pair is one phone here.
const tied: Record<string, string> = {
  aɪ: 'I',
  aʊ: 'W',
  dz: 'ʣ',
  dʒ: 'ʤ',
  eɪ: 'A',
  oʊ: 'O',
  əʊ: 'Q',
  ss: 'S',
  ts: 'ʦ',
  tʃ: 'ʧ',
  ɔɪ: 'Y',
};

// The punctuation Kokoro reads as it is, the same set kokoro-js splits on.
const punctuation = /(\s*[;:,.!?¡¿—…"«»“”(){}[\]]+\s*)+/g;

export async function espeakPhonemes(text: string, phones: Phones): Promise<string> {
  // Parentheses are read as angle quotes, like misaki does.
  const input = text.replace(/«/g, '“').replace(/»/g, '”').replace(/\(/g, '«').replace(/\)/g, '»').replace(/\s+/g, ' ').trim();
  const pieces: string[] = [];
  let at = 0;
  for (const match of input.matchAll(punctuation)) {
    if (match.index > at) pieces.push(await read(input.slice(at, match.index), phones));
    pieces.push(match[0]);
    at = match.index + match[0].length;
  }
  if (at < input.length) pieces.push(await read(input.slice(at), phones));
  return pieces.join('').replace(/-/g, '').replace(/«/g, '(').replace(/»/g, ')').trim();
}

async function read(words: string, phones: Phones): Promise<string> {
  if (!words.trim()) return words;
  const lines = await phones(words);
  return lines.map((line) => line.trim().split(/\s+/).map(word).join(' ')).join(' ');
}

function word(w: string): string {
  return w
    .split('_')
    .filter(Boolean)
    .map((phone) => {
      const stress = /^[ˈˌ]*/.exec(phone)![0];
      const rest = phone.slice(stress.length);
      return stress + (tied[rest] ?? rest);
    })
    .join('');
}
