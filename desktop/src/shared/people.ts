// How a person of a shared project looks (api.Person): their colour, one of
// the session hues, and their initials. One file for the desktop app
// (renderer/lib/people.ts) and the phone app (mobile/src/shared/people.ts), so
// the same name has the same colour and letters on every screen of both, and
// on every AgentBox.
import type * as T from './api.ts';

// PERSON_COLORS are the hues a person can have: the session hues
// (renderer/lib/sessions.ts' SESSION_COLORS, which people.test.ts keeps equal
// to this), each a [data-person-color] rule in the desktop's styles.css and an
// entry of the phone's personColors (mobile/src/ui/theme.ts).
export const PERSON_COLORS = ['rose', 'sky', 'mint', 'lilac', 'peach'] as const;
export type PersonColor = (typeof PERSON_COLORS)[number];

export function personColor(person: Pick<T.Person, 'name'>): PersonColor {
  let h = 0;
  for (const ch of person.name.trim().toLowerCase()) h = (h * 31 + ch.codePointAt(0)!) >>> 0;
  return PERSON_COLORS[h % PERSON_COLORS.length];
}

export function initials(name: string): string {
  const words = name.replace(/['’]s\b/gi, '').split(/[\s._-]+/).filter(Boolean);
  if (words.length === 0) return '?';
  const first = [...words[0]][0] ?? '';
  const second = words.length > 1 ? ([...words[1]][0] ?? '') : '';
  return (first + second).toUpperCase();
}
