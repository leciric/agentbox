// Review, the prototype navigation's (lib/uiVerbs.ts) place for what the
// agents made, in three tabs over pages that were elsewhere: To review, the
// Work page (WorkPage) opened on what waits for a verdict, whose rows open
// the review carousel and which lists what was reviewed too; Pull requests,
// a project's (PullRequestsPanel, its project tab's and its panel's); and
// Proof, every project's screenshots and recordings (MediaView, which was
// Media).
import { useQuery } from '@tanstack/react-query';
import { ClipboardCheck, GitPullRequest, Images } from 'lucide-react';
import type { ReviewTab, View } from '../../App';
import { api } from '../../lib/api';
import { useT } from '../../lib/i18n';
import type { WorkFilter } from '../../lib/work';
import { MediaView } from '../media/MediaView';
import { openSettingsSection } from '../SettingsPage';
import { PullRequestsPanel } from '../PullRequestsPanel';
import { Notice } from '../ui/card';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../ui/tabs';
import { ProjectFilter } from '../v2/ProjectFilter';
import { WorkPage } from '../WorkView';

export function ReviewView({ tab = 'work', project, filter = 'review', onSelect }: { tab?: ReviewTab; project?: string; filter?: WorkFilter; onSelect: (view: View) => void }) {
  const t = useT();
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  // Pull requests are a project's: the one asked for, else the first that has them.
  const pullsProject = project || projects.data?.find((p) => p.pullRequests)?.name || '';
  const pullsOn = projects.data?.find((p) => p.name === pullsProject)?.pullRequests;
  return (
    <Tabs value={tab} onValueChange={(v) => onSelect({ kind: 'review', tab: v as ReviewTab, project })} className="flex h-full min-h-0 flex-col" data-review-area>
      <div className="flex shrink-0 flex-wrap items-center gap-3 border-b border-line-faint px-4 py-2 md:px-8">
        <h1 className="text-base font-semibold text-title">{t('verbs.review')}</h1>
        <TabsList className="min-w-0 overflow-x-auto [scrollbar-width:none]">
          <TabsTrigger value="work" data-review-tab="work">
            <ClipboardCheck />
            {t('verbs.review.toReview')}
          </TabsTrigger>
          <TabsTrigger value="pulls" data-review-tab="pulls">
            <GitPullRequest />
            {t('verbs.review.pulls')}
          </TabsTrigger>
          <TabsTrigger value="proof" data-review-tab="proof">
            <Images />
            {t('verbs.review.proof')}
          </TabsTrigger>
        </TabsList>
      </div>
      <TabsContent value="work" className="overflow-hidden">
        <WorkPage key={`${project ?? ''}:${filter}`} project={project} filter={filter} onSelect={onSelect} />
      </TabsContent>
      <TabsContent value="pulls" className="overflow-y-auto">
        <div className="mx-auto flex max-w-6xl flex-col gap-4 px-4 py-6 md:px-8">
          <div className="flex items-center gap-2">
            <ProjectFilter value={pullsProject} onChange={(p) => onSelect({ kind: 'review', tab: 'pulls', project: p || undefined })} />
          </div>
          {pullsOn ? (
            <PullRequestsPanel key={pullsProject} project={pullsProject} onSelect={onSelect} onOpenAccount={() => openSettingsSection(`project:${pullsProject}`)} />
          ) : (
            <Notice>{t('verbs.review.pullsOff')}</Notice>
          )}
        </div>
      </TabsContent>
      <TabsContent value="proof" className="overflow-hidden">
        <MediaView heading onSelect={onSelect} />
      </TabsContent>
    </Tabs>
  );
}
