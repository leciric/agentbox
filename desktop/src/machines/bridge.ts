import type { Bridge } from '../preload';
import { webStreams } from '../renderer/web/streams.ts';

// The parts of the app's bridge that lib/vnc uses, for the page of `agentbox
// machines serve`: its streams are WebSockets to the server that served it.
// Imported before lib/vnc, which listens to the streams as it loads.
const { stream } = webStreams((path) => `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}${path}`);

window.agentbox = {
  stream,
  copyText: (text: string) => void navigator.clipboard?.writeText(text).catch(() => {}),
  readText: () => navigator.clipboard?.readText().catch(() => '') ?? Promise.resolve(''),
} as Partial<Bridge> as Bridge;
