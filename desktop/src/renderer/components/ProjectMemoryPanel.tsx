import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Brain,
  CircleCheck,
  Coins,
  Ellipsis,
  FileText,
  FlaskConical,
  Gauge,
  Globe,
  GitCompare,
  GitMerge,
  History,
  Paperclip,
  Plus,
  Search as SearchIcon,
  Sparkles,
  Trash2,
  TriangleAlert,
  Users,
} from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import * as A from '../../shared/api';
import { formatTokens } from '../lib/chat';
import { api } from '../lib/api';
import { formatNumber, useT, type MessageKey, type Translate } from '../lib/i18n';
import { pendingReveal, useReveal, type MemorySection, type Reveal } from '../lib/reveal';
import { cn, errorMessage, humanBytes, timeAgo } from '../lib/utils';
import { Markdown } from './chat/Markdown';
import { FilterChip } from './MediaTab';
import { Sparkline } from './Sparkline';
import { Badge, type BadgeVariant } from './ui/badge';
import { Button } from './ui/button';
import { Card, EmptyState, Notice, Panel } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input, Textarea } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuTrigger } from './ui/menu';
import { Select, SelectOption } from './ui/select';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';

// ProjectMemoryPanel is the Memory section of a project's Settings tab: what a project knows, kept by AgentBox
// rather than by any one agent's conversation (docs/implementation/project-memory.md,
// D72). The project's chat already reads and writes all of this through its
// own MCP tools; this is the same store, for a human looking at it directly.
//
// Five things live here, and they stay five sections rather than one feed,
// because they answer different questions: working memory is what the
// project is doing *right now*, memories are judgements worth keeping,
// events are the raw history nothing decided to keep, search reaches all
// three at once, and reports are what an agent said as it finished.
export function ProjectMemoryPanel({ project, onOpenMedia }: { project: string; onOpenMedia: () => void }) {
  const t = useT();
  const [section, setSection] = useState<'memories' | 'search' | 'events' | 'reports' | 'context'>(() => revealedIn(pendingReveal(), project)?.section ?? 'memories');
  // A search result opened here, while the page was already open.
  const reveal = useReveal();
  useEffect(() => {
    const found = revealedIn(reveal, project);
    if (found) setSection(found.section);
  }, [reveal, project]);

  return (
    <div className="mx-auto grid max-w-4xl gap-5 px-4 py-6 md:px-8 md:py-7">
      <WorkingMemoryCard project={project} />

      <Tabs value={section} onValueChange={(value) => setSection(value as typeof section)}>
        <TabsList>
          <TabsTrigger value="memories">
            <Sparkles />
            {t('memory.tab.memories')}
          </TabsTrigger>
          <TabsTrigger value="search">
            <SearchIcon />
            {t('memory.tab.search')}
          </TabsTrigger>
          <TabsTrigger value="events">
            <History />
            {t('memory.tab.events')}
          </TabsTrigger>
          <TabsTrigger value="reports">
            <FileText />
            {t('memory.tab.reports')}
          </TabsTrigger>
          <TabsTrigger value="context">
            <Gauge />
            {t('memory.tab.context')}
          </TabsTrigger>
        </TabsList>

        <TabsContent value="memories" className="mt-4">
          <MemoriesSection project={project} />
        </TabsContent>
        <TabsContent value="search" className="mt-4">
          <MemorySearchSection project={project} />
        </TabsContent>
        <TabsContent value="events" className="mt-4">
          <MemoryEventsSection project={project} />
        </TabsContent>
        <TabsContent value="reports" className="mt-4">
          <MemoryReportsSection project={project} onOpenMedia={onOpenMedia} />
        </TabsContent>
        <TabsContent value="context" className="mt-4">
          <ContextSection project={project} />
        </TabsContent>
      </Tabs>
    </div>
  );
}

// revealedIn is what a search result asked this project's Memory to show.
function revealedIn(reveal: Reveal | null, project: string) {
  return reveal?.memory?.project === project ? reveal.memory : undefined;
}

// usePinned is the memory, event or report a search result opened, when the
// list it belongs on doesn't show it (an event older than the timeline, a
// memory of a kind filtered out): it is shown first, so it can be brought
// forward all the same.
function usePinned<I extends { id: string }>(project: string, section: MemorySection, shown: readonly I[]): I | undefined {
  const found = revealedIn(useReveal(), project);
  if (found?.section !== section || shown.some((item) => item.id === found.item.id)) return undefined;
  return found.item as unknown as I;
}

// --- Working memory --------------------------------------------------------

// WorkingMemoryCard is the one document here that is replaced rather than
// added to, so it is edited the way ProjectNotes edits the project's notes:
// a draft kept until you save it, sent as a merge patch. activeAgents isn't a
// field in the form — the daemon keeps it in step with an agent being made
// and retired (memoryevents.go), and a hand edit here would just be
// overwritten by the next one, so it is shown as a fact rather than offered
// as something to type into.
function WorkingMemoryCard({ project }: { project: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  const working = useQuery({ queryKey: ['memoryWorking', project], queryFn: () => api.memoryWorking(project) });
  const saved = working.data;
  const [draft, setDraft] = useState<{ goal: string; currentTask: string; blockers: string; notes: string } | null>(null);

  const fields = draft ?? {
    goal: saved?.goal ?? '',
    currentTask: saved?.currentTask ?? '',
    blockers: (saved?.blockers ?? []).join('\n'),
    notes: saved?.notes ?? '',
  };
  const dirty =
    draft !== null &&
    (draft.goal !== (saved?.goal ?? '') ||
      draft.currentTask !== (saved?.currentTask ?? '') ||
      draft.blockers !== (saved?.blockers ?? []).join('\n') ||
      draft.notes !== (saved?.notes ?? ''));

  const save = useMutation({
    mutationFn: () =>
      api.setMemoryWorking(project, {
        goal: fields.goal,
        currentTask: fields.currentTask,
        blockers: fields.blockers
          .split('\n')
          .map((line) => line.trim())
          .filter(Boolean),
        notes: fields.notes,
      } satisfies T.WorkingMemoryPatch),
    onSuccess: (updated) => {
      setDraft(null);
      queryClient.setQueryData(['memoryWorking', project], updated);
      toast(t('memory.working.saved'));
    },
  });

  return (
    <Card
      title={t('memory.working.title')}
      icon={Brain}
      description={t('memory.working.description')}
      action={
        <>
          <Button variant="ghost" size="sm" disabled={!dirty || save.isPending} onClick={() => setDraft(null)}>
            {t('memory.revert')}
          </Button>
          <Button size="sm" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? t('common.saving') : t('common.save')}
          </Button>
        </>
      }
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t('memory.working.goal')} htmlFor="memory-goal">
          <Input
            id="memory-goal"
            placeholder={t('memory.working.goalPlaceholder')}
            value={fields.goal}
            onChange={(event) => setDraft({ ...fields, goal: event.target.value })}
          />
        </Field>
        <Field label={t('memory.working.currentTask')} htmlFor="memory-task">
          <Input
            id="memory-task"
            placeholder={t('memory.working.currentTaskPlaceholder')}
            value={fields.currentTask}
            onChange={(event) => setDraft({ ...fields, currentTask: event.target.value })}
          />
        </Field>
      </div>
      <Field label={t('memory.working.blockers')} htmlFor="memory-blockers" hint={t('memory.working.blockersHint')}>
        <Textarea
          id="memory-blockers"
          className="mt-1.5 min-h-16 font-mono text-[12.5px]"
          placeholder={t('memory.working.blockersPlaceholder')}
          value={fields.blockers}
          onChange={(event) => setDraft({ ...fields, blockers: event.target.value })}
        />
      </Field>
      <Field label={t('memory.working.notes')} htmlFor="memory-notes">
        <Textarea
          id="memory-notes"
          className="mt-1.5 min-h-16"
          placeholder={t('memory.working.notesPlaceholder')}
          value={fields.notes}
          onChange={(event) => setDraft({ ...fields, notes: event.target.value })}
        />
      </Field>

      <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-line-faint pt-3">
        <span className="flex items-center gap-1.5 text-[11.5px] text-subtle">
          <Users className="size-3.5" />
          {t('memory.working.activeAgents')}
        </span>
        {(saved?.activeAgents ?? []).length === 0 ? (
          <span className="text-[11.5px] text-faint">{t('memory.working.none')}</span>
        ) : (
          (saved?.activeAgents ?? []).map((name) => (
            <Badge key={name} variant="brand">
              {name}
            </Badge>
          ))
        )}
        <span className="ml-auto text-[11px] text-faint">
          {dirty
            ? t('memory.working.unsaved')
            : working.isPending
              ? t('common.loading')
              : saved?.updatedAt
                ? t('memory.working.savedAgo', { when: timeAgo(saved.updatedAt) })
                : t('memory.working.neverSet')}
        </span>
      </div>
      {save.error && <Notice className="mt-3">{errorMessage(save.error)}</Notice>}
      {working.error && <Notice className="mt-3">{errorMessage(working.error)}</Notice>}
    </Card>
  );
}

// --- Memories ---------------------------------------------------------------

const allKinds = [A.MemoryKindProject, A.MemoryKindDecision, A.MemoryKindDiscovery, A.MemoryKindIssue, A.MemoryKindEpisodic];
const kindLabel: Record<string, MessageKey> = {
  [A.MemoryKindProject]: 'memory.kind.project',
  [A.MemoryKindDecision]: 'memory.kind.decision',
  [A.MemoryKindDiscovery]: 'memory.kind.discovery',
  [A.MemoryKindIssue]: 'memory.kind.issue',
  [A.MemoryKindEpisodic]: 'memory.kind.episodic',
};
const kindVariant: Record<string, BadgeVariant> = {
  [A.MemoryKindProject]: 'info',
  [A.MemoryKindDecision]: 'brand',
  [A.MemoryKindDiscovery]: 'success',
  [A.MemoryKindIssue]: 'warning',
  [A.MemoryKindEpisodic]: 'default',
};

function ImportanceDots({ value }: { value: number }) {
  const t = useT();
  return (
    <span className="flex items-center gap-0.5" title={t('memory.memories.importanceOf', { value })}>
      {[1, 2, 3, 4, 5].map((n) => (
        <span key={n} className={cn('size-1.5 rounded-full', n <= value ? 'bg-brand-400' : 'bg-surface-strong')} />
      ))}
    </span>
  );
}

// Superseded is what SupersedeMemory ([memory.go]) is: the newest live memory
// keeps supersedesId, the row it replaced stops coming back from Memories or
// Search, and nothing is deleted. But there's no route that reads the old row
// back — Memories and Search both leave it out on purpose, and there's no
// GET-by-id either — so this tab can't reach back further than its own
// session. Superseding a memory here keeps the row you replaced in view,
// behind the toggle, pointing at what replaced it; a memory the project's
// chat superseded before you opened this tab is just gone from the list,
// same as it would be without a toggle at all.
interface SupersededEntry {
  old: T.Memory;
  by: T.Memory;
}

function MemoriesSection({ project }: { project: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [kind, setKind] = useState('');
  const [showSuperseded, setShowSuperseded] = useState(false);
  const [showResolved, setShowResolved] = useState(false);
  const [adding, setAdding] = useState(false);
  const [superseding, setSuperseding] = useState<T.Memory | null>(null);
  const [superseded, setSuperseded] = useState<SupersededEntry[]>([]);
  const [resolving, setResolving] = useState<T.Memory | null>(null);
  const [resolved, setResolved] = useState<T.Memory[]>([]);
  const memories = useQuery({
    queryKey: ['memories', project, kind],
    queryFn: () => api.memories(project, kind ? [kind] : []),
  });

  const items = memories.data ?? [];
  const pinnedMemory = usePinned<T.Memory>(project, 'memories', items);
  const visibleSuperseded = showSuperseded ? superseded.filter((entry) => !kind || entry.old.kind === kind) : [];
  const visibleResolved = showResolved ? resolved.filter((memory) => !kind || memory.kind === kind) : [];
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['memories', project] });

  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center gap-1">
        <FilterChip active={!kind} count={items.length} onClick={() => setKind('')}>
          {t('memory.memories.allKinds')}
        </FilterChip>
        {allKinds.map((k) => (
          <FilterChip key={k} active={kind === k} count={items.filter((m) => m.kind === k).length} onClick={() => setKind(kind === k ? '' : k)}>
            {t(kindLabel[k])}
          </FilterChip>
        ))}
        <div className="ml-auto flex items-center gap-2">
          {resolved.length > 0 && (
            <button
              type="button"
              className="text-[12px] text-subtle underline-offset-2 hover:text-tertiary hover:underline"
              onClick={() => setShowResolved((v) => !v)}
            >
              {showResolved ? t('memory.memories.hideResolved') : t('memory.memories.showResolved', { count: resolved.length })}
            </button>
          )}
          {superseded.length > 0 && (
            <button
              type="button"
              className="text-[12px] text-subtle underline-offset-2 hover:text-tertiary hover:underline"
              onClick={() => setShowSuperseded((v) => !v)}
            >
              {showSuperseded ? t('memory.memories.hideSuperseded') : t('memory.memories.showSuperseded', { count: superseded.length })}
            </button>
          )}
          <Button size="sm" onClick={() => setAdding(true)}>
            <Plus />
            {t('memory.memories.add')}
          </Button>
        </div>
      </div>

      {memories.error && <Notice>{errorMessage(memories.error)}</Notice>}
      {memories.isPending && <p className="px-1 text-[13px] text-subtle">{t('common.loading')}</p>}
      {!memories.isPending && items.length === 0 && visibleSuperseded.length === 0 && visibleResolved.length === 0 && (
        <Panel className="rounded-2xl">
          <EmptyState icon={Sparkles} title={t('memory.memories.empty.title')}>
            {t('memory.memories.empty.body')}
          </EmptyState>
        </Panel>
      )}

      <div className="grid gap-2">
        {pinnedMemory && <MemoryRow key={`found-${pinnedMemory.id}`} memory={pinnedMemory} />}
        {items.map((memory) => (
          <MemoryRow
            key={memory.id}
            memory={memory}
            onSupersede={() => setSuperseding(memory)}
            onResolve={memory.kind === A.MemoryKindIssue ? () => setResolving(memory) : undefined}
          />
        ))}
        {visibleSuperseded.map((entry) => (
          <MemoryRow key={`superseded-${entry.old.id}`} memory={entry.old} supersededBy={entry.by} />
        ))}
        {visibleResolved.map((memory) => (
          <MemoryRow key={`resolved-${memory.id}`} memory={memory} />
        ))}
      </div>

      <AddMemoryDialog
        project={project}
        open={adding}
        onOpenChange={setAdding}
        onAdded={async () => {
          await refresh();
        }}
      />
      <AddMemoryDialog
        project={project}
        open={superseding !== null}
        onOpenChange={(open) => !open && setSuperseding(null)}
        supersedes={superseding ?? undefined}
        onAdded={async (created) => {
          if (superseding) setSuperseded((prev) => [...prev, { old: superseding, by: created }]);
          await refresh();
        }}
      />
      {resolving && (
        <ResolveIssueDialog
          project={project}
          issue={resolving}
          onOpenChange={(open) => !open && setResolving(null)}
          onResolved={async (updated) => {
            setResolved((prev) => [...prev, updated]);
            await refresh();
          }}
        />
      )}
    </div>
  );
}

// MemoryRow is one memory, a project's or AgentBox-wide (global, which a
// project's search returns beside its own and Settings → Memory lists).
export function MemoryRow({
  memory,
  supersededBy,
  onSupersede,
  onResolve,
  onDelete,
}: {
  memory: T.Memory;
  supersededBy?: T.Memory;
  onSupersede?: () => void;
  onResolve?: () => void;
  onDelete?: () => void;
}) {
  const t = useT();
  const resolved = Boolean(memory.resolvedAt);
  const dimmed = Boolean(supersededBy) || resolved;
  return (
    <div
      className={cn('panel rounded-2xl px-4 py-3', dimmed && 'opacity-60')}
      data-memory={memory.id}
      data-memory-superseded={supersededBy ? true : undefined}
      data-memory-resolved={resolved ? true : undefined}
    >
      <div className="flex flex-wrap items-start gap-2">
        <Badge variant={kindVariant[memory.kind] ?? 'default'}>{kindLabel[memory.kind] ? t(kindLabel[memory.kind]) : memory.kind}</Badge>
        <h4 className="min-w-0 flex-1 text-[13.5px] font-medium text-primary">{memory.title}</h4>
        {memory.global && !onDelete && (
          <Badge variant="info" title={t('memory.global.badgeTip')}>
            <Globe />
            {t('memory.global.badge')}
          </Badge>
        )}
        {resolved && (
          <Badge variant="success">
            <CircleCheck />
            {t('memory.memories.resolved')}
          </Badge>
        )}
        <ImportanceDots value={memory.importance} />
        {(onSupersede || onResolve) && (
          <Menu>
            <MenuTrigger asChild>
              <Button size="icon-sm" variant="ghost" aria-label={t('memory.memories.actions')}>
                <Ellipsis />
              </Button>
            </MenuTrigger>
            <MenuContent>
              {onResolve && <MenuItem onSelect={onResolve}>{t('memory.memories.resolveMenu')}</MenuItem>}
              {onSupersede && <MenuItem onSelect={onSupersede}>{t('memory.memories.supersedeMenu')}</MenuItem>}
            </MenuContent>
          </Menu>
        )}
        {onDelete && (
          <Button size="icon-sm" variant="ghost" aria-label={t('memory.global.delete')} title={t('memory.global.delete')} onClick={onDelete}>
            <Trash2 />
          </Button>
        )}
      </div>
      {memory.content && <Markdown text={memory.content} className="mt-1.5 text-[12.5px] leading-relaxed text-muted" />}
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-subtle">
        <span>{timeAgo(memory.createdAt)}</span>
        {memory.global && memory.origin && (
          <span>{memory.origin === A.HomeProject ? t('memory.global.fromHome') : t('memory.global.from', { project: memory.origin })}</span>
        )}
        {memory.updatedAt !== memory.createdAt && <span>{t('memory.updatedAgo', { when: timeAgo(memory.updatedAt) })}</span>}
        {memory.supersedesId && <span className="font-mono text-faint">{t('memory.memories.supersedes', { id: memory.supersedesId })}</span>}
        {supersededBy && <span className="text-amber-300/80">{t('memory.memories.supersededBy', { title: supersededBy.title })}</span>}
        {resolved && <span className="text-emerald-300/80">{t('memory.memories.resolvedBy', { why: memory.resolvedBy ?? '' })}</span>}
      </div>
    </div>
  );
}

// ResolveIssueDialog is resolve_memory's click (ResolveMemory, D76): an issue
// that is simply over, with nothing to put in its place, so unlike Supersede
// there's no new memory to write — only the line that says what happened.
// GET …/memory/memories leaves a resolved row out exactly as it does a
// superseded one, so the row this closes is tracked locally afterwards
// (`resolved` in MemoriesSection) the same way a superseded row already is —
// this tab can't reach back past its own session either way.
function ResolveIssueDialog({
  project,
  issue,
  onOpenChange,
  onResolved,
}: {
  project: string;
  issue: T.Memory;
  onOpenChange: (open: boolean) => void;
  onResolved: (resolved: T.Memory) => Promise<unknown>;
}) {
  const t = useT();
  const [why, setWhy] = useState('');
  const resolve = useMutation({
    mutationFn: () => api.resolveMemory(project, { id: issue.id, why: why.trim() } satisfies T.ResolveMemoryRequest),
    onSuccess: async (updated) => {
      onOpenChange(false);
      toast(t('memory.memories.resolvedToast', { title: updated.title }));
      await onResolved(updated);
    },
  });

  return (
    <Dialog open onOpenChange={(next) => { if (!next) resolve.reset(); onOpenChange(next); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('memory.memories.resolve.title', { title: issue.title })}</DialogTitle>
          <DialogDescription>{t('memory.memories.resolve.description')}</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (why.trim()) resolve.mutate();
          }}
        >
          <Field label={t('memory.memories.resolve.how')} htmlFor="resolve-why" hint={t('memory.memories.resolve.hint')}>
            <Textarea
              id="resolve-why"
              autoFocus
              className="min-h-20"
              value={why}
              onChange={(event) => setWhy(event.target.value)}
              placeholder={t('memory.memories.resolve.placeholder')}
            />
          </Field>
          {resolve.error && <Notice>{errorMessage(resolve.error)}</Notice>}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={!why.trim() || resolve.isPending}>
              {resolve.isPending ? t('memory.memories.resolve.resolving') : t('memory.memories.resolve.resolve')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function AddMemoryDialog({
  project,
  open,
  onOpenChange,
  supersedes,
  prefillFrom,
  onAdded,
}: {
  project: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  supersedes?: T.Memory;
  // prefillFrom starts the form from another memory's fields rather than
  // blank — the duplicate pairs card uses it to start "supersede B" from A's
  // title and content, since the two are judged near-duplicates already.
  prefillFrom?: T.Memory;
  onAdded: (created: T.Memory) => Promise<unknown>;
}) {
  const t = useT();
  const [kind, setKind] = useState(prefillFrom?.kind ?? supersedes?.kind ?? A.MemoryKindProject);
  const [title, setTitle] = useState(prefillFrom?.title ?? '');
  const [content, setContent] = useState(prefillFrom?.content ?? '');
  const [importance, setImportance] = useState(String(prefillFrom?.importance ?? supersedes?.importance ?? 3));
  const add = useMutation({
    mutationFn: () =>
      api.addMemory(project, {
        kind,
        title: title.trim(),
        content: content.trim() || undefined,
        importance: Number(importance),
        supersedesId: supersedes?.id,
      } satisfies T.AddMemoryRequest),
    onSuccess: async (created) => {
      setTitle('');
      setContent('');
      setImportance('3');
      onOpenChange(false);
      toast(supersedes ? t('memory.memories.supersededToast', { title: created.title, old: supersedes.title }) : t('memory.memories.rememberedToast', { title: created.title }));
      await onAdded(created);
    },
  });

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) add.reset();
        onOpenChange(next);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{supersedes ? t('memory.memories.supersede.title', { title: supersedes.title }) : t('memory.memories.add.title')}</DialogTitle>
          <DialogDescription>
            {supersedes ? t('memory.memories.supersede.description') : t('memory.memories.add.description')}
          </DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (title.trim()) add.mutate();
          }}
        >
          <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_auto]">
            <Field label={t('memory.memories.add.kind')} htmlFor="memory-kind">
              <Select id="memory-kind" value={kind} onChange={setKind}>
                {allKinds.map((k) => (
                  <SelectOption key={k} value={k}>
                    {t(kindLabel[k])}
                  </SelectOption>
                ))}
              </Select>
            </Field>
            <Field label={t('memory.memories.add.importance')} htmlFor="memory-importance">
              <Select id="memory-importance" className="w-32" value={importance} onChange={setImportance}>
                {[1, 2, 3, 4, 5].map((n) => (
                  <SelectOption key={n} value={String(n)}>
                    {n === 1 ? t('memory.memories.add.worthKnowing', { n }) : n === 5 ? t('memory.memories.add.essential', { n }) : n}
                  </SelectOption>
                ))}
              </Select>
            </Field>
          </div>
          <Field label={t('memory.memories.add.titleLabel')} htmlFor="memory-title" hint={t('memory.memories.add.titleHint')}>
            <Input id="memory-title" autoFocus value={title} onChange={(event) => setTitle(event.target.value)} placeholder={t('memory.memories.add.titlePlaceholder')} />
          </Field>
          <Field label={t('memory.memories.add.content')} htmlFor="memory-content">
            <Textarea id="memory-content" className="min-h-28" value={content} onChange={(event) => setContent(event.target.value)} placeholder={t('memory.memories.add.contentPlaceholder')} />
          </Field>
          {add.error && <Notice>{errorMessage(add.error)}</Notice>}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={!title.trim() || add.isPending}>
              {add.isPending ? t('common.saving') : supersedes ? t('memory.memories.supersede.submit') : t('memory.memories.add.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// --- Search -----------------------------------------------------------------

// MemorySearchSection is one query against all three FTS5 indexes at once
// (search.go). The three answers are shown as three groups rather than one
// merged list because bm25 only ranks within one index — a memory's score
// and an event's score aren't the same currency.
function MemorySearchSection({ project }: { project: string }) {
  const t = useT();
  const [query, setQuery] = useState('');
  const search = useMutation({
    mutationFn: (q: string) => api.searchMemory(project, { query: q } satisfies T.MemorySearchRequest),
  });

  return (
    <div className="grid gap-4">
      <form
        className="flex gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (query.trim()) search.mutate(query.trim());
        }}
      >
        <Input
          aria-label={t('memory.search.label')}
          className="flex-1"
          placeholder={t('memory.search.placeholder')}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <Button type="submit" disabled={!query.trim() || search.isPending}>
          {search.isPending ? t('memory.search.searching') : t('common.search')}
          <SearchIcon />
        </Button>
      </form>

      {search.error && <Notice>{errorMessage(search.error)}</Notice>}

      {search.data && (
        <div className="grid gap-5">
          <SearchGroup title={t('memory.tab.memories')} count={search.data.memories?.length ?? 0}>
            {(search.data.memories ?? []).map((memory) => (
              <MemoryRow key={memory.id} memory={memory} />
            ))}
          </SearchGroup>
          <SearchGroup title={t('memory.tab.events')} count={search.data.events?.length ?? 0}>
            {(search.data.events ?? []).map((event) => (
              <EventRow key={event.id} event={event} />
            ))}
          </SearchGroup>
          <SearchGroup title={t('memory.tab.reports')} count={search.data.reports?.length ?? 0}>
            {(search.data.reports ?? []).map((report) => (
              <ReportRow key={report.id} report={report} />
            ))}
          </SearchGroup>
        </div>
      )}
    </div>
  );
}

function SearchGroup({ title, count, children }: { title: string; count: number; children: ReactNode }) {
  if (count === 0) return null;
  return (
    <div className="grid gap-2">
      <h3 className="px-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-subtle">
        {title} <span className="tabular-nums text-faint">{count}</span>
      </h3>
      <div className="grid gap-2">{children}</div>
    </div>
  );
}

// --- Events ------------------------------------------------------------------

const limitOptions = [50, 100, 200];

// MemoryEventsSection is the raw timeline (events.go): what the daemon
// captured on its own from an agent's lifecycle, a question, a merge, new
// media, and the lead's conversation being folded in — append-only, and
// newest first. It's the debugging view, so nothing here is summarised.
function MemoryEventsSection({ project }: { project: string }) {
  const t = useT();
  const [limit, setLimit] = useState(100);
  const [type, setType] = useState('');
  const [agent, setAgent] = useState('');
  const events = useQuery({ queryKey: ['memoryEvents', project, limit], queryFn: () => api.memoryEvents(project, { limit }) });

  const items = events.data ?? [];
  const types = new Map<string, number>();
  const agents = new Map<string, number>();
  for (const event of items) {
    types.set(event.type, (types.get(event.type) ?? 0) + 1);
    const a = event.agent ?? '';
    agents.set(a, (agents.get(a) ?? 0) + 1);
  }
  const visible = items.filter((event) => (!type || event.type === type) && (!agent || (event.agent ?? '') === agent));
  const pinnedEvent = usePinned<T.MemoryEvent>(project, 'events', visible);

  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center gap-1">
        <FilterChip active={!type} count={items.length} onClick={() => setType('')}>
          {t('memory.events.allTypes')}
        </FilterChip>
        {[...types].map(([name, count]) => (
          <FilterChip key={name} active={type === name} count={count} onClick={() => setType(type === name ? '' : name)}>
            {name}
          </FilterChip>
        ))}
      </div>
      {agents.size > 1 && (
        <div className="flex flex-wrap items-center gap-1">
          <FilterChip active={!agent} count={items.length} onClick={() => setAgent('')}>
            {t('memory.events.allAgents')}
          </FilterChip>
          {[...agents].map(([name, count]) => (
            <FilterChip key={name || t('memory.events.projectName')} active={agent === name} count={count} onClick={() => setAgent(agent === name ? '' : name)}>
              {name || t('memory.events.projectName')}
            </FilterChip>
          ))}
        </div>
      )}

      <div className="flex items-center justify-between px-1">
        <span className="text-[11.5px] text-subtle">
          {t('memory.events.newest', { count: items.length })}
          {items.length === limit && ` ${t('memory.events.atMost', { limit })}`}
        </span>
        <label className="flex items-center gap-2 text-[12px] text-subtle">
          {t('memory.events.show')}
          <Select className="h-7 w-24" value={String(limit)} onChange={(v) => setLimit(Number(v))}>
            {limitOptions.map((n) => (
              <SelectOption key={n} value={String(n)}>
                {n}
              </SelectOption>
            ))}
          </Select>
        </label>
      </div>

      {events.error && <Notice>{errorMessage(events.error)}</Notice>}
      {events.isPending && <p className="px-1 text-[13px] text-subtle">{t('common.loading')}</p>}
      {!events.isPending && items.length === 0 && (
        <Panel className="rounded-2xl">
          <EmptyState icon={History} title={t('memory.events.empty.title')}>
            {t('memory.events.empty.body')}
          </EmptyState>
        </Panel>
      )}
      {items.length > 0 && visible.length === 0 && <p className="px-1 text-[13px] text-subtle">{t('memory.noMatch')}</p>}

      <div className="grid gap-2">
        {pinnedEvent && <EventRow key={`found-${pinnedEvent.id}`} event={pinnedEvent} />}
        {visible.map((event) => (
          <EventRow key={event.id} event={event} />
        ))}
      </div>
    </div>
  );
}

function EventRow({ event }: { event: T.MemoryEvent }) {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  const payload = payloadText(event.payload);
  return (
    <div className="panel rounded-2xl px-4 py-3" data-event={event.id} data-event-type={event.type}>
      <div className="flex flex-wrap items-center gap-2">
        <Badge>{event.type}</Badge>
        {event.agent && <span className="text-[12px] text-muted">{event.agent}</span>}
        <span className="ml-auto text-[11.5px] text-subtle">{timeAgo(event.at)}</span>
      </div>
      {payload && (
        <button type="button" className="mt-1.5 text-[11px] text-subtle hover:text-tertiary" onClick={() => setExpanded((v) => !v)}>
          {expanded ? t('memory.events.hidePayload') : t('memory.events.showPayload')}
        </button>
      )}
      {expanded && payload && (
        <pre className="mt-1.5 max-h-48 overflow-auto rounded-lg border border-line-faint bg-sunken p-2.5 font-mono text-[11px] leading-relaxed text-muted">
          {payload}
        </pre>
      )}
    </div>
  );
}

function payloadText(payload: unknown): string {
  if (payload === undefined || payload === null) return '';
  try {
    const text = JSON.stringify(payload, null, 2);
    return text === '{}' || text === 'null' ? '' : text;
  } catch {
    return '';
  }
}

// --- Reports & artifacts ------------------------------------------------------

const reportStatusLabel: Record<string, MessageKey> = {
  [A.ReportDone]: 'memory.report.done',
  [A.ReportPartial]: 'memory.report.partial',
  blocked: 'memory.report.blocked',
  failed: 'memory.report.failed',
};

const statusVariant: Record<string, BadgeVariant> = {
  [A.ReportDone]: 'success',
  [A.ReportPartial]: 'warning',
  blocked: 'warning',
  failed: 'danger',
};

// MemoryReportsSection is what an agent said as it finished (report, in
// internal/cli/memory.go) and, below it, the artifacts agents recorded —
// references to something produced, never a copy. An artifact's path may
// point at something that no longer exists, so it's shown as a reference:
// the "media" ones open in the Media tab, which already knows how to show
// one, rather than this tab pretending it can open anything itself.
function MemoryReportsSection({ project, onOpenMedia }: { project: string; onOpenMedia: () => void }) {
  const t = useT();
  const reports = useQuery({ queryKey: ['memoryReports', project], queryFn: () => api.memoryReports(project) });
  const artifacts = useQuery({ queryKey: ['memoryArtifacts', project], queryFn: () => api.memoryArtifacts(project) });
  const pinnedReport = usePinned<T.AgentReport>(project, 'reports', reports.data ?? []);

  return (
    <div className="grid gap-6">
      <div className="grid gap-2">
        <h3 className="px-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-subtle">{t('memory.tab.reports')}</h3>
        {reports.error && <Notice>{errorMessage(reports.error)}</Notice>}
        {reports.isPending && <p className="px-1 text-[13px] text-subtle">{t('common.loading')}</p>}
        {!reports.isPending && (reports.data?.length ?? 0) === 0 && (
          <Panel className="rounded-2xl">
            <EmptyState icon={FileText} title={t('memory.report.empty.title')}>
              {t('memory.report.empty.body')}
            </EmptyState>
          </Panel>
        )}
        <div className="grid gap-2">
          {pinnedReport && <ReportRow key={`found-${pinnedReport.id}`} report={pinnedReport} />}
          {(reports.data ?? []).map((report) => (
            <ReportRow key={report.id} report={report} />
          ))}
        </div>
      </div>

      <div className="grid gap-2">
        <h3 className="px-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-subtle">{t('memory.report.artifacts')}</h3>
        {artifacts.error && <Notice>{errorMessage(artifacts.error)}</Notice>}
        {!artifacts.isPending && (artifacts.data?.length ?? 0) === 0 && <p className="px-1 text-[13px] text-subtle">{t('memory.report.noArtifacts')}</p>}
        <div className="grid gap-2">
          {(artifacts.data ?? []).map((artifact) => (
            <ArtifactRow key={artifact.id} artifact={artifact} onOpenMedia={onOpenMedia} />
          ))}
        </div>
      </div>
    </div>
  );
}

function ReportRow({ report }: { report: T.AgentReport }) {
  const t = useT();
  return (
    <div className="panel rounded-2xl px-4 py-3" data-report={report.id}>
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant={statusVariant[report.status] ?? 'default'}>{reportStatusLabel[report.status] ? t(reportStatusLabel[report.status]) : report.status}</Badge>
        <span className="text-[13px] font-medium text-primary">{report.agent}</span>
        {report.task && <span className="min-w-0 truncate text-[12px] text-subtle">— {report.task}</span>}
        <span className="ml-auto shrink-0 text-[11.5px] text-subtle">{timeAgo(report.createdAt)}</span>
      </div>
      <Markdown text={report.summary} className="mt-1.5 text-[12.5px] leading-relaxed text-tertiary" />
      <ReportList label={t('memory.report.discoveries')} items={report.discoveries} />
      <ReportList label={t('memory.report.decisions')} items={report.decisions} />
      <ReportList label={t('memory.report.remaining')} items={report.remainingIssues} tone="amber" />
    </div>
  );
}

function ReportList({ label, items, tone }: { label: string; items?: string[]; tone?: 'amber' }) {
  if (!items || items.length === 0) return null;
  return (
    <div className="mt-2">
      <p className="flex items-center gap-1.5 text-[10.5px] uppercase tracking-[0.07em] text-faint">
        {tone === 'amber' && <TriangleAlert className="size-3 text-amber-400/80" />}
        {label}
      </p>
      <ul className={cn('mt-1 list-disc space-y-0.5 pl-4 text-[12px] leading-relaxed', tone === 'amber' ? 'text-amber-200/80' : 'text-muted')}>
        {items.map((item, i) => (
          <li key={i}>{item}</li>
        ))}
      </ul>
    </div>
  );
}

function ArtifactRow({ artifact, onOpenMedia }: { artifact: T.MemoryArtifact; onOpenMedia: () => void }) {
  const t = useT();
  const isURL = artifact.type === 'url' || /^https?:\/\//.test(artifact.path);
  const isMedia = artifact.type === 'media';
  return (
    <div className="flex flex-wrap items-center gap-2.5 rounded-xl border border-line-faint bg-surface-faint px-3 py-2.5" data-artifact={artifact.id}>
      <Paperclip className="size-3.5 shrink-0 text-subtle" />
      <Badge>{artifact.type}</Badge>
      <span className="min-w-0 flex-1 truncate font-mono text-[12px] text-muted" title={artifact.path}>
        {artifact.path}
      </span>
      {artifact.agent && <span className="shrink-0 text-[11.5px] text-subtle">{artifact.agent}</span>}
      <span className="shrink-0 text-[11px] text-faint">{timeAgo(artifact.createdAt)}</span>
      {isMedia && (
        <Button size="sm" variant="ghost" onClick={onOpenMedia}>
          {t('memory.report.openInMedia')}
        </Button>
      )}
      {isURL && !isMedia && (
        <Button size="sm" variant="ghost" onClick={() => void window.agentbox.openExternal(artifact.path)}>
          {t('common.open')}
        </Button>
      )}
    </div>
  );
}

// --- Context: what memory costs, the budget, consolidation, a preview -------

// ContextSection is the "spending fewer tokens, not storing more" half of
// this tab (docs/implementation/project-memory.md, "The context builder" and
// "Consolidation"). Everything here reads what the builder already records —
// nothing is computed twice — and the budget control writes through the same
// PATCH `agentbox context-budget` uses.
function ContextSection({ project }: { project: string }) {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const current = projects.data?.find((p) => p.name === project);

  return (
    <div className="grid gap-4">
      <ContextCostCard project={project} current={current} />
      <ConsolidationPassesCard project={project} />
      <DuplicatePairsCard project={project} />
      <ContextPreviewCard project={project} budget={current?.contextBudget} />
    </div>
  );
}

const contextForLabel: Record<string, MessageKey> = {
  [A.ContextForLead]: 'memory.context.for.lead',
  [A.ContextForAgent]: 'memory.context.for.agent',
  [A.ContextForTool]: 'memory.context.for.tool',
};

// describeRatio turns Stats.Ratio into a sentence rather than a bare number.
// Below 1 the context is a fraction of the corpus it was drawn from, which is
// the point of having a budget; at or above 1 a project that remembers almost
// nothing costs more in headings and labels than the handful of rows it draws
// from — the honest answer the builder gives rather than a bug.
function describeRatio(ratio: number, t: Translate): string {
  if (ratio >= 1) return t('memory.context.ratio.larger', { n: formatNumber(ratio, { minimumFractionDigits: 1, maximumFractionDigits: 1 }) });
  const factor = Math.round(1 / Math.max(ratio, 0.001));
  return factor >= 2 ? t('memory.context.ratio.smaller', { n: factor }) : t('memory.context.ratio.percent', { n: Math.round(ratio * 100) });
}

function ContextCostCard({ project, current }: { project: string; current?: T.Project }) {
  const t = useT();
  const queryClient = useQueryClient();
  const stats = useQuery({ queryKey: ['memoryContextStats', project], queryFn: () => api.memoryContextStats(project) });
  const account = stats.data;
  const latest = account?.recent?.[0];
  const trend = [...(account?.recent ?? [])].reverse().map((s) => s.tokens);

  const savedBudget = current?.contextBudget ?? 0;
  const [draft, setDraft] = useState<string | null>(null);
  const budgetValue = draft ?? (savedBudget ? String(savedBudget) : '');
  const dirty = draft !== null && draft !== (savedBudget ? String(savedBudget) : '');
  const save = useMutation({
    mutationFn: (contextBudget: number) => api.updateProject(project, { contextBudget }),
    onSuccess: async (updated) => {
      setDraft(null);
      toast(t('memory.context.budgetSaved', { name: updated.name, tokens: formatTokens(updated.contextBudget) }));
      await queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
    onError: (err) => toast.error(errorMessage(err)),
  });

  return (
    <Card
      title={t('memory.context.cost.title')}
      icon={Coins}
      description={t('memory.context.cost.description')}
    >
      {stats.error && <Notice>{errorMessage(stats.error)}</Notice>}
      {stats.isPending && <p className="text-[13px] text-subtle">{t('common.loading')}</p>}

      {!stats.isPending && !latest && (
        <p className="text-[13px] leading-relaxed text-subtle">
          {t('memory.context.cost.empty')}
        </p>
      )}

      {latest && (
        <div className="grid gap-3">
          <div className="flex flex-wrap items-end justify-between gap-3">
            <div>
              <div className="text-2xl font-semibold tracking-tight text-title">{describeRatio(latest.ratio, t)}</div>
              <p className="mt-0.5 text-[12px] text-subtle">
                {t('memory.context.cost.last', {
                  for: contextForLabel[latest.for] ? t(contextForLabel[latest.for]) : latest.for,
                  tokens: formatTokens(latest.tokens),
                  corpus: formatTokens(latest.corpusTokens),
                  when: timeAgo(latest.at),
                })}
              </p>
            </div>
            {trend.length >= 2 && <Sparkline values={trend} className="mb-1 shrink-0 text-brand-300" />}
          </div>

          <TokenMeter tokens={latest.tokens} budget={latest.budget} />

          {account && (
            <p className="text-[11.5px] text-faint">
              {t('memory.context.cost.totals', { builds: account.builds, tokens: formatTokens(account.tokens), dropped: account.droppedRows })}
            </p>
          )}
        </div>
      )}

      <div className="mt-4 flex flex-wrap items-end gap-3 border-t border-line-faint pt-3.5">
        <Field label={t('memory.context.budget')} htmlFor="context-budget" hint={t('memory.context.budgetHint')}>
          <Input
            id="context-budget"
            type="number"
            min={500}
            max={32000}
            step={100}
            className="w-40"
            value={budgetValue}
            onChange={(event) => setDraft(event.target.value)}
          />
        </Field>
        <Button size="sm" disabled={!dirty || !Number(budgetValue) || save.isPending} onClick={() => save.mutate(Number(budgetValue))}>
          {save.isPending ? t('common.saving') : t('common.save')}
        </Button>
        {dirty && (
          <Button variant="ghost" size="sm" disabled={save.isPending} onClick={() => setDraft(null)}>
            {t('memory.revert')}
          </Button>
        )}
        {save.error && <Notice className="w-full">{errorMessage(save.error)}</Notice>}
      </div>
    </Card>
  );
}

// TokenMeter is the budget as a bar: how much of it the last build spent.
// Amber past 85%, the threshold the chat's own context ring uses (ContextMeter
// in chat/Composer.tsx); a build over budget is clamped by the builder rather
// than refused, so this can still read past 100%.
function TokenMeter({ tokens, budget }: { tokens: number; budget: number }) {
  const t = useT();
  if (!budget) return null;
  const fraction = tokens / budget;
  const over = fraction > 1;
  return (
    <div className="grid gap-1">
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-surface-strong">
        <div
          className={cn('h-full rounded-full', over ? 'bg-rose-400' : fraction > 0.85 ? 'bg-amber-400' : 'bg-brand-400')}
          style={{ width: `${Math.round(Math.min(1, fraction) * 100)}%` }}
        />
      </div>
      <p className="text-[11px] text-subtle">
        {t(over ? 'memory.context.meterOver' : 'memory.context.meter', { tokens: formatTokens(tokens), budget: formatTokens(budget) })}
      </p>
    </div>
  );
}

// --- Consolidation ------------------------------------------------------------

const passKindLabel: Record<string, MessageKey> = {
  [A.ConsolidationMechanical]: 'memory.context.pass.mechanical',
  [A.ConsolidationDistill]: 'memory.context.pass.distill',
};

const passKindVariant: Record<string, BadgeVariant> = {
  [A.ConsolidationMechanical]: 'default',
  [A.ConsolidationDistill]: 'brand',
};

// ConsolidationPassesCard is what turns events into memories (D76): a free
// mechanical pass — string comparison, no model — beside a distillation that
// spends the project's chat. The table is what a person can use to tell the
// two apart and see that either is actually running; GET /consolidation
// already bounds it to the last ten passes, so there's nothing here to page.
function ConsolidationPassesCard({ project }: { project: string }) {
  const t = useT();
  const consolidation = useQuery({ queryKey: ['memoryConsolidation', project], queryFn: () => api.memoryConsolidation(project) });
  const data = consolidation.data;
  const passes = data?.recent ?? [];

  return (
    <Card
      title={t('memory.context.consolidation.title')}
      icon={GitMerge}
      description={t('memory.context.consolidation.description')}
    >
      {consolidation.error && <Notice>{errorMessage(consolidation.error)}</Notice>}
      {consolidation.isPending && <p className="text-[13px] text-subtle">{t('common.loading')}</p>}

      {data && (
        <div className="grid gap-3">
          <p className="text-[12.5px] leading-relaxed text-muted">
            {t('memory.context.consolidation.summary', { events: data.events, memories: data.memories, pending: data.pending, setting: data.setting })}
          </p>

          {passes.length === 0 ? (
            <p className="text-[13px] text-subtle">{t('memory.context.consolidation.none')}</p>
          ) : (
            <div className="overflow-x-auto rounded-xl border border-line-faint">
              <table className="w-full text-[12px]">
                <thead>
                  <tr className="border-b border-line text-left text-[10.5px] uppercase tracking-[0.06em] text-subtle">
                    <th className="py-1.5 pl-3 pr-2 font-medium">{t('memory.context.col.pass')}</th>
                    <th className="px-2 py-1.5 font-medium">{t('memory.context.col.when')}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t('memory.context.col.read')}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t('memory.context.col.written')}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t('memory.context.col.superseded')}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t('memory.context.col.resolved')}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t('memory.context.col.decayed')}</th>
                    <th className="px-2 py-1.5 text-right font-medium">{t('memory.context.col.duplicates')}</th>
                    <th className="py-1.5 pl-2 pr-3 text-right font-medium">{t('memory.context.col.cost')}</th>
                  </tr>
                </thead>
                <tbody>
                  {passes.map((pass) => (
                    <PassRow key={pass.id} pass={pass} />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </Card>
  );
}

function PassRow({ pass }: { pass: T.ConsolidationPass }) {
  const t = useT();
  return (
    <tr className="border-t border-line-faint first:border-t-0" data-pass={pass.id} data-pass-kind={pass.kind}>
      <td className="py-1.5 pl-3 pr-2 align-top">
        <Badge variant={passKindVariant[pass.kind] ?? 'default'}>{passKindLabel[pass.kind] ? t(passKindLabel[pass.kind]) : pass.kind}</Badge>
        {pass.error && <div className="mt-1 max-w-[13rem] text-[10.5px] leading-snug text-rose-300">{pass.error}</div>}
      </td>
      <td className="px-2 py-1.5 align-top whitespace-nowrap text-muted">{timeAgo(pass.at)}</td>
      <td className="px-2 py-1.5 text-right align-top tabular-nums text-tertiary">{pass.eventsRead}</td>
      <td className="px-2 py-1.5 text-right align-top tabular-nums text-tertiary">{pass.memoriesWritten}</td>
      <td className="px-2 py-1.5 text-right align-top tabular-nums text-tertiary">{pass.memoriesSuperseded}</td>
      <td className="px-2 py-1.5 text-right align-top tabular-nums text-tertiary">{pass.memoriesResolved}</td>
      <td className="px-2 py-1.5 text-right align-top tabular-nums text-tertiary">{pass.memoriesDecayed}</td>
      <td className="px-2 py-1.5 text-right align-top tabular-nums text-tertiary">{pass.duplicatesFound}</td>
      <td className="py-1.5 pl-2 pr-3 text-right align-top tabular-nums whitespace-nowrap text-muted">
        {pass.kind === A.ConsolidationMechanical ? (
          <span className="text-faint">{t('memory.context.pass.free')}</span>
        ) : (
          <>
            {humanBytes(pass.inputBytes)} → {humanBytes(pass.outputBytes)}
            <div className="text-[10.5px] text-faint">
              {pass.durationMs >= 1000 ? `${formatNumber(pass.durationMs / 1000, { minimumFractionDigits: 1, maximumFractionDigits: 1 })}s` : `${pass.durationMs}ms`}
              {/* Which model actually read the events (D78): a pass that isn't on the project's
                  consolidation model is one that fell back to the chat's own session. */}
              {pass.model ? ` · ${pass.model}` : ` · ${t('memory.context.pass.chatModel')}`}
            </div>
          </>
        )}
      </td>
    </tr>
  );
}

// --- Duplicate pairs -----------------------------------------------------------

// DuplicatePairsCard is the mechanical pass's other half (D76): near-duplicate
// candidates it finds and deliberately does not merge on its own, because two
// titles that rhyme aren't always the same fact — that judgement is a
// person's. GET …/memory/duplicates already reads both memories back and caps
// at MaxLimit, so there's nothing to page here that the route doesn't already
// bound.
//
// There is no route to say "these two aren't duplicates" without also
// superseding one of them — memory_duplicates has no flag for that, and the
// list is only ever rewritten whole by the next mechanical pass. So the only
// action here is supersede; a pair that genuinely isn't a duplicate stays
// listed until a later pass's own comparison drops it or the score changes.
function DuplicatePairsCard({ project }: { project: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  const duplicates = useQuery({ queryKey: ['memoryDuplicates', project], queryFn: () => api.memoryDuplicates(project) });
  const pairs = duplicates.data ?? [];
  const [choice, setChoice] = useState<{ keep: T.Memory; replace: T.Memory } | null>(null);

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ['memoryDuplicates', project] });
    await queryClient.invalidateQueries({ queryKey: ['memories', project] });
  };

  return (
    <Card
      title={t('memory.context.dup.title')}
      icon={GitCompare}
      description={t('memory.context.dup.description')}
    >
      {duplicates.error && <Notice>{errorMessage(duplicates.error)}</Notice>}
      {duplicates.isPending && <p className="text-[13px] text-subtle">{t('common.loading')}</p>}
      {!duplicates.isPending && pairs.length === 0 && <p className="text-[13px] text-subtle">{t('memory.context.dup.none')}</p>}

      <div className="grid gap-3">
        {pairs.map((pair) => (
          <DuplicatePairRow key={`${pair.memory.id}-${pair.of.id}`} pair={pair} onChoose={(keep, replace) => setChoice({ keep, replace })} />
        ))}
      </div>

      {choice && (
        <AddMemoryDialog
          project={project}
          open
          onOpenChange={(open) => !open && setChoice(null)}
          supersedes={choice.replace}
          prefillFrom={choice.keep}
          onAdded={async () => {
            setChoice(null);
            await refresh();
          }}
        />
      )}
    </Card>
  );
}

function DuplicatePairRow({ pair, onChoose }: { pair: T.MemoryDuplicate; onChoose: (keep: T.Memory, replace: T.Memory) => void }) {
  const t = useT();
  return (
    <div className="panel rounded-2xl px-4 py-3" data-duplicate={`${pair.memory.id}-${pair.of.id}`}>
      <div className="flex flex-wrap items-center gap-2 text-[11px] text-subtle">
        <Badge variant={kindVariant[pair.memory.kind] ?? 'default'}>{kindLabel[pair.memory.kind] ? t(kindLabel[pair.memory.kind]) : pair.memory.kind}</Badge>
        <span>{t('memory.context.dup.alike', { percent: Math.round(pair.similarity * 100) })}</span>
        <span className="ml-auto">{t('memory.context.dup.found', { when: timeAgo(pair.foundAt) })}</span>
      </div>
      <div className="mt-2 grid gap-2 sm:grid-cols-2">
        <DuplicateSide memory={pair.memory} onKeep={() => onChoose(pair.memory, pair.of)} />
        <DuplicateSide memory={pair.of} onKeep={() => onChoose(pair.of, pair.memory)} />
      </div>
    </div>
  );
}

function DuplicateSide({ memory, onKeep }: { memory: T.Memory; onKeep: () => void }) {
  const t = useT();
  return (
    <div className="grid gap-2 rounded-xl border border-line-faint bg-surface-faint p-3">
      <h4 className="text-[13px] font-medium text-primary">{memory.title}</h4>
      {memory.content && <Markdown text={memory.content} className="text-[12px] leading-relaxed text-muted" />}
      <div className="flex items-center justify-between gap-2 text-[11px] text-subtle">
        <span>{timeAgo(memory.createdAt)}</span>
        <Button size="sm" variant="ghost" onClick={onKeep}>
          {t('memory.context.dup.keep')}
        </Button>
      </div>
    </div>
  );
}

// --- Preview -------------------------------------------------------------------

// ContextPreviewCard is the debugging view: what POST /context would actually
// hand an agent for a query, against the project's real budget, without
// starting one. It's the same call a worker's brief and the lead's recap make
// (For: "tool"), so what it shows is real — including that it adds itself to
// the build accounting above.
function ContextPreviewCard({ project, budget }: { project: string; budget?: number }) {
  const t = useT();
  const [query, setQuery] = useState('');
  const preview = useMutation({
    mutationFn: (q: string) => api.memoryContext(project, { query: q, for: A.ContextForTool } satisfies T.ContextRequest),
  });
  const sections = preview.data?.sections ?? [];
  const droppedSections = preview.data?.stats.droppedSections ?? [];

  return (
    <Card
      title={t('memory.context.preview.title')}
      icon={FlaskConical}
      description={t('memory.context.preview.description')}
    >
      <form
        className="flex gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          preview.mutate(query.trim());
        }}
      >
        <Input
          aria-label={t('memory.context.preview.label')}
          className="flex-1"
          placeholder={t('memory.context.preview.placeholder')}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <Button type="submit" disabled={preview.isPending}>
          {preview.isPending ? t('memory.context.preview.building') : t('memory.context.preview.preview')}
          <FlaskConical />
        </Button>
      </form>
      {budget !== undefined && <p className="mt-2 text-[11.5px] text-subtle">{t('memory.context.preview.budget', { tokens: formatTokens(budget) })}</p>}

      {preview.error && <Notice className="mt-3">{errorMessage(preview.error)}</Notice>}

      {preview.data && (
        <div className="mt-4 grid gap-3">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12px] text-muted">
            <span>
              {t.rich('memory.context.preview.tokens', {
                used: <span className="text-primary">{formatTokens(preview.data.stats.tokens)}</span>,
                budget: formatTokens(preview.data.stats.budget),
              })}
            </span>
            <span>
              {t('memory.context.preview.rows', { rows: preview.data.stats.rows, considered: preview.data.stats.consideredRows, dropped: preview.data.stats.droppedRows })}
            </span>
            <span>{t('memory.context.preview.ratio', { ratio: formatNumber(preview.data.stats.ratio, { minimumFractionDigits: 2, maximumFractionDigits: 2 }) })}</span>
          </div>
          {preview.data.stats.truncated && (
            <Notice tone="warning">{t('memory.context.preview.truncated')}</Notice>
          )}
          {droppedSections.length > 0 && <p className="text-[11.5px] text-subtle">{t('memory.context.preview.dropped', { list: droppedSections.join(', ') })}</p>}
          {sections.length > 0 && (
            <div className="flex flex-wrap gap-1.5">
              {sections.map((section) => (
                <Badge key={section.kind}>
                  {section.title} · {t('memory.context.preview.sectionRows', { count: section.rows })} · {formatTokens(section.tokens)}
                </Badge>
              ))}
            </div>
          )}
          <Markdown
            text={preview.data.text}
            className="max-h-96 overflow-auto rounded-xl border border-line-faint bg-rail p-3.5 text-[12.5px] leading-relaxed text-tertiary"
          />
        </div>
      )}
    </Card>
  );
}
