import { cva, type VariantProps } from 'class-variance-authority';
import type { HTMLAttributes } from 'react';
import { cn } from '../../lib/utils';

const badgeVariants = cva(
  'inline-flex items-center gap-1.5 whitespace-nowrap rounded-full px-2 py-0.5 text-caption font-medium [&_svg]:size-3',
  {
    variants: {
      variant: {
        default: 'bg-surface-raised text-tertiary',
        success: 'bg-emerald-400/10 text-emerald-300',
        warning: 'bg-amber-400/10 text-amber-200',
        danger: 'bg-rose-500/10 text-rose-300',
        info: 'bg-sky-400/10 text-sky-300',
        brand: 'bg-brand-500/15 text-brand-300',
      },
    },
    defaultVariants: { variant: 'default' },
  },
);

export type BadgeVariant = NonNullable<VariantProps<typeof badgeVariants>['variant']>;

export function Badge({ className, variant, ...props }: HTMLAttributes<HTMLSpanElement> & VariantProps<typeof badgeVariants>) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />;
}
