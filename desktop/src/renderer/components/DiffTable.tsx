// DiffTable is one file's diff as a table: both line numbers, the sign and
// the code, highlighted as its language. In the review (ReviewNotesContext)
// each line of code has a + beside it that opens a note on it; dragging from
// the + over more lines, or Shift-clicking a second +, takes in the lines
// between. Notes, drafts and sent, show under their last line.
import { Plus } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import type { ThemedToken } from 'shiki/core';
import type * as T from '../../shared/api';
import { highlight } from '../lib/highlight';
import { useT } from '../lib/i18n';
import { inNote, noteAt, noteRange, whereLabel } from '../lib/reviewNotes';
import { type DiffLine } from '../lib/pulls';
import { useMode } from '../lib/theme';
import { cn } from '../lib/utils';
import { NoteBox, NoteCard, useReviewNotesContext } from './ReviewNotes';

const lineTints: Record<DiffLine['kind'], string> = {
  add: 'bg-emerald-500/10',
  del: 'bg-rose-500/10',
  hunk: 'bg-sky-500/10 text-subtle',
  context: '',
  note: 'text-faint italic',
};
const signs: Record<DiffLine['kind'], string> = { add: '+', del: '−', hunk: '', context: ' ', note: '' };

const isCode = (l: DiffLine): l is Extract<DiffLine, { kind: 'add' | 'del' | 'context' }> => l.kind === 'add' || l.kind === 'del' || l.kind === 'context';

export function DiffTable({ path, lines, tokens }: { path: string; lines: DiffLine[]; tokens: ThemedToken[][] | null }) {
  const t = useT();
  const notes = useReviewNotesContext();
  // drag is the rows being swept from a +, until the button comes up.
  const [drag, setDrag] = useState<{ from: number; to: number } | null>(null);
  // anchor is the row of the last + pressed, which Shift-click extends from.
  const anchor = useRef<number | null>(null);
  useEffect(() => {
    if (!drag || !notes) return;
    const up = () => {
      setDrag(null);
      notes.compose(noteRange(path, lines, drag.from, drag.to));
    };
    window.addEventListener('mouseup', up);
    return () => window.removeEventListener('mouseup', up);
  }, [drag, notes, path, lines]);

  const mine = notes?.notes.filter((n) => n.path === path) ?? [];
  // A note shows under its last line while the diff has its code there;
  // its lines are marked while it's open.
  const under = new Map<number, T.ReviewNote[]>();
  const open: T.ReviewNote[] = [];
  for (const n of mine) {
    const at = noteAt(lines, n);
    if (at < 0) continue;
    under.set(at, [...(under.get(at) ?? []), n]);
    if (n.status !== 'resolved') open.push(n);
  }
  const composing = notes?.composing?.path === path ? notes.composing : null;
  const composeAt = composing ? noteAt(lines, composing) : -1;
  const swept = (i: number) => !!drag && i >= Math.min(drag.from, drag.to) && i <= Math.max(drag.from, drag.to);
  const noted = (line: DiffLine) => open.some((n) => inNote(line, n));
  const columns = notes ? 5 : 4;

  let code = 0;
  return (
    <table className={cn('w-full border-collapse font-mono text-[11.5px] leading-[1.55]', drag && 'select-none')}>
      <tbody>
        {lines.map((line, i) => {
          const lineIsCode = isCode(line);
          const lineTokens = lineIsCode ? tokens?.[code++] : undefined;
          const lit = swept(i) || (composing && inNote(line, composing));
          const marked = !lit && noted(line);
          return [
            <tr
              key={i}
              className={cn('group', lineTints[line.kind], lit && 'bg-brand-500/20', marked && 'shadow-[inset_2px_0_0] shadow-amber-400/70')}
              onMouseEnter={drag ? () => setDrag({ ...drag, to: i }) : undefined}
              data-diff-line={lineIsCode ? (line.kind === 'del' ? `old:${line.old}` : `new:${line.new}`) : undefined}
            >
              {notes && (
                <td className="w-5 select-none p-0 align-top">
                  {lineIsCode && (
                    <button
                      type="button"
                      className="mt-px flex size-[17px] items-center justify-center rounded bg-brand-500 text-white opacity-0 shadow transition-opacity focus-visible:opacity-100 group-hover:opacity-100"
                      title={t('review.notes.addHint')}
                      aria-label={t('review.notes.add')}
                      onMouseDown={(e) => {
                        if (e.button !== 0) return;
                        e.preventDefault();
                        if (e.shiftKey && anchor.current !== null) {
                          notes.compose(noteRange(path, lines, anchor.current, i));
                          return;
                        }
                        anchor.current = i;
                        setDrag({ from: i, to: i });
                      }}
                      onKeyDown={(e) => {
                        if (e.key !== 'Enter' && e.key !== ' ') return;
                        e.preventDefault();
                        anchor.current = i;
                        notes.compose(noteRange(path, lines, i, i));
                      }}
                      data-review-note-add
                    >
                      <Plus className="size-3" strokeWidth={3} />
                    </button>
                  )}
                </td>
              )}
              <td className="w-10 select-none px-2 text-right align-top text-faint">{lineIsCode ? line.old : ''}</td>
              <td className="w-10 select-none px-2 text-right align-top text-faint">{lineIsCode ? line.new : ''}</td>
              <td className="w-4 select-none align-top text-subtle">{signs[line.kind]}</td>
              <td className="whitespace-pre pr-4 text-tertiary">
                {lineTokens
                  ? lineTokens.map((token, j) => (
                      <span key={j} style={{ color: token.color, fontStyle: token.fontStyle === 1 ? 'italic' : undefined }}>
                        {token.content}
                      </span>
                    ))
                  : line.text}
              </td>
            </tr>,
            ...(under.get(i) ?? []).map((n) => (
              <tr key={`note-${n.id}`} data-review-note-row>
                <td colSpan={columns} className="px-2 py-1.5">
                  <div className="sticky left-2 w-[min(42rem,calc(100vw-6rem))]">
                    <NoteCard note={n} />
                  </div>
                </td>
              </tr>
            )),
            composing && notes && composeAt === i && (
              <tr key="compose" data-review-note-row>
                <td colSpan={columns} className="px-2 py-1.5">
                  <div className="sticky left-2 w-[min(42rem,calc(100vw-6rem))]">
                    <NoteBox
                      title={
                        <>
                          <span className="font-mono text-secondary">{path}</span> · {t(whereLabel(composing).key, { start: composing.start, end: composing.end })}
                        </>
                      }
                      saveLabel={t('review.notes.save')}
                      onSave={(body) => notes.add(composing, body)}
                      onCancel={() => notes.compose(null)}
                    />
                  </div>
                </td>
              </tr>
            ),
          ];
        })}
      </tbody>
    </table>
  );
}

// useHighlighted highlights a diff's code lines, all together so a line is
// read in the context of the ones around it, again when the theme turns over.
export function useHighlighted(lines: DiffLine[] | null, lang: string | undefined): ThemedToken[][] | null {
  const mode = useMode();
  const code = lines
    ?.filter(isCode)
    .map((l) => l.text)
    .join('\n');
  const [out, setOut] = useState<{ code: string; mode: string; tokens: ThemedToken[][] } | null>(null);
  useEffect(() => {
    if (!lang || code === undefined) return;
    let current = true;
    highlight(code, lang)
      .then((tokens) => current && setOut({ code, mode, tokens }))
      .catch(() => {});
    return () => {
      current = false;
    };
  }, [code, lang, mode]);
  return out && out.code === code && out.mode === mode ? out.tokens : null;
}
