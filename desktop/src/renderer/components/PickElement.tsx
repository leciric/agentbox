import { useMutation } from '@tanstack/react-query';
import { SquareDashedMousePointer } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { pickLabel, sendPick } from '../lib/picks';
import { cn, errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Tip } from './ui/tooltip';

// usePicker runs the element picker on the page in agent's browser, for the
// composer of chat: the agent's own, or the lead's when its chat shows the
// desktop. While it runs the view takes the pointer, as with Take control,
// since the overlay is on the page and answers the pointer there.
export function usePicker(agent: T.Agent, chat: string) {
  const t = useT();
  const [picking, setPicking] = useState(false);
  const live = useRef(false);
  const pick = useMutation({
    mutationFn: () => api.pickElement(agent.ref, t('pick.hint')),
    onMutate: () => {
      live.current = true;
      setPicking(true);
    },
    onSettled: () => {
      live.current = false;
      setPicking(false);
    },
    onSuccess: ({ element }) => {
      if (!element) return;
      sendPick(chat, agent.ref, element);
      toast(t('pick.added', { name: pickLabel(element) }));
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  // A view closed while picking takes the overlay off the page.
  useEffect(
    () => () => {
      if (live.current) void api.cancelPick(agent.ref).catch(() => {});
    },
    [agent.ref],
  );
  const toggle = () => (picking ? void api.cancelPick(agent.ref).catch((err) => toast.error(errorMessage(err))) : pick.mutate());
  return { picking, toggle };
}

// PickButton turns the picker on and off. compact is the panel's icon button.
export function PickButton({ picking, disabled, compact, onToggle }: { picking: boolean; disabled: boolean; compact?: boolean; onToggle: () => void }) {
  const t = useT();
  const label = picking ? t('pick.cancel') : t('pick.start');
  const lit = picking && 'border-brand-400/50 bg-brand-500/20 text-brand-100 hover:bg-brand-500/30';
  return (
    <Tip label={picking ? t('pick.cancelTip') : t('pick.tip')}>
      {compact ? (
        <Button size="icon-sm" variant="ghost" className={cn(lit)} aria-label={label} aria-pressed={picking} disabled={disabled && !picking} onClick={onToggle} data-pick-element={picking ? 'on' : 'off'}>
          <SquareDashedMousePointer className={cn(picking && 'animate-pulse')} />
        </Button>
      ) : (
        <Button size="sm" className={cn(lit)} aria-pressed={picking} disabled={disabled && !picking} onClick={onToggle} data-pick-element={picking ? 'on' : 'off'}>
          <SquareDashedMousePointer className={cn(picking && 'animate-pulse')} />
          {label}
        </Button>
      )}
    </Tip>
  );
}
