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
