// The app on a phone paired with this machine's daemon (web/PhoneApp.tsx):
// main.tsx's providers around the chats instead of the whole app.
import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
import './styles.css';
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { StrictMode, useEffect } from 'react';
import { createRoot } from 'react-dom/client';
import { Toaster } from 'sonner';
import type * as T from '../shared/api';
import { TooltipProvider } from './components/ui/tooltip';
import { api } from './lib/api';
import { connectEvents } from './lib/events';
import { applyLanguage, useLanguage } from './lib/i18n';
import { useHostTheme, useMode } from './lib/theme';
import { PhoneApp } from './web/PhoneApp';

function Toasts() {
  return (
    <Toaster
      theme={useMode()}
      position="top-center"
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

function Themed({ phone }: { phone: T.LANPhone }) {
  // The theme is the computer's: the same colours on both.
  useHostTheme();
  // And so is the language: the daemon's Settings.language, applied the way
  // main.tsx's Root does, redrawing the chats the moment it changes.
  useLanguage();
  const language = useQuery({ queryKey: ['settings'], queryFn: api.settings }).data?.language;
  useEffect(() => {
    if (language) applyLanguage(language);
  }, [language]);
  return <PhoneApp phone={phone} />;
}

export function startPhone(phone: T.LANPhone): void {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: true, staleTime: 5_000 } },
  });
  connectEvents(queryClient);
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <TooltipProvider delayDuration={250}>
          <Themed phone={phone} />
          <Toasts />
        </TooltipProvider>
      </QueryClientProvider>
    </StrictMode>,
  );
}
