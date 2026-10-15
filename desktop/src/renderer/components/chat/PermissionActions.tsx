import { Check, ChevronDown, LoaderCircle } from 'lucide-react';
import { useEffect, type ReactNode } from 'react';
import type * as T from '../../../shared/api';
import { useT } from '../../lib/i18n';
import { cn } from '../../lib/utils';
import { Kbd } from '../ui/card';
import { Menu, MenuContent, MenuItem, MenuTrigger } from '../ui/menu';
import { Tip } from '../ui/tooltip';

// PermissionActions is the answer to one permission request, the same in the
// chat's banner and the lead's card for an agent's: Deny, then Approve, the
// primary choice. The permanent one, "Always allow …", sits in a menu on
// Approve with a tooltip saying what it covers, so it isn't a click away
// from the usual answer. With keys on, Y approves and N denies (not in a
// text field); once an answer is on its way the buttons say so.
export function PermissionActions({
  options,
  agent,
  action,
  pending,
  sending,
  onAnswer,
  approveLabel,
  denyLabel,
  keys = false,
}: {
  options: readonly T.ChatPermissionOption[];
  // The agent the request is from, and the kind of tool call it is (execute, edit…).
  agent: string;
  action?: string;
  pending: boolean;
  // The option being sent, if any.
  sending?: string;
  onAnswer: (option: string) => void;
  approveLabel: string;
  denyLabel: string;
  keys?: boolean;
}) {
  const t = useT();
  const approve = options.find((o) => o.kind === 'allow_once');
  const always = options.find((o) => o.kind === 'allow_always');
  const deny = options.find((o) => o.kind === 'reject_once') ?? options.find((o) => o.kind.startsWith('reject'));
  useEffect(() => {
    if (!keys || pending) return;
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      if (e.defaultPrevented || e.ctrlKey || e.metaKey || e.altKey || e.repeat) return;
      if (target && (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName))) return;
      const option = e.key === 'y' || e.key === 'Y' ? approve : e.key === 'n' || e.key === 'N' ? deny : undefined;
      if (!option) return;
      e.preventDefault();
      onAnswer(option.id);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [keys, pending, approve, deny, onAnswer]);

  const spin = (id?: string): ReactNode => (pending && sending === id ? <LoaderCircle className="animate-spin" /> : null);
  const scope = always && always.name.length > 3 ? always.name : t('chat.permission.alwaysScope', { agent });
  return (
    <div className="mt-2.5 flex flex-wrap items-center justify-end gap-1.5" data-permission-actions>
      {deny && (
        <button
          type="button"
          disabled={pending}
          onClick={() => onAnswer(deny.id)}
          title={deny.name}
          data-agent-permission-option={deny.kind}
          className="flex h-7 items-center gap-1.5 rounded-lg px-2.5 text-[12.5px] text-rose-200/90 transition hover:bg-rose-500/10 disabled:opacity-50"
        >
          {spin(deny.id)}
          {denyLabel}
          {keys && <Kbd>N</Kbd>}
        </button>
      )}
      {approve && (
        <div className="flex items-stretch" data-permission-approve>
          <button
            type="button"
            disabled={pending}
            onClick={() => onAnswer(approve.id)}
            title={approve.name}
            data-agent-permission-option={approve.kind}
            className={cn('flex h-7 items-center gap-1.5 bg-amber-300 px-3 text-[12.5px] font-medium text-on-bright transition hover:bg-amber-200 disabled:opacity-60', always ? 'rounded-l-lg' : 'rounded-lg')}
          >
            {pending && sending === approve.id ? <LoaderCircle className="size-3.5 animate-spin" /> : null}
            {pending && sending === approve.id ? t('chat.permission.sending') : approveLabel}
            {keys && !pending && <Kbd>Y</Kbd>}
          </button>
          {always && (
            <Menu>
              <Tip label={scope} side="top">
                <MenuTrigger asChild>
                  <button
                    type="button"
                    disabled={pending}
                    aria-label={t('chat.permission.moreChoices')}
                    data-permission-more
                    className="flex h-7 items-center rounded-r-lg border-l border-black/20 bg-amber-300 px-1.5 text-on-bright transition hover:bg-amber-200 disabled:opacity-60"
                  >
                    <ChevronDown className="size-3.5" />
                  </button>
                </MenuTrigger>
              </Tip>
              <MenuContent align="end" className="min-w-56">
                <MenuItem icon={Check} onSelect={() => onAnswer(always.id)} title={scope} data-agent-permission-option={always.kind}>
                  {t('chat.permission.alwaysFor', { action: action ?? '', agent })}
                </MenuItem>
              </MenuContent>
            </Menu>
          )}
        </div>
      )}
    </div>
  );
}
