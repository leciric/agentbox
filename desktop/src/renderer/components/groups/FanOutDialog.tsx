import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { LoaderCircle, Plus, Split, X } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import type * as T from '../../../shared/api';
import type { View } from '../../App';
import { api } from '../../lib/api';
import { closeFanOut, defaultPicks, toVariant, useFanOut, variants } from '../../lib/groups';
import { useT } from '../../lib/i18n';
import { projectLabel } from '../../lib/projectName';
import { errorMessage } from '../../lib/utils';
import { AIIcon } from '../state';
import { Button } from '../ui/button';
import { Notice } from '../ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../ui/dialog';
import { Field, Input, Textarea } from '../ui/input';
import { Select, SelectOption } from '../ui/select';
import { Switch } from '../ui/switch';

// Same as the daemon's state.MaxGroupSize: more agents than this can't be
// compared side by side.
const maxAgents = 6;
const letters = 'ABCDEF';

// FanOutDialog runs one task on several agents at once, each on the AI tool
// and model picked for it, as a group (lib/groups.ts), and opens the Compare
// view on it. It is one for the app, opened with openFanOut from a project's
// menu or New agent's dialog.
export function FanOutDialog({ onSelect }: { onSelect: (view: View) => void }) {
  const t = useT();
  const seed = useFanOut();
  const open = seed !== null;
  const queryClient = useQueryClient();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects, enabled: open });
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings, enabled: open });
  const auth = useQuery({ queryKey: ['auth'], queryFn: api.auth, enabled: open });
  const options = useMemo(() => variants(settings.data, auth.data, t('group.fanOut.defaultModel')), [settings.data, auth.data, t]);
  const [project, setProject] = useState('');
  const [title, setTitle] = useState('');
  const [task, setTask] = useState('');
  const [picks, setPicks] = useState<string[]>([]);
  const [judge, setJudge] = useState(false);

  useEffect(() => {
    if (!seed) return;
    setProject(seed.project);
    setTitle(seed.title ?? '');
    setTask(seed.task ?? '');
    setPicks([]);
    setJudge(false);
  }, [seed]);
  // The first two agents, once the menus are known: two models to compare.
  useEffect(() => {
    if (open && picks.length === 0 && options.length > 0) setPicks(defaultPicks(options));
  }, [open, picks.length, options]);
  const firstProject = projects.data?.[0]?.name;
  useEffect(() => {
    if (open && !project && firstProject) setProject(firstProject);
  }, [open, project, firstProject]);

  const start = useMutation({
    mutationFn: () => api.fanOut({ project, title: title.trim(), task: task.trim(), variants: picks.map(toVariant), judge: judge || undefined }),
    onSuccess: (out: T.FanOutResult) => {
      void queryClient.invalidateQueries({ queryKey: ['groups'] });
      void queryClient.invalidateQueries({ queryKey: ['agents'] });
      closeFanOut();
      onSelect({ kind: 'group', id: out.group.id });
    },
  });
  useEffect(() => {
    if (open) start.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const setPick = (i: number, value: string) => setPicks((p) => p.map((v, j) => (j === i ? value : v)));
  const canStart = !!project && title.trim() !== '' && task.trim() !== '' && picks.length >= 2 && !start.isPending;

  return (
    <Dialog open={open} onOpenChange={(next) => !next && closeFanOut()}>
      <DialogContent className="max-w-xl" data-fan-out>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Split className="size-4 text-brand-300" />
            {t('group.fanOut.title')}
          </DialogTitle>
          <DialogDescription>{t('group.fanOut.description')}</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (canStart) start.mutate();
          }}
        >
          <Field label={t('project.newAgent.projectLabel')} htmlFor="fan-out-project">
            <Select id="fan-out-project" value={project} onChange={setProject}>
              {projects.data?.map((p) => (
                <SelectOption key={p.name} value={p.name}>
                  {projectLabel(p)}
                </SelectOption>
              ))}
            </Select>
          </Field>
          <Field label={t('group.fanOut.titleLabel')} htmlFor="fan-out-title" hint={t('group.fanOut.titleHint')}>
            <Input id="fan-out-title" autoFocus maxLength={60} value={title} placeholder={t('project.newAgent.workPlaceholder')} onChange={(e) => setTitle(e.target.value)} data-fan-out-title />
          </Field>
          <Field label={t('group.fanOut.taskLabel')} htmlFor="fan-out-task">
            <Textarea id="fan-out-task" rows={3} value={task} placeholder={t('group.fanOut.taskPlaceholder')} onChange={(e) => setTask(e.target.value)} data-fan-out-task />
          </Field>
          <div className="grid gap-1.5">
            <span className="text-[13px] font-medium text-secondary">{t('group.fanOut.agents', { count: picks.length })}</span>
            <div className="grid gap-1.5" data-fan-out-variants>
              {picks.map((value, i) => {
                const ai = value.split('|')[0];
                return (
                  <div key={i} className="flex min-w-0 items-center gap-2" data-fan-out-variant={letters[i]}>
                    <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-surface-strong font-mono text-[12px] font-semibold text-title">{letters[i]}</span>
                    <AIIcon ai={ai} className="size-4 shrink-0 text-subtle" />
                    <Select className="min-w-0 flex-1" value={value} onChange={(v) => setPick(i, v)} aria-label={t('group.fanOut.agentAria', { letter: letters[i] })} data-fan-out-pick={letters[i]}>
                      {options.map((o) => (
                        <SelectOption key={o.value} value={o.value}>
                          {o.label}
                        </SelectOption>
                      ))}
                    </Select>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      disabled={picks.length <= 2}
                      aria-label={t('group.fanOut.removeAgent', { letter: letters[i] })}
                      onClick={() => setPicks((p) => p.filter((_, j) => j !== i))}
                    >
                      <X />
                    </Button>
                  </div>
                );
              })}
            </div>
            {picks.length < maxAgents && (
              <Button size="sm" variant="ghost" className="justify-self-start" onClick={() => setPicks((p) => [...p, p[p.length - 1] ?? options[0]?.value ?? 'claude|'])} data-fan-out-add>
                <Plus />
                {t('group.fanOut.addAgent')}
              </Button>
            )}
          </div>
          <label className="flex items-start gap-3 rounded-xl border border-line bg-surface-faint px-3 py-2.5">
            <Switch checked={judge} onCheckedChange={setJudge} className="mt-0.5" data-fan-out-judge />
            <span className="grid gap-0.5">
              <span className="text-[13px] font-medium text-primary">{t('group.fanOut.judge')}</span>
              <span className="text-[12px] text-subtle">{t('group.fanOut.judgeHint')}</span>
            </span>
          </label>
          <p className="text-[12px] leading-relaxed text-subtle">{t('group.fanOut.how')}</p>
          {start.error && <Notice>{errorMessage(start.error)}</Notice>}
          <DialogFooter>
            <Button variant="ghost" onClick={closeFanOut}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={!canStart} data-fan-out-start>
              {start.isPending ? <LoaderCircle className="animate-spin" /> : <Split />}
              {t('group.fanOut.start', { count: picks.length })}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
