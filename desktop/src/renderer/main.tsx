import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
import '@xterm/xterm/css/xterm.css';
import './styles.css';
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { StrictMode, useEffect } from 'react';
import { createRoot } from 'react-dom/client';
import { Toaster } from 'sonner';
import { App } from './App';
import { TooltipProvider } from './components/ui/tooltip';
import { api } from './lib/api';
import { connectEvents } from './lib/events';
import { applyLanguage, restoreLanguage, useLanguage } from './lib/i18n';
import { restoreMode, useMode } from './lib/theme';

// The window opens in the mode it was last in, before the first paint; the
// daemon's answer confirms or corrects it a moment later (lib/theme.ts).
restoreMode();
restoreLanguage();

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 5_000 } },
});
connectEvents(queryClient);

// Sonner draws outside the app's own markup, so it is handed the colours
// rather than asking for them with a class.
function Toasts() {
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

// Root speaks the daemon's Settings.language, and draws the app again the
// moment it changes: every component under it re-renders in the new language,
// keeping its state, with no reload (lib/i18n.tsx).
function Root() {
  useLanguage();
  const language = useQuery({ queryKey: ['settings'], queryFn: api.settings }).data?.language;
  useEffect(() => {
    if (language) applyLanguage(language);
  }, [language]);
  return (
    <TooltipProvider delayDuration={250}>
      <App />
      <Toasts />
    </TooltipProvider>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <Root />
    </QueryClientProvider>
  </StrictMode>,
);
