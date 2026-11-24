// The people of a shared project (internal/daemon/sharepeople.go): who is in
// each project now and what they have open, and what this window has open,
// which it tells the daemon (api.Here) every HereRenewSeconds and at once when
// it changes. The daemon passes what is open in a remote project on to its
// owner, and the owner tells everyone in it.
//
// A person with id "" is this app's own user, on whichever machine: they are
// left out of what is shown.
import { useSyncExternalStore } from 'react';
import type * as T from '../../shared/api';
import { api } from './api.ts';

const renewMs = 15_000; // api.HereRenewSeconds
const typingMs = 4_000; // how long after the last key a message is still being typed

let byProject = new Map<string, T.PersonHere[]>();
const listeners = new Set<() => void>();
const none: T.PersonHere[] = [];

function notify(): void {
  for (const l of listeners) l();
}

function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

// others is the people of a list besides this app's own user.
export function others(people: T.PersonHere[] | undefined): T.PersonHere[] {
  return (people ?? none).filter((p) => p.id !== '');
}

// applyHere takes EventHere's data: who is in one project now.
export function applyHere(ph: T.ProjectHere): void {
  const next = new Map(byProject);
  const people = others(ph.people);
  if (people.length === 0) next.delete(ph.project);
  else next.set(ph.project, people);
  byProject = next;
  notify();
}

// seedHere replaces everything, from GET /v1/here (once, as the event stream
// connects, and in dev/preview.tsx).
export function seedHere(all: T.ProjectHere[]): void {
  byProject = new Map();
  for (const ph of all) {
    const people = others(ph.people);
    if (people.length > 0) byProject.set(ph.project, people);
  }
  notify();
}

export function loadHere(): void {
  void api
    .whoIsHere()
    .then(seedHere)
    .catch(() => {});
}

// usePeopleIn is who else is in a project now.
export function usePeopleIn(project: string | undefined): T.PersonHere[] {
  return useSyncExternalStore(subscribe, () => (project ? (byProject.get(project) ?? none) : none));
}

// useEveryoneHere is who else is in each project.
export function useEveryoneHere(): Map<string, T.PersonHere[]> {
  return useSyncExternalStore(subscribe, () => byProject);
}

// onAgent is the people on one agent; inSession those in a session of the
// project's chat, with no agent open.
export function onAgent(people: T.PersonHere[], agent: string): T.PersonHere[] {
  return people.filter((p) => p.agent === agent);
}

export function inSession(people: T.PersonHere[], session: string | undefined): T.PersonHere[] {
  return people.filter((p) => !p.agent && (p.session ?? '') === (session ?? ''));
}

// What this window has open, set by App (setHere) and the composer (typing).
let place: T.Here = { project: '' };
let typingUntil = 0;
let changed: (() => void) | undefined;

function samePlace(a: T.Here, b: T.Here): boolean {
  return a.project === b.project && (a.session ?? '') === (b.session ?? '') && (a.agent ?? '') === (b.agent ?? '') && (a.tab ?? '') === (b.tab ?? '');
}

// setHere says what this window shows now.
export function setHere(here: T.Here): void {
  if (samePlace(here, place)) return;
  place = here;
  typingUntil = 0;
  changed?.();
}

// noteTyping says a key was pressed in the composer of what is on screen.
export function noteTyping(): void {
  const was = Date.now() < typingUntil;
  typingUntil = Date.now() + typingMs;
  if (!was) changed?.();
}

// startHere tells the daemon what this window has open, for as long as it
// runs; the function it returns stops it, and says the window left. The web
// app (a phone) tells nothing: its requests are each audited (lan.go).
export function startHere(): () => void {
  const bridge = window.agentbox;
  if ('web' in bridge) return () => {};
  const client = `desktop-${crypto.randomUUID()}`;
  let last = '';
  let lastAt = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopTyping: ReturnType<typeof setTimeout> | undefined;
  const renew = (force: boolean) => {
    const typing = Date.now() < typingUntil;
    // A window behind another, or minimised, is still where its user is: a
    // window that closes says so (leaveHere), one that crashes lapses.
    const here: T.HereRequest = { client, ...place, ...(typing ? { typing } : {}) };
    const body = JSON.stringify(here);
    if (!force && body === last && Date.now() - lastAt < renewMs) return;
    last = body;
    lastAt = Date.now();
    api.renewHere(here).catch(() => {
      last = ''; // a daemon from before people: the next renewal tries again
    });
    if (stopTyping) clearTimeout(stopTyping);
    if (typing) stopTyping = setTimeout(() => renew(false), typingUntil - Date.now() + 50);
  };
  const soon = () => {
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => renew(false), 120);
  };
  changed = soon;
  const tick = setInterval(() => renew(true), renewMs);
  const onUnload = () => void api.leaveHere(client).catch(() => {});
  window.addEventListener('beforeunload', onUnload);
  soon();
  return () => {
    changed = undefined;
    if (timer) clearTimeout(timer);
    if (stopTyping) clearTimeout(stopTyping);
    clearInterval(tick);
    window.removeEventListener('beforeunload', onUnload);
    onUnload();
  };
}
