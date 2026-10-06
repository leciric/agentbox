import { Keyboard, LoaderCircle, Play, RefreshCw } from 'lucide-react';
import { useEffect, useRef, type ReactNode } from 'react';
import type * as T from '../../shared/api';
import { useT } from '../lib/i18n';
import { terminalFor, useTerminalStatus } from '../lib/terminals';
import { cn } from '../lib/utils';
import { aiLabel, stateLabel } from './state';
import { Button } from './ui/button';
import { Kbd } from './ui/card';
import { Tip } from './ui/tooltip';

export function TerminalTab({ agent, onStart, starting }: { agent: T.Agent; onStart: () => void; starting: boolean }) {
  const t = useT();
  const container = useRef<HTMLDivElement>(null);
  const terminal = terminalFor(agent.ref);
  const status = useTerminalStatus(terminal);
  const attempts = useRef(0);

  useEffect(() => {
    const element = container.current;
    if (!element) return;
    terminal.attach(element);
    const observer = new ResizeObserver(() => terminal.fitToContainer());
    observer.observe(element);
    return () => {
      observer.disconnect();
      terminal.detach();
    };
  }, [terminal]);

  // Reconnect whenever the session drops while the agent runs: after a restore,
  // a restart of the daemon, or detaching from tmux.
  useEffect(() => {
    if (status.state === 'open') attempts.current = 0;
    if (agent.state !== 'running' || status.state !== 'closed') return;
    const delay = Math.min(1000 * 2 ** attempts.current, 15_000);
    const timer = setTimeout(() => {
      attempts.current++;
      terminal.connect();
    }, delay);
    return () => clearTimeout(timer);
  }, [agent.state, status.state, terminal]);

  return (
    <div className="relative flex h-full flex-col bg-terminal">
      <div ref={container} className="min-h-0 flex-1 py-2 pl-3 pr-1" data-terminal={agent.ref} />

      <div className="absolute right-3 top-2.5 flex items-center gap-1.5">
        {status.state === 'closed' && agent.state === 'running' && (
          <Button size="sm" className="h-7" onClick={() => terminal.connect()}>
            <RefreshCw />
            {t('agent.terminal.reconnect')}
          </Button>
        )}
        <span
          data-session={status.state}
          title={status.state === 'open' ? t('agent.terminal.attached') : status.reason}
          className={cn(
            'flex max-w-80 items-center gap-1.5 truncate rounded-full border px-2.5 py-1 text-[11px] backdrop-blur',
            status.state === 'open' && 'border-emerald-400/20 bg-emerald-400/10 text-emerald-300',
            status.state === 'closed' && 'border-amber-400/20 bg-amber-400/10 text-amber-200',
            (status.state === 'connecting' || status.state === 'idle') && 'border-line-strong bg-surface text-muted',
          )}
        >
          {status.state === 'open' && <span className="size-1.5 rounded-full bg-emerald-400" />}
          {(status.state === 'connecting' || status.state === 'idle') && <LoaderCircle className="size-3 animate-spin" />}
          {status.state === 'open' ? 'tmux · main' : status.state === 'closed' ? (status.reason ? t('agent.terminal.disconnectedWhy', { reason: status.reason }) : t('agent.terminal.disconnected')) : t('agent.terminal.connecting')}
        </span>
        <Tip
          side="left"
          label={
            <div className="grid gap-1.5 py-0.5">
              <Shortcut keys="Ctrl-b 0">{t('agent.terminal.shellWindow')}</Shortcut>
              {agent.ai !== 'none' && agent.interface !== 'chat' && <Shortcut keys="Ctrl-b 1">{t('agent.terminal.aiWindow', { tool: aiLabel(agent.ai) })}</Shortcut>}
              <Shortcut keys="Ctrl-Shift-C / V">{t('agent.terminal.copyPaste')}</Shortcut>
              <Shortcut keys="Shift + drag">{t('agent.terminal.selectText')}</Shortcut>
              <Shortcut keys="Wheel">{t('agent.terminal.scrollBack')}</Shortcut>
            </div>
          }
        >
          <button aria-label={t('agent.terminal.shortcuts')} className="rounded-full border border-line-strong bg-surface p-1.5 text-muted transition hover:text-primary">
            <Keyboard className="size-3.5" />
          </button>
        </Tip>
      </div>

      {(agent.state === 'stopped' || agent.state === 'paused') && (
        <div className="absolute inset-0 flex animate-fade-in items-center justify-center bg-terminal/80 backdrop-blur-sm">
          <div className="panel grid max-w-sm justify-items-center gap-3 rounded-2xl px-8 py-7 text-center">
            <p className="text-sm text-tertiary">
              {t('agent.view.isState', { name: agent.title || agent.name, state: stateLabel(agent.state) })}{' '}
              {agent.state === 'paused' ? t('agent.terminal.pausedNote') : t('agent.terminal.stoppedNote')}
            </p>
            <Button variant="primary" onClick={onStart} disabled={starting}>
              {starting ? <LoaderCircle className="animate-spin" /> : <Play />}
              {agent.state === 'paused' ? t('agent.terminal.resumeAgent', { name: agent.name }) : t('agent.terminal.startAgent', { name: agent.name })}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

function Shortcut({ keys, children }: { keys: string; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-6">
      <span className="text-muted">{children}</span>
      <Kbd>{keys}</Kbd>
    </div>
  );
}
