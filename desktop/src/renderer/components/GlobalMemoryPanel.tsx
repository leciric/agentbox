import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Globe } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { MemoryRow } from './ProjectMemoryPanel';
import { EmptyState, Notice, Panel } from './ui/card';

// GlobalMemoryPanel is Settings → Memory: AgentBox-wide memory, what the user
// asked a chat to keep for every project (memory.Global). Every project's
// searches and contexts read these beside its own, so this is where one that
// no longer holds goes for good. Nothing is added here: a chat writes them,
// when told something applies to all projects, in the user's words.
export function GlobalMemoryPanel() {
  const t = useT();
  const queryClient = useQueryClient();
  const memories = useQuery({ queryKey: ['globalMemories'], queryFn: api.globalMemories });
  const [deleting, setDeleting] = useState<T.Memory | null>(null);
  const items = memories.data ?? [];

  return (
    <div className="grid gap-2" data-global-memories>
      {memories.error && <Notice>{errorMessage(memories.error)}</Notice>}
      {memories.isPending && <p className="px-1 text-[13px] text-subtle">{t('common.loading')}</p>}
      {!memories.isPending && !memories.error && items.length === 0 && (
        <Panel className="rounded-2xl">
          <EmptyState icon={Globe} title={t('memory.global.empty.title')}>
            {t('memory.global.empty.body')}
          </EmptyState>
        </Panel>
      )}
      {items.map((memory) => (
        <MemoryRow key={memory.id} memory={memory} onDelete={() => setDeleting(memory)} />
      ))}
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('memory.global.deleteTitle', { title: deleting?.title ?? '' })}
        description={t('memory.global.deleteDescription')}
        confirmLabel={t('memory.global.delete')}
        destructive
        onConfirm={async () => {
          if (!deleting) return;
          await api.deleteGlobalMemory(deleting.id);
          toast(t('memory.global.deletedToast', { title: deleting.title }));
          await queryClient.invalidateQueries({ queryKey: ['globalMemories'] });
        }}
      />
    </div>
  );
}
