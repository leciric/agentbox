import { X } from 'lucide-react';
import { useT } from '../lib/i18n';

// LabelRemoveButton is the small X on a label chip. It fades in when the chip
// (a `group`) is hovered or anything in it has focus, so it stays a tab stop
// that appears as you reach it. Its click stays on it, never reaching the
// row's, which opens the pull request.
export function LabelRemoveButton({ name, onRemove }: { name: string; onRemove: () => void }) {
  const t = useT();
  return (
    <button
      type="button"
      aria-label={t('memory.pulls.labels.remove', { name })}
      onClick={(e) => {
        e.stopPropagation();
        onRemove();
      }}
      className="-mr-1 ml-0.5 flex size-3.5 shrink-0 items-center justify-center rounded-full opacity-0 hover:opacity-100 focus-visible:opacity-100 group-focus-within:opacity-70 group-hover:opacity-70"
      data-label-remove={name}
    >
      <X className="size-3" />
    </button>
  );
}
