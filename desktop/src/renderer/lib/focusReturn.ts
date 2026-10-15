// useFocusReturn puts the focus back where it was when a dialog closes.
// Radix gives it back to the dialog's trigger, but a dialog opened from code
// (the review from a sidebar row or a shortcut, New task, the search) has
// none, and the focus fell to the top of the page. Its two handlers go on a
// Radix Dialog.Content: when it opens, the focus hasn't moved yet, so the
// element that has it is the one to return to.
import { useRef } from 'react';

type Handler = (event: Event) => void;

export function useFocusReturn(own: { onOpenAutoFocus?: Handler; onCloseAutoFocus?: Handler } = {}) {
  const before = useRef<HTMLElement | null>(null);
  return {
    onOpenAutoFocus: (event: Event) => {
      const active = document.activeElement;
      before.current = active instanceof HTMLElement && active !== document.body ? active : null;
      own.onOpenAutoFocus?.(event);
    },
    onCloseAutoFocus: (event: Event) => {
      own.onCloseAutoFocus?.(event);
      if (event.defaultPrevented) return;
      const el = before.current;
      before.current = null;
      if (!el?.isConnected) return;
      // What the dialog did as it closed may have put the focus somewhere
      // on purpose, a chat's composer: that stays.
      const now = document.activeElement;
      const dialog = event.target instanceof Node ? event.target : null;
      if (now && now !== document.body && !dialog?.contains(now)) return;
      event.preventDefault();
      el.focus({ preventScroll: true });
    },
  };
}
