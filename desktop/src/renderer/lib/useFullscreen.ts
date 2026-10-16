import { useCallback, useEffect, useRef, useState } from 'react';

// useFullscreen makes the element of ref fill the screen with the browser's
// Fullscreen API and tracks whether it does, however it ended (the button, Esc).
// What the element holds keeps its events, so the mouse and keyboard still work.
export function useFullscreen<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [full, setFull] = useState(false);
  useEffect(() => {
    const sync = () => setFull(!!ref.current && document.fullscreenElement === ref.current);
    document.addEventListener('fullscreenchange', sync);
    return () => document.removeEventListener('fullscreenchange', sync);
  }, []);
  const toggle = useCallback(() => {
    if (document.fullscreenElement) void document.exitFullscreen();
    else void ref.current?.requestFullscreen().catch(() => undefined);
  }, []);
  return { ref, full, toggle };
}
