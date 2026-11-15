// What the app says about a chat session you aren't looking at (lib/sessions.ts'
// sessionAlerts): a permission request waiting in it, its chat stopping, or its
// turn ending with its agents done. Each is a toast, the chime, and an OS
// notification while the window is in the background, as the notification
// settings allow: needing you is a question's kind, stopping an error's, and a
// finished turn has a kind of its own, session. The dock or launcher icon
// counts what waits on you across every session. A question passed on to you
// notifies on its own (EventNotification), so it makes no session notice.
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { t } from '../../shared/i18n/index.ts';
import { api } from './api.ts';
import { onSessionStatus } from './events.ts';
import { mayNotify, type Kind } from './notifyPrefs.ts';
import { projectLabel } from './projectName.ts';
import { playReadySound } from './reviewAlert.ts';
import { isSessionShown, needsYouCount, sendSeenWith, sessionAlerts, type SessionAlert } from './sessions.ts';

const kindOf: Record<SessionAlert['kind'], Kind> = { needsYou: 'question', failed: 'error', done: 'session' };

// alertText is what a notice about a session says.
export function alertText(a: SessionAlert, projectName: string): { title: string; body: string } {
  const vars = { topic: a.session.topic, project: projectName };
  return { title: t(`sessions.notice.${a.kind}.title`, vars), body: t(`sessions.notice.${a.kind}.body`, vars) };
}

// badgeCount is what waits on you in every session the app has read.
export function badgeCount(lists: readonly (readonly T.LeadSession[] | undefined)[]): number {
  return lists.reduce((n, list) => n + (list ?? []).reduce((m, s) => m + needsYouCount(s), 0), 0);
}

export function useSessionAlerts(open: (project: string, session: string) => void): void {
  const queryClient = useQueryClient();
  const openRef = useRef(open);
  openRef.current = open;

  // Seen here is seen everywhere.
  useEffect(() => {
    sendSeenWith((project, session, at) => void api.seeLeadSession(project, session, at).catch(() => undefined));
    return () => sendSeenWith(() => undefined);
  }, []);

  useEffect(
    () =>
      onSessionStatus((project, prev, next) => {
        for (const a of sessionAlerts(prev, next)) {
          if (isSessionShown(project, a.session.id) && document.hasFocus()) continue;
          const kind = kindOf[a.kind];
          const { title, body } = alertText(a, projectLabel(project, queryClient.getQueryData<T.Project[]>(['projects'])));
          const id = `session:${a.session.ref}:${a.kind}:${Date.now()}`;
          const go = () => openRef.current(project, a.session.id);
          toast(title, { id, description: body, duration: 10_000, action: { label: t('sessions.notice.open'), onClick: go } });
          if (mayNotify(kind, 'sound')) playReadySound();
          if (mayNotify(kind, 'system')) {
            const off = window.agentbox.onNotificationClick((clicked) => {
              if (clicked !== id) return;
              off();
              toast.dismiss(id);
              go();
            });
            void window.agentbox.notify({ id, title, body }).then((shown) => {
              if (!shown) off();
            });
          }
        }
      }),
    [queryClient],
  );

  // The badge follows every project's sessions list as the cache holds it.
  useEffect(() => {
    let last = -1;
    const update = () => {
      const lists = queryClient
        .getQueryCache()
        .findAll({ queryKey: ['leadSessions'] })
        .filter((q) => q.queryKey.length === 2)
        .map((q) => q.state.data as T.LeadSession[] | undefined);
      const n = badgeCount(lists);
      if (n !== last) {
        last = n;
        window.agentbox.badge?.(n);
      }
    };
    update();
    return queryClient.getQueryCache().subscribe(update);
  }, [queryClient]);
}
