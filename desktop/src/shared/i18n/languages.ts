// The languages the app speaks. Adding one is its catalog, a file beside
// pt-BR.ts, and its line here: Settings offers every language on this list, the
// daemon stores whichever tag is picked, and a key the catalog lacks falls back
// to en-US (i18n.test.ts says which).
import { enUS, type Messages } from './en-US.ts';
import { ptBR } from './pt-BR.ts';

export type Language = {
  // tag is the BCP 47 tag, which is also the locale dates and numbers use.
  tag: string;
  // name is the language's name in itself, as the menu shows it.
  name: string;
  messages: Partial<Messages>;
};

export const languages: Language[] = [
  { tag: 'en-US', name: 'English (US)', messages: enUS },
  { tag: 'pt-BR', name: 'Português (Brasil)', messages: ptBR },
];
