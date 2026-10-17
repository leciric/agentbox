import type { ReactNode } from 'react';
import type * as T from '../../../shared/api';

// standIn draws an agent's desktop in place of its display (LiveDesktop), for
// the dev preview, which has no machines to stream (dev/pip.tsx). A module of
// its own, so the preview sets it before anything reaches for the streams.
export let standIn: ((agent: T.Agent) => ReactNode) | null = null;
export function setDesktopStandIn(draw: (agent: T.Agent) => ReactNode): void {
  standIn = draw;
}
