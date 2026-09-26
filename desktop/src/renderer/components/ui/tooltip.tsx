import * as TooltipPrimitive from '@radix-ui/react-tooltip';
import type { ReactNode } from 'react';
import { cn } from '../../lib/utils';

export const TooltipProvider = TooltipPrimitive.Provider;

// Tip shows a label on hover or focus: a short one by default, or a wider
// card (className) like AgentInfoCard's. Radix doesn't render label until the
// tip opens, so a card that fetches its own data (a useQuery inside label)
// only asks for it once you're actually looking.
export function Tip({
  label,
  side = 'bottom',
  className,
  children,
}: {
  label: ReactNode;
  side?: 'top' | 'bottom' | 'left' | 'right';
  className?: string;
  children: ReactNode;
}) {
  return (
    <TooltipPrimitive.Root>
      <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
      <TooltipPrimitive.Portal>
        <TooltipPrimitive.Content
          side={side}
          sideOffset={6}
          className={cn('z-[60] max-w-xs animate-fade-in rounded-lg border border-line-strong bg-overlay px-2.5 py-1.5 text-xs text-secondary shadow-xl backdrop-blur', className)}
        >
          {label}
        </TooltipPrimitive.Content>
      </TooltipPrimitive.Portal>
    </TooltipPrimitive.Root>
  );
}
