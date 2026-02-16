/**
 * Generic polling utility for long-running API operations.
 *
 * Replaces 7 duplicated polling implementations across:
 * SelectionView, EnhancementView, DescriptionEditor, PublishView,
 * DownloadView, FileUploader, and TriageView.
 *
 * Usage:
 *   const { promise, abort } = createPoller({ fn, intervalMs, isDone, onPoll });
 *   promise.then(onComplete).catch(onError);
 */

export interface PollConfig<T> {
  /** Function to fetch the latest state on each tick. */
  fn: () => Promise<T>;
  /** Milliseconds between poll ticks. */
  intervalMs: number;
  /** Maximum total wall-time (ms) before the poller rejects with a timeout error. */
  timeoutMs?: number;
  /** Return `true` when the result indicates the operation has finished. */
  isDone: (result: T) => boolean;
  /** Called on every successful poll with the latest result (including the final one). */
  onPoll?: (result: T) => void;
  /**
   * Called when a single poll tick throws.
   * Return `true` to swallow the error and keep polling, `false` to abort.
   * Defaults to aborting on any error.
   */
  onPollError?: (err: unknown) => boolean;
  /**
   * If true, fire the first tick immediately instead of waiting `intervalMs`.
   * Useful when the result may already be available (e.g. navigating back to a
   * completed operation). Defaults to false.
   */
  immediate?: boolean;
}

export interface Poller<T> {
  /** Resolves with the final result when `isDone` returns true. */
  promise: Promise<T>;
  /** Call to stop polling early. The promise rejects with "Polling aborted". */
  abort: () => void;
}

/**
 * Start polling an API endpoint.
 *
 * Returns a `Poller` with a promise that resolves when `isDone(result)` is
 * true, and an `abort()` function to cancel early.
 */
export function createPoller<T>(config: PollConfig<T>): Poller<T> {
  let aborted = false;

  const promise = new Promise<T>((resolve, reject) => {
    const { fn, intervalMs, timeoutMs, isDone, onPoll, onPollError } = config;
    const start = Date.now();

    const tick = async () => {
      if (aborted) {
        reject(new Error("Polling aborted"));
        return;
      }

      if (timeoutMs && Date.now() - start > timeoutMs) {
        reject(new Error("Polling timed out"));
        return;
      }

      try {
        const result = await fn();
        onPoll?.(result);
        if (isDone(result)) {
          resolve(result);
          return;
        }
      } catch (err) {
        const shouldContinue = onPollError?.(err) ?? false;
        if (!shouldContinue) {
          reject(err instanceof Error ? err : new Error(String(err)));
          return;
        }
      }

      if (!aborted) {
        setTimeout(tick, intervalMs);
      }
    };

    // Fire the first tick immediately or after one interval.
    if (config.immediate) {
      tick();
    } else {
      setTimeout(tick, intervalMs);
    }
  });

  return { promise, abort: () => { aborted = true; } };
}
