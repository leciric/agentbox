import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Camera, Circle, Lock, LoaderCircle, MousePointer2, Play, Power, TabletSmartphone, TriangleAlert } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { cn, errorMessage } from '../lib/utils';
import { IOSScreen } from './IOSScreen';
import { Button } from './ui/button';
import { Code, Notice } from './ui/card';
import { Select, SelectOption } from './ui/select';
import { Switch } from './ui/switch';
import { Tip } from './ui/tooltip';

// IOSTab is the Mac's iOS Simulator, for an agent: pick a simulator, boot it,
// and watch and use its screen, which the agent drives too (agentbox ios).
// The simulators are the Mac's, shared by every agent; the one picked here is
// the agent's, which its commands use. On a Mac without Xcode, or before the
// front end has started the simulator helper, it says what's missing.
// compact (UI v2's narrow panel) names its toolbar's buttons in tooltips only.
export function IOSTab({ agent, onOpenMedia, compact = false }: { agent: T.Agent; onOpenMedia: () => void; compact?: boolean }) {
  const t = useT();
  const queryClient = useQueryClient();
  const status = useQuery({
    queryKey: ['ios', agent.ref],
    queryFn: () => api.ios(agent.ref),
    refetchInterval: (query) => (query.state.data?.devices?.some((d) => d.state === 'Booting') ? 1_500 : 5_000),
  });
  const devices = status.data?.devices ?? [];
  // The agent's own simulator, else the one booted, else the first.
  const [picked, setPicked] = useState('');
  const udid = picked || status.data?.device || devices.find((d) => d.state === 'Booted')?.udid || devices[0]?.udid || '';
  const device = devices.find((d) => d.udid === udid);
  const booted = device?.state === 'Booted';
  // Your mouse and keyboard go to the simulator only once you take control.
  const [control, setControl] = useState(false);
  const [connected, setConnected] = useState(false);

  const setStatus = (next: T.IOSStatus) => queryClient.setQueryData(['ios', agent.ref], next);
  const open = useMutation({ mutationFn: () => api.openIOS(agent.ref, udid), onSuccess: setStatus });
  const close = useMutation({ mutationFn: () => api.closeIOS(agent.ref, udid), onSuccess: setStatus });
  const press = useMutation({ mutationFn: (button: string) => api.iosInput(agent.ref, { device: udid, type: 'button', button }) });
  const shot = useMutation({
    mutationFn: () => api.screenshot(agent.ref, { target: 'ios', name: 'ios screenshot' }),
    onSuccess: (item) => toast(t('agent.media.saved', { name: item.name }), { action: { label: t('agent.media.view'), onClick: onOpenMedia } }),
  });
  const error = open.error ?? close.error ?? press.error ?? shot.error;
  const booting = open.isPending || device?.state === 'Booting';

  return (
    <div className="flex h-full flex-col" data-ios-tab={agent.ref}>
      <div className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-3">
        <span className={cn('size-1.5 shrink-0 rounded-full', booted ? 'bg-emerald-400 animate-glow' : booting ? 'bg-amber-400 animate-pulse' : 'bg-faint')} />
        <Select
          data-ios-device
          aria-label={t('agent.ios.device')}
          className="h-8 w-60 min-w-0 shrink text-[12.5px]"
          disabled={!status.data?.available || devices.length === 0}
          placeholder={status.isPending ? t('common.loading') : t('agent.ios.noDevices')}
          value={udid}
          onChange={setPicked}
        >
          {devices.map((d) => (
            <SelectOption key={d.udid} value={d.udid}>
              {compact ? d.name : t('agent.ios.deviceName', { name: d.name, runtime: d.runtime })}
            </SelectOption>
          ))}
        </Select>
        <div className="flex-1" />
        <Tip label={t('agent.ios.home')}>
          <Button size="icon-sm" variant="ghost" aria-label={t('agent.ios.home')} disabled={!booted} onClick={() => press.mutate('home')}>
            <Circle />
          </Button>
        </Tip>
        <Tip label={t('agent.ios.lock')}>
          <Button size="icon-sm" variant="ghost" aria-label={t('agent.ios.lock')} disabled={!booted} onClick={() => press.mutate('lock')}>
            <Lock />
          </Button>
        </Tip>
        <Tip label={t('agent.ios.screenshotTip')}>
          <Button size="icon-sm" variant="ghost" aria-label={t('agent.browser.screenshot')} disabled={!booted || shot.isPending || agent.state !== 'running'} onClick={() => shot.mutate()}>
            {shot.isPending ? <LoaderCircle className="animate-spin" /> : <Camera />}
          </Button>
        </Tip>
        <div className="mx-1 h-5 w-px shrink-0 bg-line-strong" />
        <label className={cn('flex shrink-0 items-center gap-2 rounded-lg px-1.5 py-1 text-[13px] text-tertiary', control && 'text-brand-200')}>
          <Switch id="ios-control" checked={control} disabled={!booted || !connected} onCheckedChange={setControl} />
          <MousePointer2 className="size-3.5" />
          {compact ? <span className="sr-only">{t('agent.browser.takeControl')}</span> : t('agent.browser.takeControl')}
        </label>
        {booted && (
          <Tip label={t('agent.ios.shutDownTip')}>
            <Button size="icon-sm" variant="ghost" aria-label={t('agent.ios.shutDown')} disabled={close.isPending} onClick={() => close.mutate()}>
              {close.isPending ? <LoaderCircle className="animate-spin" /> : <Power />}
            </Button>
          </Tip>
        )}
      </div>
      {error && (
        <div className="px-4 pt-3">
          <Notice>{errorMessage(error)}</Notice>
        </div>
      )}

      <div className="relative min-h-0 flex-1 overflow-hidden bg-[radial-gradient(ellipse_at_center,rgb(124_58_237/0.10),transparent_60%)]">
        {booted && device ? (
          <IOSScreen agent={agent} device={device} control={control} onState={(state) => setConnected(state.phase === 'playing')} />
        ) : (
          <div className="absolute inset-0 flex items-center justify-center p-6">
            <div className="grid max-w-md animate-slide-up justify-items-center gap-3 text-center">
              <div className="relative flex h-24 w-12 items-center justify-center rounded-[14px] border border-line-vivid bg-gradient-to-b from-surface-raised to-surface-faint shadow-[0_20px_50px_-20px_rgb(139_92_246/0.6)]">
                <span className="absolute top-1.5 h-1 w-4 rounded-full bg-surface-vivid" />
                {booting || status.isPending ? <LoaderCircle className="size-5 animate-spin text-brand-300" /> : <TabletSmartphone className="size-5 text-muted" />}
              </div>
              {status.isPending ? null : status.data && !status.data.available ? (
                <div data-ios-unavailable>
                  <p className="flex items-center justify-center gap-2 text-sm text-secondary">
                    <TriangleAlert className="size-4 text-amber-300" />
                    {t('agent.ios.unavailable')}
                  </p>
                  <p className="mt-2 text-xs leading-relaxed text-subtle">{status.data.problem}</p>
                </div>
              ) : status.error ? (
                <p className="text-xs leading-relaxed text-subtle">{errorMessage(status.error)}</p>
              ) : devices.length === 0 ? (
                <p className="text-xs leading-relaxed text-subtle">{t('agent.ios.noDevicesExplain')}</p>
              ) : booting ? (
                <>
                  <p className="text-sm text-secondary">{t('agent.ios.booting', { name: device?.name ?? '' })}</p>
                  <p className="text-xs text-subtle">{t('agent.ios.bootingExplain')}</p>
                </>
              ) : (
                <>
                  <p className="text-sm text-secondary">{t('agent.ios.off', { name: device?.name ?? '' })}</p>
                  <p className="text-xs leading-relaxed text-subtle">{t.rich('agent.ios.explain', { code: (c) => <Code>{c}</Code> })}</p>
                  <Button variant="primary" className="mt-1" disabled={!udid} onClick={() => open.mutate()}>
                    <Play />
                    {t('agent.ios.boot')}
                  </Button>
                </>
              )}
            </div>
          </div>
        )}
      </div>

      <div className="flex h-8 shrink-0 items-center gap-3 border-t border-line px-4 text-xs text-subtle">
        <span className="truncate">{status.data?.xcode ? t('agent.ios.onTheMac', { xcode: status.data.xcode }) : ''}</span>
        <span className="ml-auto shrink-0 truncate">{!booted ? '' : control ? t('agent.android.controlOn') : t('agent.android.viewOnly')}</span>
      </div>
    </div>
  );
}
