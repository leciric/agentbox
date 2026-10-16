// useSidebarLayout is v2's sidebar's hidden projects as the daemon keeps
// them (sidebarHidden.ts), and save, which shows a change at once and
// then stores it: the sidebar doesn't wait on the round trip to move a row.
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from './api';
import { useT } from './i18n';
import { emptyLayout } from './sidebarHidden';
import { errorMessage } from './utils';

const key = ['sidebarLayout'];

export function useSidebarLayout(): { layout: T.SidebarLayout; save: (next: T.SidebarLayout) => void } {
  const t = useT();
  const queryClient = useQueryClient();
  const query = useQuery({ queryKey: key, queryFn: api.sidebarLayout, staleTime: 60_000 });
  const save = (next: T.SidebarLayout) => {
    queryClient.setQueryData(key, next);
    api
      .setSidebarLayout(next)
      .then((kept) => queryClient.setQueryData(key, kept))
      .catch((err) => {
        toast.error(t('v2.sidebar.hideFailed'), { description: errorMessage(err) });
        void queryClient.invalidateQueries({ queryKey: key });
      });
  };
  return { layout: query.data ?? emptyLayout, save };
}

