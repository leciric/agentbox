import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
import '@xterm/xterm/css/xterm.css';
import './styles.css';
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { StrictMode, useEffect } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { Toasts } from './components/Toasts';
import { TooltipProvider } from './components/ui/tooltip';
import { api } from './lib/api';
import { userTimezone } from './lib/schedules';
import { connectEvents } from './lib/events';
import { applyLanguage, restoreLanguage, useLanguage } from './lib/i18n';
import { restoreMode } from './lib/theme';

// The window opens in the mode it was last in, before the first paint; the
// daemon's answer confirms or corrects it a moment later (lib/theme.ts).
restoreMode();
restoreLanguage();

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 5_000 } },
});
connectEvents(queryClient);

// Root speaks the daemon's Settings.language, and draws the app again the
// moment it changes: every component under it re-renders in the new language,
// keeping its state, with no reload (lib/i18n.tsx).
function Root() {
  useLanguage();
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings }).data;
  const language = settings?.language;
  useEffect(() => {
    if (language) applyLanguage(language);
  }, [language]);
  // The daemon runs in a VM on UTC: scheduled tasks the lead or the command
  // line set are read in the timezone this app runs in (lib/schedules.ts).
  const timezone = settings?.timezone;
  useEffect(() => {
    if (timezone === undefined) return;
    const here = userTimezone();
    if (timezone !== here) void api.updateSettings({ timezone: here }).catch(() => {});
  }, [timezone]);
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
