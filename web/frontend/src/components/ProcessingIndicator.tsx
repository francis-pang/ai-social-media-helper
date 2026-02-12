/**
 * Shared processing / waiting screen component (DDR-056).
 *
 * Provides a consistent UX across all long-running operations:
 * - Animated spinner
 * - Elapsed time stopwatch (M:SS)
 * - Status badge (pending / processing)
 * - Optional progress bar
 * - Collapsible technical details panel (job ID, session ID, etc.)
 * - Cancel button
 * - Slot for custom child content
 */
import { useState, useEffect } from "preact/hooks";
import type { ComponentChildren } from "preact";

// ---------------------------------------------------------------------------
// ProcessingIndicator
// ---------------------------------------------------------------------------

interface ProcessingIndicatorProps {
  title: string;
  description: string;
  status?: string;
  jobId?: string;
  sessionId?: string;
  pollIntervalMs?: number;
  fileCount?: number;
  completedCount?: number;
  totalCount?: number;
  children?: ComponentChildren;
  onCancel?: () => void;
}

export function ProcessingIndicator(props: ProcessingIndicatorProps) {
  const [elapsed, setElapsed] = useState(0);
  const [showDetails, setShowDetails] = useState(false);

  useEffect(() => {
    const start = Date.now();
    const interval = setInterval(() => {
      setElapsed(Math.floor((Date.now() - start) / 1000));
    }, 1000);
    return () => clearInterval(interval);
  }, []);

  const minutes = Math.floor(elapsed / 60);
  const seconds = elapsed % 60;
  const elapsedStr = `${minutes}:${seconds.toString().padStart(2, "0")}`;

  const hasProgress =
    props.completedCount != null &&
    props.totalCount != null &&
    props.totalCount > 0;
  const progressPct = hasProgress
    ? (props.completedCount! / props.totalCount!) * 100
    : 0;

  return (
    <div class="card" style={{ textAlign: "center", padding: "3rem" }}>
      {/* Spinner */}
      <div
        style={{
          width: "3rem",
          height: "3rem",
          border: "3px solid var(--color-border)",
          borderTop: "3px solid var(--color-primary)",
          borderRadius: "50%",
          animation: "spin 1s linear infinite",
          margin: "0 auto 1.5rem",
        }}
      />
      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>

      {/* Title */}
      <div
        style={{
          fontSize: "1.5rem",
          marginBottom: "0.5rem",
          color: "var(--color-text)",
        }}
      >
        {props.title}
      </div>

      {/* Description */}
      <p
        style={{
          color: "var(--color-text-secondary)",
          maxWidth: "32rem",
          margin: "0 auto 1rem",
        }}
      >
        {props.description}
      </p>

      {/* Elapsed time + status badge */}
      <div
        style={{
          display: "flex",
          justifyContent: "center",
          alignItems: "center",
          gap: "1rem",
          marginBottom: "1rem",
        }}
      >
        <span
          style={{
            fontSize: "1.125rem",
            fontFamily: "var(--font-mono)",
            color: "var(--color-text)",
          }}
        >
          {elapsedStr}
        </span>
        {props.status && (
          <span
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "0.375rem",
              fontSize: "0.75rem",
              padding: "0.125rem 0.5rem",
              borderRadius: "var(--radius)",
              background:
                props.status === "processing"
                  ? "rgba(108, 140, 255, 0.1)"
                  : "rgba(139, 143, 168, 0.1)",
              color:
                props.status === "processing"
                  ? "var(--color-primary)"
                  : "var(--color-text-secondary)",
            }}
          >
            <span
              style={{
                width: "6px",
                height: "6px",
                borderRadius: "50%",
                background:
                  props.status === "processing"
                    ? "var(--color-primary)"
                    : "var(--color-text-secondary)",
              }}
            />
            {props.status}
          </span>
        )}
      </div>

      {/* Progress bar */}
      {hasProgress && (
        <div style={{ margin: "0 auto 1rem", maxWidth: "24rem" }}>
          <div
            style={{
              display: "flex",
              justifyContent: "space-between",
              fontSize: "0.875rem",
              color: "var(--color-text-secondary)",
              marginBottom: "0.375rem",
            }}
          >
            <span>
              {props.completedCount} of {props.totalCount}
            </span>
            <span>{Math.round(progressPct)}%</span>
          </div>
          <div
            style={{
              width: "100%",
              height: "0.5rem",
              background: "var(--color-surface-hover)",
              borderRadius: "4px",
              overflow: "hidden",
            }}
          >
            <div
              style={{
                width: `${progressPct}%`,
                height: "100%",
                background: "var(--color-primary)",
                borderRadius: "4px",
                transition: "width 0.3s",
              }}
            />
          </div>
        </div>
      )}

      {/* Custom child content (e.g. per-item status grid) */}
      {props.children}

      {/* Technical details (collapsed by default) */}
      <div style={{ marginTop: "1rem" }}>
        <button
          class="outline"
          onClick={() => setShowDetails(!showDetails)}
          style={{ fontSize: "0.75rem", padding: "0.25rem 0.75rem" }}
        >
          {showDetails ? "Hide details" : "Show details"}
        </button>

        {showDetails && (
          <div
            style={{
              marginTop: "0.75rem",
              padding: "0.75rem 1rem",
              background: "var(--color-bg)",
              borderRadius: "var(--radius)",
              fontFamily: "var(--font-mono)",
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              textAlign: "left",
              maxWidth: "24rem",
              margin: "0.75rem auto 0",
            }}
          >
            {props.jobId && <div>Job ID: {props.jobId}</div>}
            {props.sessionId && <div>Session: {props.sessionId}</div>}
            {props.pollIntervalMs && (
              <div>Poll interval: {props.pollIntervalMs}ms</div>
            )}
            {props.fileCount != null && <div>Files: {props.fileCount}</div>}
            {props.status && <div>Status: {props.status}</div>}
            <div>Elapsed: {elapsedStr}</div>
          </div>
        )}
      </div>

      {/* Cancel button */}
      {props.onCancel && (
        <button
          class="outline"
          onClick={props.onCancel}
          style={{ marginTop: "1rem" }}
        >
          Cancel
        </button>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// ElapsedTimer — lightweight standalone timer for views that already have
//                their own layout (e.g. PublishView).
// ---------------------------------------------------------------------------

export function ElapsedTimer() {
  const [elapsed, setElapsed] = useState(0);

  useEffect(() => {
    const start = Date.now();
    const interval = setInterval(() => {
      setElapsed(Math.floor((Date.now() - start) / 1000));
    }, 1000);
    return () => clearInterval(interval);
  }, []);

  const minutes = Math.floor(elapsed / 60);
  const seconds = elapsed % 60;

  return (
    <span
      style={{
        fontFamily: "var(--font-mono)",
        fontSize: "0.75rem",
        color: "var(--color-text-secondary)",
      }}
    >
      {minutes}:{seconds.toString().padStart(2, "0")}
    </span>
  );
}
