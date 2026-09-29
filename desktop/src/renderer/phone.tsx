// The app on a phone paired with this machine's daemon (web/PhoneApp.tsx):
// main.tsx's providers around the chats instead of the whole app.
import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
import './styles.css';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { Toaster } from 'sonner';
import type * as T from '../shared/api';
import { TooltipProvider } from './components/ui/tooltip';
import { connectEvents } from './lib/events';
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
