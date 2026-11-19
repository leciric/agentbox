import { Slot } from '@radix-ui/react-slot';
import { cva, type VariantProps } from 'class-variance-authority';
import type { ButtonHTMLAttributes } from 'react';
import { cn } from '../../lib/utils';

const buttonVariants = cva(
  'inline-flex select-none items-center justify-center gap-2 whitespace-nowrap rounded-lg font-medium transition-colors duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400 disabled:pointer-events-none disabled:opacity-45 [&_svg]:size-4 [&_svg]:shrink-0',
  {
    variants: {
      variant: {
        // Flat fills: a button says what it is by its colour, and hovering or
        // pressing it moves that colour a step, rather than lighting it up.
        primary: 'bg-brand-500 text-on-brand hover:bg-brand-600 active:bg-brand-700',
        default: 'bg-inverted text-on-inverted hover:bg-inverted-hover active:bg-inverted',
        secondary: 'border border-line bg-surface-raised text-primary hover:bg-surface-strong active:bg-surface-vivid',
        outline: 'border border-line-strong text-secondary hover:border-line-heavy hover:bg-surface active:bg-surface-strong',
        ghost: 'text-muted hover:bg-surface-raised hover:text-primary active:bg-surface-strong',
        destructive: 'bg-rose-600 text-white hover:bg-rose-700 active:bg-rose-800',
        danger: 'text-rose-300 hover:bg-rose-500/10 hover:text-rose-200 active:bg-rose-500/20',
      },
      size: {
        default: 'h-9 px-4 text-body',
        sm: 'h-8 px-3 text-ui',
        lg: 'h-11 px-5 text-heading',
        icon: 'size-9',
        'icon-sm': 'size-8 [&_svg]:size-[15px]',
      },
    },
    defaultVariants: { variant: 'secondary', size: 'default' },
  },
);

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

export function Button({ className, variant, size, asChild, type = 'button', ...props }: ButtonProps) {
  const Comp = asChild ? Slot : 'button';
  return <Comp type={type} className={cn(buttonVariants({ variant, size }), className)} {...props} />;
}
