// The daemon paths agentbox-media://api/... reaches (main/media.ts), and
// nothing else of its API. Kept apart from media.ts, which needs Electron, so
// the test can load it.

// chatImage and artifactPage: a picture sent in an agent's chat, or in a
// session of a project's (.../sessions/<session>/chat/..., which is where the
// app asks for every session's, "main" included), or a SnapShot waiting to
// be; and the HTML of a page a project's chats published on Hatch, which the
// daemon sends with a sandboxing Content-Security-Policy, framed by the
// page's preview.
const chatImage = /^\/v1\/((projects\/[^/]+(\/sessions\/[^/]+)?|agents\/[^/]+\/[^/]+)\/chat\/images\/[0-9a-f]{16}|snaps\/[0-9a-f]{16}\/image)$/;
const artifactPage = /^\/v1\/projects\/[^/]+\/artifacts\/[A-Za-z0-9_-]+\/page$/;
// pullImage is a picture in a pull request's description, which the daemon
// reads from GitHub with the project's account: a private repository's
// attachments need its token, which never reaches the renderer.
const pullImage = /^\/v1\/projects\/[^/]+\/pulls\/image$/;

// apiPath is the daemon path an agentbox-media://api URL asks for, or null
// for one it may not.
export function apiPath(url: URL): string | null {
  if (pullImage.test(url.pathname)) return url.pathname + url.search;
  if (chatImage.test(url.pathname) || artifactPage.test(url.pathname)) return url.pathname;
  return null;
}
