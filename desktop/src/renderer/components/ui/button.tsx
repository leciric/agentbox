import { Slot } from '@radix-ui/react-slot';
import { cva, type VariantProps } from 'class-variance-authority';
import type { ButtonHTMLAttributes } from 'react';
import { cn } from '../../lib/utils';

const buttonVariants = cva(
  'inline-flex select-none items-center justify-center gap-2 whitespace-nowrap rounded-lg font-medium transition-all duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/60 disabled:pointer-events-none disabled:opacity-45 active:scale-[0.98] [&_svg]:size-4 [&_svg]:shrink-0',
  {
    variants: {
      variant: {
        primary:
          'bg-gradient-to-b from-brand-500 to-indigo-600 text-white shadow-[inset_0_1px_0_rgb(255_255_255/0.18),0_10px_24px_-10px_rgb(99_102_241/0.7)] hover:brightness-110',
        default: 'bg-inverted text-on-inverted hover:bg-inverted-hover',
        secondary: 'border border-line bg-surface-raised text-primary hover:bg-surface-strong',
        outline: 'border border-line-strong text-secondary hover:border-line-heavy hover:bg-surface',
        ghost: 'text-muted hover:bg-surface-raised hover:text-primary',
        destructive: 'bg-rose-600 text-white shadow-[0_10px_24px_-12px_rgb(225_29_72/0.8)] hover:bg-rose-500',
        danger: 'text-rose-300 hover:bg-rose-500/10 hover:text-rose-200',
      },
      size: {
        default: 'h-9 px-4 text-sm',
        sm: 'h-8 px-3 text-[13px]',
        lg: 'h-11 px-5 text-[15px]',
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
