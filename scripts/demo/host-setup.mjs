#!/usr/bin/env node
// Reproduces "Set up host" on the app's Setup page: the button runs
// `agentbox host setup` as root through pkexec, its output arrives in the page
// like a job's log, the daemon is restarted afterwards, and Incus turns ready
// in the session that was already open — nobody logs out. A run that fails,
// and a machine with no pkexec at all, fall back to the command for a terminal.
//
//   mise exec -- node scripts/demo/host-setup.mjs > .demo-runs/host-setup/demo.log 2>&1
//
// Set NO_APP=1 to skip the app and the screenshots.
//
// What is real here and what stands in (D43):
// the daemon, the command-line tool and the desktop app are the built ones, and
// the streaming, the exit codes, the daemon restart and the Setup page are
// theirs. This machine is an AgentBox agent, which has no Incus and no polkit,
// so `incus` and `pkexec` are stubs: the pkexec stub checks the argv it is
// given, then answers the way the real one does — the transcript it prints is
// canned, and nothing here runs as root. What only a root run can prove is in
// the manual checklist in .demo-runs/host-setup/checklist.md.
import { execFileSync, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
import { tmpdir, userInfo } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/host-setup/media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

const work = mkdtempSync(join(tmpdir(), 'agentbox-host-setup-'));
const bin = join(work, 'bin', 'agentbox');
const stub = join(work, 'stub'); // the stub incus
const stubPkexec = join(work, 'stub-pkexec'); // the stub pkexec, on its own so a run can leave it out
const noPkexec = join(work, 'no-pkexec'); // /usr/bin without pkexec in it
const socket = join(work, 'incus-unix.socket'); // what the ACL would open up
const calls = join(work, 'pkexec-calls.log');
const control = join(work, 'pkexec-does'); // "refuse" or "setup"
const me = userInfo().username;

const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_PREVIEW_ADDR: 'off',
  AGENTBOX_BIN: bin,
  XDG_SESSION_TYPE: 'x11',
  INCUS_SOCKET: socket,
  PKEXEC_CALLS: calls,
  PKEXEC_DOES: control,
  PATH: `${stub}:${process.env.PATH}`,
};
for (const name of ['WAYLAND_DISPLAY', 'AGENTBOX_SOCKET', 'AGENTBOX_NO_AUTOSTART', 'DISPLAY', 'SUDO_USER', 'PKEXEC_UID']) delete env[name];

const started = Date.now();
const results = [];
const hide = (text) => String(text).replaceAll(work, '$TMP');
const log = (...parts) => console.log(`[${((Date.now() - started) / 1000).toFixed(1).padStart(5)}s]`, ...parts.map(hide));
const check = (name, ok, detail = '') => {
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(0, 400)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function ab(...args) {
  const r = spawnSync(bin, args, { env, encoding: 'utf8', timeout: 120_000 });
  const out = hide(`${r.stdout ?? ''}${r.stderr ?? ''}`);
  console.log(`\n$ agentbox ${args.map((a) => (/[\s;]/.test(a) ? `'${a}'` : a)).join(' ')}\n${out.trimEnd()}\n(exit ${r.status})`);
  return { out, code: r.status };
}

function apiRequest(method, path) {
  return new Promise((done, fail) => {
    const req = http.request({ socketPath: join(work, 'data/agentbox/run/agentbox.sock'), path, method }, (res) => {
      let out = '';
      res.on('data', (c) => (out += c));
      res.on('end', () => done({ status: res.statusCode, json: out ? JSON.parse(out) : undefined }));
    });
    req.on('error', fail);
    req.end();
  });
}

// daemonPid is the running daemon of this throwaway state, so a restart is
// visible rather than assumed.
function daemonPid() {
  const r = spawnSync('pgrep', ['-f', `^${bin} daemon`], { encoding: 'utf8' });
  return (r.stdout ?? '').trim().split('\n').filter(Boolean)[0] ?? '';
}

// A stub incus, because an AgentBox agent has no Incus of its own. It refuses
// the way a machine before host setup does, until the socket file exists: the
// stub pkexec creates it, standing in for the ACL a real run puts there.
const stubIncus = `#!/bin/sh
if [ ! -e "$INCUS_SOCKET" ]; then
  echo 'Error: Get "http://unix.socket/1.0": dial unix /var/lib/incus/unix.socket: connect: no such file or directory' >&2
  exit 1
fi
case "$1" in
  list) echo '[]' ;;
  query)
    case "$2" in
      /1.0) echo '{"api_status":"stable"}' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
  storage|network) echo '' ;;
esac
exit 0
`;

// A stub pkexec. The real one asks for a password and runs the command as
// root; neither is possible here, so this one checks what it was handed and
// then answers the way the real one would. PKEXEC_DOES picks which:
//
//   refuse — exit 126, what pkexec does when the dialog is dismissed, the
//            password is wrong, or the desktop has no authentication agent
//   setup  — a transcript of what the script prints, and the socket the ACL
//            would have opened up, then exit 0
//
// The transcript is canned. Nothing here runs as root, and no Incus is
// installed: a real root run is the manual checklist's job.
const stubPkexecScript = `#!/bin/sh
printf '%s\\n' "$*" >>"$PKEXEC_CALLS"
case "$(cat "$PKEXEC_DOES" 2>/dev/null)" in
  refuse)
    echo "Error executing command as another user: Not authorized" >&2
    echo "This incident has been reported." >&2
    exit 126 ;;
esac
cat <<'EOF'

==> Installing incus
Setting up incus (6.0.4-2) ...
Setting up acl (2.3.2-2) ...

==> Subordinate UID/GID ranges for root (required by Incus)

==> Starting incus
Created symlink /etc/systemd/system/sockets.target.wants/incus.socket -> /usr/lib/systemd/system/incus.socket.

==> Granting USER access to incus
+ usermod -aG incus-admin USER
+ setfacl -m u:USER:rw /var/lib/incus/unix.socket
+ wrote /etc/systemd/system/incus.socket.d/10-agentbox-USER.conf
+ wrote /etc/systemd/system/incus.service.d/10-agentbox-USER.conf

==> Storage pool 'default' (btrfs)
==> Network bridge 'incusbr0'
==> Default profile devices
==> ufw: allow the Incus bridge
ufw not active, skipping
==> Docker coexistence

==> Result
EOF
echo "Done. USER can use Incus now, in this session too: no need to log out."
: >"$INCUS_SOCKET"
exit 0
`;

// mirrorWithout copies a directory as symlinks, leaving one command out: a
// PATH with no pkexec on it anywhere, for the machine that hasn't got one.
function mirrorWithout(dir, command, into) {
  mkdirSync(into, { recursive: true });
  for (const name of readdirSync(dir)) {
    if (name === command) continue;
    try {
      symlinkSync(join(dir, name), join(into, name));
    } catch {
      // already there
    }
  }
  return into;
}

async function main() {
  log('Building agentbox');
  mkdirSync(join(work, 'bin'), { recursive: true });
  execFileSync('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root, stdio: 'inherit' });
  mkdirSync(stub, { recursive: true });
  mkdirSync(stubPkexec, { recursive: true });
  writeFileSync(join(stub, 'incus'), stubIncus, { mode: 0o755 });
  writeFileSync(join(stubPkexec, 'pkexec'), stubPkexecScript.replaceAll('USER', me), { mode: 0o755 });
  mirrorWithout('/usr/bin', 'pkexec', noPkexec);

  // 1. The command line itself, with no app in sight. These are real runs of
  // the real `agentbox host setup`, as an ordinary user: each one stops at the
  // check that matters, before anything is installed.
  log('What `agentbox host setup` makes of who it is for');
  const noOne = ab('host', 'setup');
  check('with nobody named, it says to use sudo — and names --user as the other way', /run it as root, with sudo/.test(noOne.out) && /--user/.test(noOne.out), noOne.out);
  const named = ab('host', 'setup', '--user', me);
  check('--user names who it is for, and it stops because it is not root', named.out.includes(`host setup is for ${me}`) && named.code === 1, named.out);
  const asRoot = ab('host', 'setup', '--user', 'root');
  check('it refuses to set the machine up for root', /not for root/.test(asRoot.out) && asRoot.code === 1, asRoot.out);
  const ghost = ab('host', 'setup', '--user', 'nosuchuser-agentbox');
  check('it refuses a user who is not on this machine', /no such user/.test(ghost.out), ghost.out);
  const injected = ab('host', 'setup', '--user', 'dev; touch /tmp/agentbox-evidence-pwned');
  check('a user name with a shell command in it is not a user name', /not a user name/.test(injected.out), injected.out);
  check('and it was never run', !existsSync('/tmp/agentbox-evidence-pwned'));
  const pkexecSays = spawnSync(bin, ['host', 'setup'], { env: { ...env, PKEXEC_UID: String(process.getuid()) }, encoding: 'utf8' });
  console.log(`\n$ PKEXEC_UID=${process.getuid()} agentbox host setup\n${hide(pkexecSays.stderr).trim()}`);
  check('PKEXEC_UID is read the way SUDO_USER is: pkexec says who asked', pkexecSays.stderr.includes(`host setup is for ${me}`), pkexecSays.stderr);

  // 2. A daemon on a machine with no Incus, which is where the app starts.
  log('A daemon on a machine that has not been set up');
  ab('host', 'check');
  const before = await apiRequest('GET', '/v1/version');
  check('the daemon reports that it cannot reach Incus', before.json.incus === false, JSON.stringify(before.json));
  const setupBefore = await apiRequest('GET', '/v1/setup');
  const incusBefore = setupBefore.json.checks.find((c) => c.id === 'incus');
  check('and the Incus check is missing, with the host setup command as its fix', incusBefore.status === 'missing' && /host setup/.test(incusBefore.fix), JSON.stringify(incusBefore));

  if (!process.env.NO_APP) await shots_();

  console.log(`\n${results.join('\n')}`);
  const passed = results.filter((r) => r.startsWith('PASS')).length;
  console.log(`${passed}/${results.length} checks passed`);
  return passed === results.length;
}

// openSetup gets to the Setup page with its checks loaded.
async function openSetup(page) {
  await page.getByRole('button', { name: /^Setup/ }).click({ timeout: 30_000 });
  await page.locator('[data-setup-step="Incus"]:not([data-status="checking"])').waitFor({ timeout: 60_000 });
  await sleep(1_500);
}

async function shots_() {
  log('Starting the app on a private display');
  mkdirSync(media, { recursive: true });
  execFileSync('npm', ['run', 'build'], { cwd: desktop, stdio: 'inherit' });
  const x = await startXvfb({ width: 1500, height: 940 });
  const launch = (extra) =>
    electron.launch({
      args: ['.', '--no-sandbox', '--disable-gpu', '--disable-software-rasterizer'],
      cwd: desktop,
      env: { ...env, DISPLAY: x.display, ...extra },
    });
  const shot = async (page, name) => {
    await page.screenshot({ path: join(media, `host-setup-${name}.png`) });
    log('screenshot', `host-setup-${name}.png`);
  };
  try {
    // A. No pkexec anywhere on PATH: a server, or a desktop without polkit.
    // The page offers no button it cannot honour, only the command.
    log('A machine with no pkexec');
    let app = await launch({ PATH: `${stub}:${noPkexec}` });
    let page = await app.firstWindow();
    await page.waitForLoadState('domcontentloaded');
    await sleep(3_000);
    await openSetup(page);
    const incusStep = page.locator('[data-setup-step="Incus"]');
    await shot(page, '01-no-pkexec');
    check('with no pkexec, the Incus step offers no button', (await incusStep.getByRole('button', { name: 'Set up host' }).count()) === 0);
    check('it says why, and gives the command for a terminal', (await incusStep.locator('[data-host-setup="terminal"]').count()) === 1);
    const command = await incusStep.locator('span[title]').first().getAttribute('title');
    check('the command is the one the guide gives', command === 'sudo "$(command -v agentbox)" host setup', command);
    await app.close();
    await sleep(1_500);

    // B. pkexec is there, and the run fails: the dialog dismissed, or no agent
    // to show it. The log says so, and the command for a terminal comes back.
    log('The password dialog dismissed');
    writeFileSync(control, 'refuse');
    app = await launch({ PATH: `${stubPkexec}:${stub}:${env.PATH}` });
    page = await app.firstWindow();
    await page.waitForLoadState('domcontentloaded');
    await sleep(3_000);
    await openSetup(page);
    await shot(page, '02-set-up-host');
    check('with pkexec, the Incus step offers Set up host', (await page.locator('[data-setup-step="Incus"]').getByRole('button', { name: 'Set up host' }).count()) === 1);
    const progressBefore = await page.locator('[data-setup-progress]').getAttribute('data-setup-progress');

    await page.locator('[data-setup-step="Incus"]').getByRole('button', { name: 'Set up host' }).click({ timeout: 10_000 });
    await page.getByText(/dialog was dismissed/).waitFor({ timeout: 30_000 });
    await sleep(1_000);
    await shot(page, '03-dismissed');
    const dismissed = await page.locator('[data-host-setup-log]').innerText();
    check('a dismissed dialog is reported as that, not as a crash', /dialog was dismissed|no polkit agent/.test(await page.locator('[data-setup-step="Incus"]').innerText()));
    check("and pkexec's own words are in the log", /Not authorized/.test(dismissed), dismissed);
    check('the command for a terminal comes back after a failed run', (await page.locator('[data-setup-step="Incus"] span[title]').count()) > 0);

    // C. The run succeeds. The output arrives as it is printed, the daemon is
    // restarted, and Incus turns ready in this session.
    log('Set up host, and the page going green without a new login');
    writeFileSync(control, 'setup');
    const pidBefore = daemonPid();
    await page.locator('[data-setup-step="Incus"]').getByRole('button', { name: 'Set up host' }).click({ timeout: 10_000 });
    await page.locator('[data-setup-step="Incus"][data-status="ok"]').waitFor({ timeout: 60_000 });
    await sleep(1_500);
    await shot(page, '04-set-up');
    const pidAfter = daemonPid();
    const output = await page.locator('[data-host-setup-log]').innerText();
    check('the script’s steps arrive in the page as it prints them', /==> Installing incus/.test(output) && /==> Result/.test(output), output.slice(0, 200));
    check('the log ends with the line that says no logout is needed', /no need to log out/.test(output), output.slice(-200));
    check('Incus turns ready in the session that was already open', (await page.locator('[data-setup-step="Incus"]').getAttribute('data-status')) === 'ok');
    const progressAfter = await page.locator('[data-setup-progress]').getAttribute('data-setup-progress');
    check(`the page counts one more item ready (${progressBefore} → ${progressAfter})`, progressBefore !== progressAfter, `${progressBefore} → ${progressAfter}`);
    check('the app restarted the daemon, which had started with no Incus to reach', pidBefore !== '' && pidAfter !== '' && pidBefore !== pidAfter, `${pidBefore} → ${pidAfter}`);
    const after = await apiRequest('GET', '/v1/version');
    check('the daemon that is running now reports that it can reach Incus', after.json.incus === true, JSON.stringify(after.json));

    const argv = readFileSync(calls, 'utf8').trim().split('\n');
    console.log(`\npkexec was asked to run:\n${argv.map((a) => `  ${hide(a)}`).join('\n')}`);
    check('the app asked pkexec for the agentbox it runs the daemon from, by path', argv.every((a) => a.startsWith(`${bin} host setup`)), argv.join(' | '));
    check('and passed the user, so a pkexec that does not set PKEXEC_UID still knows', argv.every((a) => a.endsWith(`--user ${me}`)), argv.join(' | '));

    await app.close();
  } catch (err) {
    log('the app run failed:', err.message);
    check('the app runs host setup and turns Incus ready', false, err.message);
  } finally {
    x.stop();
  }
  const names = ['01-no-pkexec', '02-set-up-host', '03-dismissed', '04-set-up'];
  const got = names.filter((n) => existsSync(join(media, `host-setup-${n}.png`)));
  check(`the app was captured (${got.length}/${names.length} screenshots)`, got.length === names.length, got.join(','));
}

main()
  .then((ok) => {
    spawnSync(bin, ['daemon', 'stop'], { env });
    log('Done. Work directory:', work);
    rmSync(work, { recursive: true, force: true });
    process.exit(ok ? 0 : 1);
  })
  .catch((err) => {
    console.error(err);
    spawnSync(bin, ['daemon', 'stop'], { env });
    rmSync(work, { recursive: true, force: true });
    process.exit(1);
  });
