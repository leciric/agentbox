import * as TooltipPrimitive from '@radix-ui/react-tooltip';
import type { ReactNode } from 'react';

export const TooltipProvider = TooltipPrimitive.Provider;

// Tip shows a short label on hover or focus.
export function Tip({ label, side = 'bottom', children }: { label: ReactNode; side?: 'top' | 'bottom' | 'left' | 'right'; children: ReactNode }) {
  return (
    <TooltipPrimitive.Root>
      <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
      <TooltipPrimitive.Portal>
        <TooltipPrimitive.Content
          side={side}
          sideOffset={6}
          className="z-[60] max-w-xs animate-fade-in rounded-lg border border-line-strong bg-overlay px-2.5 py-1.5 text-xs text-secondary shadow-xl backdrop-blur"
        >
          {label}
        </TooltipPrimitive.Content>
      </TooltipPrimitive.Portal>
    </TooltipPrimitive.Root>
  );
}
