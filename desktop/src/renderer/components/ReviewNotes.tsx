// Notes on an agent's diff in the review (Orca's diff notes are the model):
// the + beside a line of the Changes view (DiffTable) opens a box under it,
// dragging or Shift-clicking takes in more lines, and what's written is a
// draft the daemon keeps (internal/daemon/reviewnotes.go), so closing the
// review loses none. Drafts are listed in Request changes, edited or removed
// there or on their lines, and go to the agent all at once with what else
// is written, each with its file, lines and code. Once sent they stay on
// their lines in the agent's next review, pending until the code changes or
// the user resolves them.
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, CornerDownLeft, MessageSquarePlus, Pencil, RotateCcw, Trash2 } from 'lucide-react';
import { createContext, useContext, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT, type MessageKey } from '../lib/i18n';
import { whereLabel, type NoteRange } from '../lib/reviewNotes';
import { cn, errorMessage } from '../lib/utils';
import { Badge, type BadgeVariant } from './ui/badge';
import { Button } from './ui/button';

export type ReviewNotes = {
  notes: T.ReviewNote[];
  // composing is the lines a new note is being written on, and editing the
  // draft being rewritten: one box at a time.
  composing: NoteRange | null;
  compose: (range: NoteRange | null) => void;
  editing: number | null;
  setEditing: (id: number | null) => void;
  add: (range: NoteRange, body: string) => Promise<boolean>;
  edit: (id: number, body: string) => Promise<boolean>;
  remove: (id: number) => void;
  resolve: (id: number, resolved: boolean) => void;
};

// ReviewNotesContext is there in the review only: the pull request modal's
// diffs, outside it, have no notes.
export const ReviewNotesContext = createContext<ReviewNotes | null>(null);
export const useReviewNotesContext = () => useContext(ReviewNotesContext);

export const reviewNotesKey = (agent: string) => ['reviewNotes', agent];

// useReviewNotes is an agent's notes as the daemon has them, and what the
// review does with them.
export function useReviewNotes(agent: string): ReviewNotes {
  const t = useT();
  const queryClient = useQueryClient();
  const key = reviewNotesKey(agent);
  const list = useQuery({ queryKey: key, queryFn: () => api.reviewNotes(agent) });
  const [composing, compose] = useState<NoteRange | null>(null);
  const [editing, setEditing] = useState<number | null>(null);
  const put = (note: T.ReviewNote) => queryClient.setQueryData<T.ReviewNote[]>(key, (notes = []) => (notes.some((n) => n.id === note.id) ? notes.map((n) => (n.id === note.id ? note : n)) : [...notes, note]));
  const failed = (err: unknown) => {
    toast.error(t('review.notes.failed', { error: errorMessage(err) }));
    return false;
  };
  return {
    notes: list.data ?? [],
    composing,
    compose: (range) => {
      setEditing(null);
      compose(range);
    },
    editing,
    setEditing: (id) => {
      compose(null);
      setEditing(id);
    },
    add: (range, body) =>
      api.addReviewNote(agent, { ...range, body }).then((note) => {
        put(note);
        compose(null);
        return true;
      }, failed),
    edit: (id, body) =>
      api.editReviewNote(agent, id, { body }).then((note) => {
        put(note);
        setEditing(null);
        return true;
      }, failed),
    remove: (id) => {
      queryClient.setQueryData<T.ReviewNote[]>(key, (notes = []) => notes.filter((n) => n.id !== id));
      api.deleteReviewNote(agent, id).catch((err: unknown) => {
        failed(err);
        void queryClient.invalidateQueries({ queryKey: key });
      });
    },
    resolve: (id, resolved) => {
      api.editReviewNote(agent, id, { resolved }).then(put, failed);
    },
  };
}

const statusLabels: Record<string, MessageKey> = {
  draft: 'review.notes.status.draft',
  pending: 'review.notes.status.pending',
  resolved: 'review.notes.status.resolved',
};
const statusVariants: Record<string, BadgeVariant> = { draft: 'warning', pending: 'brand', resolved: 'success' };

// NoteBox is where a note is written, new or rewritten: Enter keeps it,
// Shift+Enter is a new line, and Esc (the review's) or Cancel drops it.
export function NoteBox({ title, initial = '', saveLabel, onSave, onCancel }: { title: ReactNode; initial?: string; saveLabel: string; onSave: (body: string) => Promise<boolean>; onCancel: () => void }) {
  const t = useT();
  const [body, setBody] = useState(initial);
  const [saving, setSaving] = useState(false);
  const save = () => {
    const text = body.trim();
    if (!text || saving) return;
    setSaving(true);
    void onSave(text).finally(() => setSaving(false));
  };
  return (
    <div className="grid max-w-2xl gap-2 rounded-lg border border-brand-400/40 bg-modal p-2.5 font-sans shadow-sm" data-review-note-box>
      <div className="text-[11.5px] text-muted">{title}</div>
      <textarea
        autoFocus
        rows={3}
        value={body}
        onChange={(e) => setBody(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
            e.preventDefault();
            save();
          }
        }}
        placeholder={t('review.notes.placeholder')}
        aria-label={t('review.notes.add')}
        className="w-full resize-y rounded-md bg-surface-faint px-2.5 py-2 text-[12.5px] whitespace-pre-wrap text-primary outline-none ring-1 ring-line focus:ring-brand-400"
        data-review-note-input
      />
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onCancel}>
          {t('common.cancel')}
        </Button>
        <Button size="sm" variant="primary" disabled={!body.trim() || saving} onClick={save} data-review-note-save>
          {saveLabel}
          <CornerDownLeft className="opacity-70" />
        </Button>
      </div>
    </div>
  );
}

// Where is a note's lines, and its file when it's listed away from them.
function Where({ note, withPath }: { note: Pick<T.ReviewNote, 'path' | 'side' | 'start' | 'end'>; withPath?: boolean }) {
  const t = useT();
  const where = whereLabel(note);
  return (
    <span className="min-w-0 truncate">
      {withPath && <span className="font-mono text-secondary">{note.path} · </span>}
      {t(where.key, { start: where.start, end: where.end })}
    </span>
  );
}

// NoteCard is one note: where, whether it was sent and what came of it,
// what it says, and what can be done with it. A draft is rewritten in place.
export function NoteCard({ note, withPath, quote }: { note: T.ReviewNote; withPath?: boolean; quote?: boolean }) {
  const t = useT();
  const notes = useReviewNotesContext();
  if (notes?.editing === note.id) {
    return <NoteBox title={<Where note={note} withPath={withPath} />} initial={note.body} saveLabel={t('review.notes.update')} onSave={(body) => notes.edit(note.id, body)} onCancel={() => notes.setEditing(null)} />;
  }
  const first = quote ? note.code.split('\n').find((l) => l.trim()) : undefined;
  return (
    <div className={cn('grid max-w-2xl gap-1 rounded-lg border bg-modal px-2.5 py-2 font-sans', note.status === 'resolved' ? 'border-line opacity-75' : 'border-line-strong')} data-review-note={note.id} data-status={note.status}>
      <div className="flex min-w-0 items-center gap-2 text-[11.5px] text-muted">
        <MessageSquarePlus className="size-3.5 shrink-0 text-brand-300" />
        <Where note={note} withPath={withPath} />
        <Badge variant={statusVariants[note.status] ?? 'default'} className="shrink-0">
          {t(statusLabels[note.status] ?? 'review.notes.status.pending')}
        </Badge>
        {notes && (
          <span className="ml-auto flex shrink-0 items-center gap-0.5">
            {note.status === 'draft' && (
              <Button size="icon-sm" variant="ghost" className="size-6" onClick={() => notes.setEditing(note.id)} title={t('review.notes.edit')} aria-label={t('review.notes.edit')} data-review-note-edit>
                <Pencil />
              </Button>
            )}
            {note.status === 'pending' && (
              <Button size="sm" variant="ghost" className="h-6 px-1.5 text-[11.5px]" onClick={() => notes.resolve(note.id, true)} data-review-note-resolve>
                <Check />
                {t('review.notes.resolve')}
              </Button>
            )}
            {note.status === 'resolved' && note.resolvedAt && (
              <Button size="sm" variant="ghost" className="h-6 px-1.5 text-[11.5px]" onClick={() => notes.resolve(note.id, false)} data-review-note-reopen>
                <RotateCcw />
                {t('review.notes.reopen')}
              </Button>
            )}
            <Button size="icon-sm" variant="ghost" className="size-6" onClick={() => notes.remove(note.id)} title={t('review.notes.remove')} aria-label={t('review.notes.remove')} data-review-note-remove>
              <Trash2 />
            </Button>
          </span>
        )}
      </div>
      {first && <code className="block truncate font-mono text-[11px] text-subtle">{first}</code>}
      <p className="whitespace-pre-wrap break-words text-[12.5px] text-primary">{note.body}</p>
    </div>
  );
}

// useCounts is how many of notes sent earlier are pending and resolved,
// leaving out a none.
export function useCounts(notes: T.ReviewNote[]): string {
  const t = useT();
  const pending = notes.filter((n) => n.status === 'pending').length;
  const resolved = notes.filter((n) => n.status === 'resolved').length;
  return [pending > 0 && t('review.notes.pendingCount', { count: pending }), resolved > 0 && t('review.notes.resolvedCount', { count: resolved })].filter(Boolean).join(', ');
}

// NotesList is the notes in Request changes: the drafts that go with it, and
// those sent before, folded.
export function NotesList({ notes }: { notes: T.ReviewNote[] }) {
  const t = useT();
  const draft = notes.filter((n) => n.status === 'draft');
  const earlier = notes.filter((n) => n.status !== 'draft');
  const counts = useCounts(earlier);
  if (notes.length === 0) return null;
  return (
    <div className="grid max-h-[38vh] gap-2 overflow-y-auto" data-review-notes-list>
      {draft.length > 0 && (
        <>
          <div className="text-[11px] font-semibold uppercase tracking-wide text-subtle">{t('review.notes.toSend', { count: draft.length })}</div>
          {draft.map((n) => (
            <NoteCard key={n.id} note={n} withPath quote />
          ))}
        </>
      )}
      {earlier.length > 0 && (
        <details className="group" data-review-notes-earlier>
          <summary className="cursor-pointer text-[11px] font-semibold uppercase tracking-wide text-subtle">
            {t('review.notes.earlier', { list: counts })}
          </summary>
          <div className="mt-2 grid gap-2">
            {earlier.map((n) => (
              <NoteCard key={n.id} note={n} withPath quote />
            ))}
          </div>
        </details>
      )}
    </div>
  );
}
