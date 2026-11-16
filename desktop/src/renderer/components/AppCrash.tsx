// What the window shows when the app itself fails to render (main.tsx's
// ErrorBoundary), instead of a black window with no way out: what happened,
// Try again (renders the app again, keeping the daemon's state), Reload, and
// the error to copy into a report.
import { AlertTriangle, Copy, RotateCcw } from 'lucide-react';
import { useState } from 'react';
import { useT } from '../lib/i18n';
import { Button } from './ui/button';

export function AppCrash({ error, retry }: { error: unknown; retry: () => void }) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  // A stack already starts with the message; an error without one shows just that.
  const details = error instanceof Error ? (error.stack?.includes(error.message) ? error.stack : `${error.message}\n${error.stack ?? ''}`.trim()) : String(error);
  return (
    <main role="alert" className="flex h-screen items-center justify-center p-6 text-primary" data-app-crash>
      <div className="grid w-full max-w-lg gap-4">
        <div className="flex size-10 items-center justify-center rounded-xl bg-amber-400/10 text-amber-400">
          <AlertTriangle className="size-5" aria-hidden />
        </div>
        <div className="grid gap-1.5">
          <h1 className="text-lg font-semibold">{t('crash.title')}</h1>
          <p className="text-sm text-muted">{t('crash.body')}</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="primary" onClick={retry} autoFocus>
            <RotateCcw />
            {t('crash.retry')}
          </Button>
          <Button onClick={() => location.reload()}>{t('crash.reload')}</Button>
          <Button
            variant="ghost"
            onClick={() => {
              void navigator.clipboard.writeText(details).then(() => setCopied(true));
            }}
          >
            <Copy />
            {copied ? t('common.copied') : t('crash.copy')}
          </Button>
        </div>
        <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-line bg-surface p-3 font-mono text-xs text-subtle">
          {details}
        </pre>
      </div>
    </main>
  );
}
