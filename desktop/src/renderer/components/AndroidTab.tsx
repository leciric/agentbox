import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Camera, ClipboardPaste, Ellipsis, FileText, LoaderCircle, MousePointer2, Play, Smartphone, Square, TriangleAlert, Wrench } from 'lucide-react';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { clock } from '../lib/media';
import { useVncView } from '../lib/useVncView';
import { cn, errorMessage } from '../lib/utils';
import { androidViewPath } from '../lib/vnc';
import { Button } from './ui/button';
import { Code, Notice } from './ui/card';
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from './ui/menu';
import { Switch } from './ui/switch';
import { Tip } from './ui/tooltip';

// AndroidTab shows the agent's own Android emulator, shaped like the phone it
// is. You can start it, capture it into Media, and tap and type on it.
export function AndroidTab({ agent, onOpenMedia, onSetup }: { agent: T.Agent; onOpenMedia: () => void; onSetup: () => void }) {
  const queryClient = useQueryClient();
  const running = agent.state === 'running';
  const status = useQuery({
    queryKey: ['android', agent.ref],
    queryFn: () => api.android(agent.ref),
    enabled: running,
    refetchInterval: (query) => (query.state.data?.running && !query.state.data.booted ? 1_500 : 5_000),
  });
  const recording = useQuery({
    queryKey: ['recording', agent.ref],
    queryFn: () => api.recording(agent.ref),
    enabled: running,
    refetchInterval: (query) => (query.state.data?.recording ? 2_000 : 10_000),
  });
  const [control, setControl] = useState(false);
  const [now, setNow] = useState(Date.now());
  const [startedAt, setStartedAt] = useState<number | null>(null);
  const booted = running && status.data?.booted === true;
  const { view, connected, paste } = useVncView(androidViewPath(agent.ref), booted, control);

  const setStatus = (next: T.AndroidStatus) => queryClient.setQueryData(['android', agent.ref], next);
  const saved = (item: T.MediaItem) => toast(`Saved “${item.name}” to Media`, { action: { label: 'View', onClick: onOpenMedia } });
  const start = useMutation({
    mutationFn: () => api.startAndroid(agent.ref),
    onMutate: () => {
      setStartedAt(Date.now());
      // The request lasts until Android has booted; meanwhile the status shows it starting.
      setTimeout(() => void queryClient.invalidateQueries({ queryKey: ['android', agent.ref] }), 1_500);
    },
    onSuccess: setStatus,
    onSettled: () => setStartedAt(null),
  });
  const stop = useMutation({ mutationFn: () => api.stopAndroid(agent.ref), onSuccess: setStatus });
  const shot = useMutation({ mutationFn: () => api.screenshot(agent.ref, { target: 'android', name: 'android screenshot' }), onSuccess: saved });
  const logs = useMutation({ mutationFn: () => api.addLogs(agent.ref, { android: true, since: '10m', name: 'logcat' }), onSuccess: saved });
  const isRecording = recording.data?.recording === true;
  const recordingHere = isRecording && recording.data?.target === 'android';
  const record = useMutation({
    mutationFn: (): Promise<T.MediaItem | T.RecordingStatus> =>
      recordingHere ? api.stopRecording(agent.ref) : api.startRecording(agent.ref, { target: 'android', name: 'android recording' }),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ['recording', agent.ref] });
      if ('id' in result) saved(result);
    },
  });
  const error = start.error ?? stop.error ?? shot.error ?? logs.error ?? record.error;
  const starting = start.isPending || (status.data?.running === true && !booted);

  useEffect(() => {
    if (!recordingHere && !starting) return;
    const timer = setInterval(() => setNow(Date.now()), 1_000);
    return () => clearInterval(timer);
  }, [recordingHere, starting]);

  const elapsed = recording.data?.startedAt ? (now - new Date(recording.data.startedAt).getTime()) / 1000 : 0;
  const image = status.data?.images[0];

  return (
    <div className="flex h-full flex-col">
      <div className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-3">
        <span
          className={cn(
            'flex h-8 min-w-0 items-center gap-2 rounded-full border border-line-strong bg-well px-3 text-[12.5px]',
            booted ? 'text-secondary' : 'text-subtle',
          )}
          aria-label="Device"
        >
          <span className={cn('size-1.5 shrink-0 rounded-full', booted ? 'bg-emerald-400 animate-glow' : starting ? 'bg-amber-400 animate-pulse' : 'bg-faint')} />
          <Smartphone className="size-3.5 shrink-0 text-subtle" />
          <span className="truncate">{booted ? status.data?.device : starting ? 'Starting Android…' : 'Emulator off'}</span>
        </span>
        <div className="flex-1" />
        <Tip label="Take a screenshot of the device, kept in Media">
          <Button size="sm" disabled={!booted || shot.isPending} onClick={() => shot.mutate()}>
            {shot.isPending ? <LoaderCircle className="animate-spin" /> : <Camera />}
            Screenshot
          </Button>
        </Tip>
        {recordingHere ? (
          <Button size="sm" className="border-rose-400/30 bg-rose-500/15 text-rose-100 hover:bg-rose-500/25" disabled={record.isPending} onClick={() => record.mutate()}>
            {record.isPending ? <LoaderCircle className="animate-spin" /> : <span className="size-2 animate-pulse rounded-[3px] bg-rose-400" />}
            <span className="font-mono tabular-nums">{clock(elapsed)}</span>
            Stop recording
          </Button>
        ) : (
          <Tip label={isRecording ? 'The browser screen is being recorded' : "Record the device's screen, kept in Media"}>
            <Button size="sm" disabled={!booted || isRecording || record.isPending} onClick={() => record.mutate()}>
              {record.isPending ? <LoaderCircle className="animate-spin" /> : <span className="size-2.5 rounded-full bg-rose-500 shadow-[0_0_8px_rgb(244_63_94/0.7)]" />}
              Record
            </Button>
          </Tip>
        )}
        <div className="mx-1 h-5 w-px bg-line-strong" />
        <label className={cn('flex items-center gap-2 rounded-lg px-1.5 py-1 text-[13px] text-tertiary', control && 'text-brand-200')}>
          <Switch id="android-control" checked={control} disabled={!connected} onCheckedChange={setControl} />
          <MousePointer2 className="size-3.5" />
          Take control
        </label>
        <Menu>
          <MenuTrigger asChild>
            <Button size="icon-sm" variant="ghost" aria-label="Emulator actions">
              <Ellipsis />
            </Button>
          </MenuTrigger>
          <MenuContent>
            <MenuItem icon={FileText} disabled={!booted} onSelect={() => logs.mutate()}>
              Save the last 10 minutes of logcat
            </MenuItem>
            <MenuItem icon={ClipboardPaste} disabled={!connected} onSelect={() => void paste()}>
              Send your clipboard to the device
            </MenuItem>
            <MenuSeparator />
            {status.data?.running ? (
              <MenuItem icon={Square} disabled={stop.isPending} onSelect={() => stop.mutate()}>
                Stop emulator
              </MenuItem>
            ) : (
              <MenuItem icon={Play} disabled={!running || !status.data?.available || starting} onSelect={() => start.mutate()}>
                Start emulator
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

      <div className="relative min-h-0 flex-1 overflow-hidden bg-[radial-gradient(ellipse_at_center,rgb(124_58_237/0.10),transparent_60%)]">
        {booted && (
          <div className="absolute inset-0 flex items-center justify-center p-5">
            <div
              className={cn(
                'relative aspect-[1080/2400] h-full max-w-full overflow-hidden rounded-[26px] bg-black shadow-[0_40px_120px_-30px_var(--ab-shadow-deep)] ring-1 ring-line-strong transition',
                control && connected && 'ring-2 ring-brand-400/60',
              )}
            >
              <div ref={view} className="absolute inset-0" data-android-view={agent.ref} data-connected={connected} />
              {/* Keeps your mouse and keyboard away from the device until you take control. */}
              {connected && !control && <div className="absolute inset-0" data-view-guard />}
              {!connected && (
                <div className="absolute inset-0 flex items-center justify-center text-sm text-subtle">
                  <LoaderCircle className="mr-2 size-4 animate-spin" />
                  Connecting…
                </div>
              )}
            </div>
          </div>
        )}

        {!booted && (
          <div className="absolute inset-0 flex items-center justify-center p-6">
            <div className="grid max-w-md animate-slide-up justify-items-center gap-3 text-center">
              <div className="relative flex h-24 w-12 items-center justify-center rounded-[14px] border border-line-vivid bg-gradient-to-b from-surface-raised to-surface-faint shadow-[0_20px_50px_-20px_rgb(139_92_246/0.6)]">
                <span className="absolute top-1.5 h-1 w-3 rounded-full bg-surface-vivid" />
                {starting ? <LoaderCircle className="size-5 animate-spin text-brand-300" /> : <Smartphone className="size-5 text-muted" />}
              </div>
              {!running ? (
                <p className="text-sm text-tertiary">
                  {agent.title || agent.name} is {agent.state}.
                </p>
              ) : starting ? (
                <>
                  <p className="text-sm text-secondary">Starting Android…</p>
                  <p className="text-xs text-subtle">
                    Booting a Pixel 7 on KVM. It usually takes about 20 seconds
                    {startedAt ? ` (${Math.max(0, Math.round((now - startedAt) / 1000))} s so far)` : ''}.
                  </p>
                </>
              ) : status.data && !status.data.available ? (
                <>
                  <p className="flex items-center gap-2 text-sm text-secondary">
                    <TriangleAlert className="size-4 text-amber-300" />
                    This machine can't run Android emulators yet
                  </p>
                  <p className="text-xs leading-relaxed text-subtle">{status.data.problem}</p>
                  <Button className="mt-1" onClick={onSetup}>
                    <Wrench />
                    Open Settings
                  </Button>
                </>
              ) : (
                <>
                  <p className="text-sm text-secondary">{agent.name}'s Android emulator isn't running</p>
                  <p className="text-xs leading-relaxed text-subtle">
                    A Pixel 7{image ? ` on ${image.split(';')[1]?.replace('android-', 'API ')}` : ''}, with its own app data, on this agent's machine. It uses about 3
                    GB of memory while it runs. The agent can start it too, with <Code>agentbox android start</Code>.
                  </p>
                  <Button variant="primary" className="mt-1" disabled={!status.data} onClick={() => start.mutate()}>
                    <Play />
                    Start emulator
                  </Button>
                </>
              )}
            </div>
          </div>
        )}
      </div>

      <div className="flex h-8 shrink-0 items-center gap-3 border-t border-line px-4 text-xs text-subtle">
        <span className="truncate font-mono text-[11px]">{booted ? status.data?.image : ''}</span>
        <span className="ml-auto shrink-0">
          {!booted ? '' : control ? 'Your mouse and keyboard go to the device' : 'View only · turn on Take control to tap and type'}
        </span>
      </div>
    </div>
  );
}
