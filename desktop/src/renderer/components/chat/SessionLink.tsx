import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';

type Rect = { boxRight: number; panelEdge: number; top: number; bottom: number; color: string };

// readLink is where the shown session's box (ProjectChatPanel) and its
// agents' bracket (v2/AgentList) are, when both are on screen.
function readLink(): Rect | undefined {
  const box = document.querySelector<HTMLElement>('[data-session-box]');
  const bracket = document.querySelector<HTMLElement>('[data-session-bracket]');
  if (!box || !bracket) return undefined;
  const b = box.getBoundingClientRect();
  const k = bracket.getBoundingClientRect();
  const panel = bracket.closest('[data-panel-column]')?.getBoundingClientRect();
  if (!panel || k.left < b.right || k.top < b.top || k.bottom > b.bottom) return undefined;
  return { boxRight: b.right, panelEdge: panel.left, top: k.top, bottom: k.bottom, color: box.dataset.sessionColor ?? '' };
}

// SessionLink joins the box of the session's chat to the bracket of its
// agents in the right-hand panel, which are in other columns: a gap in the
// box's right edge, lines across the gap and the panel's edge at the
// bracket's top and bottom, so the two read as one outline. It is drawn over
// the app, from where the two are measured, and nothing when either is gone.
export function SessionLink() {
  const [r, setR] = useState<Rect>();
  useEffect(() => {
    let raf = 0;
    let last = '';
    const tick = () => {
      const next = readLink();
      const key = next ? JSON.stringify(next) : '';
      if (key !== last) {
        last = key;
        setR(next);
      }
      raf = requestAnimationFrame(tick);
    };
    tick();
    return () => cancelAnimationFrame(raf);
  }, []);
  if (!r) return null;
  const x = r.boxRight - 1;
  const w = r.panelEdge + 1 - x;
  return createPortal(
    <div aria-hidden className="pointer-events-none fixed z-30" data-session-color={r.color} data-session-link style={{ left: x, top: r.top, width: w, height: r.bottom - r.top }}>
      {/* The gap: the box's right edge, the space and the panel's edge, wiped
          between the lines. */}
      <div className="absolute inset-y-px left-0 bg-ink" style={{ width: w - 1 }} />
      <div className="absolute inset-y-px right-0 w-px bg-sunken" />
      <div className="session-line absolute inset-x-0 top-0 border-t" />
      <div className="session-line absolute inset-x-0 bottom-0 border-b" />
    </div>,
    document.body,
  );
}
