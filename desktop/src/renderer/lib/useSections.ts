// The sidebar's sections (D79) as both layouts draw them, Classic's sidebar
// (components/Sidebar.tsx) and UI v2's (components/v2/SidebarV2.tsx), so a
// section made in one is in the other: the lists as the daemon keeps them,
// the moves that save them, and dragging over them. The order itself is
// sidebar.ts's.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { DragEvent } from 'react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { toast } from 'sonner';
import type * as T from '../../shared/api';
import { api } from './api';
import { t as tNow } from './i18n';
import { arrange, drop, flatten, moveSection, place, targetKey, toLayout, type Dragging, type DropTarget, type SidebarList } from './sidebar';
import { errorMessage } from './utils';

export function useSections() {
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects });
  const sections = useQuery({ queryKey: ['sections'], queryFn: api.sections });
  const queryClient = useQueryClient();

  // A move on its way to the daemon: the layout it sent, shown until the
  // last move in flight has its answer. Until then a refetch (the daemon
  // announces every reorder, and a move can be dropped before the one before
  // it lands) would answer with an older order, and the rows would jump back.
  const [pending, setPending] = useState<T.ProjectLayout | null>(null);
  const inFlight = useRef(0);

  // The sidebar's order, as the daemon last said it, or as the move on its
  // way asked: the sections in theirs, then the projects in none. Every move
  // is a function of this. No sections before the projects are in either:
  // each would show as empty.
  const lists = useMemo(() => (projects.data ? arrange(projects.data, sections.data ?? [], pending) : []), [projects.data, sections.data, pending]);

  // What a keyboard move did, for a screen reader: the sidebar rearranging
  // itself is the whole of the feedback otherwise.
  const [announced, setAnnounced] = useState('');

  // A reorder sends the whole layout, and shows it before the daemon answers.
  // Reorders go one at a time (the scope), so the daemon stores the last one
  // last, and only the last answer is written: an earlier one is an order
  // already moved on from.
  const reorder = useMutation({
    scope: { id: 'project-layout' },
    mutationFn: (layout: T.ProjectLayout) => api.setProjectLayout(layout),
    onError: (err) => toast.error(errorMessage(err)),
    onSettled: (ordered, err, layout) => {
      if (--inFlight.current > 0) return;
      // A refetch started before the daemon stored this would answer with
      // the order before it.
      void queryClient.cancelQueries({ queryKey: ['projects'] });
      void queryClient.cancelQueries({ queryKey: ['sections'] });
      if (err || !ordered) {
        void queryClient.invalidateQueries({ queryKey: ['projects'] });
        void queryClient.invalidateQueries({ queryKey: ['sections'] });
      } else {
        queryClient.setQueryData<T.Project[]>(['projects'], ordered);
        queryClient.setQueryData<T.Section[]>(['sections'], (current) => current && flatten(arrange([], current, layout)).sections);
      }
      setPending(null);
    },
  });

  // save sends a move, and shows it at once: the cache is written with
  // exactly what the daemon will write, for every other view that lists the
  // projects, and the sidebar keeps it on screen until the answer.
  const save = (next: SidebarList[], then?: () => void) => {
    const layout = toLayout(next);
    inFlight.current++;
    setPending(layout);
    void queryClient.cancelQueries({ queryKey: ['projects'] });
    void queryClient.cancelQueries({ queryKey: ['sections'] });
    const { projects: ordered, sections: order } = flatten(next);
    queryClient.setQueryData<T.Project[]>(['projects'], ordered);
    queryClient.setQueryData<T.Section[]>(['sections'], order);
    reorder.mutate(layout, { onSuccess: then });
  };

  const patchSection = useMutation({
    mutationFn: ({ id, req }: { id: string; req: T.UpdateSectionRequest }) => api.updateSection(id, req),
    onMutate: ({ id, req }) =>
      queryClient.setQueryData<T.Section[]>(['sections'], (current) => current?.map((s) => (s.id === id ? { ...s, ...req } : s))),
    onError: async (err) => {
      toast.error(errorMessage(err));
      await queryClient.invalidateQueries({ queryKey: ['sections'] });
    },
  });

  const addSection = useMutation({
    mutationFn: (name: string) => api.addSection(name),
    onSuccess: async () => queryClient.invalidateQueries({ queryKey: ['sections'] }),
    onError: (err) => toast.error(errorMessage(err)),
  });

  // Applying a move: say what happened, for anyone not watching, and unfold
  // the section it landed in, so nothing a move does is invisible. The unfold
  // waits for the reorder rather than going with it: they are two writes to
  // the same database, and sending them together is asking one to lose.
  const apply = (next: SidebarList[] | null, moved?: string) => {
    if (!next) return;
    const landed = moved ? next.find((l) => l.projects.some((p) => p.name === moved)) : undefined;
    if (moved) setAnnounced(place(next, moved));
    save(next, () => {
      if (landed?.section?.collapsed) patchSection.mutate({ id: landed.section.id, req: { collapsed: false } });
    });
  };

  // Moving a section among the sections, which is the other half of what a
  // move can be and says where it landed the same way.
  const moveSectionBy = (section: T.Section, delta: -1 | 1) => {
    const next = moveSection(lists, section.id, delta);
    if (!next) return;
    const sections = next.filter((l) => l.section);
    setAnnounced(tNow('shell.sidebar.sectionPlace', { name: section.name, n: sections.findIndex((l) => l.section!.id === section.id) + 1, total: sections.length }));
    save(next);
  };

  const removeSection = async (id: string) => {
    await api.removeSection(id);
    await queryClient.invalidateQueries({ queryKey: ['sections'] });
    await queryClient.invalidateQueries({ queryKey: ['projects'] });
  };

  // addSectionWith makes a section with a project in it, the project a menu
  // asked to move into a new one: the section is made, then the project
  // moved to it, against the lists as they are by then.
  const addSectionWith = async (name: string, project: string) => {
    const made = await addSection.mutateAsync(name);
    const now = lists.filter((l) => l.section && l.section.id !== made.id);
    apply(drop([...now, { section: made, projects: [] }, ...lists.filter((l) => !l.section)], { kind: 'project', name: project }, { kind: 'list', section: made.id }), project);
  };

  return { projects, sections, lists, announced, save, apply, moveSectionBy, patchSection, addSection, addSectionWith, removeSection };
}

// edge is which half of the element under the pointer a drag is over.
export const edge = (e: DragEvent): 'before' | 'after' => {
  const box = e.currentTarget.getBoundingClientRect();
  return e.clientY < box.top + box.height / 2 ? 'before' : 'after';
};

// useSectionDrag is dragging projects and sections over lists, landing with
// apply (useSections').
export function useSectionDrag(lists: SidebarList[], apply: (next: SidebarList[] | null, moved?: string) => void) {
  // What is being dragged lives in a ref, which every drag event reads, and
  // in state a moment later, for what it draws: changing the page inside
  // dragstart puts the change in the drag image, and can cancel the drag.
  const draggingRef = useRef<Dragging | null>(null);
  const [dragging, setDragging] = useState<Dragging | null>(null);
  const [target, setTarget] = useState<DropTarget | null>(null);
  const aimed = useRef<DropTarget | null>(null); // the target, as of the last dragover

  const startDrag = (what: Dragging) => (e: DragEvent) => {
    draggingRef.current = what;
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', what.kind === 'project' ? what.name : what.id);
    setTimeout(() => draggingRef.current === what && setDragging(what));
  };

  const endDrag = useCallback(() => {
    draggingRef.current = null;
    aimed.current = null;
    setDragging(null);
    setTarget(null);
  }, []);

  // A drag whose row went away mid-drag (its section folded, the list
  // refetched without it) never hears its dragend; the first mouse move with
  // no button down says the drag is over all the same.
  useEffect(() => {
    if (!dragging) return;
    const over = (e: MouseEvent) => e.buttons === 0 && endDrag();
    window.addEventListener('mousemove', over);
    return () => window.removeEventListener('mousemove', over);
  }, [dragging, endDrag]);

  // The indicator: where a drop would land, or nothing where it would change
  // nothing (a project over itself), so what it shows is what a drop does.
  const aim = (where: DropTarget | null) => {
    const what = draggingRef.current;
    const useful = what && where && drop(lists, what, where) ? where : null;
    aimed.current = useful;
    setTarget((current) => (current && useful && targetKey(current) === targetKey(useful) ? current : useful));
  };

  // A drop goes where the drop event says, not where the last dragover did:
  // dragover comes at most every few dozen milliseconds, so a quick drag
  // lets go somewhere it hasn't reported yet.
  const commitDrop = (where: DropTarget | null) => {
    const what = draggingRef.current;
    endDrag();
    if (what && where) apply(drop(lists, what, where), what.kind === 'project' ? what.name : undefined);
  };

  // A drag that ends saying it moved something, with no drop event, was
  // dropped where the indicator last was: Chromium on X11 can let go of a
  // quick drag before telling the page, which would otherwise see the row
  // jump back. Dropped elsewhere (outside the window, or Escape), it says
  // none; one that did drop has ended already, so there is nothing left.
  const finishDrag = (e: DragEvent) => {
    if (e.dataTransfer.dropEffect !== 'none' && draggingRef.current && aimed.current) commitDrop(aimed.current);
    else endDrag();
  };

  // dropZone makes an element a drop target. where says what a drop at the
  // pointer would be, or null for a drag this element isn't a target for,
  // which then goes on to the element around it: a section dragged over a
  // project is a drag over the section that project is in. The innermost
  // target that answers decides.
  const dropZone = (where: (e: DragEvent) => DropTarget | null) => ({
    onDragOver: (e: DragEvent) => {
      if (e.isDefaultPrevented()) return;
      const at = where(e);
      if (!at) return;
      e.preventDefault();
      aim(at);
    },
    onDrop: (e: DragEvent) => {
      if (e.isDefaultPrevented()) return;
      const at = where(e);
      if (!at) return;
      e.preventDefault();
      commitDrop(at);
    },
  });

  const isTarget = (where: DropTarget) => target !== null && targetKey(target) === targetKey(where);

  return { draggingRef, dragging, target, startDrag, endDrag, aim, commitDrop, finishDrag, dropZone, isTarget };
}
