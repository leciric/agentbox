// ErrorReports watches for uncaught errors, the window's and the main
// process's, and does what lib/errorReports.ts decides: sends a report of
// each while the user has them on, and the first time one happens while
// they've never chosen, asks.
import { useQueryClient } from '@tanstack/react-query';
import { ChevronRight, LoaderCircle } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { ErrorReporter, errorReport, fromEvent, type UncaughtError } from '../lib/errorReports';
import { cn, errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';

// send sends err's report, with the app's own description beside it. A
// failure is dropped: there's nobody to tell about an error report that
// didn't go, and saying so in the console would only be another error.
async function send(err: UncaughtError): Promise<void> {
  const app = (await window.agentbox.report.sections().catch(() => [])).find((s) => s.id === 'app');
  await api.sendReport(errorReport(err, app));
}

export function ErrorReports() {
  const queryClient = useQueryClient();
  const reporter = useRef(new ErrorReporter());
  const [asking, setAsking] = useState<UncaughtError | null>(null);

  useEffect(() => {
    const handle = async (err: UncaughtError) => {
      const settings = await queryClient.fetchQuery({ queryKey: ['settings'], queryFn: api.settings }).catch(() => undefined);
      switch (reporter.current.decide(err, settings)) {
        case 'send':
          await send(err).catch(() => {});
          break;
        case 'ask':
          setAsking(err);
          break;
      }
    };
    const onWindow = (event: ErrorEvent | PromiseRejectionEvent) => {
      const err = fromEvent(event);
      if (!err) return;
      window.agentbox.report.windowError({ name: err.name, message: err.message, stack: err.stack });
      void handle(err);
    };
    window.addEventListener('error', onWindow);
    window.addEventListener('unhandledrejection', onWindow);
    const off = window.agentbox.report.onAppError((e) => void handle({ where: e.where, name: e.name, message: e.message, stack: e.stack }));
    return () => {
      window.removeEventListener('error', onWindow);
      window.removeEventListener('unhandledrejection', onWindow);
      off();
    };
  }, [queryClient]);

  return <ErrorReportOffer error={asking} onClose={() => setAsking(null)} />;
}

// ErrorReportOffer asks, once, whether to send error reports from now on,
// with the report this error would send to look at.
export function ErrorReportOffer({ error, onClose }: { error: UncaughtError | null; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [shown, setShown] = useState(false);
  const [pending, setPending] = useState<'yes' | 'no' | null>(null);
  const [failed, setFailed] = useState<string | null>(null);

  const choose = async (on: boolean) => {
    if (!error) return;
    setPending(on ? 'yes' : 'no');
    setFailed(null);
    try {
      const settings = await api.updateSettings({ errorReports: on });
      queryClient.setQueryData<T.Settings>(['settings'], settings);
      if (on) await send(error);
      onClose();
    } catch (err) {
      setFailed(errorMessage(err));
    } finally {
      setPending(null);
    }
  };

  const preview = error ? errorReport(error).sections[0].content : '';
  return (
    <Dialog open={error !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent data-error-report-offer>
        <DialogHeader>
          <DialogTitle>AgentBox ran into an error</DialogTitle>
          <DialogDescription>
            Send reports of errors like this to AgentBox’s developers automatically? Each has the error, where in the app it happened,
            and the app’s version and OS, with tokens, emails and home folders taken out. No logs, and nothing you typed. You can change
            this in Settings, General.
          </DialogDescription>
        </DialogHeader>
        <div className="min-w-0 rounded-lg border border-line">
          <button
            type="button"
            aria-expanded={shown}
            onClick={() => setShown((s) => !s)}
            className="flex w-full min-w-0 items-center gap-2 px-3 py-2 text-left font-mono text-[12px] text-primary"
          >
            <ChevronRight className={cn('size-4 shrink-0 text-subtle transition-transform', shown && 'rotate-90')} />
            <span className="min-w-0 truncate">
              {error?.name}: {error?.message}
            </span>
          </button>
          {shown && (
            <pre className="max-h-56 min-w-0 overflow-auto whitespace-pre-wrap break-all border-t border-line bg-sunken px-3 py-2 font-mono text-[11px] leading-relaxed text-secondary">
              {preview}
            </pre>
          )}
        </div>
        {failed && <Notice>{failed}</Notice>}
        <DialogFooter>
          <Button variant="ghost" disabled={pending !== null} onClick={() => void choose(false)}>
            {pending === 'no' && <LoaderCircle className="animate-spin" />}
            Don’t send
          </Button>
          <Button variant="primary" disabled={pending !== null} onClick={() => void choose(true)}>
            {pending === 'yes' && <LoaderCircle className="animate-spin" />}
            Send this one and future ones
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
