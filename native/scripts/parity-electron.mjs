// The Electron half of native/scripts/parity.sh: launches the real Electron
// app (desktop/out, built by the script) against fake-daemon's socket, opens
// each scenario's view through the renderer's agentbox:select-view event,
// plays its actions and screenshots the window's contents.
//
//   node parity-electron.mjs <socket> <WxH> <scenarios.json> <out-dir> [id-filter]
//
// Playwright only reaches into the renderer here (no person watches the
// harness); the native half plays the same actions with xdotool.
import { chmodSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const desktop = join(here, '../../desktop');
const require = createRequire(join(desktop, 'package.json'));
const { _electron: electron } = require('playwright');

const [socket, size, scenariosPath, out, only] = process.argv.slice(2);
const [width, height] = size.split('x').map(Number);
const scenarios = JSON.parse(readFileSync(scenariosPath, 'utf8')).filter((s) => !only || s.id.includes(only));
mkdirSync(out, { recursive: true });

const userData = join(process.env.TMPDIR || '/tmp', `parity-electron-${process.pid}`);
// Electron's main process asks the agentbox command about the VM (vmpower.ts)
// and the host; the native app asks only the daemon. A stub that answers as a
// host-mode install keeps what the machine happens to have out of both.
mkdirSync(userData, { recursive: true });
const stub = join(userData, 'agentbox');
writeFileSync(stub, '#!/bin/sh\ncase "$*" in *"vm power"*) echo \'{"mode":"host"}\' ;; *) echo \'{}\' ;; esac\n');
chmodSync(stub, 0o755);
const app = await electron.launch({
  executablePath: join(desktop, 'node_modules/electron/dist/electron'),
  args: [desktop, '--no-sandbox', `--user-data-dir=${userData}`, '--force-device-scale-factor=1'],
  env: {
    ...process.env,
    AGENTBOX_SOCKET: socket,
    AGENTBOX_NO_AUTOSTART: '1',
    AGENTBOX_NO_HOME_HOST: '1',
    AGENTBOX_BIN: stub,
    ELECTRON_DISABLE_SECURITY_WARNINGS: '1',
  },
});
const page = await app.firstWindow();
await page.setViewportSize({ width, height });
await page.emulateMedia({ reducedMotion: 'reduce' });
await page.waitForLoadState('domcontentloaded');
// No blinking caret: the two apps would catch it at different moments.
await page.addStyleTag({ content: '*{caret-color:transparent!important}' });
await page.waitForTimeout(2500);

const sleep = (ms) => page.waitForTimeout(ms);
for (const s of scenarios) {
  await page.keyboard.press('Escape');
  await page.mouse.move(width - 2, height - 2);
  await page.evaluate((view) => window.dispatchEvent(new CustomEvent('agentbox:select-view', { detail: view })), s.view);
  await sleep(s.wait ?? 1500);
  for (const a of s.actions ?? []) {
    if (a.hover) await page.mouse.move(a.hover[0], a.hover[1]);
    if (a.click) await page.mouse.click(a.click[0], a.click[1]);
    if (a.press) await page.keyboard.press(a.press);
    if (a.type) await page.keyboard.type(a.type);
    await sleep(a.wait ?? 700);
  }
  await page.screenshot({ path: join(out, `${s.id}.png`) });
  console.log(`electron ${s.id}`);
}
await app.close();
