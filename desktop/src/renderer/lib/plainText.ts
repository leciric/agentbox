// plainText turns a markdown message into one line for a preview: the sidebar's
// last message, Ctrl+K's results. A table is "Table: Fruit, Color · 2 rows"
// rather than its cells run together; code fences, links, emphasis, headings
// and tags are stripped.

const rule = /^\s{0,3}([-*_])(\s*\1){2,}\s*$/;
const separator = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;
const isRow = (line: string) => line.trim().startsWith('|');

// cells splits a table row into its trimmed, non-empty cells.
function cells(line: string): string[] {
  return line
    .trim()
    .replace(/^\||\|$/g, '')
    .split('|')
    .map((c) => c.trim())
    .filter(Boolean);
}

const inline = (s: string): string =>
  s
    .replace(/<[^>\n]+>/g, ' ')
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/^\s{0,3}(#{1,6}|>)+\s*/, '')
    .replace(/^\s{0,3}([-*+]\s|\d+[.)]\s)\s*/, '')
    .replace(/^\[[ xX]\]\s*/, '')
    .replace(/[*`~]+/g, '')
    .replace(/(^|\W)__?(\S[^_]*?)__?(?=\W|$)/g, '$1$2');

export function plainText(markdown: string | undefined): string {
  if (!markdown) return '';
  const lines = markdown.replace(/```[\s\S]*?(```|$)/g, '\n').split('\n');
  const out: string[] = [];
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (rule.test(line) || (isRow(line) && separator.test(line) && !lines[i - 1]?.trim())) continue;
    if (isRow(line) && separator.test(lines[i + 1] ?? '') && isRow(lines[i + 1])) {
      // A header and its separator row start a table; its body rows follow.
      let rows = 0;
      let j = i + 2;
      for (; j < lines.length && isRow(lines[j]); j++) rows++;
      const head = cells(line).map(inline).join(', ');
      out.push(`Table: ${head} · ${rows} ${rows === 1 ? 'row' : 'rows'}`);
      i = j - 1;
      continue;
    }
    // A row with no header before it, as a search snippet cut mid-table has.
    out.push(isRow(line) ? (separator.test(line) ? '' : cells(line).map(inline).join(', ')) : inline(line));
  }
  return out.join(' ').replace(/\s+/g, ' ').trim();
}

// heldBack is text without a table still arriving: its lines wait for the
// separator row, so "| Fruit | Color" isn't shown as text before the table.
export function heldBack(text: string): string {
  const lines = text.split('\n');
  if (lines.filter((l) => /^\s*(```|~~~)/.test(l)).length % 2) return text;
  let start = lines.length;
  while (start > 0 && isRow(lines[start - 1])) start--;
  if (start === lines.length) return text;
  const second = lines[start + 1];
  if (second !== undefined && separator.test(second) && isRow(second)) return text;
  return lines.slice(0, start).join('\n');
}
