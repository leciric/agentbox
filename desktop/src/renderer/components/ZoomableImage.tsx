import { Maximize, Minus, Plus } from 'lucide-react';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useT } from '../lib/i18n';
import { Button } from './ui/button';
import { Tip } from './ui/tooltip';

const MAX_ACTUAL = 8; // the most it zooms in, as a multiple of the picture's own pixels
const STEP = 1.25;

type View = { scale: number; x: number; y: number };
const FIT: View = { scale: 1, x: 0, y: 0 };

// ZoomableImage shows a picture fitted to its box and lets the wheel, a pinch,
// double-click, the buttons and + / − / 0 zoom it. `scale` is relative to the
// fitted size (1 = Fit); x and y are the pan in pixels from the centre.
export function ZoomableImage({ src, alt }: { src: string; alt: string }) {
  const t = useT();
  const box = useRef<HTMLDivElement>(null);
  const img = useRef<HTMLImageElement>(null);
  const [view, setView] = useState<View>(FIT);
  const [dragging, setDragging] = useState(false);
  const drag = useRef<{ x: number; y: number; from: View } | null>(null);
  const viewRef = useRef(view);
  viewRef.current = view;

  // Sizes the maths needs: the fitted picture (unaffected by the transform) and the box.
  const sizes = useCallback(() => {
    const b = box.current!;
    const i = img.current!;
    const fitW = i.offsetWidth || 1;
    return { bw: b.clientWidth, bh: b.clientHeight, fitW, fitH: i.offsetHeight || 1, ratio: fitW / (i.naturalWidth || fitW) };
  }, []);

  const clampTo = useCallback(
    (scale: number, x: number, y: number): View => {
      const { bw, bh, fitW, fitH, ratio } = sizes();
      const max = Math.max(MAX_ACTUAL / ratio, 2);
      const s = Math.min(max, Math.max(1, scale));
      const lx = Math.max(0, (fitW * s - bw) / 2);
      const ly = Math.max(0, (fitH * s - bh) / 2);
      return { scale: s, x: s === 1 ? 0 : Math.min(lx, Math.max(-lx, x)), y: s === 1 ? 0 : Math.min(ly, Math.max(-ly, y)) };
    },
    [sizes],
  );

  // zoomAt sets a new scale keeping the point under (px, py), in pixels from the box's centre, still.
  const zoomAt = useCallback(
    (scale: number, px: number, py: number) => {
      const v = viewRef.current;
      const k = Math.min(Math.max(scale, 1), 1e6) / v.scale;
      setView(clampTo(scale, px - (px - v.x) * k, py - (py - v.y) * k));
    },
    [clampTo],
  );

  const centre = useCallback((event: { clientX: number; clientY: number }) => {
    const r = box.current!.getBoundingClientRect();
    return { px: event.clientX - r.left - r.width / 2, py: event.clientY - r.top - r.height / 2 };
  }, []);

  // React's onWheel is passive, so the page would scroll: listen natively.
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const onWheel = (event: WheelEvent) => {
      event.preventDefault();
      const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? 400 : 1;
      const delta = Math.max(-100, Math.min(100, event.deltaY * unit));
      const { px, py } = centre(event);
      zoomAt(viewRef.current.scale * Math.exp(-delta * (event.ctrlKey ? 0.01 : 0.002)), px, py);
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => el.removeEventListener('wheel', onWheel);
  }, [centre, zoomAt]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement;
      if (event.metaKey || event.ctrlKey || event.altKey || target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'VIDEO') return;
      if (event.key === '+' || event.key === '=') zoomAt(viewRef.current.scale * STEP, 0, 0);
      else if (event.key === '-' || event.key === '_') zoomAt(viewRef.current.scale / STEP, 0, 0);
      else if (event.key === '0') setView(FIT);
      else return;
      event.preventDefault();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [zoomAt]);

  const toggle = (event: React.MouseEvent) => {
    const { ratio } = sizes();
    const actual = 1 / ratio;
    const { px, py } = centre(event);
    if (view.scale > 1.001) setView(FIT);
    else zoomAt(actual > 1.001 ? actual : 2 * actual, px, py);
  };

  const percent = Math.round(view.scale * (img.current ? sizes().ratio : 1) * 100);

  return (
    <div className="relative h-[70vh] w-full select-none">
      <div
        ref={box}
        className={`flex size-full items-center justify-center overflow-hidden ${dragging ? 'cursor-grabbing' : view.scale > 1 ? 'cursor-grab' : 'cursor-zoom-in'}`}
        style={{ touchAction: 'none' }}
        onDoubleClick={toggle}
        onPointerDown={(event) => {
          if (event.button !== 0 || viewRef.current.scale <= 1) return;
          event.currentTarget.setPointerCapture(event.pointerId);
          drag.current = { x: event.clientX, y: event.clientY, from: viewRef.current };
          setDragging(true);
        }}
        onPointerMove={(event) => {
          const d = drag.current;
          if (d) setView(clampTo(d.from.scale, d.from.x + event.clientX - d.x, d.from.y + event.clientY - d.y));
        }}
        onPointerUp={() => {
          drag.current = null;
          setDragging(false);
        }}
        onPointerCancel={() => {
          drag.current = null;
          setDragging(false);
        }}
      >
        <img
          ref={img}
          src={src}
          alt={alt}
          draggable={false}
          onLoad={() => setView((v) => ({ ...v }))}
          className="max-h-full max-w-full object-contain will-change-transform"
          style={{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.scale})` }}
        />
      </div>
      <div className="absolute bottom-3 right-3 flex items-center gap-0.5 rounded-lg border border-line bg-surface/90 p-0.5 shadow-md backdrop-blur">
        <Tip label={t('agent.mediaTab.zoomOut')}>
          <Button size="icon-sm" variant="ghost" aria-label={t('agent.mediaTab.zoomOut')} disabled={view.scale <= 1} onClick={() => zoomAt(view.scale / STEP, 0, 0)}>
            <Minus />
          </Button>
        </Tip>
        <span className="w-11 text-center text-xs tabular-nums text-secondary">{percent}%</span>
        <Tip label={t('agent.mediaTab.zoomIn')}>
          <Button size="icon-sm" variant="ghost" aria-label={t('agent.mediaTab.zoomIn')} onClick={() => zoomAt(view.scale * STEP, 0, 0)}>
            <Plus />
          </Button>
        </Tip>
        <Tip label={t('agent.mediaTab.zoomFit')}>
          <Button size="sm" variant="ghost" aria-label={t('agent.mediaTab.zoomFit')} disabled={view.scale <= 1} onClick={() => setView(FIT)}>
            <Maximize />
            {t('agent.mediaTab.zoomFit')}
          </Button>
        </Tip>
      </div>
    </div>
  );
}
