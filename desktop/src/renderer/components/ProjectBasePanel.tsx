// The project base, in the app.
//
// A base is a photograph of one machine: every new agent of the project is
// copied from it. That is the whole model, and this panel exists to teach it,
// because the question users ask — "can the two images be merged?" — has the
// answer "no", and everything else follows from that. You update a base by
// creating an agent *from the last photograph*, changing what has gone stale,
// and taking a new one. So Refresh never offers a clean start: an agent that
// began clean would, the moment you saved from it, throw away everything the
// old base had.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Camera, Ellipsis, Layers, LoaderCircle, RefreshCw, RotateCcw, Trash2, TriangleAlert } from 'lucide-react';
import { useCallback, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { formatDate, t as translate, useT } from '../lib/i18n';
import { useProjectName } from '../lib/useProjectName';
import { cn, errorMessage } from '../lib/utils';
import { ConfirmDialog } from './ConfirmDialog';
import { JobProgress } from './JobProgress';
import { Button } from './ui/button';
import { Card, Code, Notice, Row } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Field, Input, Textarea } from './ui/input';
import { Menu, MenuContent, MenuItem, MenuTrigger } from './ui/menu';
import { Select, SelectOption } from './ui/select';

// What the refresh agent is told to do. It is a brief rather than a command
// because the point of a base is the machine around the worktree, and only an
// agent that understands that can tell what has gone stale. The last line is
// the one that matters most: a disk image nobody can read is how a project
// ends up not daring to touch its own base.
const refreshTask = `Bring this project's base machine up to date.

You were created **from the project's current base**, so this machine already has everything that base had: the runtimes, the packages, the Docker images and the caches. Your job is to update what has gone stale, not to set the project up from scratch.

- Update the language runtimes and package managers this project uses (mise, npm or pnpm, pip, Go — whatever is here), and the system packages.
- Pull the Docker images the project runs, so a new agent starts with them warm.
- Install the project's dependencies and check the application still runs: build it, start it, run its tests.
- Write down what is installed and at what version — in the repository, or in the project's notes — so the knowledge isn't trapped inside a disk image.

If a note above this says AgentBox caught the machine up with its base image, that part is done: check the project works on what it installed rather than installing it again.

Don't change application code to make something pass; say so instead. When you're done, say what you updated and what everything is on now. The machine is then saved as the project's new base.`;

export function ProjectBasePanel({ project, className, onOpenAgent }: { project: T.Project; className?: string; onOpenAgent: (ref: string) => void }) {
  const t = useT();
  const name = project.name;
  const queryClient = useQueryClient();
  const base = useQuery({ queryKey: ['base', name], queryFn: () => api.base(name) });
  const agents = useQuery({ queryKey: ['agents'], queryFn: api.agents });
  const image = useQuery({ queryKey: ['image'], queryFn: api.image, staleTime: Infinity });
  const [refreshing, setRefreshing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [reverting, setReverting] = useState(false);
  const [dropping, setDropping] = useState(false);
  const mine = (agents.data ?? []).filter((a) => a.project === name);
  const previous = base.data?.previous;
  const refresh = useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey: ['base', name] });
  }, [name, queryClient]);

  return (
    <Card
      className={className}
      title={t('project.base.title')}
      icon={Layers}
      description={t('project.base.description')}
      action={
        <Menu>
          <MenuTrigger asChild>
            <Button size="icon-sm" variant="ghost" aria-label={t('project.base.actions')}>
              <Ellipsis />
            </Button>
          </MenuTrigger>
          <MenuContent>
            <MenuItem
              icon={Camera}
              disabled={mine.length === 0}
              hint={mine.length === 0 ? t('project.base.noAgents') : undefined}
              onSelect={() => setSaving(true)}
            >
              {t('project.base.saveFromAgent')}
            </MenuItem>
            {previous && (
              <MenuItem icon={Trash2} onSelect={() => setDropping(true)}>
                {t('project.base.dropKept')}
              </MenuItem>
            )}
            <MenuItem
              icon={RotateCcw}
              destructive
              disabled={!base.data}
              hint={base.data ? undefined : t('project.base.noBase')}
              onSelect={() => setRemoving(true)}
            >
              {t('project.base.backToPlain')}
            </MenuItem>
          </MenuContent>
        </Menu>
      }
    >
      {base.data ? (
        <>
          <Row label={t('project.base.savedFrom')} mono>
            {base.data.savedFrom}
          </Row>
          <Row label={t('project.base.saved')}>
            <Age iso={base.data.savedAt} />
          </Row>
          <Row label={t('project.base.snapshot')} mono>
            <span className="truncate" title={base.data.snapshot}>
              {base.data.snapshot}
            </span>
          </Row>
          <Row label={t('project.base.baseImage')} mono>
            <span className="truncate" title={base.data.tools ? t('project.base.agentTools', { tools: base.data.tools }) : undefined}>
              {base.data.image ?? t('project.base.notRecorded')}
            </span>
          </Row>
          {base.data.behind && <BehindNotice behind={base.data.behind} />}
          {stale(base.data.savedAt) && (
            <Notice tone="warning" className="mt-3.5">
              {t('project.base.stale', { age: age(base.data.savedAt), name })}
            </Notice>
          )}
          {previous && (
            <div className="mt-3.5 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-line bg-surface-faint px-3.5 py-3">
              <p className="min-w-0 flex-1 text-[12.5px] leading-relaxed text-muted">
                {t.rich('project.base.previous', {
                  from: <span className="font-mono text-[12px] text-tertiary">{previous.savedFrom}</span>,
                  age: age(previous.savedAt),
                })}
              </p>
              <Button size="sm" variant="secondary" onClick={() => setReverting(true)}>
                <RotateCcw />
                {t('project.base.goBack')}
              </Button>
            </div>
          )}
        </>
      ) : (
        <p className="text-[13px] leading-relaxed text-muted">
          {t.rich('project.base.none', { name, snapshot: image.data?.snapshot ? <Code>{image.data.snapshot}</Code> : '' })}
        </p>
      )}

      <div className="mt-4 flex flex-wrap items-center gap-2 border-t border-line-faint pt-3.5">
        <Button variant="primary" size="sm" onClick={() => setRefreshing(true)}>
          <RefreshCw />
          {base.data ? t('project.base.refresh') : t('project.base.setUp')}
        </Button>
        <Button variant="secondary" size="sm" disabled={mine.length === 0} onClick={() => setSaving(true)}>
          <Camera />
          {t('project.base.saveFromAgent')}
        </Button>
        <span className="text-xs leading-relaxed text-subtle">
          {base.data ? t('project.base.refreshHint') : t('project.base.noMergeHint')}
        </span>
      </div>

      <RefreshDialog
        open={refreshing}
        onOpenChange={setRefreshing}
        project={name}
        base={base.data ?? null}
        onCreated={(ref) => {
          setRefreshing(false);
          onOpenAgent(ref);
        }}
      />
      <SaveDialog open={saving} onOpenChange={setSaving} project={name} base={base.data ?? null} agents={mine} onSaved={() => void refresh()} />
      <ConfirmDialog
        open={reverting}
        onOpenChange={setReverting}
        title={t('project.base.revertTitle')}
        description={
          previous
            ? t.rich('project.base.revertDescription', { code: (c) => <Code>{c}</Code>, name, previous: previous.savedFrom, current: base.data?.savedFrom ?? '' })
            : undefined
        }
        confirmLabel={t('project.base.goBack')}
        onConfirm={async () => {
          const back = await api.revertBase(name);
          await refresh();
          toast(t('project.base.reverted', { name, from: back.savedFrom }), { description: t('project.base.revertedDetail') });
        }}
      />
      <ConfirmDialog
        open={dropping}
        onOpenChange={setDropping}
        title={t('project.base.dropTitle')}
        description={t('project.base.dropDescription')}
        confirmLabel={t('project.base.dropConfirm')}
        destructive
        onConfirm={async () => {
          await api.removePreviousBase(name);
          await refresh();
          toast(t('project.base.dropped', { name }));
        }}
      />
      <ConfirmDialog
        open={removing}
        onOpenChange={setRemoving}
        title={t('project.base.removeTitle', { name })}
        description={t('project.base.removeDescription', { name })}
        confirmLabel={t('project.base.backToPlain')}
        destructive
        onConfirm={async () => {
          await api.removeBase(name);
          await refresh();
          toast(t('project.base.removed', { name }));
        }}
      >
        <p className="text-[13px] leading-relaxed text-muted">
          {t('project.base.removeNote')}
        </p>
      </ConfirmDialog>
    </Card>
  );
}

// RefreshDialog makes the agent that will carry the next photograph. It is the
// one place the "start from the last base" rule is enforced: the request never
// carries clean, and the dialog says why, because a user who reached for New
// agent and ticked "skip the project base" would get a machine that looks fine
// and a base that has quietly lost everything.
function RefreshDialog({
  open,
  onOpenChange,
  project,
  base,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  project: string;
  base: T.Base | null;
  onCreated: (ref: string) => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const projectName = useProjectName(project);
  const [task, setTask] = useState(refreshTask);
  const [title, setTitle] = useState('');
  const [job, setJob] = useState<T.Job | null>(null);
  const [done, setDone] = useState<T.Job | null>(null);
  const create = useMutation({
    mutationFn: () =>
      api.createAgent({
        project,
        title: title.trim() || (base ? t('project.base.refreshDialog.title') : t('project.base.refreshDialog.defaultTitle')),
        ai: 'claude',
        interface: 'chat',
        autonomous: true,
        task: task.trim(),
        // Its machine is as far behind the base image as the base is, and
        // what the daemon catches up on it before the task is in the base
        // saved from it. A new base has nothing to catch up: it starts from
        // the image itself.
        catchUp: base !== null,
        // Never clean. An agent made from the plain image would look like a
        // working machine and make a base that has lost everything the old one
        // had, because a save is a photograph and photographs don't merge.
      }),
    onSuccess: setJob,
  });

  const reset = (next: boolean) => {
    if (!next) {
      setJob(null);
      setDone(null);
      setTask(refreshTask);
      setTitle('');
      create.reset();
    }
    onOpenChange(next);
  };

  const onDone = useCallback(
    async (finished: T.Job) => {
      setDone(finished);
      if (finished.status !== 'succeeded') return;
      await queryClient.invalidateQueries({ queryKey: ['agents'] });
    },
    [queryClient],
  );
  // A job carries the agent it made; a failed one carries the reason, which
  // JobProgress is already showing.
  const made = done?.status === 'succeeded' ? (done.result as T.Agent) : null;

  return (
    <Dialog open={open} onOpenChange={reset}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{base ? t('project.base.refreshDialog.title') : t('project.base.refreshDialog.titleNew', { project: projectName })}</DialogTitle>
          <DialogDescription>
            {base ? t('project.base.refreshDialog.description') : t('project.base.refreshDialog.descriptionNew')}
          </DialogDescription>
        </DialogHeader>

        {job ? (
          <>
            <JobProgress jobId={job.id} onDone={(done) => void onDone(done)} />
            {made && (
              <Notice tone="info">
                {t.rich('project.base.refreshDialog.made', { code: (c) => <Code>{c}</Code>, ref: made.ref })}
              </Notice>
            )}
            <DialogFooter>
              <Button variant={made ? 'ghost' : 'secondary'} onClick={() => reset(false)}>
                {done ? t('common.close') : t('project.base.keepInBackground')}
              </Button>
              {made && (
                <Button variant="primary" onClick={() => onCreated(made.ref)}>
                  {t('project.base.refreshDialog.open', { name: made.name })}
                </Button>
              )}
            </DialogFooter>
          </>
        ) : (
          <form
            className="grid gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              create.mutate();
            }}
          >
            <Notice tone="info">
              {base ? (
                <>
                  {t.rich('project.base.refreshDialog.fromBase', { code: (c) => <Code>{c}</Code>, from: base.savedFrom })}
                  {base.behind && <> {t('project.base.refreshDialog.catchUp')}</>}
                </>
              ) : (
                t('project.base.refreshDialog.noBase', { project: projectName })
              )}
            </Notice>
            <Field label={t('project.base.refreshDialog.titleLabel')} htmlFor="base-refresh-title" hint={t('project.base.refreshDialog.titleHint')}>
              <Input
                id="base-refresh-title"
                autoFocus
                maxLength={80}
                placeholder={base ? t('project.base.refreshDialog.title') : t('project.base.refreshDialog.defaultTitle')}
                value={title}
                onChange={(event) => setTitle(event.target.value)}
              />
            </Field>
            <Field label={t('project.base.refreshDialog.taskLabel')} htmlFor="base-refresh-task" hint={t('project.base.refreshDialog.taskHint')}>
              <Textarea
                id="base-refresh-task"
                className="min-h-56 font-mono text-[12px]"
                spellCheck={false}
                value={task}
                onChange={(event) => setTask(event.target.value)}
              />
            </Field>
            {create.error && <Notice>{errorMessage(create.error)}</Notice>}
            <DialogFooter>
              <Button variant="ghost" onClick={() => reset(false)}>
                {t('common.cancel')}
              </Button>
              <Button type="submit" variant="primary" disabled={create.isPending || !task.trim()}>
                {create.isPending && <LoaderCircle className="animate-spin" />}
                {t('project.base.refreshDialog.create')}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

// SaveDialog takes the photograph. It is the irreversible one, so it says so
// before it runs rather than after: the base it replaces is kept for exactly
// one step back, and the agent the old base came from is the older safety net
// that still holds when that step is used up.
function SaveDialog({
  open,
  onOpenChange,
  project,
  base,
  agents,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  project: string;
  base: T.Base | null;
  agents: T.Agent[];
  onSaved: () => void;
}) {
  const t = useT();
  const projectName = useProjectName(project);
  // The newest agent is the one a refresh just made, which is what this dialog
  // is usually opened for.
  const newest: T.Agent | undefined = [...agents].sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
  const [agent, setAgent] = useState('');
  const [job, setJob] = useState<T.Job | null>(null);
  const [done, setDone] = useState<T.Job | null>(null);
  const chosen = agents.find((a) => a.name === agent) ?? newest;
  // The machine the old base was saved from, when it is still here: its disk
  // still holds the old state, so it can be saved from again long after the
  // kept base has been spent.
  const source = base && agents.find((a) => a.ref === base.savedFrom);
  const save = useMutation({
    mutationFn: () => (chosen ? api.saveBase(project, chosen.name) : Promise.reject(new Error(t('project.base.saveDialog.noAgent', { project })))),
    onSuccess: setJob,
  });

  const reset = (next: boolean) => {
    if (!next) {
      setJob(null);
      setDone(null);
      setAgent('');
      save.reset();
    }
    onOpenChange(next);
  };

  const finished = useCallback(
    (job: T.Job) => {
      setDone(job);
      if (job.status === 'succeeded') onSaved();
    },
    [onSaved],
  );

  return (
    <Dialog open={open} onOpenChange={reset}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{chosen ? t('project.base.saveDialog.title', { ref: chosen.ref }) : t('project.base.saveDialog.titleNone')}</DialogTitle>
          <DialogDescription>
            {t('project.base.saveDialog.description')}
          </DialogDescription>
        </DialogHeader>

        {job ? (
          <>
            <JobProgress jobId={job.id} onDone={finished} />
            {done?.status === 'succeeded' && <Notice tone="info">{t('project.base.saveDialog.done', { project: projectName })}</Notice>}
            <DialogFooter>
              <Button onClick={() => reset(false)}>{done ? t('common.close') : t('project.base.keepInBackground')}</Button>
            </DialogFooter>
          </>
        ) : (
          <form
            className="grid gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              save.mutate();
            }}
          >
            <Field
              label={t('project.base.saveDialog.fromLabel')}
              htmlFor="base-save-agent"
              hint={chosen ? t('project.base.saveDialog.fromHint') : t('project.base.saveDialog.fromHintNone', { project: projectName })}
            >
              <Select id="base-save-agent" value={chosen?.name ?? ''} onChange={setAgent}>
                {agents.map((a) => (
                  <SelectOption key={a.name} value={a.name}>
                    {a.name}
                    {a.title ? ` — ${a.title}` : ''}
                  </SelectOption>
                ))}
              </Select>
            </Field>
            {base ? (
              <Notice tone="warning">
                {t.rich('project.base.saveDialog.replaces', { code: (c) => <Code>{c}</Code>, from: base.savedFrom })}
              </Notice>
            ) : (
              <Notice tone="info">
                {t('project.base.saveDialog.replacesNothing', { project: projectName })}
              </Notice>
            )}
            {source && (
              <p className="flex items-start gap-2.5 text-[12.5px] leading-relaxed text-muted">
                <TriangleAlert className="mt-px size-4 shrink-0 text-amber-300" />
                <span>
                  {t.rich('project.base.saveDialog.keepAlive', { ref: <span className="font-mono text-[12px] text-tertiary">{source.ref}</span> })}
                </span>
              </p>
            )}
            {save.error && <Notice>{errorMessage(save.error)}</Notice>}
            <DialogFooter>
              <Button variant="ghost" onClick={() => reset(false)}>
                {t('common.cancel')}
              </Button>
              <Button type="submit" variant="primary" disabled={!chosen || save.isPending}>
                {save.isPending && <LoaderCircle className="animate-spin" />}
                <Camera />
                {t('project.base.saveDialog.submit')}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

// BehindNotice says what the base image has that the base doesn't. A base is a
// copy of a machine, so it keeps the image it was first copied from however
// often it is saved again, until a refresh catches it up: this is how the user
// finds out that new agents of the project miss a fix the image already has.
function BehindNotice({ behind }: { behind: T.BaseBehind }) {
  const t = useT();
  const tools = behind.tools ?? [];
  const changes = behind.changes ?? [];
  const components = behind.components ?? [];
  return (
    <Notice tone="warning" className="mt-3.5">
      <p>{t('project.base.behind.intro')}</p>
      <ul className="mt-2 grid list-disc gap-1 pl-4 text-[12.5px]">
        {behind.imageTo && (
          <li>
            {t.rich('project.base.behind.image', {
              from: <span className="font-mono text-[12px]">{behind.imageFrom || t('project.base.notRecorded')}</span>,
              to: <span className="font-mono text-[12px]">{behind.imageTo}</span>,
            })}
            {changes.length > 0 && (
              <ul className="mt-1 grid list-[circle] gap-0.5 pl-4">
                {changes.map((c) => (
                  <li key={c.version}>
                    <span className="font-mono text-[12px]">{c.version}</span>: {c.what}
                  </li>
                ))}
              </ul>
            )}
          </li>
        )}
        {components.length > 0 && <li>{t('project.base.behind.components', { list: components.join(', ') })}</li>}
        {behind.toolsUnknown && <li>{t('project.base.behind.toolsUnknown')}</li>}
        {tools.length > 0 && (
          <li>
            {t('project.base.behind.tools')}{' '}
            {tools.map((tool, i) => (
              <span key={tool.name}>
                {i > 0 && ', '}
                <span className="font-mono text-[12px]">{tool.name}</span> {toolMove(tool)}
              </span>
            ))}
          </li>
        )}
      </ul>
    </Notice>
  );
}

function toolMove(tool: T.BaseToolChange): string {
  if (!tool.from) return translate('project.base.behind.toolNew', { to: tool.to });
  if (!tool.to) return translate('project.base.behind.toolDropped', { from: tool.from });
  return `${tool.from} → ${tool.to}`;
}

// Age says how old a base is in words as well as in a date, because the
// problem this whole panel exists for is a base nobody noticed going stale:
// "12 Sep 2025" is a fact and "a year old" is the fact you can act on.
function Age({ iso }: { iso: string }) {
  useT();
  const when = new Date(iso);
  return (
    <span className="flex flex-wrap items-baseline gap-x-2">
      <span>{formatDate(when, { day: 'numeric', month: 'short', year: 'numeric' })}</span>
      <span className={cn('text-[12.5px]', stale(iso) ? 'text-amber-300' : 'text-subtle')}>{age(iso)}</span>
    </span>
  );
}

const DAY = 86_400_000;

// A base older than this is worth a warning. Two months is roughly when a
// project's dependencies have moved enough that every new agent pays for it.
const STALE = 60 * DAY;

const stale = (iso: string, now = Date.now()): boolean => now - new Date(iso).getTime() >= STALE;

function age(iso: string, now = Date.now()): string {
  const days = Math.max(0, Math.floor((now - new Date(iso).getTime()) / DAY));
  if (days < 1) return translate('project.base.age.today');
  if (days === 1) return translate('project.base.age.yesterday');
  if (days < 31) return translate('project.base.age.days', { count: days });
  const months = Math.round(days / 30.44);
  if (days < 365) return translate('project.base.age.months', { count: months });
  const years = Math.floor(days / 365);
  return translate('project.base.age.years', { count: years });
}
