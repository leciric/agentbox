import { Circle, LoaderCircle, RotateCw, Square, Triangle } from 'lucide-react';
import { type ClipboardEvent, type KeyboardEvent, type PointerEvent, type WheelEvent, forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { useT } from '../../lib/i18n';
import { cn } from '../../lib/utils';
import { Tip } from '../ui/tooltip';
import { type DeviceButton, type DeviceInput, fit, keyInput, videoPoint, wheelClicks } from './protocol';
import { DeviceStreamSession, type StreamState } from './session';

export type DeviceStreamHandle = { send: (input: DeviceInput) => void };

type Props = {
  // path is the daemon's stream endpoint, like /v1/agents/p/a/android/stream.
  path: string;
  enabled: boolean;
  // control sends your pointer, wheel and keyboard to the device; without it
  // the view is only watched.
  control: boolean;
  // buttons are the device's own, shown beside it, in this order.
  buttons?: DeviceButton[];
  // aspect is the screen's width over its height until the stream says.
  aspect?: number;
  onState?: (state: StreamState) => void;
};

const buttonIcons = { back: Triangle, home: Circle, recents: Square, rotate: RotateCw } as const;

// DeviceStreamView shows a device stream (an Android emulator, an iOS
// Simulator): its video, fitted into the space it has and shaped like the
// screen, turned as the device turns, and, with control on, taps, swipes,
// scrolling and typing into it. The protocol is in protocol.ts.
export const DeviceStreamView = forwardRef<DeviceStreamHandle, Props>(function DeviceStreamView(
  { path, enabled, control, buttons = [], aspect = 1080 / 2400, onState },
  ref,
) {
  const t = useT();
  const box = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const screen = useRef<HTMLDivElement>(null);
  const session = useRef<DeviceStreamSession | null>(null);
  const [state, setState] = useState<StreamState>({ phase: 'connecting' });
  const [room, setRoom] = useState({ width: 0, height: 0 });
  const pointers = useRef(new Map<number, number>()); // pointer → finger
  const wheel = useRef<{ x: number; y: number; dx: number; dy: number } | null>(null);
  const onStateRef = useRef(onState);
  onStateRef.current = onState;

  useEffect(() => {
    const target = canvas.current;
    if (!enabled || !target) return;
    const s = new DeviceStreamSession(path, target, (next) => {
      setState(next);
      onStateRef.current?.(next);
    });
    session.current = s;
    return () => {
      session.current = null;
      s.close();
      const idle: StreamState = { phase: 'connecting' };
      setState(idle);
      onStateRef.current?.(idle);
    };
  }, [path, enabled]);

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const observer = new ResizeObserver(([entry]) => setRoom({ width: entry.contentRect.width, height: entry.contentRect.height }));
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    if (control) screen.current?.focus({ preventScroll: true });
  }, [control]);

  const send = (input: DeviceInput) => session.current?.send(input);
  useImperativeHandle(ref, () => ({ send }), []);

  const playing = state.phase === 'playing';
  const config = playing ? state.config : null;
  const size = fit(config?.width ?? aspect * 1000, config?.height ?? 1000, room.width - (buttons.length ? 56 : 0), room.height);

  const point = (e: { clientX: number; clientY: number }) => videoPoint(e.clientX, e.clientY, screen.current!.getBoundingClientRect());

  const finger = (pointerId: number) => {
    const used = new Set(pointers.current.values());
    let id = 0;
    while (used.has(id)) id++;
    pointers.current.set(pointerId, id);
    return id;
  };

  const onPointerDown = (e: PointerEvent<HTMLDivElement>) => {
    if (!control || !playing || (e.pointerType === 'mouse' && e.button !== 0)) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    e.currentTarget.focus({ preventScroll: true });
    send({ type: 'touch', action: 'down', ...point(e), id: finger(e.pointerId) });
  };
  const onPointerMove = (e: PointerEvent<HTMLDivElement>) => {
    const id = pointers.current.get(e.pointerId);
    if (id !== undefined) send({ type: 'touch', action: 'move', ...point(e), id });
  };
  const onPointerEnd = (e: PointerEvent<HTMLDivElement>) => {
    const id = pointers.current.get(e.pointerId);
    if (id === undefined) return;
    pointers.current.delete(e.pointerId);
    send({ type: 'touch', action: e.type === 'pointercancel' ? 'cancel' : 'up', ...point(e), id });
  };
  // A wheel sends at most once a frame, its clicks added up.
  const onWheel = (e: WheelEvent<HTMLDivElement>) => {
    if (!control || !playing) return;
    const at = point(e);
    const pending = wheel.current;
    const dx = wheelClicks(e.deltaX, e.deltaMode);
    const dy = wheelClicks(e.deltaY, e.deltaMode);
    if (pending) {
      Object.assign(pending, at, { dx: pending.dx + dx, dy: pending.dy + dy });
      return;
    }
    const next = { ...at, dx, dy };
    requestAnimationFrame(() => {
      wheel.current = null;
      send({ type: 'wheel', x: next.x, y: next.y, dx: next.dx, dy: next.dy });
    });
    wheel.current = next;
  };
  const onKey = (e: KeyboardEvent<HTMLDivElement>, action: 'down' | 'up') => {
    if (!control || !playing) return;
    // Your paste is the paste event's: it types your clipboard.
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'v') return;
    const input = keyInput(e, action);
    if (!input) return;
    e.preventDefault();
    send(input);
  };
  const onPaste = (e: ClipboardEvent<HTMLDivElement>) => {
    const text = e.clipboardData.getData('text/plain');
    if (!control || !playing || !text) return;
    e.preventDefault();
    send({ type: 'text', text });
  };

  return (
    <div ref={box} className="absolute inset-0 flex items-center justify-center gap-2">
      <div
        className={cn(
          'relative shrink-0 overflow-hidden rounded-[26px] bg-black shadow-[0_40px_120px_-30px_var(--ab-shadow-deep)] ring-1 ring-line-strong transition-[box-shadow]',
          control && playing && 'ring-2 ring-brand-400/60',
        )}
        style={{ width: size.width, height: size.height }}
      >
        <div
          ref={screen}
          tabIndex={control ? 0 : -1}
          className={cn('absolute inset-0 touch-none outline-none', control && playing ? 'cursor-pointer' : 'cursor-default')}
          data-device-stream={path}
          data-playing={playing}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerEnd}
          onPointerCancel={onPointerEnd}
          onWheel={onWheel}
          onKeyDown={(e) => onKey(e, 'down')}
          onKeyUp={(e) => onKey(e, 'up')}
          onPaste={onPaste}
          onContextMenu={(e) => e.preventDefault()}
        >
          <canvas ref={canvas} className={cn('size-full', !playing && 'invisible')} />
        </div>
        {!playing && (
          <div className="absolute inset-0 flex items-center justify-center p-4 text-center text-sm text-subtle">
            {state.phase === 'failed' ? (
              <span className="text-xs leading-relaxed">{state.reason}</span>
            ) : (
              <>
                <LoaderCircle className="mr-2 size-4 shrink-0 animate-spin" />
                {t('device.connecting')}
              </>
            )}
          </div>
        )}
      </div>
      {buttons.length > 0 && (
        <div className="flex shrink-0 flex-col gap-1 rounded-full border border-line bg-well p-1">
          {buttons.map((button) => {
            const Icon = buttonIcons[button];
            return (
              <Tip key={button} side="left" label={t(`device.button.${button}`)}>
                <button
                  type="button"
                  aria-label={t(`device.button.${button}`)}
                  disabled={!control || !playing}
                  onClick={() => send({ type: 'button', button })}
                  className="flex size-9 items-center justify-center rounded-full text-tertiary transition hover:bg-surface-raised hover:text-secondary disabled:pointer-events-none disabled:opacity-40"
                >
                  <Icon className={cn('size-4', button === 'back' && '-rotate-90', button !== 'rotate' && 'size-3.5')} />
                </button>
              </Tip>
            );
          })}
        </div>
      )}
    </div>
  );
});
