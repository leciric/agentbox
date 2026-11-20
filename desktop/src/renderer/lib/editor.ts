// The code editor an agent's "Open in …" opens its worktree in: the main
// process finds the editors on this computer (main/editors.ts), and Settings
// → Editor picks one, kept in localStorage beside the other display settings
// since it's this computer's. With none picked, or the one picked gone, it's
// the first found. In the browser there are none: the files aren't there.
import { useQuery } from '@tanstack/react-query';
import { useSyncExternalStore } from 'react';
import { toast } from 'sonner';
import type { Editor } from '../../preload';
import { t } from '../../shared/i18n/index.ts';
import { errorMessage } from './utils.ts';

const storageKey = 'agentbox.editor';
const listeners = new Set<() => void>();
let current: string | undefined;

// pickedEditor is the ID picked in Settings, or '' for the first found.
export function pickedEditor(): string {
  if (current === undefined) {
    try {
      current = localStorage.getItem(storageKey) ?? '';
    } catch {
      current = '';
    }
  }
  return current;
}

export function setPickedEditor(id: string) {
  current = id;
  try {
    localStorage.setItem(storageKey, id);
  } catch {
    // Kept for this session, if not for the next.
  }
  for (const l of listeners) l();
}

export const usePickedEditor = () =>
  useSyncExternalStore((l) => {
    listeners.add(l);
    return () => listeners.delete(l);
  }, pickedEditor);

// chosenEditor is the editor "Open in …" opens: the one picked, while it's
// installed, or else the first found.
export function chosenEditor(editors: Editor[] | undefined, picked: string): Editor | undefined {
  return editors?.find((e) => e.id === picked) ?? editors?.[0];
}

// useEditors lists the editors found, again whenever the window comes back to
// the front: one installed meanwhile shows up.
export const useEditors = () =>
  useQuery({ queryKey: ['editors'], queryFn: () => window.agentbox.editors.list(), staleTime: 60_000, refetchOnWindowFocus: 'always' });

// useEditor is the editor "Open in …" opens, or undefined when none is found.
export function useEditor(): Editor | undefined {
  const editors = useEditors();
  return chosenEditor(editors.data, usePickedEditor());
}

// openInEditor opens an agent's worktree in the editor, saying so if it can't.
export async function openInEditor(editor: Editor, worktree: string): Promise<void> {
  try {
    const opened = await window.agentbox.editors.open(editor.id, worktree);
    if ('error' in opened) {
      const message =
        opened.error === 'none'
          ? t('editor.open.none')
          : opened.error === 'folder'
            ? t('editor.open.noFolder', { path: worktree })
            : t('editor.open.failed', { editor: editor.name });
      toast.error(message, opened.detail ? { description: opened.detail } : undefined);
    }
  } catch (err) {
    toast.error(t('editor.open.failed', { editor: editor.name }), { description: errorMessage(err) });
  }
}
