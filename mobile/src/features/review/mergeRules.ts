// What stands in the way of merging a pull request from the phone, and which
// method the Merge button offers: the one last used on this phone when the
// repository allows it, else the first it allows. As the desktop's review
// decides (review.mergeBlocked.*, #329's remembered method).
import type * as T from '../../shared/api.ts';

export type MergeBlock = 'draft' | 'failing' | 'conflict' | 'noPermission';

export function mergeBlock(pr: T.PullRequest, pulls: T.ProjectPullRequests | undefined): MergeBlock | undefined {
  if (pr.draft) return 'draft';
  if (pr.conflict) return 'conflict';
  if (pr.checks === 'failing') return 'failing';
  if (pulls?.canMergeKnown && !pulls.canMerge) return 'noPermission';
  return undefined;
}

export function pickMethod(allowed: string[], remembered: string | null | undefined): string {
  return remembered && allowed.includes(remembered) ? remembered : (allowed[0] ?? 'squash');
}
