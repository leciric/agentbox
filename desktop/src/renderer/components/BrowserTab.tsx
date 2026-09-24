import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Camera, ClipboardPaste, Circle, Ellipsis, Globe, LoaderCircle, Monitor, MousePointer2, MousePointerClick, Play, Square } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { clock } from '../lib/media';
import { useVncView } from '../lib/useVncView';
import { cn, errorMessage } from '../lib/utils';
import { browserViewPath } from '../lib/vnc';
import { Button } from './ui/button';
import { Code, Notice } from './ui/card';
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from './ui/menu';
import { Switch } from './ui/switch';
import { Tip } from './ui/tooltip';

// BrowserTab shows the agent's desktop live: the display, with Chromium as one
// window on it. You can capture it into Media and take over its mouse and
// keyboard; there is no address bar, because a page is opened on the desktop
// itself, by the agent or by you with Take control. The browser is not a
// condition for any of it — a desktop with no browser on it still shows.
export function BrowserTab({ agent, onOpenMedia }: { agent: T.Agent; onOpenMedia: () => void }) {
  const queryClient = useQueryClient();
  const running = agent.state === 'running';
  const status = useQuery({ queryKey: ['browser', agent.ref], queryFn: () => api.browser(agent.ref), enabled: running, refetchInterval: 3_000 });
  const recording = useQuery({
    queryKey: ['recording', agent.ref],
    queryFn: () => api.recording(agent.ref),
    enabled: running,
    refetchInterval: (query) => (query.state.data?.recording ? 2_000 : 10_000),
  });
  const [control, setControl] = useState(false);
  const [now, setNow] = useState(Date.now());
  const desktopUp = running && status.data?.display === true;
  const browserUp = running && status.data?.running === true;
  const { view, connected, paste } = useVncView(browserViewPath(agent.ref), desktopUp, control);
  const page = status.data?.pages[0];
  const isRecording = recording.data?.recording === true;

  const setStatus = (next: T.BrowserStatus) => queryClient.setQueryData(['browser', agent.ref], next);
  const saved = (item: T.MediaItem) => toast(`Saved “${item.name}” to Media`, { action: { label: 'View', onClick: onOpenMedia } });
  const start = useMutation({ mutationFn: () => api.browserAction(agent.ref, 'start'), onSuccess: setStatus });
  const stop = useMutation({ mutationFn: () => api.browserAction(agent.ref, 'stop'), onSuccess: setStatus });
  const shot = useMutation({ mutationFn: (req: T.ScreenshotRequest | void) => api.screenshot(agent.ref, req ?? {}), onSuccess: saved });
  const record = useMutation({
    mutationFn: (input?: string): Promise<T.MediaItem | T.RecordingStatus> =>
      isRecording ? api.stopRecording(agent.ref) : api.startRecording(agent.ref, { name: 'browser recording', input }),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ['recording', agent.ref] });
      if ('id' in result) saved(result);
    },
  });
  const error = start.error ?? stop.error ?? shot.error ?? record.error;

  useEffect(() => {
    if (!isRecording) return;
    const timer = setInterval(() => setNow(Date.now()), 1_000);
    return () => clearInterval(timer);
  }, [isRecording]);

  const elapsed = recording.data?.startedAt ? (now - new Date(recording.data.startedAt).getTime()) / 1000 : 0;

  return (
    <div className="flex h-full flex-col">
      <div className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-3">
        {/* What the desktop has on it, not a way to drive it: a page is opened
            on the desktop itself, with Take control, like any other window. */}
        <span
          className={cn(
            'flex h-8 min-w-0 max-w-[45%] shrink items-center gap-2 rounded-full border border-line-strong bg-well px-3 text-[12.5px]',
            browserUp ? 'text-secondary' : 'text-subtle',
          )}
          aria-label="Browser"
        >
          <span className={cn('size-1.5 shrink-0 rounded-full', browserUp ? 'animate-glow bg-emerald-400' : 'bg-faint')} />
          <Globe className="size-3.5 shrink-0 text-subtle" />
          <span className="truncate">
            {browserUp ? page?.title || page?.url || 'No page open' : desktopUp ? 'No browser' : 'Desktop off'}
          </span>
        </span>
        <div className="flex-1" />
        <Tip label="Take a screenshot of the page, kept in Media">
          <Button size="sm" disabled={!browserUp || shot.isPending} onClick={() => shot.mutate()}>
            {shot.isPending ? <LoaderCircle className="animate-spin" /> : <Camera />}
            Screenshot
          </Button>
        </Tip>
        {isRecording ? (
          <Button size="sm" className="border-rose-400/30 bg-rose-500/15 text-rose-100 hover:bg-rose-500/25" disabled={record.isPending} onClick={() => record.mutate()}>
            {record.isPending ? <LoaderCircle className="animate-spin" /> : <span className="size-2 animate-pulse rounded-[3px] bg-rose-400" />}
            <span className="font-mono tabular-nums">{clock(elapsed)}</span>
            Stop recording
          </Button>
        ) : (
          <Menu>
            <Tip label="Record the desktop, kept in Media">
              <MenuTrigger asChild>
                <Button size="sm" disabled={!desktopUp || record.isPending}>
                  {record.isPending ? <LoaderCircle className="animate-spin" /> : <span className="size-2.5 rounded-full bg-rose-500 shadow-[0_0_8px_rgb(244_63_94/0.7)]" />}
                  Record
                </Button>
              </MenuTrigger>
            </Tip>
            <MenuContent>
              <MenuItem icon={Circle} onSelect={() => record.mutate()}>
                Record the screen
              </MenuItem>
              <MenuItem icon={MousePointerClick} onSelect={() => record.mutate('desktop')}>
                Record with keys and mouse
              </MenuItem>
            </MenuContent>
          </Menu>
        )}

        <div className="mx-1 h-5 w-px bg-line-strong" />
        <label className={cn('flex shrink-0 items-center gap-2 whitespace-nowrap rounded-lg px-1.5 py-1 text-[13px] text-tertiary', control && 'text-brand-200')}>
          <Switch id="browser-control" checked={control} disabled={!connected} onCheckedChange={setControl} />
          <MousePointer2 className="size-3.5" />
          Take control
        </label>
        <Menu>
          <MenuTrigger asChild>
            <Button size="icon-sm" variant="ghost" aria-label="Desktop actions">
              <Ellipsis />
            </Button>
          </MenuTrigger>
          <MenuContent>
            <MenuItem icon={Camera} disabled={!browserUp} onSelect={() => shot.mutate({ fullPage: true, name: 'full page' })}>
              Full-page screenshot
            </MenuItem>
            <MenuItem icon={Monitor} disabled={!desktopUp} onSelect={() => shot.mutate({ target: 'display', name: 'screen' })}>
              Screenshot of the whole screen
            </MenuItem>
            <MenuItem icon={ClipboardPaste} disabled={!connected} onSelect={() => void paste()}>
              Send your clipboard to the desktop
            </MenuItem>
            <MenuSeparator />
            {desktopUp && !browserUp && (
              <MenuItem icon={Play} onSelect={() => start.mutate()}>
                Start the browser on it
              </MenuItem>
            )}
            {desktopUp ? (
              <MenuItem icon={Square} onSelect={() => stop.mutate()}>
                Stop desktop
              </MenuItem>
            ) : (
              <MenuItem icon={Play} disabled={!running} onSelect={() => start.mutate()}>
                Start desktop
              </MenuItem>
            )}
          </MenuContent>
        </Menu>
      </div>
      {error && (
        <div className="px-4 pt-3">
          <Notice>{errorMessage(error)}</Notice>
        </div>
      )}

      <div className="relative min-h-0 flex-1 bg-stage">
        <div ref={view} className="absolute inset-0" data-browser-view={agent.ref} data-connected={connected} />
        {/* Keeps your mouse and keyboard away from the browser until you take control. */}
        {connected && !control && <div className="absolute inset-0" data-view-guard />}
        {!desktopUp && (
          <div className="absolute inset-0 flex items-center justify-center">
            <div className="grid max-w-md animate-slide-up justify-items-center gap-3 text-center">
              <div className="flex size-12 items-center justify-center rounded-2xl border border-line-strong bg-surface">
                <Monitor className="size-5 text-muted" />
              </div>
              <p className="text-sm text-tertiary">
                {running ? `${agent.name}'s desktop isn't running.` : `${agent.title || agent.name} is ${agent.state}.`}
              </p>
              <p className="text-xs leading-relaxed text-subtle">
                It starts with the agent. Start it here, or let the agent run <Code>agentbox browser start</Code>. It's the agent's own display, with
                Chromium, a file manager and a terminal on it.
              </p>
              {running && (
                <Button variant="primary" className="mt-1" disabled={start.isPending} onClick={() => start.mutate()}>
                  {start.isPending ? <LoaderCircle className="animate-spin" /> : <Play />}
                  Start desktop
                </Button>
              )}
            </div>
          </div>
        )}
        {desktopUp && !connected && (
          <div className="absolute inset-0 flex items-center justify-center text-sm text-subtle">
            <LoaderCircle className="mr-2 size-4 animate-spin" />
            Connecting to the display…
          </div>
        )}
        {control && connected && <div className="pointer-events-none absolute inset-0 rounded-b-2xl ring-2 ring-inset ring-brand-400/40" />}
      </div>

      <div className="flex h-8 shrink-0 items-center gap-3 border-t border-line px-4 text-xs text-subtle">
        {isRecording && (
          <span className="shrink-0 text-rose-300">
            Recording{recording.data?.input === 'desktop' ? ' with the keys and mouse' : ''}
          </span>
        )}
        <span className="ml-auto shrink-0">
          {!desktopUp ? '' : control ? "Your mouse and keyboard go to the agent's desktop" : 'View only · turn on Take control to click and type'}
        </span>
      </div>
    </div>
  );
}
