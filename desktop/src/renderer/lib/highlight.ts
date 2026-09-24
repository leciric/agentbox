// Syntax highlighting for code in the chat, with Shiki's JavaScript engine. The
// highlighter and each language load the first time a code block needs them.
//
// A Shiki theme is a set of inline colours, not classes, so it can't follow the
// window through CSS the way everything else does: there is a theme per mode,
// and the mode picks between them at the moment a block is highlighted. Both
// load with the highlighter — they are small next to a language grammar, and
// loading the second one only when the window turns over would leave every
// visible code block un-highlighted until it arrived.
import type { HighlighterCore, ThemedToken } from 'shiki/core';
import { currentMode } from './theme';

const themes = { dark: 'github-dark-default', light: 'github-light-default' } as const;

const languages: Record<string, () => Promise<unknown>> = {
  bash: () => import('@shikijs/langs/bash'),
  c: () => import('@shikijs/langs/c'),
  cpp: () => import('@shikijs/langs/cpp'),
  csharp: () => import('@shikijs/langs/csharp'),
  css: () => import('@shikijs/langs/css'),
  diff: () => import('@shikijs/langs/diff'),
  dockerfile: () => import('@shikijs/langs/dockerfile'),
  go: () => import('@shikijs/langs/go'),
  graphql: () => import('@shikijs/langs/graphql'),
  html: () => import('@shikijs/langs/html'),
  ini: () => import('@shikijs/langs/ini'),
  java: () => import('@shikijs/langs/java'),
  javascript: () => import('@shikijs/langs/javascript'),
  json: () => import('@shikijs/langs/json'),
  jsx: () => import('@shikijs/langs/jsx'),
  kotlin: () => import('@shikijs/langs/kotlin'),
  lua: () => import('@shikijs/langs/lua'),
  markdown: () => import('@shikijs/langs/markdown'),
  php: () => import('@shikijs/langs/php'),
  prisma: () => import('@shikijs/langs/prisma'),
  python: () => import('@shikijs/langs/python'),
  ruby: () => import('@shikijs/langs/ruby'),
  rust: () => import('@shikijs/langs/rust'),
  scss: () => import('@shikijs/langs/scss'),
  sql: () => import('@shikijs/langs/sql'),
  swift: () => import('@shikijs/langs/swift'),
  toml: () => import('@shikijs/langs/toml'),
  tsx: () => import('@shikijs/langs/tsx'),
  typescript: () => import('@shikijs/langs/typescript'),
  xml: () => import('@shikijs/langs/xml'),
  yaml: () => import('@shikijs/langs/yaml'),
};

const aliases: Record<string, string> = {
  sh: 'bash',
  shell: 'bash',
  zsh: 'bash',
  console: 'bash',
  shellsession: 'bash',
  'c++': 'cpp',
  cs: 'csharp',
  docker: 'dockerfile',
  golang: 'go',
  js: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  kt: 'kotlin',
  md: 'markdown',
  py: 'python',
  rb: 'ruby',
  rs: 'rust',
  ts: 'typescript',
  mts: 'typescript',
  yml: 'yaml',
  patch: 'diff',
};

// languageOf is the Shiki language for a code fence's info string, if there is one.
export function languageOf(name: string | undefined): string | undefined {
  const lang = name?.toLowerCase();
  if (!lang) return undefined;
  const id = aliases[lang] ?? lang;
  return id in languages ? id : undefined;
}

let core: Promise<HighlighterCore> | undefined;

function highlighter(): Promise<HighlighterCore> {
  core ??= Promise.all([import('shiki/core'), import('shiki/engine/javascript')]).then(([{ createHighlighterCore }, { createJavaScriptRegexEngine }]) =>
    createHighlighterCore({
      themes: [import('@shikijs/themes/github-dark-default'), import('@shikijs/themes/github-light-default')],
      langs: [],
      engine: createJavaScriptRegexEngine(),
    }),
  );
  return core;
}

const cache = new Map<string, ThemedToken[][]>();

export async function highlight(code: string, lang: string): Promise<ThemedToken[][]> {
  const theme = themes[currentMode()];
  const key = `${theme}\0${lang}\0${code}`;
  const hit = cache.get(key);
  if (hit) return hit;
  const h = await highlighter();
  if (!h.getLoadedLanguages().includes(lang)) await h.loadLanguage((await languages[lang]()) as Parameters<HighlighterCore['loadLanguage']>[0]);
  const { tokens } = h.codeToTokens(code, { lang, theme });
  if (cache.size > 300) cache.clear();
  cache.set(key, tokens);
  return tokens;
}
