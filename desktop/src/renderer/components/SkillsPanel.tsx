import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  AlertTriangle,
  BookOpen,
  Bot,
  Check,
  ChevronDown,
  Code2,
  Eye,
  File,
  FileText,
  Folder,
  FolderOpen,
  GitBranch,
  Globe,
  LoaderCircle,
  Pencil,
  Plus,
  Puzzle,
  Search,
  Sparkles,
  SquareTerminal,
  Trash2,
  Wand2,
  X,
  type LucideIcon,
} from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { useT } from '../lib/i18n';
import { formatBytes, frontmatter, originOf, searchSkills, skillHue, skillNameOf, skillTemplate, skillTitle, type SkillOrigin } from '../lib/skills';
import { cn, errorMessage, timeAgo } from '../lib/utils';
import { Markdown } from './chat/Markdown';
import { ConfirmDialog } from './ConfirmDialog';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from './ui/dialog';
import { Input } from './ui/input';
import { Switch } from './ui/switch';
import { Tip } from './ui/tooltip';

// Skills, AgentBox-wide (Settings → Skills) or for one project (its Settings
// → Skills): a library of cards to browse and search, each opening on its
// SKILL.md; switches that turn a skill on or off, everywhere or in the
// project; and dialogs to import skills (from the user's own AI tools, a
// folder, or a git repository) and to write or edit one.
//
// In a project a switch says whether its agents get the skill. Flipping it
// away from the AgentBox-wide setting stores an override, and flipping it
// back drops the override, so the project follows AgentBox-wide again.

type Filter = 'all' | 'on' | 'off';

const originIcons: Record<SkillOrigin | 'agentbox', LucideIcon> = {
  agentbox: Wand2,
  claude: Sparkles,
  'claude-plugin': Puzzle,
  codex: SquareTerminal,
  agents: Bot,
  opencode: Code2,
  cursor: Code2,
  folder: Folder,
  git: GitBranch,
};

function useOriginLabel() {
  const t = useT();
  return (origin: SkillOrigin | 'agentbox', plugin?: string) =>
    ({
      agentbox: t('skills.origin.agentbox'),
      claude: 'Claude Code',
      'claude-plugin': plugin ? t('skills.origin.pluginNamed', { plugin }) : t('skills.origin.plugin'),
      codex: 'Codex',
      agents: '~/.agents',
      opencode: 'OpenCode',
      cursor: 'Cursor',
      folder: t('skills.origin.folder'),
      git: t('skills.origin.git'),
    })[origin];
}

// SkillTile is a skill's monogram, in a hue of its own.
export function SkillTile({ name, size = 'md', dim }: { name: string; size?: 'sm' | 'md' | 'lg'; dim?: boolean }) {
  const hue = skillHue(name);
  const letters = skillTitle(name)
    .split(' ')
    .slice(0, 2)
    .map((w) => w[0])
    .join('');
  return (
    <span
      aria-hidden
      className={cn(
        'relative flex shrink-0 items-center justify-center overflow-hidden rounded-xl font-semibold tracking-tight ring-1 ring-inset transition-opacity',
        size === 'sm' && 'size-7 rounded-lg text-[10.5px]',
        size === 'md' && 'size-10 text-[13px]',
        size === 'lg' && 'size-12 text-[15px]',
        dim && 'opacity-45 grayscale',
      )}
      style={{
        background: `linear-gradient(140deg, hsl(${hue} 85% 62% / 0.30), hsl(${(hue + 40) % 360} 80% 52% / 0.12))`,
        color: `hsl(${hue} 90% 78%)`,
        ['--tw-ring-color' as string]: `hsl(${hue} 80% 65% / 0.35)`,
      }}
    >
      {letters || '?'}
    </span>
  );
}

// embedded drops the panel's own margins, inside a Settings page that has them.
export function SkillsPanel({ project, embedded }: { project?: string; embedded?: boolean }) {
  const t = useT();
  const queryClient = useQueryClient();
  const skills = useQuery({ queryKey: ['skills', project ?? ''], queryFn: () => api.skills(project) });
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<Filter>('all');
  const [open, setOpen] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [importing, setImporting] = useState(false);

  const isOn = (s: T.Skill) => (project ? !!s.active : s.enabled);
  const all = skills.data ?? [];
  const on = all.filter(isOn).length;
  const shown = useMemo(() => {
    const filtered = all.filter((s) => filter === 'all' || (filter === 'on') === isOn(s));
    return searchSkills(filtered, query);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [all, filter, query, project]);

  const refresh = () => queryClient.invalidateQueries({ queryKey: ['skills'] });
  const toggle = useMutation({
    mutationFn: ({ skill, value }: { skill: T.Skill; value: boolean }) =>
      project ? api.setSkillOverride(project, skill.name, value === skill.enabled ? '' : value ? 'on' : 'off') : api.setSkillEnabled(skill.name, value),
    onMutate: async ({ skill, value }) => {
      // The switch moves at once; the list catches up when the daemon answers.
      queryClient.setQueryData<T.Skill[]>(['skills', project ?? ''], (list) =>
        list?.map((s) => (s.name === skill.name ? (project ? { ...s, active: value } : { ...s, enabled: value }) : s)),
      );
    },
    onError: (err) => toast.error(errorMessage(err)),
    onSettled: refresh,
  });

  return (
    <div className={cn('mx-auto grid w-full gap-5', !embedded && 'max-w-5xl px-4 py-6 md:px-8 md:py-7')} data-skills-panel={project ?? 'agentbox'}>
      <header className="relative overflow-hidden rounded-3xl border border-line-strong bg-surface px-6 py-6">
        <div aria-hidden className="pointer-events-none absolute -right-16 -top-24 size-72 rounded-full bg-brand-500/20 blur-3xl" />
        <div aria-hidden className="pointer-events-none absolute -bottom-28 left-1/3 size-64 rounded-full bg-fuchsia-500/10 blur-3xl" />
        <div className="relative flex flex-wrap items-start gap-5">
          <div className={cn('relative', embedded && 'hidden')}>
            <div className="brand-gradient absolute inset-0 rounded-2xl opacity-40 blur-lg" />
            <div className="relative flex size-12 items-center justify-center rounded-2xl border border-line-strong bg-overlay">
              <Wand2 className="size-5 text-brand-300" />
            </div>
          </div>
          <div className="min-w-0 flex-1 basis-72">
            {!embedded && <h2 className="text-[19px] font-semibold tracking-tight text-title">{project ? t('skills.projectTitle') : t('skills.title')}</h2>}
            <p className="mt-1 max-w-2xl text-[13px] leading-relaxed text-muted">
              {project ? t('skills.projectDescription') : t('skills.description')}
            </p>
            <div className="mt-3 flex flex-wrap items-center gap-2 text-[12px] text-subtle">
              <Badge variant="brand">{t('skills.count', { count: all.length })}</Badge>
              <Badge variant={on > 0 ? 'success' : 'default'}>{t('skills.onCount', { count: on })}</Badge>
              <span className="hidden text-faint sm:inline">{t.rich('skills.composerHint', { kbd: (c) => <kbd className="rounded-md border border-line bg-sunken px-1.5 py-px font-mono text-[11px] text-tertiary">{c}</kbd> })}</span>
            </div>
          </div>
          <div className="flex shrink-0 gap-2">
            <Button variant="secondary" onClick={() => setImporting(true)} data-skills-import>
              <FolderOpen /> {t('skills.import')}
            </Button>
            <Button variant="primary" onClick={() => setCreating(true)} data-skills-new>
              <Plus /> {t('skills.new')}
            </Button>
          </div>
        </div>
      </header>

      {skills.error && <Notice>{errorMessage(skills.error)}</Notice>}

      {all.length > 0 && (
        <div className="flex flex-wrap items-center gap-3">
          <label className="relative min-w-0 flex-1 basis-64">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-faint" />
            <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('skills.search')} aria-label={t('skills.search')} className="pl-9" />
          </label>
          <div role="radiogroup" aria-label={t('skills.filter')} className="flex rounded-xl border border-line bg-sunken p-0.5">
            {(['all', 'on', 'off'] as const).map((f) => (
              <button
                key={f}
                role="radio"
                aria-checked={filter === f}
                onClick={() => setFilter(f)}
                className={cn(
                  'rounded-[10px] px-3 py-1.5 text-[12.5px] font-medium transition-colors',
                  filter === f ? 'bg-surface-raised text-title shadow-sm' : 'text-subtle hover:text-primary',
                )}
              >
                {t(`skills.filter.${f}`)}
              </button>
            ))}
          </div>
        </div>
      )}

      {skills.isPending && (
        <div className="grid gap-3 sm:grid-cols-2">
          {[0, 1, 2, 3].map((i) => (
            <div key={i} className="h-[132px] animate-pulse rounded-2xl border border-line bg-surface" />
          ))}
        </div>
      )}

      {!skills.isPending && all.length === 0 && (
        <div className="grid justify-items-center gap-3 rounded-3xl border border-dashed border-line-strong px-6 py-14 text-center">
          <div className="relative mb-1">
            <div className="brand-gradient absolute inset-0 rounded-2xl opacity-30 blur-xl" />
            <div className="relative flex size-14 items-center justify-center rounded-2xl border border-line-strong bg-overlay">
              <BookOpen className="size-6 text-brand-300" />
            </div>
          </div>
          <h3 className="text-[15px] font-semibold text-title">{t('skills.empty.title')}</h3>
          <p className="max-w-md text-[13px] leading-relaxed text-muted">{t('skills.empty.body')}</p>
          <div className="mt-2 flex flex-wrap justify-center gap-2">
            <Button variant="primary" onClick={() => setImporting(true)}>
              <Sparkles /> {t('skills.empty.import')}
            </Button>
            <Button variant="secondary" onClick={() => setCreating(true)}>
              <Pencil /> {t('skills.empty.write')}
            </Button>
          </div>
        </div>
      )}

      {all.length > 0 && shown.length === 0 && <p className="py-8 text-center text-[13px] text-subtle">{t('skills.noMatch')}</p>}

      <ul className="grid gap-3 sm:grid-cols-2" aria-label={t('skills.list')}>
        {shown.map((skill) => (
          <SkillCard
            key={skill.name}
            skill={skill}
            project={project}
            on={isOn(skill)}
            onOpen={() => setOpen(skill.name)}
            onToggle={(value) => toggle.mutate({ skill, value })}
          />
        ))}
      </ul>

      {open && (
        <SkillDialog
          name={open}
          project={project}
          summary={all.find((s) => s.name === open)}
          onClose={() => setOpen(null)}
          onToggle={(skill, value) => toggle.mutate({ skill, value })}
        />
      )}
      {creating && (
        <SkillDialog
          project={project}
          onClose={() => setCreating(false)}
          onCreated={(name) => {
            setCreating(false);
            setOpen(name);
          }}
        />
      )}
      {importing && <ImportDialog project={project} onClose={() => setImporting(false)} />}
    </div>
  );
}

function SkillCard({ skill, project, on, onOpen, onToggle }: { skill: T.Skill; project?: string; on: boolean; onOpen: () => void; onToggle: (value: boolean) => void }) {
  const t = useT();
  const originLabel = useOriginLabel();
  const origin = originOf(skill.source);
  const OriginIcon = originIcons[origin];
  const override = project ? skill.overrides[project] : undefined;
  return (
    <li
      className={cn(
        'group relative flex min-w-0 cursor-pointer flex-col gap-3 rounded-2xl border bg-surface p-4 transition-all hover:-translate-y-px hover:border-line-strong hover:shadow-[0_18px_40px_-24px_var(--ab-shadow-deep)]',
        on ? 'border-line' : 'border-line-faint bg-surface/60',
      )}
      onClick={onOpen}
      data-skill={skill.name}
    >
      <div className="flex min-w-0 items-start gap-3">
        <SkillTile name={skill.name} dim={!on} />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-baseline gap-2">
            <h3 className={cn('truncate text-[14px] font-semibold', on ? 'text-title' : 'text-muted')}>{skill.name}</h3>
          </div>
          <code className="font-mono text-[11.5px] text-subtle">${skill.name}</code>
        </div>
        <Tip label={on ? t('skills.turnOff') : t('skills.turnOn')}>
          <span onClick={(e) => e.stopPropagation()} className="pt-0.5">
            <Switch checked={on} onCheckedChange={onToggle} aria-label={t('skills.toggle', { name: skill.name })} />
          </span>
        </Tip>
      </div>
      <p className={cn('line-clamp-2 min-h-[2.6em] text-[12.5px] leading-relaxed', on ? 'text-muted' : 'text-subtle')}>{skill.description}</p>
      <div className="flex min-w-0 flex-wrap items-center gap-1.5 text-[11px] text-subtle">
        <Badge>
          <OriginIcon /> {originLabel(origin, origin === 'claude-plugin' ? skill.source.split(':')[1]?.split('/')[0] : undefined)}
        </Badge>
        <span className="tabular-nums">
          {t('skills.files', { count: skill.fileCount })} · {formatBytes(skill.size)}
        </span>
        {project && override !== undefined && (
          <Badge variant={override ? 'brand' : 'warning'} className="ml-auto">
            {override ? t('skills.onHere') : t('skills.offHere')}
          </Badge>
        )}
        {project && override === undefined && <span className="ml-auto text-subtle">{t('skills.followsWide')}</span>}
        {!project && Object.keys(skill.overrides).length > 0 && (
          <Tip label={Object.entries(skill.overrides).map(([p, v]) => `${p}: ${v ? t('skills.on') : t('skills.off')}`).join(' · ')}>
            <span className="ml-auto text-subtle">{t('skills.overrides', { count: Object.keys(skill.overrides).length })}</span>
          </Tip>
        )}
        {!skill.userInvocable && (
          <Tip label={t('skills.modelOnlyHint')}>
            <Badge variant="info">{t('skills.modelOnly')}</Badge>
          </Tip>
        )}
      </div>
    </li>
  );
}

// SkillDialog shows one skill — its SKILL.md rendered or as source, and its
// other files — and edits its SKILL.md. Without a name, it writes a new one.
function SkillDialog({
  name,
  project,
  summary,
  onClose,
  onToggle,
  onCreated,
}: {
  name?: string;
  project?: string;
  summary?: T.Skill;
  onClose: () => void;
  onToggle?: (skill: T.Skill, value: boolean) => void;
  onCreated?: (name: string) => void;
}) {
  const t = useT();
  const queryClient = useQueryClient();
  const originLabel = useOriginLabel();
  const creating = !name;
  const detail = useQuery({ queryKey: ['skill', name], queryFn: () => api.skill(name!), enabled: !creating });
  const [file, setFile] = useState('SKILL.md');
  const [view, setView] = useState<'preview' | 'source'>('preview');
  const [editing, setEditing] = useState(creating);
  const [newName, setNewName] = useState('');
  const [draft, setDraft] = useState(() => (creating ? skillTemplate('my-skill', '') : ''));
  const [deleting, setDeleting] = useState(false);

  const skillMd = detail.data?.files.find((f) => f.path === 'SKILL.md')?.content ?? '';
  useEffect(() => {
    if (!creating && editing) setDraft(skillMd);
    // Only when editing starts: typing must not be overwritten by a refetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editing]);

  const save = useMutation({
    mutationFn: () => {
      const target = creating ? skillNameOf(newName) : name!;
      return api.saveSkill(target, { content: draft, ...(creating && project ? { project } : {}) });
    },
    onSuccess: async (saved) => {
      toast.success(creating ? t('skills.created', { name: saved.name }) : t('skills.saved', { name: saved.name }));
      await queryClient.invalidateQueries({ queryKey: ['skills'] });
      await queryClient.invalidateQueries({ queryKey: ['skill', saved.name] });
      if (creating) onCreated?.(saved.name);
      else setEditing(false);
    },
  });

  const skill = detail.data ?? summary;
  const current = detail.data?.files.find((f) => f.path === file);
  const on = skill ? (project ? !!summary?.active : skill.enabled) : false;
  const origin = skill ? originOf(skill.source) : 'agentbox';
  const OriginIcon = originIcons[origin];
  const title = creating ? skillNameOf(newName) || 'new-skill' : name!;

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="flex h-[86vh] max-w-6xl flex-col gap-0 overflow-hidden p-0" data-skill-dialog={name ?? 'new'}>
        <div className="relative flex shrink-0 items-start gap-4 border-b border-line px-6 py-5 pr-14">
          <div aria-hidden className="pointer-events-none absolute -left-10 -top-20 size-56 rounded-full blur-3xl" style={{ background: `hsl(${skillHue(name ?? (skillNameOf(newName) || 'new-skill'))} 80% 55% / 0.14)` }} />
          <SkillTile name={name ?? (skillNameOf(newName) || 'new-skill')} size="lg" dim={!creating && !on} />
          <div className="relative min-w-0 flex-1">
            <DialogTitle className="truncate">{creating ? t('skills.newTitle') : title}</DialogTitle>
            {creating ? (
              <DialogDescription className="mt-1">{project ? t('skills.newDescriptionProject') : t('skills.newDescription')}</DialogDescription>
            ) : (
              <div className="mt-1 flex flex-wrap items-center gap-2 text-[12px] text-subtle">
                <code className="font-mono text-faint">${name}</code>
                {skill && (
                  <Badge>
                    <OriginIcon /> {originLabel(origin, origin === 'claude-plugin' ? skill.source.split(':')[1]?.split('/')[0] : undefined)}
                  </Badge>
                )}
                {skill?.source && origin !== 'agentbox' && (
                  <span className="max-w-80 truncate font-mono text-[11px] text-faint" title={skill.source}>
                    {skill.source}
                  </span>
                )}
                {skill && <span>{t('skills.updated', { when: timeAgo(skill.updatedAt) })}</span>}
              </div>
            )}
          </div>
          {!creating && skill && !editing && (
            <div className="relative flex shrink-0 items-center gap-2">
              <label className="mr-1 flex items-center gap-2 rounded-xl border border-line bg-sunken px-3 py-1.5 text-[12.5px] text-muted">
                {project ? t('skills.inThisProject') : t('skills.agentboxWide')}
                <Switch checked={on} onCheckedChange={(v) => summary && onToggle?.(summary, v)} aria-label={t('skills.toggle', { name: name! })} />
              </label>
              <Button variant="secondary" size="sm" onClick={() => setEditing(true)} disabled={!detail.data} data-skill-edit>
                <Pencil /> {t('skills.edit')}
              </Button>
              <Tip label={t('skills.delete')}>
                <Button variant="danger" size="icon-sm" onClick={() => setDeleting(true)} aria-label={t('skills.delete')}>
                  <Trash2 />
                </Button>
              </Tip>
            </div>
          )}
        </div>

        {editing ? (
          <div className="flex min-h-0 flex-1 flex-col">
            {creating && (
              <div className="flex shrink-0 items-center gap-3 border-b border-line-faint px-6 py-3">
                <label htmlFor="skill-name" className="text-[12.5px] font-medium text-muted">
                  {t('skills.name')}
                </label>
                <div className="relative w-72">
                  <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 font-mono text-[13px] text-faint">$</span>
                  <Input
                    id="skill-name"
                    autoFocus
                    value={newName}
                    placeholder="review-pr"
                    className="pl-6 font-mono"
                    onChange={(e) => {
                      const next = skillNameOf(e.target.value, true);
                      setNewName(next);
                      setDraft((d) => d.replace(/^(---\r?\n(?:[\s\S]*?\n)?)name:.*$/m, `$1name: ${next.replace(/-+$/, '') || 'my-skill'}`).replace(/^# .*$/m, `# ${skillTitle(next || 'my-skill')}`));
                    }}
                  />
                </div>
                <span className="text-[12px] text-faint">{t('skills.nameHint')}</span>
              </div>
            )}
            <div className="grid min-h-0 flex-1 grid-cols-1 md:grid-cols-2">
              <div className="flex min-h-0 flex-col border-line-faint md:border-r">
                <div className="flex shrink-0 items-center gap-2 px-5 py-2 text-[11.5px] font-medium uppercase tracking-wide text-faint">
                  <Code2 className="size-3.5" /> SKILL.md
                </div>
                <textarea
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  spellCheck={false}
                  aria-label="SKILL.md"
                  className="min-h-0 flex-1 resize-none bg-sunken px-5 py-4 font-mono text-[12.5px] leading-relaxed text-primary outline-none"
                  data-skill-editor
                />
              </div>
              <div className="hidden min-h-0 flex-col md:flex">
                <div className="flex shrink-0 items-center gap-2 px-5 py-2 text-[11.5px] font-medium uppercase tracking-wide text-faint">
                  <Eye className="size-3.5" /> {t('skills.preview')}
                </div>
                <div className="min-h-0 flex-1 overflow-y-auto px-6 pb-6">
                  <SkillPreview content={draft} />
                </div>
              </div>
            </div>
            <div className="flex shrink-0 items-center gap-3 border-t border-line px-6 py-3">
              {save.error ? (
                <p className="flex min-w-0 items-center gap-2 text-[12.5px] text-rose-300">
                  <AlertTriangle className="size-4 shrink-0" /> <span className="truncate">{errorMessage(save.error)}</span>
                </p>
              ) : (
                <p className="text-[12px] text-faint">{t('skills.editHint')}</p>
              )}
              <div className="ml-auto flex gap-2">
                <Button variant="ghost" onClick={() => (creating ? onClose() : setEditing(false))}>
                  {t('common.cancel')}
                </Button>
                <Button variant="primary" onClick={() => save.mutate()} disabled={save.isPending || (creating && !skillNameOf(newName))} data-skill-save>
                  {save.isPending ? <LoaderCircle className="animate-spin" /> : <Check />} {creating ? t('skills.create') : t('common.save')}
                </Button>
              </div>
            </div>
          </div>
        ) : (
          <div className="flex min-h-0 flex-1">
            <nav aria-label={t('skills.filesLabel')} className="hidden w-60 shrink-0 flex-col gap-0.5 overflow-y-auto border-r border-line-faint bg-sunken/40 p-3 md:flex">
              <p className="px-2 pb-1.5 text-[11px] font-medium uppercase tracking-wide text-faint">{t('skills.files', { count: detail.data?.files.length ?? 0 })}</p>
              {detail.data?.files.map((f) => (
                <button
                  key={f.path}
                  onClick={() => setFile(f.path)}
                  className={cn(
                    'flex min-w-0 items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[12.5px] transition-colors',
                    f.path === file ? 'bg-surface-raised text-title' : 'text-muted hover:bg-surface hover:text-primary',
                  )}
                >
                  {f.path === 'SKILL.md' ? <FileText className="size-3.5 shrink-0 text-brand-300" /> : <File className="size-3.5 shrink-0 text-faint" />}
                  <span className="min-w-0 flex-1 truncate font-mono text-[12px]">{f.path}</span>
                  <span className="shrink-0 text-[10.5px] tabular-nums text-faint">{formatBytes(f.size)}</span>
                </button>
              ))}
            </nav>
            <div className="flex min-h-0 min-w-0 flex-1 flex-col">
              {file === 'SKILL.md' && (
                <div className="flex shrink-0 items-center justify-end gap-1 px-6 pt-3">
                  <div role="radiogroup" className="flex rounded-lg border border-line bg-sunken p-0.5">
                    {(['preview', 'source'] as const).map((v) => (
                      <button
                        key={v}
                        role="radio"
                        aria-checked={view === v}
                        onClick={() => setView(v)}
                        className={cn('flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[12px]', view === v ? 'bg-surface-raised text-title' : 'text-subtle hover:text-primary')}
                      >
                        {v === 'preview' ? <Eye className="size-3.5" /> : <Code2 className="size-3.5" />} {t(`skills.view.${v}`)}
                      </button>
                    ))}
                  </div>
                </div>
              )}
              <div className="min-h-0 flex-1 overflow-y-auto px-6 pb-8 pt-2">
                {detail.isPending && <p className="py-6 text-[13px] text-subtle">{t('common.loading')}</p>}
                {detail.error && <Notice>{errorMessage(detail.error)}</Notice>}
                {current && file === 'SKILL.md' && view === 'preview' && <SkillPreview content={current.content ?? ''} />}
                {current && (file !== 'SKILL.md' || view === 'source') &&
                  (current.binary ? (
                    <p className="py-6 text-[13px] text-subtle">{t('skills.binary', { size: formatBytes(current.size) })}</p>
                  ) : (
                    <pre className="overflow-x-auto rounded-xl border border-line-faint bg-sunken p-4 font-mono text-[12px] leading-relaxed text-tertiary">{current.content}</pre>
                  ))}
              </div>
            </div>
          </div>
        )}
        {deleting && name && (
          <ConfirmDialog
            open
            onOpenChange={(o) => !o && setDeleting(false)}
            title={t('skills.deleteTitle', { name })}
            description={t('skills.deleteDescription')}
            confirmLabel={t('skills.delete')}
            destructive
            onConfirm={async () => {
              await api.removeSkill(name);
              toast.success(t('skills.deleted', { name }));
              await queryClient.invalidateQueries({ queryKey: ['skills'] });
              onClose();
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

// SkillPreview renders a SKILL.md: its frontmatter as a card of fields, then
// its instructions as the chat renders a message.
function SkillPreview({ content }: { content: string }) {
  const t = useT();
  const { fields, body } = frontmatter(content);
  const description = fields.find(([k]) => k === 'description')?.[1];
  const rest = fields.filter(([k]) => k !== 'name' && k !== 'description');
  return (
    <div className="grid gap-5">
      {(description || rest.length > 0) && (
        <div className="rounded-2xl border border-line bg-surface p-4">
          <p className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-faint">{t('skills.whenToUse')}</p>
          <p className={cn('text-[13.5px] leading-relaxed', description ? 'text-primary' : 'italic text-rose-300')}>{description || t('skills.noDescription')}</p>
          {rest.length > 0 && (
            <dl className="mt-3 flex flex-wrap gap-1.5">
              {rest.map(([k, v]) => (
                <div key={k} className="flex max-w-full items-center gap-1.5 rounded-lg border border-line-faint bg-sunken px-2 py-1 text-[11.5px]">
                  <dt className="font-mono text-faint">{k}</dt>
                  <dd className="min-w-0 truncate text-tertiary">{v}</dd>
                </div>
              ))}
            </dl>
          )}
        </div>
      )}
      <Markdown text={body} className="text-[13.5px]" />
    </div>
  );
}

// ImportDialog finds skills in one of three places and imports the ones
// picked. The user's own AI tools are looked at as soon as it opens.
function ImportDialog({ project, onClose }: { project?: string; onClose: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const originLabel = useOriginLabel();
  const [from, setFrom] = useState<'tools' | 'folder' | 'git'>('tools');
  const [input, setInput] = useState({ folder: '', git: '' });
  const [source, setSource] = useState<string | null>('');
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<string | null>(null);
  const scan = useQuery({ queryKey: ['skillScan', source], queryFn: () => api.scanSkills(source ?? ''), enabled: source !== null, retry: false, staleTime: 0, gcTime: 0 });
  const found = useMemo(() => scan.data ?? [], [scan.data]);
  useEffect(() => {
    // What can be imported and isn't there yet starts picked.
    setPicked(new Set(found.filter((c) => !c.problem && !c.exists).map((c) => c.name)));
    setExpanded(null);
  }, [found]);

  const choose = (next: typeof from) => {
    setFrom(next);
    setSource(next === 'tools' ? '' : null);
  };
  const look = () => {
    const value = (from === 'folder' ? input.folder : input.git).trim();
    if (value) setSource(value);
  };
  const run = useMutation({
    mutationFn: () => api.importSkills({ source: source ?? '', names: [...picked], ...(project ? { project } : {}) }),
    onSuccess: async (list) => {
      toast.success(t('skills.imported', { count: list.length }));
      await queryClient.invalidateQueries({ queryKey: ['skills'] });
      onClose();
    },
  });

  const groups = useMemo(() => {
    const by = new Map<string, T.SkillCandidate[]>();
    for (const c of found) {
      const key = c.origin === 'claude-plugin' ? `claude-plugin:${c.plugin ?? ''}` : c.origin;
      by.set(key, [...(by.get(key) ?? []), c]);
    }
    return [...by.entries()];
  }, [found]);
  const importable = found.filter((c) => !c.problem);
  const tabs: { id: typeof from; icon: LucideIcon; label: string }[] = [
    { id: 'tools', icon: Sparkles, label: t('skills.importFrom.tools') },
    { id: 'folder', icon: Folder, label: t('skills.importFrom.folder') },
    { id: 'git', icon: Globe, label: t('skills.importFrom.git') },
  ];

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="flex h-[82vh] max-w-3xl flex-col gap-0 overflow-hidden p-0" data-skills-import-dialog>
        <div className="shrink-0 px-6 pb-4 pt-5">
          <DialogTitle>{t('skills.importTitle')}</DialogTitle>
          <DialogDescription className="mt-1">{project ? t('skills.importDescriptionProject') : t('skills.importDescription')}</DialogDescription>
          <div role="tablist" className="mt-4 grid grid-cols-3 gap-2">
            {tabs.map(({ id, icon: Icon, label }) => (
              <button
                key={id}
                role="tab"
                aria-selected={from === id}
                onClick={() => choose(id)}
                className={cn(
                  'flex items-center justify-center gap-2 rounded-xl border px-3 py-2.5 text-[13px] font-medium transition-all',
                  from === id ? 'border-brand-400/50 bg-brand-500/10 text-title shadow-[0_8px_24px_-16px_rgb(99_102_241/0.9)]' : 'border-line bg-surface text-muted hover:border-line-strong hover:text-primary',
                )}
              >
                <Icon className={cn('size-4', from === id ? 'text-brand-300' : 'text-subtle')} /> {label}
              </button>
            ))}
          </div>
          {from !== 'tools' && (
            <form
              className="mt-3 flex gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                look();
              }}
            >
              <Input
                autoFocus
                value={from === 'folder' ? input.folder : input.git}
                onChange={(e) => setInput((v) => ({ ...v, [from]: e.target.value }))}
                placeholder={from === 'folder' ? '~/skills/review-pr' : 'https://github.com/anthropics/skills/tree/main/skills'}
                className="font-mono text-[12.5px]"
                aria-label={from === 'folder' ? t('skills.importFrom.folder') : t('skills.importFrom.git')}
              />
              <Button type="submit" variant="secondary" disabled={scan.isFetching}>
                {scan.isFetching ? <LoaderCircle className="animate-spin" /> : <Search />} {t('skills.look')}
              </Button>
            </form>
          )}
          {from !== 'tools' && <p className="mt-2 text-[11.5px] text-faint">{from === 'folder' ? t('skills.folderHint') : t('skills.gitHint')}</p>}
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto border-t border-line-faint px-6 py-4">
          {source === null && <p className="py-10 text-center text-[13px] text-subtle">{from === 'folder' ? t('skills.folderPrompt') : t('skills.gitPrompt')}</p>}
          {source !== null && scan.isFetching && (
            <p className="flex items-center justify-center gap-2 py-10 text-[13px] text-subtle">
              <LoaderCircle className="size-4 animate-spin" /> {from === 'git' ? t('skills.cloning') : t('skills.looking')}
            </p>
          )}
          {scan.error && !scan.isFetching && <Notice>{errorMessage(scan.error)}</Notice>}
          {source !== null && !scan.isFetching && !scan.error && found.length === 0 && (
            <p className="py-10 text-center text-[13px] text-subtle">{from === 'tools' ? t('skills.noneInTools') : t('skills.noneThere')}</p>
          )}
          {!scan.isFetching &&
            groups.map(([key, list]) => {
              const origin = list[0].origin as SkillOrigin;
              const Icon = originIcons[origin] ?? Folder;
              return (
                <section key={key} className="mb-5 last:mb-0">
                  <h4 className="mb-2 flex items-center gap-2 text-[11.5px] font-medium uppercase tracking-wide text-faint">
                    <Icon className="size-3.5" /> {originLabel(origin, list[0].plugin)} <span className="tabular-nums">· {list.length}</span>
                  </h4>
                  <ul className="grid gap-1.5">
                    {list.map((c) => {
                      const checked = picked.has(c.name);
                      const isOpen = expanded === `${key}/${c.name}`;
                      return (
                        <li key={c.source + c.name} className={cn('rounded-xl border transition-colors', checked ? 'border-brand-400/40 bg-brand-500/[0.06]' : 'border-line bg-surface', c.problem && 'opacity-60')}>
                          <div className="flex items-start gap-3 px-3 py-2.5">
                            <button
                              role="checkbox"
                              aria-checked={checked}
                              aria-label={c.name}
                              disabled={!!c.problem}
                              onClick={() =>
                                setPicked((p) => {
                                  const next = new Set(p);
                                  if (next.has(c.name)) next.delete(c.name);
                                  else next.add(c.name);
                                  return next;
                                })
                              }
                              className={cn(
                                'mt-2 flex size-[18px] shrink-0 items-center justify-center rounded-md border transition-colors',
                                checked ? 'border-transparent bg-brand-500 text-white' : 'border-line-vivid bg-sunken',
                              )}
                            >
                              {checked && <Check className="size-3" strokeWidth={3} />}
                            </button>
                            <SkillTile name={c.name} size="sm" dim={!!c.problem} />
                            <div className="min-w-0 flex-1">
                              <div className="flex min-w-0 flex-wrap items-center gap-2">
                                <span className="font-mono text-[13px] font-medium text-title">{c.name}</span>
                                <span className="text-[11px] tabular-nums text-faint">
                                  {t('skills.files', { count: c.files })} · {formatBytes(c.size)}
                                </span>
                                {c.exists && !c.problem && <Badge variant="warning">{t('skills.replaces')}</Badge>}
                              </div>
                              {c.problem ? (
                                <p className="mt-0.5 flex items-center gap-1.5 text-[12px] text-rose-300">
                                  <AlertTriangle className="size-3.5" /> {t('skills.cantImport', { why: c.problem })}
                                </p>
                              ) : (
                                <p className={cn('mt-0.5 text-[12px] leading-relaxed text-muted', !isOpen && 'line-clamp-2')}>{c.description}</p>
                              )}
                            </div>
                            {c.content && (
                              <Tip label={isOpen ? t('skills.hidePreview') : t('skills.showPreview')}>
                                <Button variant="ghost" size="icon-sm" onClick={() => setExpanded(isOpen ? null : `${key}/${c.name}`)} aria-label={t('skills.showPreview')}>
                                  <ChevronDown className={cn('transition-transform', isOpen && 'rotate-180')} />
                                </Button>
                              </Tip>
                            )}
                          </div>
                          {isOpen && (
                            <div className="mx-3 mb-3 max-h-72 overflow-y-auto rounded-lg border border-line-faint bg-sunken px-4 py-3">
                              <SkillPreview content={c.content} />
                            </div>
                          )}
                        </li>
                      );
                    })}
                  </ul>
                </section>
              );
            })}
        </div>

        <div className="flex shrink-0 items-center gap-3 border-t border-line px-6 py-3">
          {run.error ? (
            <p className="flex min-w-0 items-center gap-2 text-[12.5px] text-rose-300">
              <AlertTriangle className="size-4 shrink-0" /> <span className="truncate">{errorMessage(run.error)}</span>
            </p>
          ) : importable.length > 0 ? (
            <div className="flex items-center gap-3 text-[12.5px]">
              <button className="text-brand-300 hover:underline" onClick={() => setPicked(new Set(importable.map((c) => c.name)))}>
                {t('skills.selectAll')}
              </button>
              <button className="text-subtle hover:text-primary" onClick={() => setPicked(new Set())}>
                {t('skills.selectNone')}
              </button>
            </div>
          ) : null}
          <div className="ml-auto flex gap-2">
            <Button variant="ghost" onClick={onClose}>
              <X /> {t('common.cancel')}
            </Button>
            <Button variant="primary" disabled={picked.size === 0 || run.isPending || scan.isFetching} onClick={() => run.mutate()} data-skills-import-run>
              {run.isPending ? <LoaderCircle className="animate-spin" /> : <Plus />} {t('skills.importCount', { count: picked.size })}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
