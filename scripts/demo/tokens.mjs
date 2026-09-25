#!/usr/bin/env node
// Reproduces the token ledger and the compact window (D83), the Claude limits
// (D85) and subagent cards in the chat (D86): what the daemon
// reports for a project whose agents spent the way a real project's did on
// 2026-09-21 — hours of autonomous work, each step resending a context that
// grew towards 1M tokens — through the API, `agentbox tokens` and the app's
// Tokens tab, an agent's Overview card and the "Compact chats at" setting; the
// account limits in the CLI, the top bar and the Tokens tab; and a subagent's
// card in an agent's chat, opened to show what it did.
//
// The ledger rows, the limit readings and the chat are SYNTHETIC, written
// straight into the throwaway state.db off camera: the chat is what writes
// them for real, and that path is proven by go test ./internal/chat (a
// scripted adapter) and by the captures of real claude-agent-acp turns in
// internal/acp's tests. Nothing here talks to Anthropic.
//
//   mise exec -- node scripts/demo/tokens.mjs > .demo-runs/tokens/demo.log 2>&1
import { execFileSync, spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, rmSync } from 'node:fs';
import http from 'node:http';
import { createRequire } from 'node:module';
import { homedir } from 'node:os';
import { join, resolve } from 'node:path';
import { startXvfb } from '../record/xvfb.mjs';

const root = resolve(import.meta.dirname, '../..');
const desktop = join(root, 'desktop');
const media = join(root, '.demo-runs/tokens/media');
const { _electron: electron } = createRequire(join(desktop, 'package.json'))('playwright');

// A short path: the daemon's unix socket has to fit in 107 bytes.
const work = join(homedir(), '.cache', 'agentbox-tokens');
const bin = join(work, 'bin', 'agentbox');
const repo = join(work, 'repo');
const db = join(work, 'data/agentbox/state.db');
const socket = join(work, 'data/agentbox/run/agentbox.sock');
const P = 'hello-stack';
const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(work, 'config'),
  XDG_DATA_HOME: join(work, 'data'),
  AGENTBOX_BIN: bin,
  AGENTBOX_PREVIEW_ADDR: 'off',
  XDG_SESSION_TYPE: 'x11',
  ANTHROPIC_BASE_URL: 'http://127.0.0.1:1',
};
for (const name of ['WAYLAND_DISPLAY', 'AGENTBOX_SOCKET', 'AGENTBOX_NO_AUTOSTART']) delete env[name];

const started = Date.now();
const results = [];
const hide = (text) => String(text).replaceAll(work, '$WORK');
const log = (...parts) => console.log(`[${((Date.now() - started) / 1000).toFixed(1).padStart(5)}s]`, ...parts.map(hide));
const check = (name, ok, detail = '') => {
  results.push(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : ` (got: ${hide(detail).trim().slice(0, 400)})`}`);
  log(ok ? 'PASS' : 'FAIL', name);
};
const ab = (...args) => {
  const r = spawnSync(bin, args, { env, encoding: 'utf8', input: '' });
  const out = hide(`${r.stdout}${r.stderr}`);
  console.log(`\n$ agentbox ${args.join(' ')}\n${out.trimEnd()}`);
  return out;
};
const run = (cmd, args, options = {}) => execFileSync(cmd, args, { env, encoding: 'utf8', ...options });
const shot = (page, name) => page.screenshot({ path: join(media, `tokens-${name}.png`) });

function api(method, path, body) {
  return new Promise((ok, fail) => {
    const req = http.request({ socketPath: socket, path, method, headers: { 'content-type': 'application/json' } }, (res) => {
      let text = '';
      res.on('data', (chunk) => (text += chunk));
      res.on('end', () => (res.statusCode >= 400 ? fail(new Error(`${res.statusCode} ${text}`)) : ok(JSON.parse(text))));
    });
    req.on('error', fail);
    req.end(body ? JSON.stringify(body) : undefined);
  });
}

async function step(title, fn) {
  console.log(`\n######## ${title}`);
  try {
    await fn();
  } catch (err) {
    check(title, false, err.message);
    console.log(hide(err.stack ?? err));
    throw err;
  }
}

// The seed: turns shaped like a real project's busy afternoon. Each is [agent, minutes
// ago, kind, model, calls, average context, context at the end, output].
// Every call of a turn resends its context, so cache reads are calls × the
// average; cost is Sonnet's and Opus's list prices, which is what the adapter
// estimates by.
const price = {
  'claude-sonnet-5': { in: 3, out: 15, read: 0.3, write: 3.75 },
  'claude-haiku-4-5': { in: 1, out: 5, read: 0.1, write: 1.25 },
  'claude-opus-5': { in: 5, out: 25, read: 0.5, write: 6.25 },
};
const turns = [
  ['agent-27', 1900, 'turn', 'claude-sonnet-5', 120, 180_000, 310_000, 40_000],
  ['agent-24', 290, 'turn', 'claude-sonnet-5', 300, 160_000, 262_000, 90_000],
  ['agent-29', 280, 'turn', 'claude-sonnet-5', 145, 120_000, 230_000, 37_000],
  ['agent-29', 280, 'turn', 'claude-haiku-4-5', 576, 297_000, 0, 26_000],
  ['lead', 270, 'turn', 'claude-opus-5', 12, 300_000, 312_000, 9_000],
  ['agent-24', 250, 'turn', 'claude-sonnet-5', 360, 430_000, 576_000, 110_000],
  ['agent-24', 200, 'turn', 'claude-sonnet-5', 150, 630_000, 726_000, 60_000],
  ['agent-24', 150, 'background', 'claude-sonnet-5', 0, 0, 760_000, 0],
  ['agent-24', 130, 'turn', 'claude-sonnet-5', 230, 860_000, 948_000, 70_000],
  ['agent-30', 110, 'turn', 'claude-sonnet-5', 250, 330_000, 420_000, 80_000],
  ['lead', 95, 'compaction', 'claude-opus-5', 1, 320_000, 320_000, 4_000],
  ['agent-24', 60, 'turn', 'claude-sonnet-5', 250, 150_000, 190_000, 45_000],
  ['agent-30', 40, 'turn', 'claude-sonnet-5', 232, 360_000, 447_000, 75_000],
  ['agent-24', 12, 'turn', 'claude-sonnet-5', 190, 400_000, 447_000, 30_000],
];

function seed() {
  const now = Date.now();
  const rows = turns.map(([agent, ago, kind, model, calls, avg, context, output], i) => {
    const p = price[model];
    const read = calls * avg;
    const write = kind === 'background' ? 0 : Math.round(avg * 0.02 + 20_000);
    const input = calls * 2;
    const cost = kind === 'background' ? 1.2 : (input * p.in + output * p.out + read * p.read + write * p.write) / 1e6;
    const tokens = kind === 'background' ? [0, 0, 0, 0] : [input, output, read, write];
    return `('${P}','${agent}','claude','session-${agent}','turn-${i}','${kind}','${model}',${now - ago * 60_000},${tokens.join(',')},${cost.toFixed(4)},${context})`;
  });
  run('sqlite3', [
    db,
    `INSERT INTO token_usage (project, agent, ai, session_id, turn, kind, model, at, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, context_tokens) VALUES ${rows.join(',\n')};`,
  ]);
  // agent-24 and agent-30 exist; agent-29 and agent-27 have been retired, and
  // the ledger still has them.
  for (const [name, title] of [
    ['agent-24', 'Verify the auto-fill and open its PR'],
    ['agent-30', 'Fix the flaky e2e run'],
  ]) {
    run('sqlite3', [
      db,
      `INSERT INTO agents (project, name, instance, ai, autonomous, branch, base_ref, base_commit, worktree, status, created_at, title, interface)
       VALUES ('${P}', '${name}', 'ab-${P}-${name}', 'claude', 1, 'agentbox/${name}', 'main', '0000000', '${join(work, 'worktrees', name)}', 'ready', ${Math.floor(Date.now() / 1000) - 18_000}, '${title}', 'chat')`,
    ]);
  }
  // Each account's limits, as a chat on it would have been told: the default
  // account most of the way through its five-hour window.
  const hour = 3600;
  const unix = Math.floor(now / 1000);
  for (const [account, five, week, status, ago] of [
    ['default', 0.82, 0.47, 'allowed_warning', 4],
    ['work', 0.12, 0.3, 'allowed', 190],
  ]) {
    const reading = JSON.stringify({
      status,
      resetsAt: unix + 2 * hour,
      rateLimitType: 'five_hour',
      unifiedWindows: { five_hour: { utilization: five, resetsAt: unix + 2 * hour }, seven_day: { utilization: week, resetsAt: unix + 70 * hour } },
    });
    run('sqlite3', [db, `INSERT INTO claude_limits (account, reading, at) VALUES ('${account}', '${reading}', ${now - ago * 60_000})`]);
  }
  // agent-24's chat: a turn whose search went to an Explore subagent, as the
  // chat records one — a card, with the subagent's tool calls and its report
  // nested under it.
  const t = (m) => new Date(now - m * 60_000).toISOString();
  const items = [
    { id: 'u1', turn: 'u1', kind: 'user', text: 'Where does the chat read the rate limits, and is it covered by a test?', createdAt: t(9), updatedAt: t(4), result: { state: 'completed', stopReason: 'end_turn', endedAt: t(4) } },
    { id: 'a1', turn: 'u1', kind: 'assistant', text: "I'll have a subagent search the tree for it.", createdAt: t(9), updatedAt: t(9) },
    { id: 's1', turn: 'u1', kind: 'subagent', subagent: { name: 'Explore', task: 'Find where the chat reads _claude/rateLimit, and any test that covers it', state: 'completed' }, createdAt: t(9), updatedAt: t(6) },
    { id: 's1t1', turn: 'u1', parent: 's1', kind: 'tool', tool: { callId: 'x1', name: 'Grep', title: 'grep "rateLimit" internal/', kind: 'search', status: 'completed' }, createdAt: t(9), updatedAt: t(8) },
    { id: 's1t2', turn: 'u1', parent: 's1', kind: 'tool', tool: { callId: 'x2', name: 'Read', title: 'Read internal/chat/chat.go', kind: 'read', status: 'completed', paths: ['internal/chat/chat.go'] }, createdAt: t(8), updatedAt: t(8) },
    { id: 's1t3', turn: 'u1', parent: 's1', kind: 'tool', tool: { callId: 'x3', name: 'Grep', title: 'grep "_claude/rateLimit" *_test.go', kind: 'search', status: 'completed' }, createdAt: t(7), updatedAt: t(7) },
    { id: 's1a', turn: 'u1', parent: 's1', kind: 'assistant', text: 'The chat reads it in `handler.Notify` (`internal/chat/chat.go`), on `usage_update`, and hands it to `Manager.Limits`. `TestLimitsReachTheDaemon` in `internal/chat/tokens_test.go` covers it.', createdAt: t(6), updatedAt: t(6) },
    { id: 'a2', turn: 'u1', kind: 'assistant', text: 'It is read in `handler.Notify` on every `usage_update`, and `TestLimitsReachTheDaemon` covers it.', createdAt: t(5), updatedAt: t(4) },
  ];
  items.forEach((it, i) => {
    run('sqlite3', [db, `INSERT INTO chat_items (project, agent, id, position, data) VALUES ('${P}', 'agent-24', '${it.id}', ${i}, '${JSON.stringify(it).replaceAll("'", "''")}')`]);
  });
}

let xvfb;
let app;
let page;
let failed = false;
try {
  console.log('######## Setup (off camera)');
  rmSync(work, { recursive: true, force: true });
  mkdirSync(media, { recursive: true });
  run('go', ['build', '-o', bin, './cmd/agentbox'], { cwd: root });
  // Two Claude accounts to hold limits; made-up tokens that never leave the
  // machine (ANTHROPIC_BASE_URL points nowhere).
  run(bin, ['auth', 'claude', '--token-stdin'], { input: 'sk-ant-oat01-demo-default\n' });
  run(bin, ['auth', 'claude', '--token-stdin', '--account', 'work'], { input: 'sk-ant-oat01-demo-work\n' });
  cpSync(join(root, 'testdata/fixtures/hello-stack'), repo, { recursive: true });
  run('git', ['-C', repo, 'init', '-q', '-b', 'main']);
  run('git', ['-C', repo, 'add', '-A']);
  run('git', ['-C', repo, '-c', 'commit.gpgsign=false', 'commit', '-qm', 'hello-stack fixture']);
  run('npm', ['run', 'build'], { cwd: desktop, stdio: 'ignore' });
  ab('add', repo, '--name', P);
  seed();
  log(`built agentbox and the app, added ${P} and seeded ${turns.length} synthetic ledger rows`);

  await step('1. The API adds the ledger up per agent, most expensive first', async () => {
    const report = await api('GET', `/v1/tokens?project=${P}&since=5h`);
    const refs = report.agents.map((a) => a.ref);
    console.log(JSON.stringify({ total: report.total, costUSD: report.costUSD, agents: refs, bucketSeconds: report.bucketSeconds }, null, 2));
    check('agent-24 spent the most in the last five hours', refs[0] === `${P}/agent-24`, refs.join(', '));
    check('the turn from 31 hours ago is outside the window', !refs.includes(`${P}/agent-27`), refs.join(', '));
    const a24 = report.agents[0];
    check('its fullest context is the 948K it reached before compacting', a24.maxContext === 948_000, String(a24.maxContext));
    check('cache reads are most of what it spent', a24.cacheRead / a24.total > 0.95, String(a24.cacheRead / a24.total));
    const a29 = report.agents.find((a) => a.agent === 'agent-29');
    check('a retired agent is still reported, and its subagent model is its own line', a29 && !a29.exists && a29.models.length === 2, JSON.stringify(a29?.models.map((m) => m.model)));
    check('the five-hour window is drawn in ten-minute columns', report.bucketSeconds === 600, String(report.bucketSeconds));
    const all = await api('GET', `/v1/tokens?project=${P}`);
    check('all time includes the retired agent from yesterday', all.agents.some((a) => a.agent === 'agent-27'));
  });

  await step('2. agentbox tokens says the same on the command line', async () => {
    const table = ab('tokens', P);
    check('the table leads with agent-24', table.split('\n').find((l) => l.startsWith(`${P}/`))?.startsWith(`${P}/agent-24`), table);
    check('a retired agent is marked gone', table.includes(`${P}/agent-29 (gone)`), table);
    const lines = ab('tokens', `${P}/agent-24`, '--turns', '5');
    check('--turns lists the ledger lines, newest first, with their context', /turn\s+claude-sonnet-5/.test(lines) && lines.includes('447.0K'), lines);
    const everything = ab('tokens', '--since', 'all');
    check('--since all covers every project and the whole ledger', everything.includes(`${P}/agent-27 (gone)`), everything);
    check('it leads with each account\'s limits', /default: 5-hour 82% \(resets .*\), Weekly 47%.* · allowed warning/.test(everything) && everything.includes('work: 5-hour 12%'), everything);
  });

  await step('2b. The API reports each account\'s limits, the default first', async () => {
    const limits = await api('GET', '/v1/limits');
    console.log(JSON.stringify(limits, null, 2));
    check('two accounts, the default first', limits.length === 2 && limits[0].account === 'default' && limits[0].default, JSON.stringify(limits.map((l) => l.account)));
    check('its windows in reading order', limits[0].windows.map((w) => w.label).join(', ') === '5-hour, Weekly', JSON.stringify(limits[0].windows));
  });

  await step('3. The compact window is a setting, with Claude Code\'s own bounds', async () => {
    const settings = await api('GET', '/v1/settings');
    check('it starts at the 200K default', settings.claudeCompactWindow === 200_000 && settings.defaultClaudeCompactWindow === 200_000, JSON.stringify(settings.claudeCompactWindow));
    const refused = await api('PATCH', '/v1/settings', { claudeCompactWindow: 5000 }).then(
      () => '',
      (err) => err.message,
    );
    check('a window Claude Code would clamp is refused', refused.includes('between 100000 and 1000000'), refused);
  });

  await step('4. The app: the project\'s Tokens tab', async () => {
    let display = process.env.DEMO_DISPLAY;
    if (!display) {
      xvfb = await startXvfb({ width: 1440, height: 1000 });
      display = xvfb.display;
    }
    app = await electron.launch({ args: [desktop, '--disable-gpu'], cwd: desktop, env: { ...env, DISPLAY: display } });
    page = await app.firstWindow();
    await page.setViewportSize({ width: 1440, height: 1000 }).catch(() => {});
    await page.locator('[data-connection="connected"]').waitFor({ timeout: 30_000 });
    const meter = await page.locator('[data-claude-meter]').first().getAttribute('data-claude-meter', { timeout: 15_000 });
    check('the top bar shows the default account\'s five-hour window', meter === '82', meter ?? '');
    await page.locator(`[data-project="${P}"]`).first().click();
    await page.getByRole('tab', { name: /Tokens/ }).click();
    await page.locator('[data-token-agent]').first().waitFor({ timeout: 15_000 });
    check('the Tokens tab shows both accounts\' limits', (await page.locator('[data-claude-limit]').count()) === 2);
    const first = await page.locator('[data-token-agent]').first().getAttribute('data-token-agent');
    check('the table leads with agent-24', first === `${P}/agent-24`, first ?? '');
    const retired = await page.locator(`[data-token-agent="${P}/agent-29"]`).innerText();
    check('agent-29 is marked retired', retired.includes('retired'), retired);
    await page.waitForTimeout(600);
    await shot(page, '01-project-5h');

    await page.locator(`[data-token-agent="${P}/agent-24"]`).click();
    await page.getByText('Last turns').first().waitFor();
    await page.waitForTimeout(800);
    await shot(page, '02-agent-24-open');

    await page.locator('[data-period="7d"]').click();
    await page.waitForTimeout(1200);
    const rows = await page.locator('[data-token-agent]').count();
    check('seven days brings the retired agent-27 back', rows === 5, String(rows));
    await shot(page, '03-project-7d');

    await page.locator('[data-tokens-as-table]').click();
    await page.waitForTimeout(400);
    await shot(page, '04-over-time-as-table');
  });

  await step('5. The app: an agent\'s own card, and the setting', async () => {
    await page.locator(`button:has-text("Verify the auto-fill and open its PR")`).first().click();
    await page.getByRole('tab', { name: /Overview/ }).click();
    await page.getByRole('tab', { name: /AI tool/ }).click();
    await page.locator(`[data-agent-tokens="${P}/agent-24"]`).waitFor({ timeout: 15_000 });
    const card = await page.locator(`[data-agent-tokens="${P}/agent-24"]`).innerText();
    check('the agent\'s card shows its fullest context', card.includes('948.0K'), card);
    await page.waitForTimeout(600);
    await shot(page, '05-agent-overview');

    await page.getByRole('button', { name: /^Setup|^Settings/ }).first().click();
    await page.getByRole('button', { name: 'Show the checklist' }).first().click({ timeout: 5_000 }).catch(() => {});
    await page.getByRole('tab', { name: 'Agents' }).click();
    const select = page.locator('[data-compact-window]');
    await select.waitFor({ timeout: 15_000 });
    await select.scrollIntoViewIfNeeded();
    await page.waitForTimeout(400);
    await shot(page, '06-settings-compact-window');
    // The app's Select is a menu, not a native <select>: open it and pick.
    await select.click();
    await page.getByRole('menuitem', { name: /500k/ }).click();
    await page.waitForTimeout(800);
    const settings = await api('GET', '/v1/settings');
    check('choosing 500K in the app stores it', settings.claudeCompactWindow === 500_000, String(settings.claudeCompactWindow));
  });

  await step('5b. The app: a subagent is a card in the chat, with its work under it', async () => {
    await page.locator(`[data-project="${P}"]`).first().click();
    await page.locator(`button:has-text("Verify the auto-fill and open its PR")`).first().click();
    await page.getByRole('tab', { name: /Chat/ }).click();
    await page.locator('[data-chat-fold]').first().click({ timeout: 15_000 });
    const card = page.locator('[data-chat-subagent]').first();
    await card.waitFor({ timeout: 10_000 });
    check('the subagent is a card that finished', (await card.getAttribute('data-chat-subagent')) === 'completed');
    const closed = await card.innerText();
    check('closed, it says who it was, what it was asked and what it did', closed.includes('Explore') && closed.includes('Find where the chat reads') && /read 1 file and searched 2 times/i.test(closed), closed);
    const topLevel = await page.locator('[data-chat-timeline] > * [data-chat-item="assistant"]').allInnerTexts();
    check('its report is not the agent\'s own message', !topLevel.some((text) => text.includes('Manager.Limits')), topLevel.join(' | '));
    await page.waitForTimeout(400);
    await shot(page, '08-subagent-closed');
    await card.locator('button').first().click();
    await page.getByText('TestLimitsReachTheDaemon').first().waitFor();
    await page.waitForTimeout(500);
    await shot(page, '09-subagent-open');
  });

  await step('6. Light appearance', async () => {
    await api('PATCH', '/v1/theme', { appearance: 'light' });
    await page.locator(`[data-project="${P}"]`).first().click();
    await page.getByRole('tab', { name: /Tokens/ }).click();
    await page.locator('[data-token-agent]').first().waitFor({ timeout: 15_000 });
    await page.waitForTimeout(1000);
    await shot(page, '07-project-light');
  });
} catch {
  failed = true;
} finally {
  console.log('\n######## Results');
  for (const line of results) console.log(line);
  const passed = results.filter((r) => r.startsWith('PASS')).length;
  console.log(`\n${passed}/${results.length} checks passed in ${((Date.now() - started) / 1000).toFixed(0)}s`);
  console.log(`Screenshots: ${media}`);
  await app?.close().catch(() => {});
  // The throwaway daemon goes too, so a second run starts from nothing.
  await api('POST', '/v1/shutdown').catch(() => {});
  xvfb?.stop();
  process.exit(failed || passed !== results.length ? 1 : 0);
}
