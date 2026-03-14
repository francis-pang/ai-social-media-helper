/**
 * Shared processing / waiting screen component (DDR-056, DDR-058).
 *
 * Phase 3b: "AI Analysis Dashboard" layout using .layout-sidebar grid,
 * step-pipeline indicators, streaming log console, and sidebar telemetry.
 *
 * Preserves all original exports and props interface.
 */
import { useState, useEffect, useRef } from "preact/hooks";
import { useElapsedTimer, formatElapsed } from "../hooks/useElapsedTimer";
import type { ComponentChildren } from "preact";
import { getTriageLogs } from "../api/client";
import type { TriageLogEntry } from "../types/api";

// ---------------------------------------------------------------------------
// Public types
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
  startedAt?: string;
  inputTokens?: number;
  outputTokens?: number;
  /** Per-file processing status (DDR-058). */
  items?: ProcessingItem[];
  /** Triage job ID for raw CloudWatch log fetching (DDR-076). */
  triageJobId?: string;
  /** Triage session ID for raw CloudWatch log fetching (DDR-076). */
  triageSessionId?: string;
  /** Override derived stage (1–3) for the step pipeline. */
  stage?: number;
  children?: ComponentChildren;
  onCancel?: () => void;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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

interface LogEntry {
  timestamp: string;
  message: string;
  level: "info" | "success" | "warn" | "error" | "debug";
}

const STAGES = [
  { label: "Upload to Gemini", icon: "☁️", num: 1 },
  { label: "AI Analysis", icon: "🤖", num: 2 },
  { label: "Generating Results", icon: "📋", num: 3 },
] as const;

function deriveStage(status?: string): number {
  if (!status) return 1;
  const s = status.toLowerCase();
  if (s.includes("complete") || s.includes("generat") || s.includes("result")) return 3;
  if (s.includes("analy") || s.includes("process") || s.includes("evaluat")) return 2;
  return 1;
}

function nowTimestamp(): string {
  const d = new Date();
  return d.toLocaleTimeString("en-US", {
    hour12: false,
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

// ---------------------------------------------------------------------------
// ProcessingIndicator
// ---------------------------------------------------------------------------

export function ProcessingIndicator(props: ProcessingIndicatorProps) {
  const startedAtMs = props.startedAt ? new Date(props.startedAt).getTime() : undefined;
  const elapsed = useElapsedTimer(startedAtMs);
  const elapsedStr = formatElapsed(elapsed);

  const [logsExpanded, setLogsExpanded] = useState(true);
  const [logs, setLogs] = useState<LogEntry[]>([]);

  const logConsoleRef = useRef<HTMLDivElement>(null);
  const startTimeRef = useRef(new Date());
  const prevStatusRef = useRef(props.status);
  const prevDescRef = useRef(props.description);

  const [showRawLogs, setShowRawLogs] = useState(false);
  const [rawLogs, setRawLogs] = useState<TriageLogEntry[]>([]);
  const rawLogSinceRef = useRef(0);

  const currentStage = props.stage ?? deriveStage(props.status);

  const hasProgress =
    props.completedCount != null &&
    props.totalCount != null &&
    props.totalCount > 0;
  const progressPct = hasProgress
    ? (props.completedCount! / props.totalCount!) * 100
    : 0;

  // ── Synthetic log generation ──────────────────────────────────────────

  useEffect(() => {
    setLogs([
      { timestamp: nowTimestamp(), message: "Analysis job started", level: "info" },
    ]);
  }, []);

  useEffect(() => {
    if (props.status && props.status !== prevStatusRef.current) {
      if (prevStatusRef.current) {
        setLogs((prev) => [
          ...prev,
          { timestamp: nowTimestamp(), message: "Phase completed", level: "success" },
        ]);
      }
      setLogs((prev) => [
        ...prev,
        { timestamp: nowTimestamp(), message: `Phase: ${props.status}`, level: "info" },
      ]);
      prevStatusRef.current = props.status;
    }
  }, [props.status]);

  useEffect(() => {
    if (props.description && props.description !== prevDescRef.current) {
      setLogs((prev) => [
        ...prev,
        { timestamp: nowTimestamp(), message: `Status: ${props.description}`, level: "info" },
      ]);
      prevDescRef.current = props.description;
    }
  }, [props.description]);

  useEffect(() => {
    if (logConsoleRef.current) {
      logConsoleRef.current.scrollTop = logConsoleRef.current.scrollHeight;
    }
  }, [logs]);

  // Raw CloudWatch log polling (DDR-076)
  useEffect(() => {
    if (!showRawLogs || !props.triageJobId || !props.triageSessionId) return;

    let cancelled = false;
    const poll = async () => {
      if (cancelled) return;
      try {
        const res = await getTriageLogs(props.triageJobId!, props.triageSessionId!, rawLogSinceRef.current || undefined);
        if (!cancelled && res.entries.length > 0) {
          setRawLogs((prev) => [...prev, ...res.entries]);
          rawLogSinceRef.current = res.nextSince;
        }
      } catch {
        // Ignore polling errors
      }
      if (!cancelled) {
        setTimeout(poll, 4000);
      }
    };
    poll();

    return () => { cancelled = true; };
  }, [showRawLogs, props.triageJobId, props.triageSessionId]);

  // ── Render ────────────────────────────────────────────────────────────

  return (
    <div class="layout-sidebar">
      <style>{`@keyframes spin{to{transform:rotate(360deg)}}`}</style>

      {/* ── Left column ── */}
      <div>
        {/* Job header card */}
        <div class="card" style={{ padding: "2rem", marginBottom: "1.5rem" }}>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "0.75rem",
              marginBottom: "1rem",
            }}
          >
            <div
              style={{
                width: "1.25rem",
                height: "1.25rem",
                border: "2.5px solid var(--color-border)",
                borderTop: "2.5px solid var(--color-primary)",
                borderRadius: "50%",
                animation: "spin 1s linear infinite",
                flexShrink: 0,
              }}
            />
            <h2
              style={{
                margin: 0,
                fontSize: "1.25rem",
                fontWeight: 600,
                color: "var(--color-text)",
              }}
            >
              AI Analysis in Progress
            </h2>
          </div>

          <p
            style={{
              color: "var(--color-text-secondary)",
              margin: "0 0 1.25rem",
              fontSize: "0.95rem",
            }}
          >
            {props.description}
          </p>

          <div
            style={{
              display: "flex",
              alignItems: "baseline",
              gap: "1.5rem",
              marginBottom: "0.75rem",
            }}
          >
            <div
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: "1.75rem",
                fontWeight: 600,
                color: "var(--color-text)",
                letterSpacing: "0.02em",
              }}
            >
              {elapsedStr}
            </div>
            {props.status && (
              <span
                class={`status-badge status-badge--${props.status === "pending" ? "pending" : "processing"}`}
              >
                {props.status}
              </span>
            )}
          </div>

          <div style={{ fontSize: "0.8rem", color: "var(--color-text-secondary)" }}>
            Started{" "}
            {startTimeRef.current.toLocaleTimeString("en-US", {
              hour: "2-digit",
              minute: "2-digit",
              second: "2-digit",
            })}
          </div>
        </div>

        {/* 3-stage pipeline */}
        <div class="step-pipeline" style={{ marginBottom: "1.5rem" }}>
          {STAGES.flatMap((stage, i) => {
            const modifier =
              currentStage > stage.num
                ? "done"
                : currentStage === stage.num
                  ? "active"
                  : "pending";
            const els: preact.JSX.Element[] = [
              <div
                class={`step-pipeline__step step-pipeline__step--${modifier}`}
                key={`s-${stage.num}`}
              >
                <div class="step-pipeline__icon">{stage.icon}</div>
                <div class="step-pipeline__label">{stage.label}</div>
                <div
                  style={{
                    fontSize: "0.7rem",
                    color: "var(--color-text-secondary)",
                    marginTop: "0.25rem",
                  }}
                >
                  {currentStage > stage.num
                    ? "Done"
                    : currentStage === stage.num
                      ? "Running..."
                      : "Pending"}
                </div>
              </div>,
            ];
            if (i < STAGES.length - 1) {
              els.push(
                <div
                  class={`step-pipeline__connector${currentStage > stage.num ? " step-pipeline__connector--done" : ""}`}
                  key={`c-${i}`}
                />,
              );
            }
            return els;
          })}
        </div>

        {/* Progress bar (preserved) */}
        {hasProgress && (
          <div style={{ marginBottom: "1.5rem" }}>
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

        {/* Per-file status list (preserved) */}
        {props.items && props.items.length > 0 && (
          <div style={{ marginBottom: "1.5rem" }}>
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
                  <span class={itemBadgeClass(item.status)}>{item.status}</span>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Custom child content slot (preserved) */}
        {props.children}

        {/* Abort Job */}
        <div style={{ marginBottom: "1.5rem" }}>
          <a
            href="#"
            onClick={(e: Event) => {
              e.preventDefault();
              props.onCancel?.();
            }}
            style={{
              color: "var(--color-danger)",
              fontSize: "0.875rem",
              textDecoration: "none",
              cursor: "pointer",
            }}
          >
            ✕ Abort Job
          </a>
        </div>

        {/* Streaming Logs with Raw/Synthetic toggle (DDR-076) */}
        <div>
          <div style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            marginBottom: "0.5rem",
          }}>
            <div
              onClick={() => setLogsExpanded(!logsExpanded)}
              style={{
                cursor: "pointer",
                fontSize: "0.875rem",
                fontWeight: 600,
                color: "var(--color-text)",
                userSelect: "none",
              }}
            >
              {logsExpanded ? "\u25BC" : "\u25B6"} Streaming Logs
            </div>
            {logsExpanded && props.triageJobId && (
              <div style={{
                display: "flex",
                gap: "0.25rem",
                fontSize: "0.75rem",
              }}>
                <button
                  onClick={() => setShowRawLogs(false)}
                  style={{
                    padding: "0.125rem 0.5rem",
                    borderRadius: "4px",
                    border: "1px solid var(--color-border)",
                    background: !showRawLogs ? "var(--color-primary)" : "transparent",
                    color: !showRawLogs ? "white" : "var(--color-text-secondary)",
                    cursor: "pointer",
                    fontSize: "0.75rem",
                  }}
                >
                  Synthetic
                </button>
                <button
                  onClick={() => setShowRawLogs(true)}
                  style={{
                    padding: "0.125rem 0.5rem",
                    borderRadius: "4px",
                    border: "1px solid var(--color-border)",
                    background: showRawLogs ? "var(--color-primary)" : "transparent",
                    color: showRawLogs ? "white" : "var(--color-text-secondary)",
                    cursor: "pointer",
                    fontSize: "0.75rem",
                  }}
                >
                  Raw Logs
                </button>
              </div>
            )}
          </div>
          {logsExpanded && !showRawLogs && (
            <div class="log-console" ref={logConsoleRef}>
              {logs.map((entry, i) => (
                <div class={`log-entry log-entry--${entry.level}`} key={i}>
                  <span
                    style={{
                      color: "var(--color-text-secondary)",
                      marginRight: "0.75rem",
                    }}
                  >
                    {entry.timestamp}
                  </span>
                  [{entry.level.toUpperCase()}] {entry.message}
                </div>
              ))}
            </div>
          )}
          {logsExpanded && showRawLogs && (
            <div class="log-console" ref={logConsoleRef}>
              {rawLogs.length === 0 ? (
                <div class="log-entry log-entry--info">
                  <span style={{ color: "var(--color-text-secondary)" }}>
                    Loading CloudWatch logs...
                  </span>
                </div>
              ) : (
                rawLogs.map((entry, i) => {
                  const ts = new Date(entry.timestamp).toLocaleTimeString("en-US", {
                    hour12: false,
                    hour: "2-digit",
                    minute: "2-digit",
                    second: "2-digit",
                  });
                  const level = entry.message.includes('"level":"error"') ? "error" :
                    entry.message.includes('"level":"warn"') ? "warn" :
                    entry.message.includes('"level":"info"') ? "info" : "debug";
                  return (
                    <div class={`log-entry log-entry--${level}`} key={i}>
                      <span style={{ color: "var(--color-text-secondary)", marginRight: "0.75rem" }}>
                        {ts}
                      </span>
                      {entry.message.length > 200 ? entry.message.slice(0, 200) + "..." : entry.message}
                    </div>
                  );
                })
              )}
            </div>
          )}
        </div>
      </div>

      {/* ── Right column (sidebar) ── */}
      <div>
        {/* Job Telemetry */}
        <div class="sidebar-panel">
          <h3>Job Telemetry</h3>

          {props.status && (
            <div style={{ marginBottom: "0.75rem" }}>
              <span class="status-badge status-badge--processing">
                {props.status}
              </span>
            </div>
          )}

          <div
            style={{
              fontSize: "0.8rem",
              color: "var(--color-text-secondary)",
              display: "flex",
              flexDirection: "column",
              gap: "0.5rem",
            }}
          >
            <div>
              <span style={{ fontWeight: 500 }}>Session ID: </span>
              <span style={{ fontFamily: "var(--font-mono)" }}>
                {props.sessionId ?? "—"}
              </span>
            </div>
            <div>
              <span style={{ fontWeight: 500 }}>Job ID: </span>
              <span style={{ fontFamily: "var(--font-mono)" }}>
                {props.jobId ?? "—"}
              </span>
            </div>
            {props.fileCount != null && (
              <div>
                <span style={{ fontWeight: 500 }}>Total Files: </span>
                {props.fileCount}
              </div>
            )}
            <div>
              <span style={{ fontWeight: 500 }}>Poll Interval: </span>
              {props.pollIntervalMs
                ? `${props.pollIntervalMs / 1000}s`
                : "5s"}
            </div>
            <div>
              <span style={{ fontWeight: 500 }}>Elapsed: </span>
              <span style={{ fontFamily: "var(--font-mono)" }}>
                {elapsedStr}
              </span>
            </div>
          </div>
        </div>

        {/* Resource Usage */}
        <div class="sidebar-panel" style={{ marginTop: "1rem" }}>
          <h3>Resource Usage</h3>

          <div
            style={{
              fontSize: "0.8rem",
              color: "var(--color-text-secondary)",
              display: "flex",
              flexDirection: "column",
              gap: "0.75rem",
            }}
          >
            {/* Gemini Tokens */}
            <div>
              <span style={{ fontWeight: 500 }}>Gemini Tokens: </span>
              {props.inputTokens != null || props.outputTokens != null ? (
                <span>
                  {((props.inputTokens ?? 0) + (props.outputTokens ?? 0)).toLocaleString()}
                  <span
                    style={{
                      fontSize: "0.7rem",
                      color: "var(--color-text-secondary)",
                      marginLeft: "0.25rem",
                    }}
                  >
                    ({props.inputTokens?.toLocaleString() ?? 0} in / {props.outputTokens?.toLocaleString() ?? 0} out)
                  </span>
                </span>
              ) : (
                <span style={{ fontStyle: "italic" }}>Estimating...</span>
              )}
            </div>

            {/* Token Budget */}
            {(() => {
              const total = (props.inputTokens ?? 0) + (props.outputTokens ?? 0);
              const budget = Math.max(props.totalCount ?? 1, 1) * 8000;
              const pct = Math.min(Math.round((total / budget) * 100), 100);
              const hasData = props.inputTokens != null || props.outputTokens != null;
              return (
                <div>
                  <div
                    style={{
                      display: "flex",
                      justifyContent: "space-between",
                      marginBottom: "0.25rem",
                    }}
                  >
                    <span style={{ fontWeight: 500 }}>Token Budget</span>
                    <span>{hasData ? `${pct}%` : "—"}</span>
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
                        width: hasData ? `${pct}%` : "0%",
                        height: "100%",
                        background: "var(--color-primary)",
                        borderRadius: "4px",
                        transition: "width 0.5s ease",
                      }}
                    />
                  </div>
                </div>
              );
            })()}

            {/* Items Processed */}
            {props.totalCount != null && props.totalCount > 0 && (
              <div>
                <span style={{ fontWeight: 500 }}>Items Processed: </span>
                <span>
                  {props.completedCount ?? 0} / {props.totalCount}
                </span>
              </div>
            )}
          </div>
        </div>

      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// ElapsedTimer — lightweight standalone timer for views that already have
//                their own layout (e.g. PublishView).
// ---------------------------------------------------------------------------

export function ElapsedTimer() {
  const elapsed = useElapsedTimer();
  return (
    <span
      style={{
        fontFamily: "var(--font-mono)",
        fontSize: "0.75rem",
        color: "var(--color-text-secondary)",
      }}
    >
      {formatElapsed(elapsed)}
    </span>
  );
}
