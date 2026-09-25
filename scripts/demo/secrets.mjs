#!/usr/bin/env node
// Reproduces the Secrets evidence: an API key handed to a project's agents
// after they exist, and everywhere its value doesn't appear.
//
//   mise exec -- node scripts/demo/secrets.mjs > .demo-runs/secrets/demo.log 2>&1
//
// Real daemon, real command line, real `agentbox mcp` on the project's own lead
// socket, real Electron app on a private X display; a stub incus that keeps
// every file AgentBox writes into an agent, because this runs inside an
// AgentBox agent and those have no Incus of their own (D43).
// The stub is what makes delivery visible: what lands under its files directory
// is byte for byte what `incus exec` would have written inside the machine.
//
// Set NO_APP=1 to skip the app, and DEMO_DISPLAY to use a display that
// already exists instead of starting one.
import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { homedir, userInfo } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/secrets/media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

// A short path: the daemon's unix socket has to fit in 107 bytes.
const work = join(homedir(), '.cache', 'agentbox-secrets');
const bin = join(work, 'bin', 'agentbox');
const stub = join(work, 'stub');
const incusState = join(work, 'incus');
const files = join(incusState, 'files');
const P = 'hello-stack';
const INSTANCE = `ab-${P}-agent-01`;

// The values this run hands out. They are the string every check looks for
// where it must not be.
const KEY = 'sk-openai-EVIDENCE-VALUE-1';
const KEY2 = 'sk-openai-EVIDENCE-VALUE-2';
const STRIPE = 'sk-stripe-EVIDENCE-VALUE';
const AGENT_ONLY = 'sk-stripe-AGENT-ONLY-VALUE';
const FROM_APP = 'sk-sentry-FROM-THE-APP';

const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_BIN: bin,
  AGENTBOX_PREVIEW_ADDR: 'off',
  INCUS_STATE: incusState,
  XDG_SESSION_TYPE: 'x11',
  PATH: `${stub}:${process.env.PATH}`,
};
for (const name of ['WAYLAND_DISPLAY', 'AGENTBOX_SOCKET', 'AGENTBOX_NO_AUTOSTART', 'DISPLAY']) delete env[name];

const started = Date.now();
const results = [];
const hide = (t) => String(t).replaceAll(work, '$WORK');
const log = (...parts) => console.log(`[${((Date.now() - started) / 1000).toFixed(1).padStart(5)}s]`, ...parts.map(hide));
const check = (name, ok, detail = '') => {
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(0, 400)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ab runs agentbox, echoing the command. Never with a value in argv: that is
// the point of the feature, and of this script.
function ab(...args) {
  let input = '';
  if (args[0] === '--stdin') {
    [, input, ...args] = args;
  }
  const r = spawnSync(bin, args, { env, encoding: 'utf8', input, timeout: 120_000 });
  const out = hide(`${r.stdout ?? ''}${r.stderr ?? ''}`);
  const shown = input ? `printf '%s' <the value> | agentbox ${args.join(' ')}` : `agentbox ${args.join(' ')}`;
  console.log(`\n$ ${shown}\n${out.trimEnd()}`);
  return out;
}
const sh = (script) => execFileSync('/bin/sh', ['-c', script], { encoding: 'utf8' });
const shot = (page, name) => page.screenshot({ path: join(media, `secrets-${name}.png`) });

// inAgent reads a file AgentBox wrote inside an agent, as the stub kept it.
const agentPath = (path, instance = INSTANCE) => join(files, instance, path);
const inAgent = (path, instance = INSTANCE) => (existsSync(agentPath(path, instance)) ? readFileSync(agentPath(path, instance), 'utf8') : '');
// The agent's user is the host user: AgentBox maps it into the machine, so the
// paths inside an agent are that user's home.
const USER = userInfo().username;
const HOME = `home/${USER}`;
const SECRETS_ENV = `${HOME}/.config/agentbox/secrets.env`;
const ENV_FILE = `${HOME}/.config/agentbox/env`;
const BRIEF = `${HOME}/.claude/CLAUDE.md`;

// instances tells the stub which machines exist. Written before a step that
// needs a new one, and read fresh on every stub call.
function instances(...names) {
  const all = [`${P}-base`, `${P}-base-next`, ...names].map(
    (n, i) => `{"name":"ab-${n.startsWith(P) ? n : `${P}-${n}`}","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.${10 + i}"}]}}}}`,
  );
  writeFileSync(join(incusState, 'instances.json'), `[${all.join(',')}]\n`);
}

// The stub incus. Everything exits 0; the one case that does real work is the
// shape incus.WriteFile uses, which is how a file reaches an agent:
//   incus exec <instance> -T -- sh -c <script> sh <path> <uid:gid> <mode>
// with the content on stdin. Every invocation is logged, so a command AgentBox
// runs *inside* a machine (the base-save scrub, for one) can be read back.
// (\${11} is escaped: this is a JavaScript template literal, and the shell's
// eleventh positional parameter is the file's mode.)
const stubIncus = `#!/bin/sh
echo "$@" >> "$INCUS_STATE/commands.log"
case "$1" in
  list) cat "$INCUS_STATE/instances.json" ;;
  query)
    case "$2" in
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
  exec)
    if [ "$5" = "sh" ] && [ "$6" = "-c" ] && [ $# -eq 11 ]; then
      dest="$INCUS_STATE/files/$2$9"
      mkdir -p "$(dirname "$dest")"
      cat > "$dest"
      chmod "\${11}" "$dest"
    fi ;;
esac
exit 0
`;

// mcp drives the project chat's tools the way Claude Code does: JSON-RPC on
// stdin and stdout, one message per line.
function mcp(socket) {
  const proc = spawn(bin, ['mcp'], { env: { ...env, AGENTBOX_SOCKET: socket }, stdio: ['pipe', 'pipe', 'inherit'] });
  let buffer = '';
  const pending = new Map();
  proc.stdout.on('data', (chunk) => {
    buffer += chunk;
    for (let i; (i = buffer.indexOf('\n')) >= 0; ) {
      const line = buffer.slice(0, i).trim();
      buffer = buffer.slice(i + 1);
      if (!line) continue;
      const msg = JSON.parse(line);
      const done = pending.get(msg.id);
      if (done) {
        pending.delete(msg.id);
        done(msg);
      }
    }
  });
  let id = 0;
  const send = (method, params) =>
    new Promise((done) => {
      const n = ++id;
      pending.set(n, done);
      proc.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', id: n, method, params })}\n`);
    });
  return {
    proc,
    send,
    async call(name, args = {}) {
      const answer = await send('tools/call', { name, arguments: args });
      const text = answer.result?.content?.[0]?.text ?? JSON.stringify(answer.error);
      console.log(`\n[the chat calls] ${name}(${JSON.stringify(args)})\n${hide(text).trimEnd()}`);
      return text;
    },
  };
}

function leadSocket() {
  const dir = join(work, 'data/agentbox/run/leads');
  const found = existsSync(dir) ? readdirSync(dir) : [];
  if (found.length === 0) throw new Error('no lead socket');
  return join(dir, found[0]); // one project here, so one socket
}

function repoFor(name) {
  const path = join(work, name);
  cpSync(join(root, 'testdata/fixtures/hello-stack'), path, { recursive: true });
  for (const args of [['init', '-q', '-b', 'main'], ['add', '-A'], ['-c', 'commit.gpgsign=false', 'commit', '-qm', `${name} fixture`]]) {
    execFileSync('git', ['-C', path, ...args], { env, encoding: 'utf8' });
  }
  return path;
}

async function step(title, fn) {
  console.log(`\n\n######## ${title}`);
  try {
    await fn();
  } catch (err) {
    check(title, false, err.message);
    console.log(hide(err.stack ?? err));
    throw err;
  }
}

let xvfb;
let app;
let page;
let chat;
let failed = false;
try {
  console.log('######## Setup (off camera)');
  rmSync(work, { recursive: true, force: true });
  mkdirSync(media, { recursive: true });
  mkdirSync(join(work, 'bin'), { recursive: true });
  mkdirSync(stub, { recursive: true });
  mkdirSync(files, { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });
  writeFileSync(join(stub, 'incus'), stubIncus, { mode: 0o755 });
  instances('agent-01', 'agent-02');
  const repo = repoFor(P);
  if (!process.env.NO_APP) execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'ignore' });
  // A stand-in Claude Code token, so an agent that runs Claude Code can be
  // created here. Nothing in this run talks to Anthropic.
  spawnSync(bin, ['auth', 'claude', '--token-stdin'], { env, input: 'sk-ant-oat01-evidence', encoding: 'utf8' });
  log('built agentbox and the desktop app; a stub incus keeps what is written into agents');
  ab('add', repo, '--name', P);

  await step('1. A secret from the command line, and the places its value is not', async () => {
    // The whole reason the value comes from stdin: an argument stays in the
    // shell's history, and is readable in `ps` by anything on the machine.
    const refused = ab('secrets', 'set', P, 'OPENAI_API_KEY', KEY);
    check('a value passed as an argument is refused, with the reason', refused.includes('never an argument') && refused.includes('shell history') && refused.includes('ps'), refused);
    check('and the refusal does not echo the value', !refused.includes(KEY), refused);

    const stored = ab('--stdin', `${KEY}\n`, 'secrets', 'set', P, 'OPENAI_API_KEY', '--value-stdin');
    check('a secret read from stdin is stored for every agent of the project', stored.includes(`Stored OPENAI_API_KEY for every agent of ${P}`), stored);
    check('the answer names the variable, and says the value cannot be read back', stored.includes('$OPENAI_API_KEY') && stored.includes("can't be read back"), stored);
    check('storing it prints no value', !stored.includes(KEY), stored);

    const list = ab('secrets', 'list', P);
    check('the list gives names, scopes and times', /\$OPENAI_API_KEY\s+project\s+/.test(list), list);
    check('the list gives no values', !list.includes(KEY), list);

    // At rest: the key is encrypted, and the key file is the user's alone.
    const db = join(work, 'data/agentbox/state.db');
    const raw = readFileSync(db);
    check('the value is not in state.db in the clear', !raw.includes(KEY), `${raw.length} bytes scanned`);
    const rows = sh(`sqlite3 ${db} "SELECT project, agent, name, length(value), hex(substr(value,1,8)) FROM secrets;"`).trim();
    console.log(`\n$ sqlite3 state.db "SELECT project, agent, name, length(value), hex(substr(value,1,8)) FROM secrets"\n${rows}`);
    check('what is stored is a sealed blob, longer than the value', Number(rows.split('|')[3]) > KEY.length, rows);
    const keyFile = join(work, 'config/agentbox/secrets.key');
    check('the machine key was created on first use', existsSync(keyFile));
    check('and it is mode 0600', (statSync(keyFile).mode & 0o777).toString(8) === '600', (statSync(keyFile).mode & 0o777).toString(8));
    // The database alone is not enough: moving the key aside locks it.
    sh(`mv ${keyFile} ${keyFile}.away`);
    const withoutKey = ab('create', P, '--name', 'agent-01', '--ai', 'none', '--title', 'Needs the key');
    check('without the key, a secret cannot be opened, and the error says which key', withoutKey.includes("can't be opened with") && withoutKey.includes('secrets.key'), withoutKey);
    sh(`mv ${keyFile}.away ${keyFile}`);
  });

  await step('2. It reaches the agent as an environment variable', async () => {
    ab('create', P, '--name', 'agent-01', '--ai', 'claude', '--title', 'Uses the key');
    const file = inAgent(SECRETS_ENV);
    console.log(`\n# inside ${INSTANCE}: ~/.config/agentbox/secrets.env\n${file}`);
    check('the agent has a secrets file with the export line', file.includes(`export OPENAI_API_KEY='${KEY}'`), file);
    check('it is mode 0600, owned by the agent', (statSync(agentPath(SECRETS_ENV)).mode & 0o777).toString(8) === '600');
    check('it carries the rule, where the agent will read it', file.includes('Never print, commit or echo'), file);

    const envFile = inAgent(ENV_FILE);
    check('the env file every shell and the ACP adapter source pulls it in', envFile.includes(`. '/${SECRETS_ENV}'`), envFile);
    check('and the env file itself holds no secret value', !envFile.includes(KEY), envFile);

    // What a shell inside the agent would read back, checked with a shell.
    const readBack = sh(`. ${agentPath(SECRETS_ENV)}; printf '%s' "$OPENAI_API_KEY"`);
    check('a shell sourcing it reads the value back whole', readBack === KEY, readBack === KEY ? '' : `${readBack.length} bytes`);

    const brief = inAgent(BRIEF);
    const section = brief.slice(brief.indexOf('## Secrets you were given')).split('\n##')[0];
    console.log(`\n# inside ${INSTANCE}: ~/.claude/CLAUDE.md, the new section\n${section.trim()}`);
    check("the agent's brief names the variable", section.includes('$OPENAI_API_KEY'), section);
    check('and tells it never to print, commit or echo a value', section.includes('Never print, commit or echo'), section);
    check('no value is anywhere in the brief', !brief.includes(KEY));
  });

  await step('3. Added, changed and removed while the agent runs', async () => {
    ab('--stdin', `${STRIPE}\n`, 'secrets', 'set', P, 'STRIPE_SECRET_KEY', '--value-stdin');
    let file = inAgent(SECRETS_ENV);
    check('a secret added after the agent existed reaches it', file.includes(`export STRIPE_SECRET_KEY='${STRIPE}'`), file);
    check('and the first one is still there: the file is the whole set', file.includes('OPENAI_API_KEY'), file);

    const changed = ab('--stdin', `${KEY2}\n`, 'secrets', 'set', P, 'OPENAI_API_KEY', '--value-stdin');
    check('setting it again says which agents got the new value', changed.includes(`Written into 1 agent: ${P}/agent-01`), changed);
    file = inAgent(SECRETS_ENV);
    check('the new value replaced the old one in the agent', file.includes(KEY2) && !file.includes(`'${KEY}'`), file);

    ab('secrets', 'rm', P, 'STRIPE_SECRET_KEY');
    file = inAgent(SECRETS_ENV);
    check('a removed secret leaves the agent', !file.includes('STRIPE_SECRET_KEY'), file);
    check('and the others stay', file.includes('OPENAI_API_KEY'), file);
    console.log(`\n# inside ${INSTANCE}: ~/.config/agentbox/secrets.env, after add, change and remove\n${file}`);
  });

  await step("4. One agent's own key, and the project's for everyone else", async () => {
    instances('agent-01', 'agent-02');
    ab('create', P, '--name', 'agent-02', '--ai', 'none', '--title', 'Shares the project key');
    ab('--stdin', `${AGENT_ONLY}\n`, 'secrets', 'set', `${P}/agent-01`, 'OPENAI_API_KEY', '--value-stdin');

    const one = inAgent(SECRETS_ENV);
    const two = inAgent(SECRETS_ENV, `ab-${P}-agent-02`);
    check("the agent with its own value gets that one", one.includes(`export OPENAI_API_KEY='${AGENT_ONLY}'`) && !one.includes(KEY2), one);
    check("the other agent keeps the project's", two.includes(`export OPENAI_API_KEY='${KEY2}'`), two);

    const list = ab('secrets', 'list', `${P}/agent-01`);
    check("an agent's list shows the scope of what it has", /\$OPENAI_API_KEY\s+agent/.test(list), list);
    check('and no values', !list.includes(AGENT_ONLY) && !list.includes(KEY2), list);
    const projectList = ab('secrets', 'list', P);
    check("the project's list is the project's own secrets, not its agents'", /\$OPENAI_API_KEY\s+project\s+.*agent/.test(projectList), projectList);

    ab('secrets', 'rm', `${P}/agent-01`, 'OPENAI_API_KEY');
    check("removing an agent's own gives it the project's again", inAgent(SECRETS_ENV).includes(`export OPENAI_API_KEY='${KEY2}'`), inAgent(SECRETS_ENV));
  });

  await step("5. The project's chat can name the secrets, and cannot read one", async () => {
    // The lead socket exists per project; the chat's tools are served on it.
    chat = mcp(leadSocket());
    const tools = await chat.send('tools/list');
    const names = tools.result.tools.map((t) => t.name);
    console.log(`\nthe chat's tools: ${names.join(', ')}`);
    check('the chat has a tool for the names', names.includes('list_secrets'), names.join(', '));
    check('and no tool that reads, sets or removes a value', !names.some((n) => /secret/.test(n) && n !== 'list_secrets'), names.join(', '));

    const text = await chat.call('list_secrets');
    check('it names the project variable', text.includes('$OPENAI_API_KEY'), text);
    check('it says where each one applies', text.includes('every agent of this project'), text);
    check('it carries no value', !text.includes(KEY2) && !text.includes(AGENT_ONLY), text);
    check('and it tells the chat what to do when a key is missing', text.includes('You can name them, not read them'), text);

    // The socket a chat has is the narrow one: it cannot store a secret.
    const forbidden = await chat.send('tools/call', { name: 'list_agents', arguments: {} });
    check('the chat can still do its own work on that socket', !forbidden.result?.isError, JSON.stringify(forbidden.result));
  });

  if (!process.env.NO_APP) {
    await step('6. The Secrets tab on a project page', async () => {
      let display = process.env.DEMO_DISPLAY;
      if (display) {
        log(`using the display it was given, ${display}`);
      } else {
        xvfb = await startXvfb({ width: 1440, height: 900 });
        display = xvfb.display;
        log(`started a private X display, ${display}`);
      }
      app = await electron.launch({ args: [desktop, '--disable-gpu'], cwd: desktop, env: { ...env, DISPLAY: display } });
      page = await app.firstWindow();
      await page.waitForLoadState('domcontentloaded');
      await page.locator('[data-connection="connected"]').waitFor({ timeout: 30_000 });
      await page.locator(`[data-project="${P}"]`).click();
      await page.getByRole('tab', { name: 'Secrets' }).click();
      const row = page.locator('[data-secret="OPENAI_API_KEY"]');
      await row.waitFor({ timeout: 15_000 });
      const shown = await row.innerText();
      check('the tab lists what the command line stored', shown.includes('$OPENAI_API_KEY'), shown);
      check('with its scope, how old it is, and where it is', shown.includes('project') && shown.includes('updated') && /in \d+ agents|in agent-0/.test(shown), shown);
      check('and no value on the row', !shown.includes(KEY2), shown);
      await shot(page, '01-project-tab');

      // The value box is a password field: nothing on the page can show a
      // stored value, because nothing can fetch one.
      const value = page.locator('#secret-value');
      check('the value field is masked', (await value.getAttribute('type')) === 'password');
      await page.locator('#secret-name').fill('SENTRY_DSN');
      await value.fill(FROM_APP);
      await page.waitForTimeout(400);
      await shot(page, '02-form');
      await page.getByRole('button', { name: 'Store secret' }).click();
      await page.locator('[data-secret="SENTRY_DSN"]').waitFor({ timeout: 15_000 });
      check('storing it from the app reaches the daemon', ab('secrets', 'list', P).includes('$SENTRY_DSN'));
      check('and reaches the running agent', inAgent(SECRETS_ENV).includes(`export SENTRY_DSN='${FROM_APP}'`), inAgent(SECRETS_ENV));
      check('the form clears the value it just stored', (await value.inputValue()) === '');
      await page.waitForTimeout(600);
      await shot(page, '03-stored');

      const pageText = await page.locator('body').innerText();
      check('no value appears anywhere on the page', !pageText.includes(FROM_APP) && !pageText.includes(KEY2), pageText.slice(0, 200));
    });

    await step("7. An agent's own tab: the project's read-only, its own editable", async () => {
      await page.locator(`[data-agent-card="${P}/agent-01"]`).first().click().catch(async () => {
        // The project page opens on the chat; the rail is the other way in.
        await page.locator(`[data-agent="${P}/agent-01"]`).first().click();
      });
      await page.getByRole('tab', { name: 'Secrets' }).click();
      const inherited = page.locator('[data-secret-scope="project"]');
      await inherited.first().waitFor({ timeout: 15_000 });
      check("the agent's tab shows the project's secrets", (await inherited.count()) >= 2, String(await inherited.count()));
      const note = await page.getByText("Change them on the project's Secrets tab").innerText();
      check('marked as the project\'s, with where to change them', note.length > 0, note);
      check("they are read-only here: no remove button on a project's row", (await inherited.first().getByRole('button').count()) === 0);

      await page.locator('#secret-name').fill('STRIPE_SECRET_KEY');
      await page.locator('#secret-value').fill(AGENT_ONLY);
      await page.getByRole('button', { name: 'Store secret' }).click();
      const own = page.locator('[data-secret-scope="agent"]');
      await own.first().waitFor({ timeout: 15_000 });
      check("a secret stored here is the agent's own", (await own.first().innerText()).includes('this agent'), await own.first().innerText());
      check('and it reached that agent alone', inAgent(SECRETS_ENV).includes(AGENT_ONLY) && !inAgent(SECRETS_ENV, `ab-${P}-agent-02`).includes(AGENT_ONLY));
      await page.waitForTimeout(600);
      await shot(page, '04-agent-tab');

      // Removing asks first, and says what it means for the agent.
      await own.first().getByRole('button', { name: /^Remove/ }).click();
      const dialog = page.getByRole('dialog');
      await dialog.waitFor({ timeout: 10_000 });
      const asked = await dialog.innerText();
      check('removing asks first, and says the variable leaves the agent', asked.includes('takes the variable out of'), asked);
      await shot(page, '05-remove');
      await dialog.getByRole('button', { name: 'Remove' }).click();
      await page.waitForTimeout(1500);
      check('the secret is gone from the agent', !inAgent(SECRETS_ENV).includes('STRIPE_SECRET_KEY'), inAgent(SECRETS_ENV));
    });
  }

  await step('8. A project base never carries a key out of the agent it was given to', async () => {
    // `base save` copies an agent's machine for every future agent of the
    // project to start from, then strips what belonged to that one agent. The
    // stub logs the command AgentBox runs inside the copy, so the scrub can be
    // read exactly as the machine would receive it.
    rmSync(join(incusState, 'commands.log'), { force: true });
    ab('base', 'save', `${P}/agent-01`);
    const commands = readFileSync(join(incusState, 'commands.log'), 'utf8');
    const scrub = commands.split('\n').find((line) => line.includes('rm -rf')) ?? '';
    console.log(`\n# the command AgentBox runs inside the copy\n${scrub}`);
    check('the base save deletes the secrets file', scrub.includes('.config/agentbox/secrets.env'), scrub);
    check('along with the logins it already removed', scrub.includes('.config/agentbox/env'), scrub);
  });
} catch {
  failed = true;
} finally {
  console.log('\n\n######## Results');
  for (const line of results) console.log(line);
  const passed = results.filter((r) => r.startsWith('PASS')).length;
  console.log(`\n${passed}/${results.length} checks passed in ${((Date.now() - started) / 1000).toFixed(0)}s`);
  if (!process.env.NO_APP) console.log(`Screenshots: ${media}`);
  await app?.close().catch(() => {});
  chat?.proc.kill();
  spawnSync(bin, ['daemon', 'stop'], { env, encoding: 'utf8' });
  xvfb?.stop();
  process.exit(failed || passed !== results.length ? 1 : 0);
}
