// How a person of a shared project looks (lib/here.ts, api.Person): their
// colour, one of the session hues (styles.css' [data-person-color]), the same
// for the same name on every AgentBox, and their initials.
import type * as T from '../../shared/api';
import { SESSION_COLORS } from './sessions.ts';

export function personColor(person: Pick<T.Person, 'name'>): string {
  let h = 0;
  for (const ch of person.name.trim().toLowerCase()) h = (h * 31 + ch.codePointAt(0)!) >>> 0;
  return SESSION_COLORS[h % SESSION_COLORS.length];
}

export function initials(name: string): string {
  const words = name.replace(/['’]s\b/gi, '').split(/[\s._-]+/).filter(Boolean);
  if (words.length === 0) return '?';
  const first = [...words[0]][0] ?? '';
  const second = words.length > 1 ? ([...words[1]][0] ?? '') : '';
  return (first + second).toUpperCase();
}
