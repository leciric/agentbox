// Holds Electron's 'prevent-app-suspension' blocker while a renderer says
// agents are busy: system idle sleep only, the display may still sleep. On a
// Mac, idle sleep pauses the VM and freezes agents mid-turn. What counts as
// busy is the renderer's call (lib/keepAwake.ts); this is only the switch (D19).
// Each window's hold is dropped when it navigates, crashes or closes, so the
// blocker can't outlive the renderer that asked for it. Electron comes in as
// types only, so the tests can run it against fakes; index.ts wires it up.
import type { PowerSaveBlocker, WebContents } from 'electron';

type Blocker = Pick<PowerSaveBlocker, 'start' | 'stop'>;
type Contents = Pick<WebContents, 'id' | 'on' | 'once'>;

export function keepAwake(blocker: Blocker): (contents: Contents, on: boolean) => void {
  let id: number | undefined;
  const holders = new Set<number>();
  // Windows whose listeners are on, once each for the window's life: busy and
  // idle come and go many times, and each one used to add three more.
  const watched = new Set<number>();

  const apply = () => {
    if (holders.size > 0 && id === undefined) id = blocker.start('prevent-app-suspension');
    else if (holders.size === 0 && id !== undefined) {
      blocker.stop(id);
      id = undefined;
    }
  };
  const release = (contents: Contents) => {
    holders.delete(contents.id);
    apply();
  };
  const watch = (contents: Contents) => {
    const wid = contents.id;
    if (watched.has(wid)) return;
    watched.add(wid);
    // .on, not .once: a crashed window can be reloaded and hold again.
    contents.on('render-process-gone', () => release(contents));
    // A reload or a new page, not the app's own in-page navigation.
    contents.on('did-start-navigation', (e) => e.isMainFrame && !e.isSameDocument && release(contents));
    contents.once('destroyed', () => {
      watched.delete(wid);
      release(contents);
    });
  };

  return (contents, on) => {
    if (on !== true) return release(contents);
    watch(contents);
    holders.add(contents.id);
    apply();
  };
}
