import { useEffect, useRef, useState } from 'react';
import { VncSession } from './vncSession';

// useVncView shows a VNC display in the element view points at, reconnecting if
// it drops (VncSession). Until control is on, the caller covers the view with
// an overlay, and the view never takes focus.
export function useVncView(path: string, enabled: boolean, control: boolean) {
  const view = useRef<HTMLDivElement>(null);
  const session = useRef<VncSession | null>(null);
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    const target = view.current;
    if (!enabled || !target) return;
    const s = new VncSession(path, target, setConnected);
    session.current = s;
    return () => {
      session.current = null;
      s.close();
    };
  }, [path, enabled]);

  useEffect(() => {
    session.current?.setControl(control);
  }, [control, connected]);

  const paste = async () => session.current?.paste();

  return { view, connected, paste };
}
