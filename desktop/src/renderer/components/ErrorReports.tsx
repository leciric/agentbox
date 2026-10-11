// ErrorReports watches for uncaught errors, the window's and the main
// process's, and does what lib/errorReports.ts decides: sends a report of
// each while the user has them on (they are, until turned off), and tells
// them once that they are, with a link to the setting.
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';
import { toast } from 'sonner';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { ErrorReporter, errorReport, fromEvent, type UncaughtError } from '../lib/errorReports';
import { openSettingsSection } from './SettingsPage';

// noticeKey remembers that the user was told error reports are on.
const noticeKey = 'agentbox:error-reports-notice';

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
  const t = useT();

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

  // The notice: once, and only while they're on. Whoever turned them off has
  // no need of it, and whoever dismisses it isn't shown it again.
  useEffect(() => {
    if (localStorage.getItem(noticeKey)) return;
    let id: string | number | undefined;
    let cancelled = false;
    void queryClient
      .fetchQuery({ queryKey: ['settings'], queryFn: api.settings })
      .then((settings) => {
        if (cancelled || !settings.errorReports) return;
        const seen = () => localStorage.setItem(noticeKey, '1');
        id = toast(t('defaults.errorReports.title'), {
          description: t('defaults.errorReports.description'),
          duration: Infinity,
          closeButton: true,
          action: {
            label: t('defaults.errorReports.settings'),
            onClick: () => openSettingsSection('general'),
          },
          onDismiss: seen,
          onAutoClose: seen,
        });
      })
      .catch(() => {});
    return () => {
      cancelled = true;
      if (id !== undefined) toast.dismiss(id);
    };
  }, [queryClient, t]);

  return null;
}
