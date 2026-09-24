#!/usr/bin/env node
// Reproduces starting a chat on a model Claude Code's own mid-session picker
// doesn't offer — Claude Fable — by writing it into Claude Code's settings.json
// before the adapter is launched (D45).
//
//   mise exec -- node scripts/demo/launch-model.mjs
//
// The bar here is a real turn, not an accepted setting: every model claim below
// is a real message to the real adapter, answered by the model naming itself.
//
// A project's chat runs Claude Code on the host, so that path runs here in
// full: the real daemon, the real API, the real adapter, the real credential.
// An agent's own machine needs Incus, which an AgentBox agent doesn't have
// (D43), so that path runs the real Go code against a stub `incus` whose exec
// really runs the commands, over a copy of the settings file provision.sh
// actually writes. What is stood in for is the container boundary, nothing else.
//
// Set NO_APP=1 to skip the screenshots, NO_LIVE=1 to skip every real turn
// (which spends real tokens and needs a Claude Code login on this machine).
import { execFileSync, spawnSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
import { homedir, tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const out = process.env.OUT_DIR ?? join(tmpdir(), 'launch-model-evidence');
const work = mkdtempSync(join(tmpdir(), 'agentbox-launch-model-'));
const bin = join(work, 'bin', 'agentbox');
const P = 'hello-stack';
const FABLE = 'claude-fable-5-1';

const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
  PATH: `${join(work, 'stub')}:${process.env.PATH}`,
};
for (const name of ['WAYLAND_DISPLAY', 'AGENTBOX_SOCKET', 'AGENTBOX_NO_AUTOSTART', 'DISPLAY']) delete env[name];

const started = Date.now();
const results = [];
const hide = (t) => String(t).replaceAll(work, '$TMP').replaceAll(homedir(), '$HOME');
const log = (...p) => console.log(`[${((Date.now() - started) / 1000).toFixed(1).padStart(5)}s]`, ...p.map(hide));
const check = (name, ok, detail = '') => {
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(0, 400)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function ab(...args) {
  const r = spawnSync(bin, args, { env, encoding: 'utf8', timeout: 300_000 });
  const o = hide(`${r.stdout ?? ''}${r.stderr ?? ''}`);
  console.log(`\n$ agentbox ${args.join(' ')}\n${o.trimEnd()}`);
  return o;
}
const git = (dir, ...a) => execFileSync('git', ['-C', dir, ...a], { encoding: 'utf8' }).trim();

function apiRequest(method, path, body) {
  return new Promise((done, fail) => {
    const data = body === undefined ? undefined : JSON.stringify(body);
    const req = http.request(
      { socketPath: sock, path, method, headers: data ? { 'content-type': 'application/json', 'content-length': Buffer.byteLength(data) } : {} },
      (res) => {
        let text = '';
        res.on('data', (c) => (text += c));
        res.on('end', () => {
          let json;
          try {
            json = JSON.parse(text);
          } catch {
            json = undefined;
          }
          done({ status: res.statusCode, text, json });
        });
      },
    );
    req.once('error', fail);
    if (data) req.write(data);
    req.end();
  });
}

function makeRepo(dir) {
  mkdirSync(dir, { recursive: true });
  git(dir, 'init', '-q', '-b', 'main');
  writeFileSync(join(dir, 'README.md'), '# demo\n');
  git(dir, 'add', '-A');
  execFileSync('git', ['-C', dir, '-c', 'user.email=d@x.invalid', '-c', 'user.name=D', 'commit', '-q', '-m', 'first']);
}

// leadSettings is the file AgentBox writes for a project's chat, on the host.
const sock = join(work, 'data', 'agentbox', 'run', 'agentbox.sock');
const leadSettingsPath = (project) => join(work, 'data', 'agentbox', 'projects', project, 'lead-home', '.claude', 'settings.json');
const readSettings = (path) => (existsSync(path) ? JSON.parse(readFileSync(path, 'utf8')) : undefined);

// say sends one real message to a chat and returns what the model answered.
async function say(project, text, { timeout = 240_000 } = {}) {
  const sent = await apiRequest('POST', `/v1/projects/${project}/chat/messages`, { text });
  if (sent.status >= 300 || !sent.json?.id) return { error: sent.text };
  const id = sent.json.id;
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    await sleep(1500);
    const th = await apiRequest('GET', `/v1/projects/${project}/chat`);
    const items = th.json?.items ?? [];
    const turn = items.find((i) => i.id === id);
    if (turn?.result) {
      const after = items.slice(items.indexOf(turn) + 1);
      return {
        state: turn.result.state,
        said: after.filter((i) => i.kind === 'assistant').map((i) => i.text ?? '').join('').trim(),
        errors: after.filter((i) => i.kind === 'error').map((i) => i.text ?? ''),
        notices: items.filter((i) => i.kind === 'notice').map((i) => i.text ?? ''),
        session: th.json.session,
      };
    }
    if (th.json?.session?.error) return { error: th.json.session.error, session: th.json.session };
  }
  return { error: 'timed out' };
}

const setModel = (project, value) => apiRequest('PUT', `/v1/projects/${project}/chat/options/model`, { value });

// restart ends the chat's session so the next start launches a fresh adapter,
// which is the only time Claude Code reads its settings.json. Clearing drops
// the conversation with it; the daemon going down keeps it, which is what a
// resume needs.
async function restart(project, { keepConversation = false } = {}) {
  if (keepConversation) {
    ab('daemon', 'stop');
    await sleep(1500);
    ab('daemon', 'start');
    for (let i = 0; i < 30 && !existsSync(sock); i++) await sleep(500);
    await sleep(1000);
  } else {
    await apiRequest('DELETE', `/v1/projects/${project}/chat`);
    await sleep(1000);
  }
  await apiRequest('POST', `/v1/projects/${project}/chat/start`);
  for (let i = 0; i < 60; i++) {
    await sleep(1000);
    const th = await apiRequest('GET', `/v1/projects/${project}/chat`);
    if (th.json?.session?.state && th.json.session.state !== 'off' && th.json.session.state !== 'starting') return th.json;
    if (th.json?.session?.error) return th.json;
  }
  return undefined;
}

async function main() {
  mkdirSync(out, { recursive: true });
  log('Building agentbox');
  mkdirSync(join(work, 'bin'), { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });

  // A stub incus. `list`/`query` answer for the project; `exec` really runs
  // the command, with an agent's /home/<user> remapped into this run's
  // directory, so the in-agent read-merge-write is the real Go code over a
  // real file — only the machine boundary is stood in for (D43).
  const fakeHome = join(work, 'agent-home');
  mkdirSync(join(fakeHome, '.claude'), { recursive: true });
  const user = process.env.USER ?? 'agent';
  mkdirSync(join(work, 'stub'), { recursive: true });
  writeFileSync(
    join(work, 'stub', 'incus'),
    `#!/usr/bin/env bash
# A stub incus for an evidence run: see scripts/demo/launch-model.mjs.
case "$1" in
  list) echo '[{"name":"ab-${P}-agent-01","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]' ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
  exec)
    # Drop "exec <instance> [-T] --", then run the rest here, with the agent's
    # home remapped. runuser -l <user> -c <cmd> becomes bash -c <cmd>.
    shift 2
    while [ "$1" = "-T" ] || [ "$1" = "--" ]; do shift; done
    args=()
    for a in "$@"; do args+=("\${a//\\/home\\/${user}/${fakeHome}}"); done
    if [ "\${args[0]}" = "runuser" ]; then exec bash -c "\${args[4]}"; fi
    exec "\${args[@]}"
    ;;
esac
exit 0
`,
    { mode: 0o755 },
  );

  const repo = join(work, 'repos', P);
  makeRepo(repo);
  ab('add', repo, '--name', P);

  // The real Claude Code login on this machine: without it nothing below runs
  // a real turn, and a real turn is the whole point.
  const token = process.env.CLAUDE_CODE_OAUTH_TOKEN ?? (existsSync('/tmp/probe/token') ? readFileSync('/tmp/probe/token', 'utf8') : '');
  const live = !process.env.NO_LIVE && token.startsWith('sk-ant-oat');
  spawnSync(bin, ['auth', 'claude', '--token-stdin'], { env, input: token || 'sk-ant-oat01-demo', encoding: 'utf8' });
  if (!live) log('NO real credential: skipping every real turn');

  // ---------------------------------------------------------------- the lead
  // A project's chat starts, so AgentBox writes its private HOME the way it
  // always has: the permission policy that keeps it off this machine's shell.
  log('Starting the project chat once, so its settings exist');
  await apiRequest('POST', `/v1/projects/${P}/chat/start`);
  await sleep(4000);
  const settingsPath = leadSettingsPath(P);
  const before = readSettings(settingsPath);
  console.log(`\nthe project chat's settings.json, before any model:\n${JSON.stringify(before, null, 2)}`);
  check('the project chat has its permission policy', Boolean(before?.permissions?.deny?.includes('Bash')), JSON.stringify(before));
  check('and no model of its own yet', before !== undefined && before.model === undefined, JSON.stringify(before));

  // 1. Naming a model the menu never offered. This is the chicken and egg:
  // Fable is advertised only once a session runs on it, so the first time it
  // can only be named, never picked.
  const named = await setModel(P, FABLE);
  check(`naming ${FABLE} is accepted, though no menu listed it`, named.status === 200, named.text);
  const model = named.json?.options?.find((o) => o.id === 'model');
  check('and the composer shows it back', model?.value === FABLE, JSON.stringify(model));

  // 2. It reaches Claude Code's own settings — merged, not written over.
  log('Starting the project chat on ' + FABLE);
  await restart(P);
  const after = readSettings(settingsPath);
  console.log(`\nthe project chat's settings.json, after choosing ${FABLE}:\n${JSON.stringify(after, null, 2)}`);
  check(`settings.json now names ${FABLE}`, after?.model === FABLE, JSON.stringify(after));
  check('and the permission policy that keeps the lead off this machine survived', Boolean(after?.permissions?.deny?.includes('Bash')) && after?.permissions?.blockReadsOutsideWorkingDirectories === true, JSON.stringify(after?.permissions));
  check('and so did every other setting', after?.includeCoAuthoredBy === false, JSON.stringify(after));
  writeFileSync(join(out, 'lead-settings-fable.json'), JSON.stringify(after, null, 2));

  // 3. The bar: a real turn, answering with its own model name.
  if (live) {
    log('Asking the project chat what it is');
    const said = await say(P, 'Reply with just your model name, nothing else.');
    console.log(`\nthe project chat said: ${JSON.stringify(said.said ?? said.error)}`);
    check('the project chat really runs on Claude Fable', /fable/i.test(said.said ?? ''), said.said ?? said.error);
    const menu = said.session?.options?.find((o) => o.id === 'model');
    check('and the adapter advertises Fable back, so nothing is applied silently', (menu?.choices ?? []).some((c) => c.value === FABLE), JSON.stringify((menu?.choices ?? []).map((c) => c.value)));
    check('the menu it is chosen from is still the adapter\'s own', (menu?.choices ?? []).length > 1, JSON.stringify((menu?.choices ?? []).map((c) => c.value)));
    writeFileSync(join(out, 'lead-turn-fable.txt'), `${said.said}\n\nmenu: ${JSON.stringify((menu?.choices ?? []).map((c) => c.value))}\n`);

    // 4. Away from Fable mid-session, and back. This still goes through
    // set_config_option, which the launch-time channel doesn't replace.
    log('Switching to Opus mid-session');
    const toOpus = await setModel(P, 'opus');
    check('switching away from Fable mid-session is accepted', toOpus.status === 200, toOpus.text);
    const onOpus = await say(P, 'Reply with just your model name, nothing else.');
    console.log(`\nafter switching to opus, it said: ${JSON.stringify(onOpus.said ?? onOpus.error)}`);
    check('and the next turn really runs on Opus', /opus/i.test(onOpus.said ?? ''), onOpus.said ?? onOpus.error);

    log('Switching back to Fable mid-session');
    const backToFable = await setModel(P, FABLE);
    check('switching back to Fable mid-session is accepted', backToFable.status === 200, backToFable.text);
    const backOn = await say(P, 'Reply with just your model name, nothing else.');
    console.log(`\nafter switching back, it said: ${JSON.stringify(backOn.said ?? backOn.error)}`);
    check('and the turn after that really runs on Fable again', /fable/i.test(backOn.said ?? ''), backOn.said ?? backOn.error);
    writeFileSync(join(out, 'lead-switching.txt'), `fable: ${said.said}\nopus: ${onOpus.said}\nfable again: ${backOn.said}\n`);

    // 5. Resume. settings.json is priority 2 and a resumed session's own model
    // is priority 3, so what AgentBox stored wins — which is what keeps a
    // resumed chat on the model the composer says it is on.
    log('Restarting AgentBox and resuming the chat, with Fable stored');
    const itemsBefore = ((await apiRequest('GET', `/v1/projects/${P}/chat`)).json?.items ?? []).length;
    await restart(P, { keepConversation: true });
    const resumed = await say(P, 'Reply with just your model name, nothing else.');
    console.log(`\nafter a resume, it said: ${JSON.stringify(resumed.said ?? resumed.error)}`);
    check('a resumed session still runs on the stored model', /fable/i.test(resumed.said ?? ''), resumed.said ?? resumed.error);
    const resumedThread = await apiRequest('GET', `/v1/projects/${P}/chat`);
    const itemsAfter = (resumedThread.json?.items ?? []).length;
    check('and it resumed rather than starting over', itemsAfter > itemsBefore, `${itemsBefore} items before, ${itemsAfter} after`);
    check('with no notice that the earlier session was lost', !(resumed.notices ?? []).some((n) => /doesn't remember the conversation above/.test(n)), JSON.stringify(resumed.notices));
    writeFileSync(join(out, 'resume.txt'), `after a restart, on the stored model: ${resumed.said}\n`);
  }

  // ------------------------------------------------------- a normal model
  // The path every existing agent is on has to behave exactly as before.
  const Q = 'plain-stack';
  const repo2 = join(work, 'repos', Q);
  makeRepo(repo2);
  ab('add', repo2, '--name', Q);
  log('A second project chat, left on the model it was given');
  await apiRequest('POST', `/v1/projects/${Q}/chat/start`);
  await sleep(4000);
  const plainBefore = readSettings(leadSettingsPath(Q));
  check('a chat nobody chose a model for gets no model key at all', plainBefore !== undefined && plainBefore.model === undefined, JSON.stringify(plainBefore));
  const toOpus = await setModel(Q, 'opus');
  check('picking a model off the menu still works', toOpus.status === 200, toOpus.text);
  await restart(Q);
  const plainAfter = readSettings(leadSettingsPath(Q));
  console.log(`\nthe second project chat's settings.json:\n${JSON.stringify(plainAfter, null, 2)}`);
  check('an ordinary model goes into settings.json the same way', plainAfter?.model === 'opus', JSON.stringify(plainAfter));
  if (live) {
    const plainSaid = await say(Q, 'Reply with just your model name, nothing else.');
    console.log(`\nthe ordinary chat said: ${JSON.stringify(plainSaid.said ?? plainSaid.error)}`);
    check('and a chat on an ordinary model is unaffected', /opus/i.test(plainSaid.said ?? ''), plainSaid.said ?? plainSaid.error);
    check('with nothing said about a model that did not apply', !(plainSaid.notices ?? []).some((n) => /wouldn't set the model|couldn't tell/i.test(n)), JSON.stringify(plainSaid.notices));
  }

  // --------------------------------------------- an agent's own settings
  // The other path: the same merge, inside an agent's machine, over the file
  // provision.sh really writes.
  log("An agent's own settings, over what provision.sh writes");
  const provisioned = { skipDangerousModePermissionPrompt: true };
  writeFileSync(join(fakeHome, '.claude', 'settings.json'), JSON.stringify(provisioned));
  ab('create', P, '--name', 'agent-01', '--ai', 'claude', '--title', 'On Fable');
  await apiRequest('PUT', `/v1/agents/${P}/agent-01/chat/options/model`, { value: FABLE });
  // Starting its chat runs PrepareChatModel through the stub's exec; the
  // adapter itself can't launch without a machine, which is expected here.
  await apiRequest('POST', `/v1/agents/${P}/agent-01/chat/start`);
  await sleep(4000);
  const agentSettings = readSettings(join(fakeHome, '.claude', 'settings.json'));
  console.log(`\nthe agent's own ~/.claude/settings.json afterwards:\n${JSON.stringify(agentSettings, null, 2)}`);
  check(`an agent's settings.json names ${FABLE}`, agentSettings?.model === FABLE, JSON.stringify(agentSettings));
  check('and skipDangerousModePermissionPrompt is still there', agentSettings?.skipDangerousModePermissionPrompt === true, JSON.stringify(agentSettings));
  writeFileSync(join(out, 'agent-settings-fable.json'), JSON.stringify(agentSettings, null, 2));

  // ------------------------------------------------------- failing visibly
  // A model that doesn't exist is accepted by the launch-time resolver and
  // advertised back, and only fails when a turn is tried. That is loud, and
  // it is the reason nothing here treats the menu as proof.
  if (live) {
    log('A model that does not exist, to see it fail out loud');
    await setModel(P, 'claude-not-a-real-model-9');
    await restart(P);
    const bad = await say(P, 'Reply with just your model name, nothing else.', { timeout: 120_000 });
    const shown = [...(bad.errors ?? []), bad.error ?? ''].join(' ');
    console.log(`\na turn on a model that doesn't exist: ${JSON.stringify(shown.slice(0, 300))}`);
    check('a model that does not exist fails out loud rather than answering on something else', /model/i.test(shown) && /claude-not-a-real-model-9/.test(shown), shown);
    check('and it names the model it could not use', /claude-not-a-real-model-9/.test(shown), shown);
    writeFileSync(join(out, 'missing-model.txt'), shown);

    // And it is recoverable from the chat, which is what the message tells you
    // to do: pick another model, and the next session is on it.
    log('Recovering by choosing a model that does exist');
    await setModel(P, FABLE);
    await restart(P);
    const recovered = await say(P, 'Reply with just your model name, nothing else.');
    console.log(`\nafter picking another model, it said: ${JSON.stringify(recovered.said ?? recovered.error)}`);
    check('choosing another model afterwards recovers the chat', /fable/i.test(recovered.said ?? ''), recovered.said ?? recovered.error);
  }

  // ------------------------------------------------------------- the app
  if (!process.env.NO_APP) await screenshots();

  ab('daemon', 'stop');
  const failed = results.filter((r) => r.startsWith('FAIL'));
  const summary = `${results.filter((r) => r.startsWith('PASS')).length}/${results.length} checks passed\n\n${results.join('\n')}\n`;
  writeFileSync(join(out, 'checks.txt'), summary);
  console.log(`\n${summary}\nEvidence in ${out}`);
  process.exit(failed.length ? 1 : 0);
}

// screenshots drives the real Electron app on a private display (D20, D43),
// for the two menus a model can be named in.
async function screenshots() {
  const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');
  log('Building the desktop app');
  execFileSync('npm', ['--prefix', desktop, 'run', 'build'], { cwd: root, stdio: 'inherit' });
  const xvfb = await startXvfb({ width: 1500, height: 1000 });
  const app = await electron.launch({ args: [desktop], env: { ...env, DISPLAY: xvfb.display } });
  const page = await app.firstWindow();
  await page.waitForLoadState('domcontentloaded');
  await page.waitForTimeout(3000);
  const shot = async (name) => {
    await page.screenshot({ path: join(out, `${name}.png`) });
    log('screenshot', `${name}.png`);
  };

  // Settings' "Model for new agents", with the box a model is named in.
  await page.getByRole('button', { name: 'Settings', exact: true }).first().click();
  await page.waitForTimeout(900);
  const defaults = page.locator('[data-default-model]');
  if (await defaults.count()) {
    await defaults.first().click();
    await page.waitForTimeout(700);
    await shot('settings-model-menu');
    await page.locator('[data-model-by-name]').first().scrollIntoViewIfNeeded();
    await page.waitForTimeout(400);
    await shot('settings-name-a-model');
    await page.keyboard.press('Escape');
    await page.waitForTimeout(400);
  }

  // And the composer's own model menu, in the project chat now on Fable.
  await page.getByText(P, { exact: false }).first().click();
  await page.waitForTimeout(2500);
  await shot('chat-on-fable');
  const menu = page.locator('[data-chat-option="model"]');
  if (await menu.count()) {
    await menu.first().click();
    await page.waitForTimeout(700);
    await shot('composer-model-menu-with-fable');
    await page.locator('[data-model-by-name]').first().scrollIntoViewIfNeeded();
    await page.waitForTimeout(400);
    await shot('composer-name-a-model');
  }
  await app.close();
  await xvfb.stop();
}

main().catch(async (err) => {
  console.error(err);
  ab('daemon', 'stop');
  process.exit(1);
});
