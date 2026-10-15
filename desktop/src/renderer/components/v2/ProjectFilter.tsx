// ProjectFilter is the project picker atop v2's Overview and Past agents,
// the pages for every project: every project, or one.
import { useQuery } from '@tanstack/react-query';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import { projectLabel } from '../../lib/projectName';
import { ProjectIcon } from '../ProjectIcon';
import { Select, SelectOption } from '../ui/select';

export function ProjectFilter({ value, onChange }: { value: string; onChange: (project: string) => void }) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  return (
    <Select value={value} onChange={onChange} aria-label={t('v2.filters.project')} className="w-56" data-v2-project-filter>
      <SelectOption value="">{t('shell.allMedia.allProjects')}</SelectOption>
      {(projects.data ?? []).map((p) => (
        <SelectOption key={p.name} value={p.name}>
          <span className="flex min-w-0 items-center gap-2">
            <ProjectIcon project={p} className="size-4 rounded text-[9px]" />
            <span className="truncate">{projectLabel(p)}</span>
          </span>
        </SelectOption>
      ))}
    </Select>
  );
}
