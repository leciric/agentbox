// ErrorReports watches for uncaught errors, the window's and the main
// process's, and does what lib/errorReports.ts decides: sends a report of
// each while the user has them on (they are, until turned off).
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';
import { api } from '../lib/api';
import { ErrorReporter, errorReport, fromEvent, type UncaughtError } from '../lib/errorReports';

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

  useEffect(() => {
    const handle = async (err: UncaughtError) => {
      const settings = await queryClient.fetchQuery({ queryKey: ['settings'], queryFn: api.settings }).catch(() => undefined);
      switch (reporter.current.decide(err, settings)) {
        case 'send':
          await send(err).catch(() => {});
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

  return null;
}
