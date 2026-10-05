// The app's messages in Brazilian Portuguese.
import type { Messages } from './en-US.ts';
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

export const ptBR: Messages = {
  ...common.pt,
  ...shell.pt,
  ...settings.pt,
  ...defaults.pt,
  ...memory.pt,
  ...chat.pt,
  ...agent.pt,
  ...project.pt,
  ...vm.pt,
  ...web.pt,
};
