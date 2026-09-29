// Which language a reply is in, so it is read by a voice that speaks it: a
// Portuguese voice reading English, or the other way round, can't be followed.
// Only the two languages the voices have, told apart by their most common
// small words and by Portuguese's accents, which is plenty for a paragraph.
import type { VoiceLanguage } from './speakable.ts';

// Words common in one language and not a word in the other ("a", "as", "do"
// and "no" are both, so they count for neither).
const words: Record<VoiceLanguage, Set<string>> = {
  en: new Set(
    'the and is are was were be been to of in it its that this these those for with on you your i we they he she have has had not but at by from or will would can could should now done all so if an there what which when then than just also about into here after before because how'.split(
      ' ',
    ),
  ),
  pt: new Set(
    'que não de da das dos em na nas nos um uma umas uns é e o os para com por se mas isso isto essa esse está estou estão você vocês foi já também ao à às seu sua mais como quando ele ela eu agora aqui então porque ou fiz vou pode tem são ser está pelo pela nem só depois antes sobre muito'.split(' '),
  ),
};

const accented = /[ãõçáéíóúâêôà]/;

// detectLanguage says which of the voices' languages text is in, or fallback
// when it can't tell (no words it knows, or as many of each).
export function detectLanguage(text: string, fallback: VoiceLanguage = 'en'): VoiceLanguage {
  const prose = text
    .replace(/```[\s\S]*?(```|$)/g, ' ')
    .replace(/`[^`\n]*`/g, ' ')
    .replace(/\bhttps?:\/\/\S+/g, ' ')
    .toLowerCase();
  let en = 0;
  let pt = 0;
  for (const word of prose.match(/[\p{L}']+/gu) ?? []) {
    if (words.en.has(word)) en++;
    if (words.pt.has(word)) pt++;
    else if (accented.test(word)) pt += 2;
  }
  return pt > en ? 'pt' : en > pt ? 'en' : fallback;
}
