// Finding in a chat: the daemon says which items hold the query (GET
// …/chat/search), and the app marks where in them, comparing the way the
// daemon's index does — ignoring case and accents — so that what it marks is
// what was found.

// fold is text as a find compares it: lower case, without accents, and with
// each run of whitespace one space. at[i] is the index in text that
// folded[i] came from.
export function fold(text: string): { folded: string; at: number[] } {
  let folded = '';
  const at: number[] = [];
  let i = 0;
  for (const ch of text) {
    if (/\s/.test(ch)) {
      if (!folded.endsWith(' ')) {
        folded += ' ';
        at.push(i);
      }
    } else {
      const f = ch.normalize('NFD').replace(/\p{Mn}/gu, '').toLowerCase();
      for (let k = 0; k < f.length; k++) at.push(i);
      folded += f;
    }
    i += ch.length;
  }
  return { folded, at };
}

// matchesIn is where query is in text: [start, end) pairs of indexes into
// text, in order and not overlapping. A query of nothing but whitespace is in
// nothing.
export function matchesIn(text: string, query: string): [number, number][] {
  const needle = fold(query).folded;
  if (needle.trim() === '') return [];
  const { folded, at } = fold(text);
  const out: [number, number][] = [];
  for (let s = folded.indexOf(needle); s >= 0; s = folded.indexOf(needle, s + needle.length)) {
    const last = at[s + needle.length - 1];
    out.push([at[s], last + (text.codePointAt(last)! > 0xffff ? 2 : 1)]);
  }
  return out;
}

// stepHit moves through a search's hits, which come oldest first, by
// direction: 'older' toward the chat's start and 'newer' toward its end,
// going round at either. From no hit, or one no longer found, it starts at
// the newest: a chat is read from its end.
export function stepHit(hits: readonly { id: string }[], current: string | undefined, direction: 'older' | 'newer' | 'stay'): string | undefined {
  if (hits.length === 0) return undefined;
  const i = current === undefined ? -1 : hits.findIndex((h) => h.id === current);
  if (i < 0) return hits[hits.length - 1].id;
  if (direction === 'stay') return hits[i].id;
  const n = hits.length;
  return hits[(i + (direction === 'older' ? n - 1 : 1)) % n].id;
}

// Who a find looks at the messages of: anyone, you (what you wrote, asides
// included), or the AI tool (the lead in a project's chat, the agent in its
// own). Notices and errors are only found from anyone.
export type FindFrom = 'anyone' | 'you' | 'ai';

// findKinds is the item kinds a find from someone asks the daemon for; none
// is every kind.
export function findKinds(from: FindFrom): string[] {
  return from === 'you' ? ['user', 'aside'] : from === 'ai' ? ['assistant'] : [];
}

// hitsByDay is a find's hits for its list: newest first, in a group per day
// of the time each was said (local time), the day as YYYY-MM-DD. Hits with no
// time (a daemon from before they had one) come last, in a group of their own
// whose day is ''.
export function hitsByDay<H extends { at?: string }>(hits: readonly H[]): { day: string; hits: H[] }[] {
  const out: { day: string; hits: H[] }[] = [];
  const undated: H[] = [];
  for (let i = hits.length - 1; i >= 0; i--) {
    const hit = hits[i];
    if (!hit.at) {
      undated.push(hit);
      continue;
    }
    const day = localDay(new Date(hit.at));
    const last = out.at(-1);
    if (last?.day === day) last.hits.push(hit);
    else out.push({ day, hits: [hit] });
  }
  return undated.length ? [...out, { day: '', hits: undated }] : out;
}

// localDay is a time's day where you are, as YYYY-MM-DD.
export function localDay(at: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())}`;
}
