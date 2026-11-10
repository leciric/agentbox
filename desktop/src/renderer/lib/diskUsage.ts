// Names for the daemon's disk breakdown (GET /v1/usage/disk) in the app's
// language. The daemon's labels are English, for the CLI; a category and an
// item that isn't an agent or a project carry a kind, which picks the key.
import type * as T from '../../shared/api';
import { t } from '../../shared/i18n/index.ts';

export function diskCategoryLabel(cat: T.DiskUsageCategory): string {
  switch (cat.kind) {
    case 'machines':
      return t('storage.kind.machines');
    case 'bases':
      return t('storage.kind.bases');
    case 'worktrees':
      return t('storage.kind.worktrees');
    case 'media':
      return t('storage.kind.media');
    case 'caches':
      return t('storage.kind.caches');
    case 'state':
      return t('storage.kind.state');
    default:
      return cat.label;
  }
}

export function diskItemLabel(item: T.DiskUsageItem): string {
  const name = item.name ?? '';
  switch (item.kind) {
    case 'base-image':
      return t('storage.item.baseImage');
    case 'project-base':
      return t('storage.item.projectBase', { name });
    case 'previous-base':
      return t('storage.item.previousBase', { name });
    case 'package-cache':
      return t('storage.item.packageCache', { name: name === 'other' ? t('storage.item.other') : name });
    case 'docker-images':
      return t('storage.item.dockerImages');
    case 'state-db':
    case 'daemon-log':
      return item.label;
    default:
      return item.label;
  }
}
