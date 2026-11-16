// The file viewer's table preview of CSV and TSV files (components/files/
// DataGrid.tsx): which files are, and a parser by RFC 4180, quoted fields
// with doubled quotes and line breaks in them included, that reads a file of
// a few megabytes in a few dozen milliseconds.

// delimiterOf is a table file's field separator, or undefined for any other.
export function delimiterOf(path: string): string | undefined {
  const ext = /\.(csv|tsv|tab)$/i.exec(path)?.[1].toLowerCase();
  return ext === 'csv' ? ',' : ext ? '\t' : undefined;
}

export interface Delimited {
  rows: string[][];
  // columns is the widest row's count.
  columns: number;
  // unclosed is a quote still open at the end: a cut file, or not a table.
  unclosed: boolean;
}

// parseDelimited splits text into rows of fields. A last empty line is not a
// row; \r\n ends a line as \n does.
export function parseDelimited(text: string, sep: string): Delimited {
  const rows: string[][] = [];
  let columns = 0;
  let row: string[] = [];
  let i = 0;
  const n = text.length;
  const endRow = () => {
    rows.push(row);
    if (row.length > columns) columns = row.length;
    row = [];
  };
  if (n === 0) return { rows, columns, unclosed: false };
  for (;;) {
    if (text.charCodeAt(i) === 34 /* " */) {
      // A quoted field: up to the quote not doubled.
      let value = '';
      let from = i + 1;
      for (;;) {
        const q = text.indexOf('"', from);
        if (q < 0) {
          row.push(value + text.slice(from));
          endRow();
          return { rows, columns, unclosed: true };
        }
        value += text.slice(from, q);
        if (text.charCodeAt(q + 1) === 34) {
          value += '"';
          from = q + 2;
          continue;
        }
        i = q + 1;
        break;
      }
      // Anything between the closing quote and the separator is kept, as
      // spreadsheets do.
      const end = nextEnd(text, i, sep);
      row.push(end > i ? value + text.slice(i, end) : value);
      i = end;
    } else {
      const end = nextEnd(text, i, sep);
      row.push(text.slice(i, end));
      i = end;
    }
    if (i >= n) {
      endRow();
      break;
    }
    if (text[i] === sep) {
      i++;
      if (i >= n) {
        row.push('');
        endRow();
        break;
      }
      continue;
    }
    // A line's end.
    i += text.charCodeAt(i) === 13 && text.charCodeAt(i + 1) === 10 ? 2 : 1;
    endRow();
    if (i >= n) break;
  }
  return { rows, columns, unclosed: false };
}

// nextEnd is where the field from i ends: the next separator, line break or
// the text's end.
function nextEnd(text: string, i: number, sep: string): number {
  const s = sep.charCodeAt(0);
  for (; i < text.length; i++) {
    const c = text.charCodeAt(i);
    if (c === s || c === 10 || c === 13) return i;
  }
  return i;
}

// columnWidths is each column's width in characters for the grid, from its
// header and the first rows, between min and max.
export function columnWidths(header: string[], rows: string[][], columns: number, min = 4, max = 40, sample = 200): number[] {
  const out: number[] = [];
  for (let c = 0; c < columns; c++) {
    let w = (header[c] ?? '').length;
    for (let r = 0; r < Math.min(rows.length, sample); r++) {
      const v = rows[r][c];
      if (v && v.length > w) w = v.length;
    }
    out.push(Math.min(max, Math.max(min, w)));
  }
  return out;
}
