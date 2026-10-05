// The app's messages in US English: every key there is, and what any other
// language shows for a key its catalog lacks. Messages are in the format
// format.ts describes.
import * as agent from './parts/agent.ts';
import * as chat from './parts/chat.ts';
import * as common from './parts/common.ts';
import * as defaults from './parts/defaults.ts';
import * as memory from './parts/memory.ts';
import * as project from './parts/project.ts';
import * as settings from './parts/settings.ts';
import * as shell from './parts/shell.ts';
import * as vm from './parts/vm.ts';
import * as web from './parts/web.ts';

export const enUS = {
  ...common.en,
  ...shell.en,
  ...settings.en,
  ...defaults.en,
  ...memory.en,
  ...chat.en,
  ...agent.en,
  ...project.en,
  ...vm.en,
  ...web.en,
};

export type MessageKey = keyof typeof enUS;
export type Messages = Record<MessageKey, string>;
