/**
 * Shared processing / waiting screen component (DDR-056, DDR-058).
 *
 * Provides a consistent UX across all long-running operations:
 * - Inline spinner next to title (DDR-058: smaller, left-aligned)
 * - Elapsed time stopwatch (M:SS)
 * - Status badge pill (DDR-058)
 * - Optional progress bar
 * - Optional per-file status list via `items` prop (DDR-058)
 * - Collapsible technical details panel (job ID, session ID, etc.)
 * - Cancel button
 * - Slot for custom child content
 */
import { useState, useEffect } from "preact/hooks";
import type { ComponentChildren } from "preact";

// ---------------------------------------------------------------------------
// ProcessingIndicator
// ---------------------------------------------------------------------------

/** Per-file item for the processing file list (DDR-058). */
export interface ProcessingItem {
  name: string;
  status: string;
}

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
  /** Per-file processing status (DDR-058). */
  items?: ProcessingItem[];
  children?: ComponentChildren;
  onCancel?: () => void;
}

/** Map a processing item status string to a status-badge CSS modifier. */
function itemBadgeClass(status: string): string {
  switch (status) {
    case "done":
    case "complete":
    case "completed":
      return "status-badge status-badge--done";
    case "error":
    case "failed":
      return "status-badge status-badge--error";
    case "processing":
    case "enhancing":
    case "analyzing":
      return "status-badge status-badge--processing";
    default:
      return "status-badge status-badge--pending";
  }
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
    <div class="card" style={{ padding: "2.5rem" }}>
      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>

      {/* Header: inline spinner + title (DDR-058) */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          gap: "0.75rem",
          marginBottom: "0.5rem",
        }}
      >
        <div
          style={{
            width: "1.5rem",
            height: "1.5rem",
            border: "2.5px solid var(--color-border)",
            borderTop: "2.5px solid var(--color-primary)",
            borderRadius: "50%",
            animation: "spin 1s linear infinite",
            flexShrink: 0,
          }}
        />
        <div
          style={{
            fontSize: "1.5rem",
            color: "var(--color-text)",
            fontWeight: 600,
          }}
        >
          {props.title}
        </div>
      </div>

      {/* Description */}
      <p
        style={{
          color: "var(--color-text-secondary)",
          maxWidth: "32rem",
          margin: "0 auto 1rem",
          textAlign: "center",
        }}
      >
        {props.description}
      </p>

      {/* Elapsed time + status badge (DDR-058: pill badge) */}
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
            class={`status-badge status-badge--${props.status === "pending" ? "pending" : "processing"}`}
          >
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

      {/* Per-file status list (DDR-058) */}
      {props.items && props.items.length > 0 && (
        <div
          style={{
            maxWidth: "32rem",
            margin: "0 auto 1rem",
          }}
        >
          <div class="file-list" style={{ maxHeight: "320px" }}>
            {props.items.map((item) => (
              <div class="file-row" key={item.name}>
                <span
                  style={{
                    fontSize: "1rem",
                    flexShrink: 0,
                    width: "1.25rem",
                    textAlign: "center",
                    opacity: 0.6,
                  }}
                >
                  {"\u{1F4C4}"}
                </span>
                <span
                  style={{
                    flex: 1,
                    fontSize: "0.875rem",
                    fontFamily: "var(--font-mono)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                  title={item.name}
                >
                  {item.name}
                </span>
                <span class={itemBadgeClass(item.status)}>
                  {item.status}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Custom child content (e.g. per-item status grid from EnhancementView) */}
      {props.children}

      {/* Technical details (collapsed by default) */}
      <div style={{ marginTop: "1rem", textAlign: "center" }}>
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
        <div style={{ textAlign: "center" }}>
          <button
            class="outline"
            onClick={props.onCancel}
            style={{ marginTop: "1rem" }}
          >
            Cancel
          </button>
        </div>
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
