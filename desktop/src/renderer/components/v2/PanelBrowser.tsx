// The right-hand panel's Browser tab: a port of the agent's machine, its dev
// server say, framed through the daemon's preview proxy, as OverviewTab's
// PreviewLink opens it in your own browser. The ports its own Chromium has
// pages open on are offered first.
import { useQuery } from '@tanstack/react-query';
import { ExternalLink, Globe, RotateCw } from 'lucide-react';
import { useState } from 'react';
import type * as T from '../../../shared/api';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { browserPorts, previewUrl } from '../../lib/rightPanel';
import { cn } from '../../lib/utils';
import { Tip } from '../ui/tooltip';

export function PanelBrowser({ agent }: { agent: T.Agent }) {
  const t = useT();
  const preview = useQuery({ queryKey: ['preview'], queryFn: api.preview, staleTime: Infinity });
  const running = agent.state === 'running';
  const status = useQuery({ queryKey: ['browser', agent.ref], queryFn: () => api.browser(agent.ref), enabled: running, refetchInterval: 5_000 });
  const ports = browserPorts(status.data?.pages ?? []);
  const [port, setPort] = useState('');
  const [shown, setShown] = useState('');
  const [reloads, setReloads] = useState(0);
  const current = shown || ports[0]?.port || '';
  const addr = preview.data?.addr;
  const page = ports.find((p) => p.port === current);
  const url = addr && current ? previewUrl(agent, current, addr, page?.path) : '';

  return (
    <div className="flex min-h-0 flex-1 flex-col" data-panel-browser>
      <form
        className="flex shrink-0 items-center gap-1.5 border-b border-line-faint px-2 py-1.5"
        onSubmit={(event) => {
          event.preventDefault();
          if (port) setShown(port);
          setReloads((n) => n + 1);
        }}
      >
        <Tip label={t('panel.browser.reload')}>
          <button type="submit" aria-label={t('panel.browser.reload')} className="flex size-7 shrink-0 items-center justify-center rounded-md text-subtle hover:bg-surface-raised hover:text-primary" disabled={!url && !port}>
            <RotateCw className="size-3.5" />
          </button>
        </Tip>
        <label className="flex h-7 min-w-0 flex-1 items-center gap-1 rounded-md border border-line bg-well px-2 text-[12px]">
          <span className="shrink-0 text-faint">{t('panel.browser.port')}</span>
          <input
            value={port || current}
            onChange={(event) => setPort(event.target.value.replace(/\D/g, '').slice(0, 5))}
            placeholder="3000"
            aria-label={t('panel.browser.portLabel')}
            className="w-14 shrink-0 bg-transparent font-mono text-primary outline-none placeholder:text-faint"
            data-panel-browser-port
          />
          <span className="min-w-0 truncate font-mono text-[11px] text-faint" title={url}>
            {url}
          </span>
        </label>
        <Tip label={t('panel.browser.openOutside')}>
          <button type="button" aria-label={t('panel.browser.openOutside')} disabled={!url} onClick={() => void window.agentbox.openExternal(url)} className="flex size-7 shrink-0 items-center justify-center rounded-md text-subtle hover:bg-surface-raised hover:text-primary disabled:opacity-40">
            <ExternalLink className="size-3.5" />
          </button>
        </Tip>
      </form>
      {ports.length > 0 && (
        <div className="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-line-faint px-2 py-1 [scrollbar-width:none]" data-panel-browser-pages>
          {ports.map((p) => (
            <button
              key={p.port}
              type="button"
              onClick={() => {
                setPort('');
                setShown(p.port);
              }}
              title={p.title || p.url}
              className={cn('flex max-w-48 shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-[11.5px]', p.port === current ? 'bg-surface-raised text-primary' : 'text-subtle hover:bg-surface hover:text-primary')}
            >
              <Globe className="size-3 shrink-0" />
              <span className="truncate">{p.title || `:${p.port}`}</span>
            </button>
          ))}
        </div>
      )}
      <div className="relative min-h-0 flex-1 bg-white">
        {url ? (
          <iframe
            key={`${url}#${reloads}`}
            title={t('panel.view.browser')}
            src={url}
            // Its own origin, a port of the agent's machine, never the app's.
            sandbox="allow-scripts allow-same-origin allow-forms"
            referrerPolicy="no-referrer"
            className="absolute inset-0 size-full border-0"
            data-panel-browser-frame={url}
          />
        ) : (
          <div className="absolute inset-0 flex flex-col items-center justify-center gap-2 bg-sunken px-6 text-center text-[12.5px] text-subtle">
            <Globe className="size-7 text-faint" />
            {!addr && !preview.isPending ? t('panel.browser.proxyOff') : t('panel.browser.none')}
          </div>
        )}
      </div>
    </div>
  );
}
