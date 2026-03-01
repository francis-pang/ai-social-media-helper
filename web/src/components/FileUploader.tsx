import { signal } from "@preact/signals";
import { useState } from "preact/hooks";
import { initTriage, updateTriageFiles, finalizeTriageUploads, getTriageResults, startTriage } from "../api/client";
import { createUploadEngine } from "../upload/uploadEngine";
import { selectedPaths, uploadSessionId, triageJobId, navigateToStep, currentStep, fileHandles, economyMode } from "../app";
import { syncUrlToStep } from "../router";
import { getFilesFromDataTransfer } from "../utils/fileSystem";
import { formatBytes, formatSpeed } from "../utils/format";
import { formatElapsed } from "../hooks/useElapsedTimer";
import type { FileProcessingStatus } from "../types/api";

// Engine with dedup + speed tracking for triage upload (DDR-080)
const engine = createUploadEngine({ enableDedup: true, enableSpeedTracking: true });

// Aliases for engine state used throughout this module
const files = engine.files;
const error = engine.error;
const uploadSpeed = engine.uploadSpeed;

/** Media file MIME types accepted by the uploader. */
const ACCEPT =
  "image/jpeg,image/png,image/gif,image/webp,image/heic,image/heif," +
  "video/mp4,video/quicktime,video/x-msvideo,video/webm,video/x-matroska";

/**
 * Combined lifecycle status for a file (DDR-063).
 * Merges S3 upload status with server-side processing status.
 */
type FileLifecycleStatus = "uploading" | "processing" | "ready" | "error";

interface FileWithLifecycle {
  name: string;
  size: number;
  key: string;
  lifecycleStatus: FileLifecycleStatus;
  uploadProgress: number;
  loaded: number;
  error?: string;
  thumbnailUrl?: string;
  converted?: boolean;
}

const triageInitialized = signal<boolean>(false);
const triagePolling = signal<boolean>(false);
const triageFinalized = signal<boolean>(false);

/** Per-file server-side processing statuses from poll results (DDR-063). */
const serverFileStatuses = signal<FileProcessingStatus[]>([]);
const serverProcessedCount = signal<number>(0);
const serverExpectedFileCount = signal<number>(0);

function generateSessionId(): string {
  return crypto.randomUUID();
}

function handleFileSelect(e: Event) {
  const input = e.target as HTMLInputElement;
  if (!input.files || input.files.length === 0) return;
  addFiles(Array.from(input.files));
  input.value = "";
}

async function handleDrop(e: DragEvent) {
  e.preventDefault();
  e.stopPropagation();
  if (!e.dataTransfer) return;

  const allFiles = await getFilesFromDataTransfer(e.dataTransfer);
  if (allFiles.length > 0) {
    addFiles(allFiles);
  }
}

function handleDragOver(e: DragEvent) {
  e.preventDefault();
  e.stopPropagation();
}

/** DDR-074: Use showOpenFilePicker when available to retain handles for local deletion. */
const supportsFilePicker = typeof window !== "undefined" && "showOpenFilePicker" in window;

async function handleBrowseFiles() {
  if (supportsFilePicker && window.showOpenFilePicker) {
    try {
      const handles = await window.showOpenFilePicker({
        multiple: true,
        types: [
          {
            description: "Media files",
            accept: {
              "image/jpeg": [".jpg", ".jpeg"],
              "image/png": [".png"],
              "image/gif": [".gif"],
              "image/webp": [".webp"],
              "image/heic": [".heic"],
              "image/heif": [".heif"],
              "video/mp4": [".mp4"],
              "video/quicktime": [".mov"],
              "video/webm": [".webm"],
            },
          },
        ],
      });

      const newHandleMap = new Map(fileHandles.value);
      const filesToAdd: File[] = [];
      for (const handle of handles) {
        const file = await handle.getFile();
        newHandleMap.set(file.name, handle);
        filesToAdd.push(file);
      }
      fileHandles.value = newHandleMap;

      if (filesToAdd.length > 0) {
        addFiles(filesToAdd);
      }
    } catch (e) {
      if (e instanceof DOMException && e.name === "AbortError") return;
      (document.getElementById("file-input") as HTMLInputElement)?.click();
    }
  } else {
    (document.getElementById("file-input") as HTMLInputElement)?.click();
  }
}

async function addFiles(newFiles: File[]) {
  if (!uploadSessionId.value) {
    uploadSessionId.value = generateSessionId();
    syncUrlToStep(currentStep.value, uploadSessionId.value);
  }

  const sessionId = uploadSessionId.value;

  const added = await engine.addFiles(sessionId, newFiles);
  if (added === 0) return;

  // DDR-061: Initialize triage on first file drop
  if (!triageInitialized.value) {
    triageInitialized.value = true;
    initTriageSession(sessionId, files.value.length);
  } else {
    const jobId = triageJobId.value;
    if (jobId) {
      updateTriageFiles({
        sessionId,
        jobId,
        expectedFileCount: files.value.length,
      }).catch((e) => console.error("Failed to update file count:", e));
    }
  }
}

async function initTriageSession(sessionId: string, fileCount: number) {
  try {
    const res = await initTriage({ sessionId, expectedFileCount: fileCount, economy_mode: economyMode.value });
    triageJobId.value = res.id;
    triagePolling.value = true;
    pollTriageResults(res.id, sessionId);
  } catch (e) {
    error.value = e instanceof Error ? e.message : "Failed to initialize triage";
  }
}

async function pollTriageResults(jobId: string, sessionId: string) {
  let finalizedAt: number | null = null;
  const FINALIZE_TIMEOUT_MS = 2 * 60 * 1000; // 2 minutes after finalize

  while (triagePolling.value) {
    try {
      const results = await getTriageResults(jobId, sessionId);

      // DDR-063: Store per-file processing statuses for display
      if (results.fileStatuses) {
        serverFileStatuses.value = results.fileStatuses;
      }
      if (results.processedCount != null) {
        serverProcessedCount.value = results.processedCount;
      }
      if (results.expectedFileCount != null) {
        serverExpectedFileCount.value = results.expectedFileCount;
      }

      // Detect backend error status (e.g. Step Function failure that wrote error to DDB)
      if (results.status === "error") {
        triagePolling.value = false;
        const doneFiles = files.value.filter(f => f.status === "done");
        selectedPaths.value = doneFiles.map(f => f.key);
        navigateToStep("processing");
        return;
      }

      // Triage complete — navigate immediately. The API omits expectedFileCount/
      // processedCount for completed jobs, so the allProcessed check below would
      // never fire. Navigating here avoids an infinite poll when the job goes
      // directly from "pending" → "complete" without an intermediate "processing"
      // state with per-file counts.
      if (results.status === "complete") {
        triagePolling.value = false;
        const doneFiles = files.value.filter(f => f.status === "done");
        selectedPaths.value = doneFiles.map(f => f.key);
        navigateToStep("processing");
        return;
      }

      // DDR-067: Finalize triage (start SF) when all uploads are done
      const allUploaded = files.value.length > 0 && files.value.every(
        f => f.status === "done" || f.status === "error"
      );
      if (allUploaded && !triageFinalized.value) {
        triageFinalized.value = true;
        finalizedAt = Date.now();
        const doneCount = files.value.filter(f => f.status === "done").length;
        if (doneCount > 0) {
          finalizeTriageUploads({ sessionId, jobId, economy_mode: economyMode.value }).catch(
            (e) => console.error("Failed to finalize triage uploads:", e)
          );
        }
      }

      // Detect stuck-pending after finalization: if the job stays "pending"
      // well after finalization, the Step Function likely crashed before it
      // could update the job status.
      if (
        triageFinalized.value &&
        finalizedAt != null &&
        results.status === "pending" &&
        Date.now() - finalizedAt > FINALIZE_TIMEOUT_MS
      ) {
        triagePolling.value = false;
        error.value = "Processing pipeline failed to start — please try again";
        return;
      }

      // DDR-076: Navigate to TriageView as soon as all per-file processing
      // completes, without waiting for the Gemini triage-run step to finish.
      const allProcessed = results.expectedFileCount != null &&
        results.expectedFileCount > 0 &&
        results.processedCount != null &&
        results.processedCount >= results.expectedFileCount;

      if (allUploaded && allProcessed && triageFinalized.value) {
        const doneFiles = files.value.filter(f => f.status === "done");
        const errorFiles = files.value.filter(f => f.status === "error");
        if (errorFiles.length > 0 && doneFiles.length > 0) {
          await updateTriageFiles({
            sessionId,
            jobId,
            expectedFileCount: doneFiles.length,
          }).catch(e => console.error("Failed to update expected file count:", e));
        }
        triagePolling.value = false;
        selectedPaths.value = doneFiles.map(f => f.key);
        navigateToStep("processing");
        return;
      }
    } catch {
      // Ignore polling errors
    }
    await new Promise(resolve => setTimeout(resolve, 2000));
  }
}

/**
 * Merge upload file list with server-side processing statuses (DDR-063).
 * Returns files tagged with their combined lifecycle status.
 */
function getFilesWithLifecycle(): FileWithLifecycle[] {
  const serverMap = new Map(
    serverFileStatuses.value.map((fs) => [fs.filename, fs] as const),
  );

  return files.value.map((f) => {
    const serverStatus = serverMap.get(f.name);

    if (f.status === "error") {
      return {
        name: f.name,
        size: f.size,
        key: f.key,
        lifecycleStatus: "error" as const,
        uploadProgress: f.progress,
        loaded: f.loaded,
        error: f.error,
      };
    }

    if (f.status === "uploading" || f.status === "pending") {
      return {
        name: f.name,
        size: f.size,
        key: f.key,
        lifecycleStatus: "uploading" as const,
        uploadProgress: f.progress,
        loaded: f.loaded,
      };
    }

    // File uploaded to S3 — check server-side processing status
    if (serverStatus) {
      if (serverStatus.status === "error" || serverStatus.status === "invalid") {
        return {
          name: f.name,
          size: f.size,
          key: f.key,
          lifecycleStatus: "error" as const,
          uploadProgress: 100,
          loaded: f.loaded,
          error: serverStatus.error || (serverStatus.status === "invalid" ? "Invalid file" : undefined),
        };
      }
      if (serverStatus.status === "valid") {
        return {
          name: f.name,
          size: f.size,
          key: f.key,
          lifecycleStatus: "ready" as const,
          uploadProgress: 100,
          loaded: f.loaded,
          thumbnailUrl: serverStatus.thumbnailUrl,
          converted: serverStatus.converted,
        };
      }
    }

    // Uploaded but no server status yet, or server status is "processing"
    return {
      name: f.name,
      size: f.size,
      key: f.key,
      lifecycleStatus: "processing" as const,
      uploadProgress: 100,
      loaded: f.loaded,
    };
  });
}

function clearAll() {
  engine.clearAll();
  uploadSessionId.value = null;
  serverFileStatuses.value = [];
  serverProcessedCount.value = 0;
  serverExpectedFileCount.value = 0;
  triageFinalized.value = false;
}

/** Reset FileUploader state (called from navigateToLanding — DDR-042). */
export function resetFileUploaderState() {
  engine.resetState();
  triageInitialized.value = false;
  triagePolling.value = false;
  triageFinalized.value = false;
  serverFileStatuses.value = [];
  serverProcessedCount.value = 0;
  serverExpectedFileCount.value = 0;
  uploadSessionId.value = null;
}

/** Proceed to triage: start the triage job and navigate to processing (DDR-042). */
async function proceedToTriage() {
  const uploadedKeys = files.value
    .filter((f) => f.status === "done")
    .map((f) => f.key);

  if (uploadedKeys.length === 0) return;

  const sessionId = uploadSessionId.value;
  if (!sessionId) return;

  error.value = null;
  selectedPaths.value = uploadedKeys;

  try {
    const res = await startTriage({ sessionId, economy_mode: economyMode.value });
    triageJobId.value = res.id;
    navigateToStep("processing");
  } catch (e) {
    error.value = e instanceof Error ? e.message : "Failed to start triage";
  }
}

// ---------------------------------------------------------------------------
// Thumbnail card for grid view (Phase 3a)
// ---------------------------------------------------------------------------

function statusDotColor(status: FileLifecycleStatus): string {
  switch (status) {
    case "uploading": return "var(--color-primary)";
    case "processing": return "var(--color-warning)";
    case "ready": return "var(--color-success)";
    case "error": return "var(--color-danger)";
  }
}

function statusCardLabel(status: FileLifecycleStatus): string {
  switch (status) {
    case "uploading": return "UPLOADING\u2026";
    case "processing": return "PROCESSING\u2026";
    case "ready": return "READY";
    case "error": return "ERROR";
  }
}

type MiniPipelineStage = "done" | "active" | "pending";

interface MiniPipelineStep {
  label: string;
  stage: MiniPipelineStage;
}

function getMiniPipelineSteps(status: FileLifecycleStatus): MiniPipelineStep[] {
  switch (status) {
    case "uploading":
      return [
        { label: "Uploading", stage: "active" },
        { label: "Downsizing", stage: "pending" },
        { label: "Thumbnail", stage: "pending" },
      ];
    case "processing":
      return [
        { label: "Uploading", stage: "done" },
        { label: "Downsizing", stage: "active" },
        { label: "Thumbnail", stage: "pending" },
      ];
    case "ready":
      return [
        { label: "Uploading", stage: "done" },
        { label: "Downsizing", stage: "done" },
        { label: "Thumbnail", stage: "done" },
      ];
    case "error":
      return [
        { label: "Uploading", stage: "pending" },
        { label: "Downsizing", stage: "pending" },
        { label: "Thumbnail", stage: "pending" },
      ];
  }
}

function FileCard({ f }: { f: FileWithLifecycle }) {
  const dotColor = statusDotColor(f.lifecycleStatus);
  const pipelineSteps = getMiniPipelineSteps(f.lifecycleStatus);

  return (
    <div style={{
      background: "var(--color-surface)",
      border: "1px solid var(--color-border)",
      borderRadius: "var(--radius)",
      overflow: "hidden",
      transition: "box-shadow 0.15s",
    }}>
      {/* Aspect-ratio thumbnail area */}
      <div style={{
        aspectRatio: "1",
        background: "var(--color-surface-alt)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        position: "relative",
        overflow: "hidden",
      }}>
        {f.thumbnailUrl ? (
          <img
            src={f.thumbnailUrl}
            alt=""
            loading="lazy"
            style={{ width: "100%", height: "100%", objectFit: "cover" }}
            onError={(e) => { (e.target as HTMLImageElement).style.display = "none"; }}
          />
        ) : (
          <span style={{ fontSize: "2rem", opacity: 0.35 }}>{"\u{1F4C4}"}</span>
        )}
        {/* Status dot */}
        <span style={{
          position: "absolute",
          top: "0.5rem",
          right: "0.5rem",
          width: "0.625rem",
          height: "0.625rem",
          borderRadius: "50%",
          background: dotColor,
          boxShadow: "0 0 0 2px var(--color-surface)",
          animation: f.lifecycleStatus === "uploading" ? "pulse-ring 1.5s ease-in-out infinite" : "none",
        }} />
        {/* Mini step pipeline overlay */}
        <div class="mini-pipeline">
          {pipelineSteps.flatMap((step, i) => {
            const els = [
              <div class={`mini-pipeline__step mini-pipeline__step--${step.stage}`} key={`s-${i}`}>
                <div class="mini-pipeline__circle">
                  {step.stage === "done" ? "✓" : ""}
                </div>
                <div class="mini-pipeline__label">{step.label}</div>
              </div>,
            ];
            if (i < pipelineSteps.length - 1) {
              const connDone = step.stage === "done";
              els.push(
                <div
                  class={`mini-pipeline__connector${connDone ? " mini-pipeline__connector--done" : ""}`}
                  key={`c-${i}`}
                />,
              );
            }
            return els;
          })}
        </div>
      </div>

      {/* File info below thumbnail */}
      <div style={{ padding: "0.5rem 0.625rem" }}>
        <div
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: "0.75rem",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            color: "var(--color-text)",
          }}
          title={f.name}
        >
          {f.name}
        </div>
        <div style={{
          fontSize: "0.6875rem",
          color: "var(--color-text-secondary)",
          marginTop: "0.125rem",
        }}>
          {f.lifecycleStatus === "uploading"
            ? `${formatBytes(f.loaded)} / ${formatBytes(f.size)}`
            : formatBytes(f.size)}
        </div>
        <div style={{
          fontSize: "0.625rem",
          fontWeight: 600,
          letterSpacing: "0.05em",
          color: dotColor,
          marginTop: "0.25rem",
        }}>
          {statusCardLabel(f.lifecycleStatus)}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Sidebar status row (Phase 3a)
// ---------------------------------------------------------------------------

function StatusRow({ label, count, color }: { label: string; count: number; color: string }) {
  if (count === 0) return null;
  return (
    <div style={{
      display: "flex",
      alignItems: "center",
      justifyContent: "space-between",
      padding: "0.375rem 0",
    }}>
      <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
        <span style={{
          width: "0.5rem",
          height: "0.5rem",
          borderRadius: "50%",
          background: color,
          flexShrink: 0,
        }} />
        <span style={{ fontSize: "0.875rem", color: "var(--color-text)" }}>{label}</span>
      </div>
      <span style={{
        fontSize: "0.875rem",
        fontWeight: 600,
        fontVariantNumeric: "tabular-nums",
      }}>
        {count}
      </span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main component (Phase 3a: 2-column layout redesign)
// ---------------------------------------------------------------------------

export function FileUploader() {
  const [dragActive, setDragActive] = useState(false);

  const allDone = files.value.length > 0 && files.value.every(
    (f) => f.status === "done" || f.status === "error",
  );
  const doneCount = files.value.filter((f) => f.status === "done").length;
  const anyUploading = files.value.some((f) => f.status === "uploading");
  const totalFiles = files.value.length;

  const overallProgress =
    totalFiles > 0
      ? Math.round(
          files.value.reduce((sum, f) => sum + f.progress, 0) / totalFiles,
        )
      : 0;

  // DDR-063: Compute grouped file lifecycle statuses
  const lifecycle = getFilesWithLifecycle();
  const uploading = lifecycle.filter((f) => f.lifecycleStatus === "uploading");
  const processing = lifecycle.filter((f) => f.lifecycleStatus === "processing");
  const ready = lifecycle.filter((f) => f.lifecycleStatus === "ready");
  const errored = lifecycle.filter((f) => f.lifecycleStatus === "error");

  const totalSize = files.value.reduce((sum, f) => sum + f.size, 0);
  const filesRemaining = totalFiles - ready.length;
  const hasFiles = files.value.length > 0;

  const etaSeconds: number | null = (() => {
    if (!anyUploading || uploadSpeed.value <= 0) return null;
    const remainingBytes = totalSize - engine.getTotalLoaded();
    if (remainingBytes <= 0) return null;
    return Math.ceil(remainingBytes / uploadSpeed.value);
  })();

  function onDragEnter(e: DragEvent) {
    e.preventDefault();
    setDragActive(true);
  }

  function onDragLeave(e: DragEvent) {
    e.preventDefault();
    const ct = e.currentTarget as HTMLElement;
    const rt = e.relatedTarget as Node | null;
    if (!rt || !ct.contains(rt)) {
      setDragActive(false);
    }
  }

  function onDropWrapped(e: DragEvent) {
    setDragActive(false);
    handleDrop(e);
  }

  return (
    <>
      <input
        id="file-input"
        type="file"
        multiple
        accept={ACCEPT}
        onChange={handleFileSelect}
        style={{ display: "none" }}
      />
      <style>{`@keyframes pulse-dot { 0%, 100% { opacity: 1; } 50% { opacity: 0.4; } }`}</style>

      {error.value && (
        <div style={{
          color: "var(--color-danger)",
          marginBottom: "1rem",
          fontSize: "0.875rem",
          padding: "0.75rem 1rem",
          background: "var(--color-primary-light)",
          borderRadius: "var(--radius)",
          borderLeft: "3px solid var(--color-danger)",
        }}>
          {error.value}
        </div>
      )}

      {!hasFiles ? (
        /* ── Empty state: drop zone + tips sidebar ── */
        <div class="layout-sidebar">
          <div
            class={`drop-zone${dragActive ? " drop-zone--active" : ""}`}
            onDrop={onDropWrapped}
            onDragOver={handleDragOver}
            onDragEnter={onDragEnter}
            onDragLeave={onDragLeave}
          >
            <span class="drop-zone__icon">{"\u2601\uFE0F"}</span>
            <span class="drop-zone__title">Drop your media files here</span>
            <span class="drop-zone__subtitle">
              Supports JPEG, PNG, MP4, MOV &bull; Max 500MB per file
            </span>
            <button
              class="primary"
              onClick={(e) => {
                e.stopPropagation();
                handleBrowseFiles();
              }}
              style={{ marginTop: "1rem" }}
            >
              Click to Browse
            </button>
            <p style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              marginTop: "1rem",
              marginBottom: 0,
            }}>
              Files are processed securely and not stored permanently
            </p>
          </div>

          <div class="sidebar-panel">
            <h3>Tips &amp; Guidance</h3>
            <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
              <div style={{ display: "flex", gap: "0.75rem", alignItems: "flex-start" }}>
                <span style={{ fontSize: "1.25rem", flexShrink: 0 }}>{"\uD83D\uDCF8"}</span>
                <div>
                  <div style={{ fontWeight: 600, marginBottom: "0.125rem" }}>Blurry Photos</div>
                  <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)" }}>
                    AI detects motion blur, focus issues, and camera shake
                  </div>
                </div>
              </div>
              <div style={{ display: "flex", gap: "0.75rem", alignItems: "flex-start" }}>
                <span style={{ fontSize: "1.25rem", flexShrink: 0 }}>{"\uD83C\uDF11"}</span>
                <div>
                  <div style={{ fontWeight: 600, marginBottom: "0.125rem" }}>Dark / Underexposed</div>
                  <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)" }}>
                    Identifies photos too dark to recover even with editing
                  </div>
                </div>
              </div>
              <div style={{ display: "flex", gap: "0.75rem", alignItems: "flex-start" }}>
                <span style={{ fontSize: "1.25rem", flexShrink: 0 }}>{"\uD83D\uDCF1"}</span>
                <div>
                  <div style={{ fontWeight: 600, marginBottom: "0.125rem" }}>Burst &amp; Duplicates</div>
                  <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)" }}>
                    Finds near-identical shots from burst mode or rapid shooting
                  </div>
                </div>
              </div>
              <div style={{ display: "flex", gap: "0.75rem", alignItems: "flex-start" }}>
                <span style={{ fontSize: "1.25rem", flexShrink: 0 }}>{"\uD83D\uDCA1"}</span>
                <div>
                  <div style={{ fontWeight: 600, marginBottom: "0.125rem" }}>Pro Tip</div>
                  <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)" }}>
                    Upload entire folders — the AI works best with full context to identify the best shots
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      ) : (
        /* ── In-flight state: file grid + pipeline sidebar ── */
        <div class="layout-sidebar">
          <div
            onDrop={onDropWrapped}
            onDragOver={handleDragOver}
            onDragEnter={onDragEnter}
            onDragLeave={onDragLeave}
          >
            {/* Header row */}
            <div style={{
              display: "flex",
              justifyContent: "space-between",
              alignItems: "center",
              marginBottom: "1rem",
            }}>
              <div style={{ display: "flex", alignItems: "center", gap: "0.625rem" }}>
                <h3 style={{ margin: 0, fontSize: "1.125rem" }}>In-Flight Assets</h3>
                <span
                  class="status-badge"
                  style={{ fontVariantNumeric: "tabular-nums" }}
                >
                  {totalFiles}
                </span>
              </div>
              <button
                class="outline"
                onClick={() => handleBrowseFiles()}
                style={{ fontSize: "0.875rem" }}
              >
                + Add More
              </button>
            </div>

            {/* Overall upload progress bar */}
            {anyUploading && (
              <div style={{ marginBottom: "1rem" }}>
                <div style={{
                  display: "flex",
                  justifyContent: "space-between",
                  fontSize: "0.75rem",
                  color: "var(--color-text-secondary)",
                  marginBottom: "0.375rem",
                }}>
                  <span>
                    Uploading {doneCount} of {totalFiles} files…
                  </span>
                  <span style={{ fontVariantNumeric: "tabular-nums" }}>
                    {uploadSpeed.value > 0 && (
                      <span style={{ marginRight: "0.5rem" }}>
                        {formatSpeed(uploadSpeed.value)}
                      </span>
                    )}
                    {overallProgress}%
                  </span>
                </div>
                <div style={{
                  height: "4px",
                  background: "var(--color-border)",
                  borderRadius: "2px",
                  overflow: "hidden",
                }}>
                  <div style={{
                    height: "100%",
                    width: `${overallProgress}%`,
                    background: "var(--color-primary)",
                    transition: "width 0.3s ease",
                    borderRadius: "2px",
                  }} />
                </div>
              </div>
            )}

            {/* Server-side processing progress (DDR-063) */}
            {!anyUploading && allDone && serverExpectedFileCount.value > 0 &&
              serverProcessedCount.value < serverExpectedFileCount.value && (
              <div style={{ marginBottom: "1rem" }}>
                <div style={{
                  display: "flex",
                  justifyContent: "space-between",
                  fontSize: "0.75rem",
                  color: "var(--color-text-secondary)",
                  marginBottom: "0.375rem",
                }}>
                  <span>
                    Processing {serverProcessedCount.value} of {serverExpectedFileCount.value} files…
                  </span>
                  <span style={{ fontVariantNumeric: "tabular-nums" }}>
                    {serverExpectedFileCount.value > 0
                      ? Math.round((serverProcessedCount.value / serverExpectedFileCount.value) * 100)
                      : 0}%
                  </span>
                </div>
                <div style={{
                  height: "4px",
                  background: "var(--color-border)",
                  borderRadius: "2px",
                  overflow: "hidden",
                }}>
                  <div style={{
                    height: "100%",
                    width: `${serverExpectedFileCount.value > 0
                      ? (serverProcessedCount.value / serverExpectedFileCount.value) * 100
                      : 0}%`,
                    background: "var(--color-success)",
                    transition: "width 0.3s ease",
                    borderRadius: "2px",
                  }} />
                </div>
              </div>
            )}

            {/* Thumbnail card grid */}
            <div style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fill, minmax(var(--grid-card-sm), 1fr))",
              gap: "0.75rem",
              marginBottom: "1rem",
            }}>
              {lifecycle.map((f) => (
                <FileCard key={f.name} f={f} />
              ))}
            </div>
          </div>

          {/* ── Right sidebar ── */}
          <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
            {/* Pipeline Summary */}
            <div class="sidebar-panel">
              <h3>Pipeline Summary</h3>
              <StatusRow label="Uploading" count={uploading.length} color="var(--color-primary)" />
              <StatusRow label="Processing" count={processing.length} color="var(--color-warning)" />
              <StatusRow label="Ready" count={ready.length} color="var(--color-success)" />
              <StatusRow label="Error" count={errored.length} color="var(--color-danger)" />
              {uploading.length === 0 && processing.length === 0 && ready.length === 0 && errored.length === 0 && (
                <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)", padding: "0.25rem 0" }}>
                  Waiting for status…
                </div>
              )}
            </div>

            {/* Batch Statistics */}
            <div class="sidebar-panel">
              <h3>Batch Statistics</h3>
              <div style={{ display: "flex", flexDirection: "column", gap: "0.375rem", fontSize: "0.875rem" }}>
                <div style={{ display: "flex", justifyContent: "space-between" }}>
                  <span style={{ color: "var(--color-text-secondary)" }}>Total files</span>
                  <span style={{ fontWeight: 600, fontVariantNumeric: "tabular-nums" }}>{totalFiles}</span>
                </div>
                <div style={{ display: "flex", justifyContent: "space-between" }}>
                  <span style={{ color: "var(--color-text-secondary)" }}>Total size</span>
                  <span style={{ fontWeight: 600 }}>{formatBytes(totalSize)}</span>
                </div>
                <div style={{ display: "flex", justifyContent: "space-between" }}>
                  <span style={{ color: "var(--color-text-secondary)" }}>Files remaining</span>
                  <span style={{ fontWeight: 600, fontVariantNumeric: "tabular-nums" }}>{filesRemaining}</span>
                </div>
                {anyUploading && uploadSpeed.value > 0 && (
                  <div style={{ display: "flex", justifyContent: "space-between" }}>
                    <span style={{ color: "var(--color-text-secondary)" }}>Upload speed</span>
                    <span style={{ fontWeight: 600 }}>{formatSpeed(uploadSpeed.value)}</span>
                  </div>
                )}
                {anyUploading && (
                  <div style={{ display: "flex", justifyContent: "space-between" }}>
                    <span style={{ color: "var(--color-text-secondary)" }}>ETA</span>
                    <span style={{
                      fontWeight: 600,
                      fontVariantNumeric: "tabular-nums",
                      color: etaSeconds != null ? "var(--color-text)" : "var(--color-text-secondary)",
                    }}>
                      {etaSeconds != null ? formatElapsed(etaSeconds) : "—"}
                    </span>
                  </div>
                )}
              </div>
            </div>

            {/* Triage status */}
            {triageInitialized.value && (
              <div style={{
                textAlign: "center",
                fontSize: "0.875rem",
                color: "var(--color-text-secondary)",
                padding: "0.375rem 0",
              }}>
                Triage will start automatically
              </div>
            )}
            {!triageInitialized.value && allDone && doneCount > 0 && (
              <button class="primary" onClick={proceedToTriage} style={{ width: "100%" }}>
                Continue to Triage
              </button>
            )}

            {/* Cancel / Clear */}
            <button
              class="outline"
              onClick={clearAll}
              disabled={anyUploading}
              style={{
                width: "100%",
                color: "var(--color-danger)",
                borderColor: "var(--color-danger)",
              }}
            >
              Cancel All Uploads
            </button>
          </div>
        </div>
      )}
    </>
  );
}
