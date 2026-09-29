// Phones on the local network are served the app's web version by the daemon
// (internal/daemon/lan.go), which is built without it: the app is what has
// the build, so it installs it in the daemon whenever the daemon has another
// version, over the same socket everything else goes. That works the same with
// the daemon on this machine or in AgentBox's VM.
//
// The voice models' runtime (onnxruntime's .wasm, most of the build's size)
// and the voice workers stay behind: a phone's browser records and speaks
// with its own.
import { createHash } from 'node:crypto';
import { readdirSync, readFileSync } from 'node:fs';
import { join, relative, sep } from 'node:path';
import { isLocal, requestOptions } from './connection';

const rendererDir = () => join(__dirname, '../renderer');

// left out of what phones are served
const skip = (name: string) => /\.(wasm|onnx)$/.test(name) || /^(kokoro|whisper)\.worker-/.test(name) || name === 'index.html';

function files(dir: string, root = dir): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) out.push(...files(path, root));
    else if (!skip(entry.name)) out.push(relative(root, path).split(sep).join('/'));
  }
  return out;
}

// webVersion names this build: the app's version, and a hash of web.html,
// which names every asset by its content's hash, so two builds of one version
// in development aren't taken for each other.
export function webVersion(appVersion: string, webHtml: Buffer): string {
  return `${appVersion}-${createHash('sha256').update(webHtml).digest('hex').slice(0, 12)}`;
}

function send(method: string, path: string, body?: Buffer): Promise<{ status: number; body: string }> {
  const { module, options } = requestOptions(path);
  return new Promise((resolve, reject) => {
    const req = module.request(
      { ...options, method, headers: { ...(options.headers as Record<string, string>), 'Content-Length': body?.length ?? 0 } },
      (res) => {
        const chunks: Buffer[] = [];
        res.on('data', (c: Buffer) => chunks.push(c));
        res.on('error', reject);
        res.on('end', () => resolve({ status: res.statusCode ?? 0, body: Buffer.concat(chunks).toString('utf8') }));
      },
    );
    req.on('error', reject);
    req.end(body);
  });
}

let installing: Promise<void> | undefined;

// installPhoneWeb puts this build in this machine's daemon, unless it has it.
export function installPhoneWeb(appVersion: string): Promise<void> {
  installing ??= install(appVersion).finally(() => {
    installing = undefined;
  });
  return installing;
}

async function install(appVersion: string): Promise<void> {
  if (!isLocal()) return;
  const root = rendererDir();
  let html: Buffer;
  try {
    html = readFileSync(join(root, 'web.html'));
  } catch {
    return; // a build without the web version
  }
  const version = webVersion(appVersion, html);
  const status = await send('GET', '/v1/lan');
  if (status.status === 404) return; // a daemon from before phones
  if (status.status !== 200) throw new Error(`GET /v1/lan: ${status.status} ${status.body}`);
  if ((JSON.parse(status.body) as { webVersion?: string }).webVersion === version) return;
  for (const file of files(root)) {
    const path = `/v1/lan/web/${encodeURIComponent(version)}/files/${file.split('/').map(encodeURIComponent).join('/')}`;
    const res = await send('PUT', path, readFileSync(join(root, file)));
    if (res.status >= 300) throw new Error(`installing ${file} for phones: ${res.status} ${res.body}`);
  }
  const res = await send('POST', `/v1/lan/web/${encodeURIComponent(version)}`);
  if (res.status >= 300) throw new Error(`installing the web app for phones: ${res.status} ${res.body}`);
}
