// What reading a reply aloud says: the reply's markdown turned into plain
// sentences, and the sentences of a reply that is still streaming handed out
// as each one ends. Pure text, so it is tested without a voice to hear it.

export type VoiceLanguage = 'en' | 'pt';

// What a code block is read as: the code itself is no use heard.
const codeNote: Record<VoiceLanguage, string> = {
  en: 'The code is in the chat.',
  pt: 'O código está no chat.',
};
const linkWord: Record<VoiceLanguage, string> = { en: 'a link', pt: 'um link' };

// Inline code longer than this is a command or a path nobody wants spelled
// out; shorter, it's a name, read like any other word.
const inlineCodeLimit = 32;

// speakableText turns markdown into what is said. A fenced block still open —
// a reply streaming in the middle of one — is read the same as a closed one,
// so the text before it and the note for it don't change once it closes.
export function speakableText(markdown: string, language: VoiceLanguage): string {
  const out: string[] = [];
  let fence: string | null = null;
  for (const line of markdown.split('\n')) {
    const marker = /^\s{0,3}(`{3,}|~{3,})/.exec(line)?.[1];
    if (fence !== null) {
      if (marker && marker[0] === fence[0] && marker.length >= fence.length && line.trim() === marker) fence = null;
      continue;
    }
    if (marker) {
      fence = marker;
      out.push('', codeNote[language], '');
      continue;
    }
    out.push(speakableLine(line, language));
  }
  return out
    .join('\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

function speakableLine(line: string, language: VoiceLanguage): string {
  // A table's separator row, and a rule, say nothing.
  if (/^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?\s*$/.test(line) || /^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) return '';
  let s = line
    .replace(/^\s{0,3}#{1,6}\s+/, '') // headings
    .replace(/^\s*>\s?/, '') // quotes
    .replace(/^\s*(?:[-*+]|\d+[.)])\s+(?:\[[ xX]\]\s+)?/, '') // list items and task boxes
    .replace(/<[^>\n]+>/g, ' '); // HTML
  s = s
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1') // images
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1') // links
    .replace(/`([^`]+)`/g, (_, code: string) => (code.length > inlineCodeLimit ? 'code' : code))
    .replace(/\bhttps?:\/\/\S+/g, linkWord[language])
    .replace(/(\*\*|__)(.+?)\1/g, '$2')
    .replace(/(^|[^\w*])[*_]([^*_\s][^*_]*?)[*_](?=[^\w*]|$)/g, '$1$2')
    .replace(/~~(.+?)~~/g, '$1')
    .replace(/^\s*\|(.*?)\|?\s*$/, '$1') // a table row's outer bars
    .replace(/\s*\|\s*/g, ', ')
    .replace(/\s+/g, ' ')
    .trim();
  // A heading or a list item without an ending of its own ends where its line does.
  if (s && line !== s && /^\s*(#{1,6}\s|[-*+]\s|\d+[.)]\s)/.test(line) && !/[.!?…:;,]$/.test(s)) s += '.';
  return s;
}

// Sentences longer than this are cut at a comma or a space: Kokoro reads at
// most 510 phonemes at once, and a short first piece starts speaking sooner.
const longSentence = 240;

// sentences splits speakable text where one sentence ends and the next
// begins. The last piece is returned too, whether it has ended or not: only
// the caller knows whether more is coming.
export function sentences(text: string): string[] {
  const parts: string[] = [];
  for (const paragraph of text.split(/\n+/)) {
    for (const s of paragraph.split(/(?<=[.!?…])["')\]]*\s+(?=\S)/)) {
      const t = s.trim();
      if (t) parts.push(...cut(t));
    }
  }
  return parts;
}

function cut(sentence: string): string[] {
  const parts: string[] = [];
  let rest = sentence;
  while (rest.length > longSentence) {
    const head = rest.slice(0, longSentence);
    let at = Math.max(head.lastIndexOf(', '), head.lastIndexOf('; '), head.lastIndexOf(': '));
    if (at < longSentence / 3) at = head.lastIndexOf(' ');
    if (at <= 0) at = longSentence - 1;
    parts.push(rest.slice(0, at + 1).trim());
    rest = rest.slice(at + 1).trim();
  }
  if (rest) parts.push(rest);
  return parts;
}

// SentenceFeed follows one reply as it streams. Each call gets the reply's
// whole text so far and returns the sentences that have ended since the last
// call; once the reply is done, the one still open is returned too.
//
// Sentences are counted rather than cut at a character offset: the markdown
// of a line still being written can read differently once it ends (a `**`
// that closes), but only in the sentence that hasn't ended, which isn't
// handed out yet.
export class SentenceFeed {
  private said = 0;
  private readonly language: VoiceLanguage;

  // skipText starts the feed past what the reply had already said when
  // reading was turned on, so turning it on mid-reply picks up from there.
  constructor(language: VoiceLanguage, skipText?: string, skipDone = false) {
    this.language = language;
    if (skipText !== undefined) this.said = this.ended(skipText, skipDone).length;
  }

  next(text: string, done: boolean): string[] {
    const all = this.ended(text, done);
    const fresh = all.slice(this.said);
    this.said = Math.max(this.said, all.length);
    return fresh;
  }

  private ended(text: string, done: boolean): string[] {
    const all = sentences(speakableText(text, this.language));
    if (done || all.length === 0) return all;
    // The last piece has ended if the text goes on past its end: a space, or a
    // new line, after its full stop.
    const last = all[all.length - 1];
    const closed = /[.!?…]["')\]]*$/.test(last) && /[.!?…]["')\]*_`]*\s+$/.test(text);
    return closed ? all : all.slice(0, -1);
  }
}
