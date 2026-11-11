import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Copy, GitFork } from 'lucide-react';
import type { ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../../shared/api';
import { api, sessionOfRef } from '../../lib/api';
import { forgetThread } from '../../lib/chat';
import { useT } from '../../lib/i18n';
import { requestSessionFocus } from '../../lib/newTask';
import { chooseSession, withSession } from '../../lib/sessions';
import { errorMessage } from '../../lib/utils';
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from '../ui/context-menu';

// useForkSession forks the session chatRef is into a new one at a message
// of its conversation (NewLeadSessionRequest.forkFrom), and shows the new
// one, its composer focused: it starts on this session's topic and settings,
// its chat a copy up to that message, and its lead is told that conversation
// with its first message. The agents stay with this session.
export function useForkSession(chatRef: string) {
  const queryClient = useQueryClient();
  const project = chatRef.split('/')[0];
  return useMutation({
    mutationFn: (item: string) => api.newLeadSession(project, { forkFrom: { session: sessionOfRef(chatRef) ?? '', item } }),
    onSuccess: (s: T.LeadSession) => {
      forgetThread(queryClient, s.ref);
      queryClient.setQueryData<T.LeadSession[]>(['leadSessions', project], (list) => withSession(list ?? [], s));
      chooseSession(project, s.id);
      requestSessionFocus(s.ref);
      void queryClient.invalidateQueries({ queryKey: ['leadSessions', project] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });
}

// MessageMenu is a message's right-click menu in a chat with sessions (a
// project's or the Home chat's): copy it, or fork a new session from it. In
// any other chat, and for a message not saved yet, it is the message alone.
export function MessageMenu({ chatRef, item, children }: { chatRef: string; item: T.ChatItem; children: ReactNode }) {
  if (sessionOfRef(chatRef) === undefined || item.streaming) return children;
  return (
    <SessionMessageMenu chatRef={chatRef} item={item}>
      {children}
    </SessionMessageMenu>
  );
}

function SessionMessageMenu({ chatRef, item, children }: { chatRef: string; item: T.ChatItem; children: ReactNode }) {
  const t = useT();
  const fork = useForkSession(chatRef);
  const copy = () => {
    // What is selected in it, or the whole message.
    const picked = window.getSelection()?.toString();
    void navigator.clipboard.writeText(picked || item.text || '');
  };
  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
      <ContextMenuContent data-message-menu>
        <ContextMenuItem icon={Copy} onSelect={copy}>
          {t('chat.message.copy')}
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem icon={GitFork} disabled={fork.isPending} onSelect={() => fork.mutate(item.id)} data-message-fork>
          {t('chat.message.fork')}
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}
