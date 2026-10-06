// Fetching a Chromium browser's cookie-encryption passphrase from the host's
// keyring. The daemon reads and decrypts the user's browser cookies in Go
// (internal/cookieimport), but it runs in AgentBox's Linux VM and can't reach
// the host's keyring, where Chrome, Brave and the others keep the passphrase
// their cookies are sealed with. This module is that one piece the host must
// do: given a browser's keyring name, it returns the passphrase, which the
// renderer then hands to the import request. Firefox needs nothing here.
//
// The passphrase is not a cookie and is never logged or stored; it is read
// here, passed once to the daemon over the socket, used to decrypt, and
// dropped. On Windows modern Chromium seals cookies with app-bound encryption
// only the browser can open, so there is nothing to fetch (and the daemon
// reads none): this returns ''.
import { execFile } from 'node:child_process';
import { ipcMain } from 'electron';

// macKeychain maps a browser's keyring name to its macOS Keychain "Safe
// Storage" service and account.
const macKeychain: Record<string, { service: string; account: string }> = {
  chrome: { service: 'Chrome Safe Storage', account: 'Chrome' },
  chromium: { service: 'Chromium Safe Storage', account: 'Chromium' },
  brave: { service: 'Brave Safe Storage', account: 'Brave' },
  msedge: { service: 'Microsoft Edge Safe Storage', account: 'Microsoft Edge' },
  vivaldi: { service: 'Vivaldi Safe Storage', account: 'Vivaldi' },
};

// run executes a command, resolving to its trimmed stdout, or '' if it fails
// or writes nothing. It never surfaces the output in a rejection, so the
// passphrase can't end up in a log.
function run(cmd: string, args: string[]): Promise<string> {
  return new Promise((resolve) => {
    execFile(cmd, args, { timeout: 10_000, maxBuffer: 1 << 20 }, (err, stdout) => {
      if (err) return resolve('');
      resolve(stdout.toString().replace(/\n$/, ''));
    });
  });
}

// keyringSecret returns the passphrase a Chromium browser's cookies are sealed
// with on this host, or '' when there is none to fetch (Firefox, Windows, a
// locked or empty keyring). keyring is the browser's keyring name as the
// daemon reported it (chrome, chromium, brave, msedge, vivaldi).
export async function keyringSecret(keyring: string): Promise<string> {
  if (!keyring) return '';
  if (process.platform === 'darwin') {
    const k = macKeychain[keyring];
    if (!k) return '';
    // -w prints only the password; the user may be prompted to allow access.
    return run('/usr/bin/security', ['find-generic-password', '-w', '-s', k.service, '-a', k.account]);
  }
  if (process.platform === 'linux') {
    // secret-tool reaches both GNOME Keyring and KWallet through the Secret
    // Service API. Chromium stores the passphrase under this schema with an
    // `application` attribute naming the browser.
    const secret = await run('secret-tool', [
      'lookup',
      'xdg:schema',
      'chrome_libsecret_os_crypt_password_v2',
      'application',
      keyring,
    ]);
    if (secret) return secret;
    // Older Chromium used a schema-less item keyed only by `application`.
    return run('secret-tool', ['lookup', 'application', keyring]);
  }
  return '';
}

export function registerBrowserKeys(): void {
  ipcMain.handle('browserkeys:get', (_event, keyring: string) => keyringSecret(keyring));
}
