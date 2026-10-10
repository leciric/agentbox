import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Globe, Search } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { MemoryRow } from './ProjectMemoryPanel';
import { EmptyState, Notice, Panel } from './ui/card';
import { Input } from './ui/input';

// GlobalMemoryPanel is Settings → Memory and the Home chat's Memory tab: AgentBox-wide memory, what the user
// asked a chat to keep for every project (memory.Global). Every project's
// searches and contexts read these beside its own, so this is where one that
// no longer holds goes for good. Nothing is added here: a chat writes them,
// when told something applies to all projects, in the user's words.
export function GlobalMemoryPanel() {
  const t = useT();
  const queryClient = useQueryClient();
  const memories = useQuery({ queryKey: ['globalMemories'], queryFn: api.globalMemories });
  const [deleting, setDeleting] = useState<T.Memory | null>(null);
  const [filter, setFilter] = useState('');
  const all = memories.data ?? [];
  // Few enough to filter here, by every word anywhere in the title or text.
  const words = filter.toLowerCase().split(/\s+/).filter(Boolean);
  const items = all.filter((m) => words.every((w) => `${m.title}\n${m.content}`.toLowerCase().includes(w)));

  return (
    <div className="grid gap-2" data-global-memories>
      {memories.error && <Notice>{errorMessage(memories.error)}</Notice>}
      {memories.isPending && <p className="px-1 text-[13px] text-subtle">{t('common.loading')}</p>}
      {all.length > 0 && (
        <div className="relative">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-subtle" />
          <Input aria-label={t('memory.search.label')} className="pl-9" placeholder={t('memory.search.placeholder')} value={filter} onChange={(e) => setFilter(e.target.value)} />
        </div>
      )}
      {all.length > 0 && items.length === 0 && <p className="px-1 text-[13px] text-subtle">{t('memory.global.noMatch')}</p>}
      {!memories.isPending && !memories.error && all.length === 0 && (
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
