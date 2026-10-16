// UI v2's sidebar's hidden projects (T.SidebarLayout, kept by the daemon so
// every window and the phone see the same): the edits, each a whole new
// layout for PUT /v1/sidebar, and the reading of it over projectOrder.ts's
// order. Nothing but the sidebar looks at it.
import type * as T from '../../shared/api';
import type { Ordered } from './projectOrder.ts';

export const emptyLayout: T.SidebarLayout = { hidden: [] };

export const hide = (l: T.SidebarLayout, name: string): T.SidebarLayout => ({ ...l, hidden: [...l.hidden.filter((n) => n !== name), name] });

export const unhide = (l: T.SidebarLayout, name: string): T.SidebarLayout => ({ ...l, hidden: l.hidden.filter((n) => n !== name) });

export type Arranged = Ordered & { hidden: T.Project[] };

// arrange takes the hidden projects out of the order's lists, Idle included,
// into their own at the bottom. One with something going on (active) stays
// in its place until it has none; a pin is never hidden.
export function arrange(order: Ordered, layout: T.SidebarLayout, active: ReadonlySet<string>): Arranged {
  const hiddenSet = new Set(layout.hidden);
  const isHidden = (p: T.Project) => hiddenSet.has(p.name) && !active.has(p.name);
  return {
    pinned: order.pinned,
    rest: order.rest.filter((p) => !isHidden(p)),
    idle: order.idle.filter((p) => !isHidden(p)),
    hidden: [...order.rest, ...order.idle].filter(isHidden),
  };
}
