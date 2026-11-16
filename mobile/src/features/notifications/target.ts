// What a push notification opens, and the quiet hours' arithmetic: kept out
// of push.ts, which needs the phone's native modules, so Node's test runner
// can read them.

const path = (...parts: string[]) => parts.map(encodeURIComponent).join('/');

// notificationTarget is the screen a notification opens, from what the
// daemon put in it (internal/daemon/phonepush.go): a pull request ready to
// review opens its project's review on that agent; anything else about an
// agent opens its chat. Checks failing on a pull request whose agent is gone
// open the project's review, which lists its open pull requests.
export function notificationTarget(data: Record<string, unknown> | undefined): string | undefined {
  const ref = typeof data?.ref === 'string' ? data.ref : '';
  const [project, agent] = ref.split('/');
  if (!project) return undefined;
  if (data?.kind === 'pr_ready') return `/review/${path(project)}${agent ? `?agent=${encodeURIComponent(ref)}` : ''}`;
  if (!agent && data?.kind === 'ci_failed') return `/review/${path(project)}`;
  return `/chat/${agent ? path(project, agent) : path(project)}`;
}

// sameScreen says the app shows target already, so its notification isn't
// shown over it.
export const sameScreen = (target: string | undefined, pathname: string) => !!target && decodeURIComponent(target.split('?')[0]) === decodeURIComponent(pathname);

const day = 24 * 60;

// hhmm is minutes after midnight as "22:00".
export const hhmm = (minutes: number) => `${String(Math.floor(minutes / 60)).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`;

// shiftQuiet moves a quiet-hours bound by step minutes, round the clock.
export const shiftQuiet = (minutes: number, step: number) => (((minutes + step) % day) + day) % day;
