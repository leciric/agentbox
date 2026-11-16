// The command palette's logic (components/SearchModal.tsx, components/
// paletteCommands.tsx): which actions and settings answer what was typed, in
// what order, and whether they go above what the daemon found or below it.
// Pure: the words come in already translated, so this is tested without the
// app's catalog.

// A command is something the palette does rather than finds: an action (a
// verb with a handler) or a setting (which opens Settings at it).
export type CommandGroup = 'actions' | 'settings';

export type Command = {
  id: string;
  group: CommandGroup;
  // title is what the row says: "Start Android emulator", "Model for new agents".
  title: string;
  // keywords are what else finds it: synonyms, the words of its description.
  keywords?: string;
  // where is the place it lives, shown beside it and searched too: a
  // setting's section and group ("Models · New agents").
  where?: string;
};

export type Ranked<C extends Command = Command> = { command: C; score: number };

// normalize folds case and accents, as Settings' own search does, so "camera"
// finds "câmera" and "nao" finds "não".
export function normalize(text: string): string {
  return text
    .normalize('NFD')
    .replace(/\p{Diacritic}/gu, '')
    .toLowerCase()
    .replace(/[’']/g, '');
}

const wordsOf = (text: string) => normalize(text).split(/[^\p{L}\p{N}]+/u).filter(Boolean);

// commandsOnly is a query that starts with ">", as VS Code's palette has it:
// only actions and settings, nothing searched in the daemon. rest is the
// query without it.
export function commandsOnly(query: string): { only: boolean; rest: string } {
  const trimmed = query.trimStart();
  return trimmed.startsWith('>') ? { only: true, rest: trimmed.slice(1).trim() } : { only: false, rest: query.trim() };
}

// looksLikeVerb is whether the query starts with a verb of the language's
// list ("record", "start", "turn on"), or the start of one typed so far
// ("rec"): then what you want is to do something, and actions and settings go
// above what was found. One letter is too little to tell.
export function looksLikeVerb(query: string, verbs: readonly string[]): boolean {
  const first = wordsOf(commandsOnly(query).rest)[0];
  if (!first || first.length < 2) return false;
  return verbs.some((verb) => {
    const v = normalize(verb);
    return v === first || (first.length >= 3 && v.startsWith(first));
  });
}

// Scores, highest first: the whole query starts the title; every word starts
// a word of the title; every word is somewhere in the title; and the rest,
// found only among the keywords or the place.
const startsTitle = 400;
const titleWords = 300;
const inTitle = 200;
const elsewhere = 100;

// scoreCommand is how well a command answers the query, 0 for not at all:
// every word typed has to be found, each as the start of a word of its title,
// keywords or place (or inside one, for three letters or more).
export function scoreCommand(command: Command, query: string): number {
  const words = wordsOf(query);
  if (words.length === 0) return 0;
  const title = wordsOf(command.title);
  const rest = wordsOf(`${command.keywords ?? ''} ${command.where ?? ''}`);
  const starts = (list: string[], w: string) => list.some((x) => x.startsWith(w));
  const inside = (list: string[], w: string) => w.length >= 3 && list.some((x) => x.includes(w));
  if (!words.every((w) => starts(title, w) || starts(rest, w) || inside(title, w) || inside(rest, w))) return 0;
  const typed = words.join(' ');
  // Shorter titles first among equals: "Record agent screen" before
  // "Record agent screen with sound".
  const brevity = Math.max(0, 50 - title.length * 5);
  if (title.join(' ').startsWith(typed)) return startsTitle + brevity;
  if (words.every((w) => starts(title, w))) return titleWords + brevity;
  if (words.every((w) => starts(title, w) || inside(title, w))) return inTitle + brevity;
  return elsewhere + brevity;
}

// rankCommands is the commands that answer the query, best first, the order
// they were given in among equals. An empty query answers with all of them,
// as given.
export function rankCommands<C extends Command>(commands: readonly C[], query: string): Ranked<C>[] {
  const q = commandsOnly(query).rest;
  if (wordsOf(q).length === 0) return commands.map((command) => ({ command, score: 0 }));
  return commands
    .map((command, i) => ({ command, score: scoreCommand(command, q), i }))
    .filter((r) => r.score > 0)
    .sort((a, b) => b.score - a.score || a.i - b.i)
    .map(({ command, score }) => ({ command, score }));
}

// PaletteRow is one row of the palette's list: a command, or something the
// daemon found (T); the order of rows is the order the arrow keys walk.
export type PaletteRow<C extends Command, T> = { kind: 'command'; command: C } | { kind: 'found'; item: T };

// paletteRows lays the list out: the actions, then the settings, above what
// was found when the query looks like a verb (or starts with ">", which drops
// what was found), and below it otherwise. Each group keeps its own order.
export function paletteRows<C extends Command, T>(ranked: readonly Ranked<C>[], found: readonly T[], query: string, verbs: readonly string[]): PaletteRow<C, T>[] {
  const of = (group: CommandGroup) => ranked.filter((r) => r.command.group === group).map((r): PaletteRow<C, T> => ({ kind: 'command', command: r.command }));
  const commands = [...of('actions'), ...of('settings')];
  if (commandsOnly(query).only) return commands;
  const foundRows = found.map((item): PaletteRow<C, T> => ({ kind: 'found', item }));
  return looksLikeVerb(query, verbs) ? [...commands, ...foundRows] : [...foundRows, ...commands];
}

// pickChoices is what the palette offers when an action asks which agent or
// project it's for: the ones whose name answers what was typed, the current
// one first.
export function pickChoices<P extends { id: string; title: string; detail?: string }>(choices: readonly P[], query: string, current?: string): P[] {
  const words = wordsOf(query);
  const found = choices.filter((c) => {
    const hay = wordsOf(`${c.title} ${c.id} ${c.detail ?? ''}`);
    return words.every((w) => hay.some((x) => x.startsWith(w) || (w.length >= 3 && x.includes(w))));
  });
  return [...found.filter((c) => c.id === current), ...found.filter((c) => c.id !== current)];
}
