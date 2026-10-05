// The message format the catalogs are written in: a small subset of ICU's,
// enough for the app and nothing a translator has to learn twice.
//
//   {name}                                   a value, put in as it is
//   {count, plural, =0 {none} one {# agent} other {# agents}}
//                                            the branch the language's plural
//                                            rules pick; # is the number
//   {mode, select, light {light} other {dark}}
//                                            the branch named by the value
//   <b>bold</b>                              a tag, which the caller turns into
//                                            markup (tr in the renderer)
//
// A placeholder with no value is left as it is, so a missing one shows up on
// screen instead of quietly vanishing. Values may be anything — strings and
// numbers for t, React nodes for the renderer's tr — and are returned as parts
// for the caller to join or render.

export type Values = Record<string, unknown>;

// A tag's renderer: what <name>chunk</name> becomes.
export type TagRenderer = (chunk: unknown[]) => unknown;

// formatParts turns a message into its parts: strings, values, and whatever
// the tag renderers made of their tags.
export function formatParts(message: string, values: Values | undefined, lang: string): unknown[] {
  const out: unknown[] = [];
  let text = '';
  const flush = () => {
    if (text) out.push(text);
    text = '';
  };
  let i = 0;
  while (i < message.length) {
    const c = message[i];
    if (c === '{') {
      const end = closingBrace(message, i);
      if (end < 0) {
        text += message.slice(i);
        break;
      }
      const parts = placeholder(message.slice(i + 1, end), values, lang);
      for (const part of parts) {
        if (typeof part === 'string') text += part;
        else {
          flush();
          out.push(part);
        }
      }
      i = end + 1;
      continue;
    }
    if (c === '<') {
      const open = /^<([a-zA-Z][\w-]*)>/.exec(message.slice(i));
      const name = open?.[1];
      const render = name ? values?.[name] : undefined;
      if (open && typeof render === 'function') {
        const close = `</${name}>`;
        const end = message.indexOf(close, i + open[0].length);
        if (end >= 0) {
          flush();
          out.push((render as TagRenderer)(formatParts(message.slice(i + open[0].length, end), values, lang)));
          i = end + close.length;
          continue;
        }
      }
    }
    text += c;
    i++;
  }
  flush();
  return out;
}

// format is a message as one string: values become strings, and a tag with no
// renderer is dropped, keeping its text.
export function format(message: string, values: Values | undefined, lang: string): string {
  const plain: Values = { ...values };
  for (const name of tagNames(message)) if (typeof plain[name] !== 'function') plain[name] = (chunk: unknown[]) => chunk.map(String).join('');
  return formatParts(message, plain, lang).map(String).join('');
}

function tagNames(message: string): string[] {
  return [...message.matchAll(/<([a-zA-Z][\w-]*)>/g)].map((m) => m[1]);
}

// closingBrace is the index of the } that closes the { at start, or -1.
function closingBrace(s: string, start: number): number {
  let depth = 0;
  for (let i = start; i < s.length; i++) {
    if (s[i] === '{') depth++;
    else if (s[i] === '}' && --depth === 0) return i;
  }
  return -1;
}

const pluralRules = new Map<string, Intl.PluralRules>();

function pluralCategory(lang: string, n: number): string {
  let rules = pluralRules.get(lang);
  if (!rules) {
    rules = new Intl.PluralRules(lang);
    pluralRules.set(lang, rules);
  }
  return rules.select(n);
}

// branchesOf reads the "one {…} other {…}" after a plural's or select's kind.
function branchesOf(rest: string): Map<string, string> {
  const branches = new Map<string, string>();
  const branch = /\s*(=?[\w-]+)\s*\{/g;
  let at = 0;
  while (at < rest.length) {
    branch.lastIndex = at;
    const b = branch.exec(rest);
    if (!b || b.index !== at) break;
    const open = b.index + b[0].length - 1;
    const close = closingBrace(rest, open);
    if (close < 0) break;
    branches.set(b[1], rest.slice(open + 1, close));
    at = close + 1;
    while (at < rest.length && /\s/.test(rest[at])) at++;
  }
  return branches;
}

function placeholder(body: string, values: Values | undefined, lang: string): unknown[] {
  const m = /^\s*([\w-]+)\s*,\s*(plural|select)\s*,([\s\S]*)$/.exec(body);
  if (!m) {
    const name = body.trim();
    return values && name in values && values[name] !== undefined ? [values[name]] : [`{${body}}`];
  }
  const [, name, kind, rest] = m;
  const branches = branchesOf(rest);
  const value = values?.[name];
  let chosen: string | undefined;
  if (kind === 'plural') {
    const n = Number(value ?? 0);
    chosen = branches.get(`=${n}`) ?? branches.get(pluralCategory(lang, n)) ?? branches.get('other');
    if (chosen !== undefined) chosen = chosen.replace(/#/g, formatNumberIn(lang, n));
  } else {
    chosen = branches.get(String(value)) ?? branches.get('other');
  }
  return chosen === undefined ? [] : formatParts(chosen, values, lang);
}

export function formatNumberIn(lang: string, n: number, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(lang, options).format(n);
}

// placeholders lists a message's placeholder and tag names, for the test that
// keeps every language's messages taking the same values as en-US's. Branch
// text is searched too, without mistaking a branch for a placeholder.
export function placeholders(message: string): string[] {
  const names = new Set<string>();
  const walk = (s: string) => {
    for (const name of tagNames(s)) names.add(`<${name}>`);
    let i = s.indexOf('{');
    while (i >= 0) {
      const end = closingBrace(s, i);
      if (end < 0) return;
      const body = s.slice(i + 1, end);
      const m = /^\s*([\w-]+)\s*(?:,\s*(?:plural|select)\s*,([\s\S]*))?$/.exec(body);
      if (m) {
        names.add(m[1]);
        if (m[2] !== undefined) for (const branch of branchesOf(m[2]).values()) walk(branch);
      }
      i = s.indexOf('{', end + 1);
    }
  };
  walk(message);
  return [...names].sort();
}
