import type * as T from '../../shared/api';

// What the Edit folder dialog starts from: the values it opened with. The dialog
// compares against these, not against the section as it is now, so a field the
// user never touched isn't sent back — a refresh while the dialog was open
// (the section folded, or recoloured from another window) must not be undone.
export type SectionDraft = { name: string; description: string; color: string };

// sectionChanges is the request that carries only the fields the user changed
// from what the dialog opened with; {} when nothing changed.
export function sectionChanges(opened: SectionDraft, draft: SectionDraft): T.UpdateSectionRequest {
  const req: T.UpdateSectionRequest = {};
  const name = draft.name.trim();
  if (name && name !== opened.name) req.name = name;
  const description = draft.description.trim();
  if (description !== opened.description) req.description = description;
  if (draft.color !== opened.color) req.color = draft.color;
  return req;
}

// nextRadio is the option a radio group's arrow keys land on, or undefined for
// a key that isn't one of them: Left/Up go back and Right/Down forward, both
// wrapping, Home and End to the ends.
export function nextRadio(key: string, current: number, count: number): number | undefined {
  if (count <= 0) return undefined;
  switch (key) {
    case 'ArrowRight':
    case 'ArrowDown':
      return (current + 1) % count;
    case 'ArrowLeft':
    case 'ArrowUp':
      return (current - 1 + count) % count;
    case 'Home':
      return 0;
    case 'End':
      return count - 1;
    default:
      return undefined;
  }
}
