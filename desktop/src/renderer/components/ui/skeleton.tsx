import type { HTMLAttributes } from 'react';
import { cn } from '../../lib/utils';

// Skeleton stands in for something still loading: a shimmering block the size
// of what will be there. A list shows a few of these until its first answer
// arrives, rather than an empty list that says there's nothing in it.
export function Skeleton({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div aria-hidden data-skeleton className={cn('skeleton rounded-md', className)} {...props} />;
}

// The widths of a column of placeholder rows, so it reads as a list of names
// rather than a stack of identical bars.
export const skeletonWidths = ['w-3/4', 'w-1/2', 'w-2/3', 'w-2/5'];
