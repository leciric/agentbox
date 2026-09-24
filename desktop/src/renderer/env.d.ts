import type { Bridge } from '../preload';

declare global {
  interface Window {
    agentbox: Bridge;
  }
}

export {};
