import type * as T from '../../shared/api';

// PrNumberLink is a pull request's number as a link that opens it on GitHub in
// the system browser.
export function PrNumberLink({ pr, className }: { pr: Pick<T.PullRequest, 'number' | 'url'>; className?: string }) {
  if (!pr.url) return <span className={className}>#{pr.number}</span>;
  return (
    <a
      href={pr.url}
      className={`${className ?? ''} cursor-pointer text-brand-300 hover:underline`}
      data-pr-link={pr.number}
      onClick={(e) => {
        e.preventDefault();
        e.stopPropagation();
        void window.agentbox.openExternal(pr.url);
      }}
    >
      #{pr.number}
    </a>
  );
}
