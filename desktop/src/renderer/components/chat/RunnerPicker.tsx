import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowRight, Check, ChevronDown } from 'lucide-react';
import type { ComponentType } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { chatKey } from '../../lib/chat';
import { useT } from '../../lib/i18n';
import { fiveHour, runnerChoices, runnerReason, type RunnerChoice } from '../../lib/leadRunner';
import { limitTone } from '../../lib/tokens';
import { useNow } from '../../lib/useNow';
import { cn, errorMessage } from '../../lib/utils';
import { LimitRow } from '../TopBar';
import { AIIcon, aiLabel } from '../state';
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from '../ui/menu';

// RunnerPicker is a lead's AI tool and account, in its composer: what the chat
// runs, and the tools and accounts it can switch to, from Settings → Accounts
// and the project's own list. A choice applies from the next message. Within
// Claude Code an account switch resumes the same session; another tool starts
// a new one, carried over by the rollover summary (internal/chat/runner.go).
// The model stays in its own menu beside this one: switching it within a tool
// is the adapter's own option, with no new session.
export function RunnerPicker({ agent, session }: { agent: T.Agent; session?: T.ChatSession }) {
  const t = useT();
  const clock = useNow(60_000);
  const queryClient = useQueryClient();
  const runners = useQuery({ queryKey: ['chatRunners', agent.project], queryFn: () => api.chatRunners(agent.project), refetchInterval: 30_000 });
  const choose = useMutation({
    mutationFn: (c: RunnerChoice) => api.setChatRunner(agent.project, { tool: c.tool, account: c.account }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['chatRunners', agent.project] });
      void queryClient.invalidateQueries({ queryKey: chatKey(agent.ref) });
      void queryClient.invalidateQueries({ queryKey: ['claudeLimits'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
  const tool = session?.tool || agent.ai;
  const next = session?.nextTool;
  const account = !next ? session?.account || (tool === 'claude' ? runners.data?.account : runners.data?.toolAccounts?.[tool]) : undefined;
  const choices = runners.data ? runnerChoices(runners.data) : [];
  const shown = choices.find((c) => c.current);
  const used = fiveHour(shown?.reading, clock);
  // A tool with several accounts names the account on each row, and on the trigger.
  const several = (of: string) => choices.filter((c) => c.tool === of).length > 1;
  const multi = several(tool);
  return (
    <Menu>
      <MenuTrigger asChild>
        <button
          aria-label={t('chat.runner.label', { tool: aiLabel(tool) })}
          data-runner-picker
          title={t('chat.runner.tip')}
          className="flex h-7 min-w-0 shrink-0 items-center gap-1.5 rounded-lg px-2 text-[12.5px] text-muted transition-colors hover:bg-surface hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/40 data-[state=open]:bg-surface-raised data-[state=open]:text-primary [&_svg]:size-4 [&_svg]:shrink-0"
        >
          <AIIcon ai={tool} />
          <span className="max-w-[8rem] truncate max-sm:hidden">{aiLabel(tool)}</span>
          {account && multi && <span className="max-w-[6rem] truncate text-subtle max-sm:hidden">· {account}</span>}
          {next && (
            <span className="flex items-center gap-1 rounded-full bg-brand-400/15 px-1.5 py-px text-[11px] text-brand-200" title={t('chat.runner.nextTip', { tool: aiLabel(next) })}>
              <ArrowRight className="!size-3" />
              <AIIcon ai={next} className="!size-3" />
              <span className="max-sm:hidden">{aiLabel(next)}</span>
            </span>
          )}
          {used !== null && !next && <UsageDot used={used} />}
          <ChevronDown className="!size-3.5 text-faint" />
        </button>
      </MenuTrigger>
      <MenuContent align="start" side="top" className="max-h-[28rem] w-80 overflow-y-auto">
        <MenuLabel>{t('chat.runner.title')}</MenuLabel>
        {runners.isPending && <p className="px-2.5 py-2 text-[12px] text-subtle">{t('common.loading')}</p>}
        {runners.isError && <p className="px-2.5 py-2 text-[12px] text-rose-300">{errorMessage(runners.error)}</p>}
        {choices.map((c) => (
          <MenuItem
            key={c.tool + '/' + c.account}
            disabled={!c.ready || choose.isPending}
            onSelect={() => !c.current && choose.mutate(c)}
            title={runners.data && runnerReason(runners.data, c.tool)}
            icon={toolIcons[c.tool]}
            data-runner={c.tool}
            data-account={c.account || undefined}
            hint={c.current ? <Check className="size-3.5 text-brand-300" /> : undefined}
          >
            <div className="grid min-w-0 gap-0.5">
              <span className="flex min-w-0 items-center gap-1.5">
                <span className="truncate">{c.name}</span>
                {several(c.tool) && c.account && <span className="truncate text-subtle">· {c.account}</span>}
              </span>
              {c.note && <span className="text-[11px] text-subtle">{c.note}</span>}
              {c.reading && <AccountUsage reading={c.reading} clock={clock} />}
            </div>
          </MenuItem>
        ))}
        <MenuSeparator />
        <p className="px-2.5 pb-1.5 pt-1 text-[11px] leading-snug text-subtle">{t('chat.runner.applies')}</p>
      </MenuContent>
    </Menu>
  );
}

// toolIcons are the tools' marks as MenuItem icons, made once so a row's icon
// keeps its identity between renders.
const toolIcons: Record<string, ComponentType<{ className?: string }>> = Object.fromEntries(
  ['claude', 'codex', 'opencode', 'cursor'].map((ai) => [ai, ({ className }: { className?: string }) => <AIIcon ai={ai} className={className} />]),
);

// UsageDot is the chosen account's 5-hour use, on the trigger.
function UsageDot({ used }: { used: number }) {
  const tone = limitTone(used);
  return (
    <span className={cn('font-mono text-[10.5px] tabular-nums', tone === 'high' ? 'text-rose-300' : tone === 'warn' ? 'text-amber-300' : 'text-subtle')}>
      {Math.round(used * 100)}%
    </span>
  );
}

// AccountUsage is an account's limits, as the top bar's meter shows them.
function AccountUsage({ reading, clock }: { reading: T.ClaudeLimit; clock: number }) {
  const windows = reading.windows.filter((w) => w.name === 'five_hour' || w.name === 'seven_day');
  if (windows.length === 0) return null;
  return (
    <div className="mt-1 grid gap-1.5" data-usage={reading.account}>
      {windows.map((w) => (
        <LimitRow key={w.name} window={w} clock={clock} />
      ))}
    </div>
  );
}
