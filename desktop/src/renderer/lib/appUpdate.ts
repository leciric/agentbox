// "Update available" updates the app in place where this install can
// (main/appupdate.ts): the label shows how far it got, a failure says why and
// offers the release page, and an install that can't update itself opens the
// release page as it always did, saying why in its tooltip. The hook that
// runs it is useAppUpdate.ts.
import type { AppUpdateProgress, AppUpdateResult, AppUpdateSupport } from '../../shared/appupdate.ts';
import { t, type MessageKey } from '../../shared/i18n/index.ts';

// updateLabel is what the update's item says while it runs: null before it starts.
export function updateLabel(progress: AppUpdateProgress | 'preparing' | null): string | null {
  if (progress === null) return null;
  if (progress === 'preparing') return t('shell.update.preparing');
  switch (progress.phase) {
    case 'download':
      return t('shell.update.downloading', { percent: progress.total ? Math.floor((progress.received / progress.total) * 100) : 0 });
    case 'verify':
      return t('shell.update.verifying');
    case 'install':
      return t('shell.update.installing');
    case 'restart':
      return t('shell.update.restarting');
  }
}

// updateHint is the update item's tooltip: what clicking it does.
export function updateHint(support: AppUpdateSupport | undefined, version: string): string {
  if (!support) return '';
  if (support.inPlace) return t('shell.update.inPlace', { version });
  return t(`shell.update.releasePage.${support.reason}` as MessageKey);
}

// failureMessage says why an update stopped.
export function failureMessage(result: Extract<AppUpdateResult, { ok: false }>): string {
  const count = Number(result.detail) || 0;
  return t(`shell.update.error.${result.code}` as MessageKey, { detail: result.detail, version: result.detail, count });
}
