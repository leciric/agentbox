// Where the phone's web app is (web/PhoneApp.tsx), as the page's fragment:
// #/p/<project>, #/a/<project>/<agent>, #/pr/<project>[/<number>], or the
// list, so a reload, a link and the phone's back gesture all land there.
// pulls' pr is the pull request to point out, opened from its agent's chat.
export type Route = { kind: 'list' } | { kind: 'project'; project: string } | { kind: 'agent'; ref: string } | { kind: 'pulls'; project: string; pr?: number };

export function parseRoute(hash: string): Route {
  let parts: string[];
  try {
    parts = hash.replace(/^#\/?/, '').split('/').map(decodeURIComponent);
  } catch {
    return { kind: 'list' }; // a malformed escape
  }
  // A name is one segment: an escaped slash in it would split the ref.
  if (parts.some((part) => part.includes('/'))) return { kind: 'list' };
  if (parts[0] === 'p' && parts[1]) return { kind: 'project', project: parts[1] };
  if (parts[0] === 'a' && parts[1] && parts[2]) return { kind: 'agent', ref: `${parts[1]}/${parts[2]}` };
  if (parts[0] === 'pr' && parts[1]) return { kind: 'pulls', project: parts[1], pr: Number(parts[2]) || undefined };
  return { kind: 'list' };
}

export function routeHash(route: Route): string {
  if (route.kind === 'project') return `#/p/${encodeURIComponent(route.project)}`;
  if (route.kind === 'pulls') return `#/pr/${encodeURIComponent(route.project)}${route.pr ? `/${route.pr}` : ''}`;
  if (route.kind === 'agent') return `#/a/${route.ref.split('/').map(encodeURIComponent).join('/')}`;
  return '#/';
}
