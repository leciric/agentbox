import type RFB from '@novnc/novnc';
import { useEffect, useRef, useState } from 'react';
import { connectView } from './vnc';

// useVncView shows a VNC display in the element view points at, reconnecting if
// it drops. noVNC only resizes the remote display for a client that takes
// input, so the client always does; until control is on, the caller covers the
// view with an overlay, and the view never takes focus.
export function useVncView(path: string, enabled: boolean, control: boolean) {
  const view = useRef<HTMLDivElement>(null);
  const rfb = useRef<RFB | null>(null);
  const [connected, setConnected] = useState(false);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const target = view.current;
    if (!enabled || !target) return;
    let active = true;
    const client = connectView(path, target);
    client.focusOnClick = false;
    client.addEventListener('connect', () => setConnected(true));
    client.addEventListener('disconnect', () => {
      setConnected(false);
      if (active) setTimeout(() => active && setAttempt((n) => n + 1), 2_000);
    });
    rfb.current = client;
    return () => {
      active = false;
      rfb.current = null;
      setConnected(false);
      try {
        client.disconnect();
      } catch {
        // already disconnected
      }
    };
  }, [path, enabled, attempt]);

  // paste sends your clipboard to the display, for the app in focus to paste.
  const paste = async () => {
    const text = await window.agentbox.readText();
    if (text && rfb.current) rfb.current.clipboardPasteFrom(text);
  };

  useEffect(() => {
    const client = rfb.current;
    if (!client) return;
    client.focusOnClick = control;
    if (control) {
      client.focus();
      void paste();
    } else {
      client.blur();
    }
  }, [control, connected]);

  return { view, connected, paste };
}
