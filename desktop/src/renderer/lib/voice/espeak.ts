// eSpeak NG, the phonemizer Kokoro reads from, fetched when a voice is first
// used rather than shipped with the app: it is GPL-3.0, and AgentBox is MIT.
// kokoro-js imports it as the npm package "phonemizer", an eSpeak NG build of
// its own with English only; vite.config.mts points that import here instead,
// so nothing of eSpeak NG is bundled, and Portuguese is there too.
//
// The build is Echogarden's Emscripten port, every language in one data file,
// pinned by version and by hash: what is run is what was checked, and a copy
// in the cache that doesn't match is fetched again.
import { cachedFetch, type Progress } from './fetch.ts';

const base = 'https://cdn.jsdelivr.net/npm/@echogarden/espeak-ng-emscripten@0.3.5/';
const files = {
  script: { url: `${base}espeak-ng.js`, sha256: '3502d997af2640e54518a06845776c8507bfeebd8ff75f80176370e35de9896b' },
  data: { url: `${base}espeak-ng.data`, sha256: 'f7f8eff5685c709db9dae81e88a0e0556867d60514f7308ae99e0579369397b5' },
};

type Engine = {
  set_voice(voice: string): void;
  synthesize_ipa(text: string): { code: number; ipa: string };
};

let engine: Promise<Engine> | undefined;
let progress: Progress = () => {};

// onEspeakProgress is told how the download goes, the first time.
export function onEspeakProgress(report: Progress): void {
  progress = report;
}

export function loadEspeak(): Promise<Engine> {
  engine ??= (async () => {
    const [script, data] = await Promise.all([cachedFetch(files.script, progress), cachedFetch(files.data, progress)]);
    const url = URL.createObjectURL(new Blob([script], { type: 'text/javascript' }));
    try {
      const { default: init } = (await import(/* @vite-ignore */ url)) as { default: (module: object) => Promise<{ eSpeakNGWorker: new () => Engine }> };
      const module = await init({ getPreloadedPackage: () => data, print: () => {}, printErr: () => {} });
      return new module.eSpeakNGWorker();
    } finally {
      URL.revokeObjectURL(url);
    }
  })();
  engine.catch(() => (engine = undefined));
  return engine;
}

// phones is eSpeak NG's IPA for text, a line per clause, with its phones
// separated by "_": a tied pair (tʃ, eɪ) stays one phone.
export async function phones(text: string, language: string): Promise<string[]> {
  const espeak = await loadEspeak();
  espeak.set_voice(language);
  const { code, ipa } = espeak.synthesize_ipa(text);
  if (code !== 0) throw new Error(`eSpeak NG could not read "${text}"`);
  return ipa.split('\n').filter((line) => line.length > 0);
}

// phonemize is the "phonemizer" package's own function, for kokoro-js.
export async function phonemize(text: string, language = 'en-us'): Promise<string[]> {
  return (await phones(text, language)).map((line) => line.replaceAll('_', ''));
}
