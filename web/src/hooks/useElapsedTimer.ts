/**
 * Shared elapsed-time hook.
 *
 * Replaces duplicated timer logic in ProcessingIndicator and ElapsedTimer.
 * Returns the number of elapsed seconds since mount and a formatted M:SS string.
 */
import { useState, useEffect } from "preact/hooks";

/** Hook that returns elapsed seconds since mount, or since a given start time. */
export function useElapsedTimer(startedAtMs?: number): number {
  const [elapsed, setElapsed] = useState(
    startedAtMs != null ? Math.floor((Date.now() - startedAtMs) / 1000) : 0
  );

  useEffect(() => {
    const interval = setInterval(() => {
      setElapsed(
        startedAtMs != null
          ? Math.floor((Date.now() - startedAtMs) / 1000)
          : (prev) => prev + 1
      );
    }, 1000);
    return () => clearInterval(interval);
  }, [startedAtMs]);

  return elapsed;
}

/** Format seconds into a M:SS string. */
export function formatElapsed(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}:${s.toString().padStart(2, "0")}`;
}
