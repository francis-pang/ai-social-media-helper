import { signal } from "@preact/signals";
import { useState } from "preact/hooks";
import { getUploadUrl, uploadToS3, uploadToS3Multipart, MULTIPART_THRESHOLD, initTriage, updateTriageFiles, getTriageResults, startTriage } from "../api/client";
import { selectedPaths, uploadSessionId, triageJobId, navigateToStep, currentStep } from "../app";
import { syncUrlToStep } from "../router";
import { getFilesFromDataTransfer } from "../utils/fileSystem";
import { formatBytes, formatSpeed } from "../utils/format";
import { badgeClass, badgeLabel } from "../utils/statusBadge";
import type { FileProcessingStatus } from "../types/api";

/** Media file MIME types accepted by the uploader. */
const ACCEPT =
  "image/jpeg,image/png,image/gif,image/webp,image/heic,image/heif," +
  "video/mp4,video/quicktime,video/x-msvideo,video/webm,video/x-matroska";

interface UploadedFile {
  name: string;
  size: number;
  key: string;
  status: "pending" | "uploading" | "done" | "error";
  progress: number; // 0-100
  loaded: number; // bytes uploaded so far
  error?: string;
}

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

const files = signal<UploadedFile[]>([]);
const error = signal<string | null>(null);
const triageInitialized = signal<boolean>(false);
const triagePolling = signal<boolean>(false);

/** Per-file server-side processing statuses from poll results (DDR-063). */
const serverFileStatuses = signal<FileProcessingStatus[]>([]);
const serverProcessedCount = signal<number>(0);
const serverExpectedFileCount = signal<number>(0);

/** Current aggregate upload speed in bytes per second. */
const uploadSpeed = signal<number>(0);
let speedTimer: ReturnType<typeof setInterval> | null = null;
let prevSpeedBytes = 0;
let prevSpeedTime = 0;

function getTotalLoaded(): number {
  return files.value.reduce((sum, f) => sum + f.loaded, 0);
}

function startSpeedTracking() {
  if (speedTimer) return;
  prevSpeedBytes = getTotalLoaded();
  prevSpeedTime = performance.now();
  speedTimer = setInterval(() => {
    const now = performance.now();
    const currentLoaded = getTotalLoaded();
    const elapsedSec = (now - prevSpeedTime) / 1000;
    if (elapsedSec > 0) {
      uploadSpeed.value = (currentLoaded - prevSpeedBytes) / elapsedSec;
    }
    prevSpeedBytes = currentLoaded;
    prevSpeedTime = now;
    if (!files.value.some((f) => f.status === "uploading")) {
      stopSpeedTracking();
    }
  }, 1000);
}

function stopSpeedTracking() {
  if (speedTimer) {
    clearInterval(speedTimer);
    speedTimer = null;
  }
  uploadSpeed.value = 0;
}

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

function addFiles(newFiles: File[]) {
  if (!uploadSessionId.value) {
    uploadSessionId.value = generateSessionId();
    syncUrlToStep(currentStep.value, uploadSessionId.value);
  }

  const sessionId = uploadSessionId.value;
  const existing = new Set(files.value.map((f) => f.name));

  const toAdd: UploadedFile[] = [];
  const fileMap = new Map<string, File>();

  for (const file of newFiles) {
    if (existing.has(file.name)) continue;
    existing.add(file.name);
    toAdd.push({
      name: file.name,
      size: file.size,
      key: `${sessionId}/${file.name}`,
      status: "pending",
      progress: 0,
      loaded: 0,
    });
    fileMap.set(file.name, file);
  }

  if (toAdd.length === 0) return;

  files.value = [...files.value, ...toAdd];

  // DDR-061: Initialize triage on first file drop
  if (!triageInitialized.value) {
    triageInitialized.value = true;
    initTriageSession(sessionId, files.value.length);
  } else {
    const jobId = triageJobId.value;
    if (jobId) {
      updateTriageFiles({ sessionId, jobId, expectedFileCount: files.value.length }).catch(
        (e) => console.error("Failed to update file count:", e)
      );
    }
  }

  for (const entry of toAdd) {
    uploadFile(sessionId, entry.name, fileMap.get(entry.name)!);
  }
}

async function uploadFile(sessionId: string, filename: string, file: File) {
  updateFile(filename, { status: "uploading", progress: 0, loaded: 0 });
  startSpeedTracking();

  try {
    let key: string;

    if (file.size > MULTIPART_THRESHOLD) {
      key = await uploadToS3Multipart(sessionId, file, (loaded, total) => {
        updateFile(filename, { progress: Math.round((loaded / total) * 100), loaded });
      });
    } else {
      const res = await getUploadUrl(sessionId, filename, file.type);
      key = res.key;

      await uploadToS3(res.uploadUrl, file, (loaded, total) => {
        updateFile(filename, { progress: Math.round((loaded / total) * 100), loaded });
      });
    }

    updateFile(filename, { status: "done", progress: 100, loaded: file.size, key });
  } catch (e) {
    updateFile(filename, {
      status: "error",
      error: e instanceof Error ? e.message : "Upload failed",
    });
  }
}

function updateFile(filename: string, updates: Partial<UploadedFile>) {
  files.value = files.value.map((f) =>
    f.name === filename ? { ...f, ...updates } : f,
  );
}

function removeFile(filename: string) {
  files.value = files.value.filter((f) => f.name !== filename);
}

async function initTriageSession(sessionId: string, fileCount: number) {
  try {
    const res = await initTriage({ sessionId, expectedFileCount: fileCount });
    triageJobId.value = res.id;
    triagePolling.value = true;
    pollTriageResults(res.id, sessionId);
  } catch (e) {
    error.value = e instanceof Error ? e.message : "Failed to initialize triage";
  }
}

async function pollTriageResults(jobId: string, sessionId: string) {
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

      // DDR-063: Stay on upload screen until all per-file processing completes,
      // then transition to Gemini analysis screen.
      const allUploaded = files.value.length > 0 && files.value.every(
        f => f.status === "done" || f.status === "error"
      );
      const allProcessed = results.expectedFileCount != null &&
        results.expectedFileCount > 0 &&
        results.processedCount != null &&
        results.processedCount >= results.expectedFileCount;

      if (allUploaded && allProcessed && results.status !== "pending") {
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
  const serverMap = new Map<string, FileProcessingStatus>();
  for (const fs of serverFileStatuses.value) {
    serverMap.set(fs.filename, fs);
  }

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
  files.value = [];
  uploadSessionId.value = null;
  serverFileStatuses.value = [];
  serverProcessedCount.value = 0;
  serverExpectedFileCount.value = 0;
  stopSpeedTracking();
}

/** Reset FileUploader state (called from navigateToLanding — DDR-042). */
export function resetFileUploaderState() {
  files.value = [];
  error.value = null;
  triageInitialized.value = false;
  triagePolling.value = false;
  serverFileStatuses.value = [];
  serverProcessedCount.value = 0;
  serverExpectedFileCount.value = 0;
  stopSpeedTracking();
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
    const res = await startTriage({ sessionId });
    triageJobId.value = res.id;
    navigateToStep("processing");
  } catch (e) {
    error.value = e instanceof Error ? e.message : "Failed to start triage";
  }
}

// ---------------------------------------------------------------------------
// Status group section component (DDR-063)
// ---------------------------------------------------------------------------

interface StatusGroupProps {
  label: string;
  count: number;
  color: string;
  defaultCollapsed?: boolean;
  children: preact.ComponentChildren;
}

function StatusGroup({ label, count, color, defaultCollapsed, children }: StatusGroupProps) {
  const [collapsed, setCollapsed] = useState(defaultCollapsed ?? false);

  if (count === 0) return null;

  return (
    <div style={{ marginBottom: "0.75rem" }}>
      <button
        class="outline"
        onClick={() => setCollapsed(!collapsed)}
        style={{
          display: "flex",
          alignItems: "center",
          gap: "0.5rem",
          width: "100%",
          padding: "0.375rem 0.75rem",
          fontSize: "0.875rem",
          fontWeight: 600,
          color,
          border: "none",
          background: "transparent",
          cursor: "pointer",
          minHeight: "unset",
        }}
      >
        <span style={{ fontSize: "0.75rem", transition: "transform 0.15s", transform: collapsed ? "rotate(-90deg)" : "rotate(0)" }}>
          ▼
        </span>
        {label} ({count})
      </button>
      {!collapsed && <div style={{ marginTop: "0.25rem" }}>{children}</div>}
    </div>
  );
}

// ---------------------------------------------------------------------------
// File row for grouped view (DDR-063)
// ---------------------------------------------------------------------------

function FileRow({ f, onRemove }: { f: FileWithLifecycle; onRemove: () => void }) {
  return (
    <div class="file-row" key={f.name}>
      {/* Thumbnail or file icon */}
      {f.thumbnailUrl ? (
        <img
          src={f.thumbnailUrl}
          alt=""
          loading="lazy"
          style={{
            width: "2rem",
            height: "2rem",
            objectFit: "cover",
            borderRadius: "4px",
            flexShrink: 0,
          }}
          onError={(e) => { (e.target as HTMLImageElement).style.display = "none"; }}
        />
      ) : (
        <span
          style={{
            fontSize: "1.125rem",
            flexShrink: 0,
            width: "2rem",
            textAlign: "center",
            opacity: 0.6,
          }}
        >
          {"\u{1F4C4}"}
        </span>
      )}

      {/* Filename */}
      <span
        style={{
          flex: 1,
          fontSize: "0.875rem",
          fontFamily: "var(--font-mono)",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
        title={f.name}
      >
        {f.name}
      </span>

      {/* Size / upload progress */}
      <span
        style={{
          fontSize: "0.75rem",
          color: "var(--color-text-secondary)",
          flexShrink: 0,
          fontVariantNumeric: "tabular-nums",
        }}
      >
        {f.lifecycleStatus === "uploading"
          ? `${formatBytes(f.loaded)} / ${formatBytes(f.size)}`
          : formatBytes(f.size)}
      </span>

      {/* Status badge */}
      <span class={lifecycleBadgeClass(f.lifecycleStatus)}
        style={f.lifecycleStatus === "uploading" ? {
          background: `linear-gradient(to right, rgba(108, 140, 255, 0.3) ${f.uploadProgress}%, rgba(108, 140, 255, 0.1) ${f.uploadProgress}%)`,
        } : undefined}
        title={f.error || undefined}
      >
        {lifecycleBadgeLabel(f)}
      </span>

      {/* Converted indicator */}
      {f.converted && (
        <span class="status-badge status-badge--processing" style={{ fontSize: "0.75rem" }}>
          converted
        </span>
      )}

      {/* Remove button */}
      <button
        class="btn-remove"
        onClick={onRemove}
        disabled={f.lifecycleStatus === "uploading"}
        title="Remove file"
      >
        ✕
      </button>

      {/* Per-file progress bar */}
      {f.lifecycleStatus === "uploading" && f.uploadProgress > 0 && (
        <div
          class="file-progress-bar"
          style={{ width: `${f.uploadProgress}%` }}
        />
      )}
    </div>
  );
}

function lifecycleBadgeClass(status: FileLifecycleStatus): string {
  switch (status) {
    case "uploading": return "status-badge status-badge--uploading";
    case "processing": return "status-badge status-badge--processing";
    case "ready": return "status-badge status-badge--done";
    case "error": return "status-badge status-badge--error";
  }
}

function lifecycleBadgeLabel(f: FileWithLifecycle): string {
  switch (f.lifecycleStatus) {
    case "uploading": return `Uploading ${f.uploadProgress}%`;
    case "processing": return "Processing";
    case "ready": return "Ready";
    case "error": return "Failed";
  }
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export function FileUploader() {
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

  const hasServerStatuses = serverFileStatuses.value.length > 0;

  return (
    <div class="card">
      <p
        style={{
          color: "var(--color-text-secondary)",
          fontSize: "0.875rem",
          marginBottom: "1.5rem",
        }}
      >
        Upload media files for AI triage analysis. Drag and drop or use the
        button below.
      </p>

      {/* Drop zone (DDR-058: compact secondary when files exist) */}
      <div
        onDrop={handleDrop}
        onDragOver={handleDragOver}
        style={{
          border: "2px dashed var(--color-border)",
          borderRadius: "var(--radius)",
          padding: files.value.length > 0 ? "1rem" : "2rem",
          textAlign: "center",
          marginBottom: "1.5rem",
          cursor: "pointer",
          transition: "border-color 0.2s, padding 0.2s",
        }}
        onClick={() =>
          (document.getElementById("file-input") as HTMLInputElement)?.click()
        }
      >
        <input
          id="file-input"
          type="file"
          multiple
          accept={ACCEPT}
          onChange={handleFileSelect}
          style={{ display: "none" }}
        />
        <div
          style={{
            fontSize: files.value.length > 0 ? "0.875rem" : "1.25rem",
            marginBottom: files.value.length > 0 ? "0" : "0.5rem",
            color: "var(--color-text-secondary)",
          }}
        >
          {files.value.length > 0 ? "Drop more files here" : "Drop files here"}
        </div>
        {files.value.length === 0 && (
          <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)" }}>
            or click to browse — JPEG, PNG, GIF, WebP, HEIC, MP4, MOV
          </div>
        )}
      </div>

      {/* Summary counts (DDR-063) */}
      {files.value.length > 0 && hasServerStatuses && (
        <div
          style={{
            display: "flex",
            gap: "1rem",
            flexWrap: "wrap",
            marginBottom: "1rem",
            fontSize: "0.875rem",
          }}
        >
          {uploading.length > 0 && (
            <span style={{ color: "var(--color-primary)" }}>
              {uploading.length} uploading
            </span>
          )}
          {processing.length > 0 && (
            <span style={{ color: "var(--color-primary)" }}>
              {processing.length} processing
            </span>
          )}
          {ready.length > 0 && (
            <span style={{ color: "var(--color-success)" }}>
              {ready.length} ready
            </span>
          )}
          {errored.length > 0 && (
            <span style={{ color: "var(--color-danger)" }}>
              {errored.length} error
            </span>
          )}
        </div>
      )}

      {/* DDR-063: Grouped file list when server statuses are available */}
      {files.value.length > 0 && hasServerStatuses && (
        <div style={{ marginBottom: "1rem" }}>
          <StatusGroup label="Uploading" count={uploading.length} color="var(--color-primary)">
            {uploading.map((f) => (
              <FileRow key={f.name} f={f} onRemove={() => removeFile(f.name)} />
            ))}
          </StatusGroup>

          <StatusGroup label="Processing" count={processing.length} color="var(--color-primary)">
            {processing.map((f) => (
              <FileRow key={f.name} f={f} onRemove={() => removeFile(f.name)} />
            ))}
          </StatusGroup>

          <StatusGroup label="Ready" count={ready.length} color="var(--color-success)" defaultCollapsed={ready.length > 10}>
            {ready.map((f) => (
              <FileRow key={f.name} f={f} onRemove={() => removeFile(f.name)} />
            ))}
          </StatusGroup>

          <StatusGroup label="Error" count={errored.length} color="var(--color-danger)">
            {errored.map((f) => (
              <FileRow key={f.name} f={f} onRemove={() => removeFile(f.name)} />
            ))}
          </StatusGroup>
        </div>
      )}

      {/* Flat file list (DDR-058: before server statuses are available) */}
      {files.value.length > 0 && !hasServerStatuses && (
        <div class="file-list">
          {files.value.map((f) => (
            <div class="file-row" key={f.name}>
              <span
                style={{
                  fontSize: "1.125rem",
                  flexShrink: 0,
                  width: "1.5rem",
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
                title={f.name}
              >
                {f.name}
              </span>

              <span
                style={{
                  fontSize: "0.75rem",
                  color: "var(--color-text-secondary)",
                  flexShrink: 0,
                  fontVariantNumeric: "tabular-nums",
                }}
              >
                {f.status === "uploading"
                  ? `${formatBytes(f.loaded)} / ${formatBytes(f.size)}`
                  : formatBytes(f.size)}
              </span>

              <span
                class={badgeClass(f.status)}
                style={f.status === "uploading" ? {
                  background: `linear-gradient(to right, rgba(108, 140, 255, 0.3) ${f.progress}%, rgba(108, 140, 255, 0.1) ${f.progress}%)`,
                } : undefined}
                title={f.status === "error" ? (f.error || "Upload failed") : undefined}
              >
                {badgeLabel(f.status, f.progress)}
              </span>

              <button
                class="btn-remove"
                onClick={() => removeFile(f.name)}
                disabled={f.status === "uploading"}
                title="Remove file"
              >
                ✕
              </button>

              {f.status === "uploading" && (
                <div
                  class="file-progress-bar"
                  style={{ width: `${f.progress}%` }}
                />
              )}
            </div>
          ))}
        </div>
      )}

      {/* Overall upload progress bar */}
      {anyUploading && (
        <div style={{ marginBottom: "1rem" }}>
          <div
            style={{
              display: "flex",
              justifyContent: "space-between",
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              marginBottom: "0.375rem",
            }}
          >
            <span>
              Uploading {doneCount} of {totalFiles} files...
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
          <div
            style={{
              height: "4px",
              background: "var(--color-border)",
              borderRadius: "2px",
              overflow: "hidden",
            }}
          >
            <div
              style={{
                height: "100%",
                width: `${overallProgress}%`,
                background: "var(--color-primary)",
                transition: "width 0.3s ease",
                borderRadius: "2px",
              }}
            />
          </div>
        </div>
      )}

      {/* Server-side processing progress (DDR-063) */}
      {!anyUploading && allDone && serverExpectedFileCount.value > 0 &&
        serverProcessedCount.value < serverExpectedFileCount.value && (
        <div style={{ marginBottom: "1rem" }}>
          <div
            style={{
              display: "flex",
              justifyContent: "space-between",
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              marginBottom: "0.375rem",
            }}
          >
            <span>
              Processing {serverProcessedCount.value} of {serverExpectedFileCount.value} files...
            </span>
            <span style={{ fontVariantNumeric: "tabular-nums" }}>
              {serverExpectedFileCount.value > 0
                ? Math.round((serverProcessedCount.value / serverExpectedFileCount.value) * 100)
                : 0}%
            </span>
          </div>
          <div
            style={{
              height: "4px",
              background: "var(--color-border)",
              borderRadius: "2px",
              overflow: "hidden",
            }}
          >
            <div
              style={{
                height: "100%",
                width: `${serverExpectedFileCount.value > 0
                  ? (serverProcessedCount.value / serverExpectedFileCount.value) * 100
                  : 0}%`,
                background: "var(--color-success)",
                transition: "width 0.3s ease",
                borderRadius: "2px",
              }}
            />
          </div>
        </div>
      )}

      {error.value && (
        <div
          style={{
            color: "var(--color-danger)",
            marginBottom: "1rem",
            fontSize: "0.875rem",
          }}
        >
          {error.value}
        </div>
      )}

      {/* Empty state */}
      {files.value.length === 0 && (
        <p
          style={{
            color: "var(--color-text-secondary)",
            padding: "2rem 1rem",
            textAlign: "center",
            fontSize: "0.875rem",
          }}
        >
          No files selected yet. Drag and drop media files or click the drop zone above.
        </p>
      )}

      {/* Actions (DDR-058: add-more at left, primary actions at right) */}
      {files.value.length > 0 && (
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            flexWrap: "wrap",
            gap: "0.75rem",
          }}
        >
          <button
            class="btn-add-more"
            onClick={() =>
              (document.getElementById("file-input") as HTMLInputElement)?.click()
            }
          >
            + Add more Files
          </button>
          <div style={{ display: "flex", alignItems: "center", gap: "0.75rem" }}>
            <span
              style={{
                fontSize: "0.875rem",
                color: "var(--color-text-secondary)",
              }}
            >
              {doneCount}/{totalFiles} uploaded
            </span>
            <button
              class="outline"
              onClick={clearAll}
              disabled={anyUploading}
            >
              Clear all
            </button>
            {triageInitialized.value ? (
              <span style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)" }}>
                Triage will start automatically
              </span>
            ) : (
              <button
                class="primary"
                onClick={proceedToTriage}
                disabled={!allDone || doneCount === 0}
              >
                Continue to Triage
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
