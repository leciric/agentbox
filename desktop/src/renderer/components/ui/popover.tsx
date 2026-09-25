import * as PopoverPrimitive from '@radix-ui/react-popover';
import type { ComponentProps } from 'react';
import { cn } from '../../lib/utils';

export const Popover = PopoverPrimitive.Root;
export const PopoverTrigger = PopoverPrimitive.Trigger;

export function PopoverContent({ className, align = 'end', ...props }: ComponentProps<typeof PopoverPrimitive.Content>) {
  return (
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Content
        align={align}
        sideOffset={8}
        className={cn(
          'z-50 animate-fade-in rounded-xl border border-line-strong bg-overlay p-3 shadow-[0_24px_60px_-20px_var(--ab-shadow-deep)] backdrop-blur',
          className,
        )}
        {...props}
      />
    </PopoverPrimitive.Portal>
  );
}
