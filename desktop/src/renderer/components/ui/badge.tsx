import { cva, type VariantProps } from 'class-variance-authority';
import type { HTMLAttributes } from 'react';
import { cn } from '../../lib/utils';

const badgeVariants = cva(
  'inline-flex items-center gap-1.5 whitespace-nowrap rounded-full px-2 py-0.5 text-[11px] font-medium ring-1 ring-inset [&_svg]:size-3',
  {
    variants: {
      variant: {
        default: 'bg-surface text-tertiary ring-line-strong',
        success: 'bg-emerald-400/10 text-emerald-300 ring-emerald-400/25',
        warning: 'bg-amber-400/10 text-amber-200 ring-amber-400/25',
        danger: 'bg-rose-500/10 text-rose-300 ring-rose-400/25',
        info: 'bg-sky-400/10 text-sky-300 ring-sky-400/25',
        brand: 'bg-brand-500/15 text-brand-300 ring-brand-400/30',
      },
    },
    defaultVariants: { variant: 'default' },
  },
);

export type BadgeVariant = NonNullable<VariantProps<typeof badgeVariants>['variant']>;

export function Badge({ className, variant, ...props }: HTMLAttributes<HTMLSpanElement> & VariantProps<typeof badgeVariants>) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />;
}
