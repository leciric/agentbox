import type { QueryClient } from '@tanstack/react-query';
import { api } from './api';

// noticeSeen records in the daemon that the user was told error reports are
// on, or opened Settings, so the notice isn't shown again (see
// shouldShowNotice in errorReports.ts).
export function noticeSeen(queryClient: QueryClient): void {
  void api
    .updateSettings({ errorReportsNoticeSeen: true })
    .then((settings) => queryClient.setQueryData(['settings'], settings))
    .catch(() => {});
}
