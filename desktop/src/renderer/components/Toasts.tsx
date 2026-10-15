import { Toaster } from 'sonner';
import { useMode } from '../lib/theme';

// Sonner draws outside the app's own markup, so it is handed the colours
// rather than asking for them with a class.
export function Toasts() {
  return (
    <Toaster
      theme={useMode()}
      position="bottom-right"
      toastOptions={{
        style: {
          background: 'var(--ab-overlay)',
          border: '1px solid var(--ab-line-strong)',
          color: 'var(--ab-primary)',
          borderRadius: '14px',
          fontFamily: 'var(--font-sans)',
        },
      }}
    />
  );
}
