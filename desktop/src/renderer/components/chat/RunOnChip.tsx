import * as MenuPrimitive from '@radix-ui/react-dropdown-menu';
import { Check, ChevronDown, Laptop, Server } from 'lucide-react';
import type * as T from '../../../shared/api';
import { useT } from '../../lib/i18n';
import type { RunOn } from '../../lib/runOn';
import { cn } from '../../lib/utils';
import { Menu, MenuContent, MenuTrigger } from '../ui/menu';
import { Tip } from '../ui/tooltip';

// RunOnChip sits beside the To: chip in the chat of a project another
// AgentBox shares with this one, while the message goes to its lead: where
// the agents the lead makes for you run. On the owner's PC, on their AI
// accounts and within the slots they gave you; or on yours, cloned with your
// GitHub access and on your AI accounts (internal/daemon/sharemachines.go).
// "On my PC" is greyed out with the reason when this computer can't (info.why).
export function RunOnChip({ owner, info, chosen, onChoose }: { owner: string; info: T.ShareRunOn | undefined; chosen: RunOn; onChoose: (runOn: RunOn) => void }) {
  const t = useT();
  const here = !!info?.here;
  // A choice this computer can't honour now reads, and is sent, as the
  // owner's (lib/runOn.ts sentRunOn).
  const shown: RunOn = chosen === 'peer' && here ? 'peer' : 'owner';
  const label = shown === 'peer' ? t('chat.runOn.mine') : t('chat.runOn.owner', { owner });
  const item = (value: RunOn, text: string, hint: string, disabled?: boolean) => (
    <MenuPrimitive.Item
      key={value}
      disabled={disabled}
      onSelect={() => onChoose(value)}
      className="flex cursor-default select-none items-start gap-2.5 rounded-lg px-2.5 py-1.5 text-ui text-secondary outline-none data-[disabled]:cursor-not-allowed data-[highlighted]:bg-surface-raised data-[disabled]:opacity-50"
      data-run-on-option={value}
      data-run-on-disabled={disabled || undefined}
    >
      {value === 'peer' ? <Laptop className="mt-0.5 size-3.5 shrink-0 text-subtle" /> : <Server className="mt-0.5 size-3.5 shrink-0 text-subtle" />}
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span className="font-medium text-primary">{text}</span>
        <span className="text-caption leading-snug text-subtle">{hint}</span>
      </span>
      <Check className={cn('mt-0.5 size-3.5 shrink-0', shown === value ? 'text-primary' : 'invisible')} />
    </MenuPrimitive.Item>
  );
  return (
    <Menu>
      <Tip label={t('chat.runOn.tip', { owner })} side="top">
        <MenuTrigger asChild>
          <button
            type="button"
            className={cn(
              'flex h-8 max-w-48 shrink-0 items-center gap-1 rounded-full px-2.5 text-caption transition',
              shown === 'peer' ? 'bg-brand-500/15 text-brand-200 ring-1 ring-brand-400/40 hover:bg-brand-500/25' : 'text-subtle hover:bg-surface-raised hover:text-primary',
            )}
            aria-label={t('chat.runOn.label', { where: label })}
            data-run-on-chip={shown}
          >
            {shown === 'peer' ? <Laptop className="size-3.5 shrink-0" /> : <Server className="size-3.5 shrink-0" />}
            <span className="min-w-0 truncate font-medium">{label}</span>
            <ChevronDown className="size-3 shrink-0 opacity-70" />
          </button>
        </MenuTrigger>
      </Tip>
      <MenuContent align="start" side="top" className="w-80">
        <div className="px-2.5 pb-1 pt-1.5 text-micro text-faint">{t('chat.runOn.heading')}</div>
        {item('owner', t('chat.runOn.owner', { owner }), t('chat.runOn.ownerHint', { owner }))}
        {item('peer', t('chat.runOn.mine'), here ? t('chat.runOn.mineHint') : info ? info.why || t('chat.runOn.unavailable') : t('chat.runOn.checking'), !here)}
      </MenuContent>
    </Menu>
  );
}
