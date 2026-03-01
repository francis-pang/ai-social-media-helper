/**
 * Shared elapsed-time hook.
 *
 * Replaces duplicated timer logic in ProcessingIndicator and ElapsedTimer.
 * Returns the number of elapsed seconds since mount and a formatted M:SS string.
 */
import { useState, useEffect } from "preact/hooks";

/** Hook that returns elapsed seconds since the component mounted. */
export function useElapsedTimer(): number {
  const [elapsed, setElapsed] = useState(0);

  useEffect(() => {
    const start = Date.now();
    const interval = setInterval(() => {
      setElapsed(Math.floor((Date.now() - start) / 1000));
    }, 1000);
    return () => clearInterval(interval);
  }, []);

  return elapsed;
}

/** Format seconds into a M:SS string. */
export function formatElapsed(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}:${s.toString().padStart(2, "0")}`;
}
