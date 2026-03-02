import { signal } from "@preact/signals";
import { useState } from "preact/hooks";
import { createUploadEngine } from "../upload/uploadEngine";
import { fbPrepMediaKeys } from "./FBPrepView";
import { uploadSessionId, navigateToStep } from "../app";
import { syncUrlToStep } from "../router";
import { getFilesFromDataTransfer } from "../utils/fileSystem";
import { formatBytes, formatSpeed } from "../utils/format";
import { formatElapsed } from "../hooks/useElapsedTimer";
import { generateThumbnail } from "./media-uploader/thumbnailGenerator";
import { getSessionFileStatuses } from "../api/client";
import { MiniPipeline, type MiniPipelineStep } from "./shared/MiniPipeline";
import type { FileProcessingStatus } from "../types/api";

const ACCEPT =
  "image/jpeg,image/png,image/gif,image/webp,image/heic,image/heif," +
  "video/mp4,video/quicktime,video/x-msvideo,video/webm,video/x-matroska";

const engine = createUploadEngine({ enableDedup: true, enableSpeedTracking: true });
const files = engine.files;
const error = engine.error;
const uploadSpeed = engine.uploadSpeed;

type FileLifecycleStatus = "uploading" | "processing" | "thumbnailed" | "ready" | "error";

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

const serverFileStatuses = signal<FileProcessingStatus[]>([]);
const pollingActive = signal<boolean>(false);
const statusFilter = signal<Set<FileLifecycleStatus>>(new Set());

function generateSessionId(): string {
  return crypto.randomUUID();
}

// ---------------------------------------------------------------------------
// Server-side file status polling
// ---------------------------------------------------------------------------

let pollTimer: ReturnType<typeof setTimeout> | null = null;

function startPolling(sessionId: string) {
  if (pollingActive.value) return;
  pollingActive.value = true;
  pollLoop(sessionId);
}

function stopPolling() {
  pollingActive.value = false;
  if (pollTimer) {
    clearTimeout(pollTimer);
    pollTimer = null;
  }
}

async function pollLoop(sessionId: string) {
  if (!pollingActive.value) return;
  try {
    const res = await getSessionFileStatuses(sessionId);
    if (res.fileStatuses) {
      serverFileStatuses.value = res.fileStatuses;
    }
  } catch {
    // non-fatal
  }
  if (pollingActive.value) {
    pollTimer = setTimeout(() => pollLoop(sessionId), 2000);
  }
}

// ---------------------------------------------------------------------------
// Lifecycle merging
// ---------------------------------------------------------------------------

function getFilesWithLifecycle(): FileWithLifecycle[] {
  const serverMap = new Map(
    serverFileStatuses.value.map((fs) => [fs.filename, fs] as const),
  );

  return files.value.map((f) => {
    const serverStatus = serverMap.get(f.name);

    if (f.status === "error") {
      return {
        name: f.name, size: f.size, key: f.key,
        lifecycleStatus: "error" as const,
        uploadProgress: f.progress, loaded: f.loaded, error: f.error,
      };
    }

    if (f.status === "uploading" || f.status === "pending") {
      return {
        name: f.name, size: f.size, key: f.key,
        lifecycleStatus: "uploading" as const,
        uploadProgress: f.progress, loaded: f.loaded,
      };
    }

    if (serverStatus) {
      if (serverStatus.status === "error" || serverStatus.status === "invalid") {
        return {
          name: f.name, size: f.size, key: f.key,
          lifecycleStatus: "error" as const,
          uploadProgress: 100, loaded: f.loaded,
          error: serverStatus.error || (serverStatus.status === "invalid" ? "Invalid file" : undefined),
        };
      }
      if (serverStatus.status === "valid") {
        return {
          name: f.name, size: f.size, key: f.key,
          lifecycleStatus: "ready" as const,
          uploadProgress: 100, loaded: f.loaded,
          thumbnailUrl: serverStatus.thumbnailUrl,
          converted: serverStatus.converted,
        };
      }
      if (serverStatus.status === "thumbnailed") {
        return {
          name: f.name, size: f.size, key: f.key,
          lifecycleStatus: "thumbnailed" as const,
          uploadProgress: 100, loaded: f.loaded,
          thumbnailUrl: serverStatus.thumbnailUrl,
        };
      }
    }

    return {
      name: f.name, size: f.size, key: f.key,
      lifecycleStatus: "processing" as const,
      uploadProgress: 100, loaded: f.loaded,
    };
  });
}

// ---------------------------------------------------------------------------
// Pipeline step generation
// ---------------------------------------------------------------------------

function getMiniPipelineSteps(status: FileLifecycleStatus): MiniPipelineStep[] {
  switch (status) {
    case "uploading":
      return [
        { label: "Uploading", stage: "active" },
        { label: "Thumbnail", stage: "pending" },
        { label: "Downsizing", stage: "pending" },
      ];
    case "processing":
      return [
        { label: "Uploading", stage: "done" },
        { label: "Thumbnail", stage: "active" },
        { label: "Downsizing", stage: "pending" },
      ];
    case "thumbnailed":
      return [
        { label: "Uploading", stage: "done" },
        { label: "Thumbnail", stage: "done" },
        { label: "Downsizing", stage: "active" },
      ];
    case "ready":
      return [
        { label: "Uploading", stage: "done" },
        { label: "Thumbnail", stage: "done" },
        { label: "Downsizing", stage: "done" },
      ];
    case "error":
      return [
        { label: "Uploading", stage: "pending" },
        { label: "Thumbnail", stage: "pending" },
        { label: "Downsizing", stage: "pending" },
      ];
  }
}

function statusDotColor(status: FileLifecycleStatus): string {
  switch (status) {
    case "uploading": return "var(--color-primary)";
    case "processing": return "var(--color-warning)";
    case "thumbnailed": return "var(--color-info, #0891b2)";
    case "ready": return "var(--color-success)";
    case "error": return "var(--color-danger)";
  }
}

function statusCardLabel(status: FileLifecycleStatus): string {
  switch (status) {
    case "uploading": return "UPLOADING\u2026";
    case "processing": return "THUMBNAIL\u2026";
    case "thumbnailed": return "DOWNSIZING\u2026";
    case "ready": return "READY";
    case "error": return "ERROR";
  }
}

// ---------------------------------------------------------------------------
// File selection handlers
// ---------------------------------------------------------------------------

async function handleBrowseFiles() {
  if (typeof window !== "undefined" && "showOpenFilePicker" in window) {
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
      const filesToAdd: File[] = [];
      for (const handle of handles) {
        filesToAdd.push(await handle.getFile());
      }
      if (filesToAdd.length > 0) addFiles(filesToAdd);
    } catch (e) {
      if (e instanceof DOMException && e.name === "AbortError") return;
      (document.getElementById("fb-prep-file-input") as HTMLInputElement)?.click();
    }
  } else {
    (document.getElementById("fb-prep-file-input") as HTMLInputElement)?.click();
  }
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
  if (allFiles.length > 0) addFiles(allFiles);
}

function handleDragOver(e: DragEvent) {
  e.preventDefault();
  e.stopPropagation();
}

// ---------------------------------------------------------------------------
// Upload orchestration
// ---------------------------------------------------------------------------

async function addFiles(newFiles: File[]) {
  if (!uploadSessionId.value) {
    uploadSessionId.value = generateSessionId();
    syncUrlToStep("fb-prep-upload", uploadSessionId.value);
  }

  const sessionId = uploadSessionId.value;
  await engine.addFiles(sessionId, newFiles);

  for (const file of newFiles) {
    generateThumbnail(file).then((dataUrl) => {
      if (dataUrl) engine.updateFile(file.name, { thumbnailDataUrl: dataUrl });
    });
  }

  startPolling(sessionId);
}

function clearAll() {
  stopPolling();
  engine.clearAll();
  serverFileStatuses.value = [];
  statusFilter.value = new Set();
  uploadSessionId.value = null;
}

export function resetFBPrepUploaderState() {
  stopPolling();
  engine.resetState();
  serverFileStatuses.value = [];
  statusFilter.value = new Set();
  uploadSessionId.value = null;
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

function proceedToFBPrep() {
  const lifecycle = getFilesWithLifecycle();
  const doneFiles = lifecycle.filter((f) => f.lifecycleStatus === "ready" || f.lifecycleStatus === "thumbnailed");
  if (doneFiles.length === 0) {
    const uploadedFiles = files.value.filter((f) => f.status === "done");
    if (uploadedFiles.length === 0) return;
    fbPrepMediaKeys.value = uploadedFiles.map((f) => f.key);
  } else {
    fbPrepMediaKeys.value = doneFiles.map((f) => f.key);
  }
  stopPolling();
  navigateToStep("fb-prep");
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function StatusRow({ label, count, color, status }: { label: string; count: number; color: string; status: FileLifecycleStatus }) {
  if (count === 0) return null;
  const isActive = statusFilter.value.has(status);
  return (
    <div
      onClick={() => {
        const next = new Set(statusFilter.value);
        if (next.has(status)) next.delete(status);
        else next.add(status);
        statusFilter.value = next;
      }}
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        padding: "0.375rem 0.5rem",
        margin: "0 -0.5rem",
        borderRadius: "var(--radius)",
        cursor: "pointer",
        background: isActive ? "var(--color-primary-light, rgba(99,102,241,0.08))" : "transparent",
        transition: "background 0.15s",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
        <span style={{
          width: "0.5rem",
          height: "0.5rem",
          borderRadius: "50%",
          background: color,
          flexShrink: 0,
        }} />
        <span style={{ fontSize: "0.875rem", color: "var(--color-text)", fontWeight: isActive ? 600 : 400 }}>{label}</span>
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

function FileCard({ f }: { f: FileWithLifecycle }) {
  const dotColor = statusDotColor(f.lifecycleStatus);
  const pipelineSteps = getMiniPipelineSteps(f.lifecycleStatus);

  return (
    <div style={{
      background: "var(--color-surface)",
      border: "1px solid var(--color-border)",
      borderRadius: "var(--radius)",
      overflow: "hidden",
    }}>
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
        <MiniPipeline steps={pipelineSteps} />
      </div>

      <div class="file-card__info" style={{ padding: "0.5rem 0.625rem" }}>
        {f.lifecycleStatus === "uploading" && (
          <div
            class="file-card__gauge"
            style={{ transform: `scaleX(${f.uploadProgress / 100})` }}
          />
        )}
        <div
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: "0.75rem",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            color: "var(--color-text)",
            position: "relative",
          }}
          title={f.name}
        >
          {f.name}
        </div>
        <div style={{
          fontSize: "0.6875rem",
          color: "var(--color-text-secondary)",
          marginTop: "0.125rem",
          position: "relative",
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
          position: "relative",
        }}>
          {statusCardLabel(f.lifecycleStatus)}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export function FBPrepUploader() {
  const [dragActive, setDragActive] = useState(false);

  const lifecycle = getFilesWithLifecycle();
  const uploading = lifecycle.filter((f) => f.lifecycleStatus === "uploading");
  const processing = lifecycle.filter((f) => f.lifecycleStatus === "processing");
  const thumbnailed = lifecycle.filter((f) => f.lifecycleStatus === "thumbnailed");
  const ready = lifecycle.filter((f) => f.lifecycleStatus === "ready");
  const errored = lifecycle.filter((f) => f.lifecycleStatus === "error");

  const activeFilters = statusFilter.value;
  const filteredLifecycle = activeFilters.size === 0
    ? lifecycle
    : lifecycle.filter((f) => activeFilters.has(f.lifecycleStatus));

  const totalFiles = lifecycle.length;
  const allDone = totalFiles > 0 && lifecycle.every(
    (f) => f.lifecycleStatus === "ready" || f.lifecycleStatus === "error",
  );
  const doneCount = ready.length;
  const anyUploading = uploading.length > 0;

  const overallProgress =
    totalFiles > 0
      ? Math.round(
          files.value.reduce((sum, f) => sum + f.progress, 0) / totalFiles,
        )
      : 0;

  const totalSize = files.value.reduce((sum, f) => sum + f.size, 0);
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
    if (!rt || !ct.contains(rt)) setDragActive(false);
  }

  function onDropWrapped(e: DragEvent) {
    setDragActive(false);
    handleDrop(e);
  }

  return (
    <>
      <input
        id="fb-prep-file-input"
        type="file"
        multiple
        accept={ACCEPT}
        onChange={handleFileSelect}
        style={{ display: "none" }}
      />
      <style>{`@keyframes pulse-ring { 0%, 100% { opacity: 1; } 50% { opacity: 0.4; } }`}</style>

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
        <div class="layout-sidebar">
          <div
            class={`drop-zone${dragActive ? " drop-zone--active" : ""}`}
            onDrop={onDropWrapped}
            onDragOver={handleDragOver}
            onDragEnter={onDragEnter}
            onDragLeave={onDragLeave}
          >
            <span class="drop-zone__icon">{"\uD83C\uDF10"}</span>
            <span class="drop-zone__title">Drop your photos & videos here</span>
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
            <h3>What Facebook Prep Does</h3>
            <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
              <div style={{ display: "flex", gap: "0.75rem", alignItems: "flex-start" }}>
                <span style={{ fontSize: "1.25rem", flexShrink: 0 }}>{"\u270D\uFE0F"}</span>
                <div>
                  <div style={{ fontWeight: 600, marginBottom: "0.125rem" }}>Unique Captions</div>
                  <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)" }}>
                    AI generates captions for each photo and video as part of a coherent narrative
                  </div>
                </div>
              </div>
              <div style={{ display: "flex", gap: "0.75rem", alignItems: "flex-start" }}>
                <span style={{ fontSize: "1.25rem", flexShrink: 0 }}>{"\uD83D\uDCCD"}</span>
                <div>
                  <div style={{ fontWeight: 600, marginBottom: "0.125rem" }}>Location Tags</div>
                  <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)" }}>
                    Verified location names from Google Maps for Facebook Memories
                  </div>
                </div>
              </div>
              <div style={{ display: "flex", gap: "0.75rem", alignItems: "flex-start" }}>
                <span style={{ fontSize: "1.25rem", flexShrink: 0 }}>{"\uD83D\uDCC5"}</span>
                <div>
                  <div style={{ fontWeight: 600, marginBottom: "0.125rem" }}>Date & Time</div>
                  <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)" }}>
                    EXIF dates extracted automatically for each media item
                  </div>
                </div>
              </div>
              <div style={{ display: "flex", gap: "0.75rem", alignItems: "flex-start" }}>
                <span style={{ fontSize: "1.25rem", flexShrink: 0 }}>{"\uD83D\uDCA1"}</span>
                <div>
                  <div style={{ fontWeight: 600, marginBottom: "0.125rem" }}>Pro Tip</div>
                  <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)" }}>
                    Upload all photos from an event together — the AI tells a coherent story across items
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      ) : (
        <div class="layout-sidebar">
          <div
            onDrop={onDropWrapped}
            onDragOver={handleDragOver}
            onDragEnter={onDragEnter}
            onDragLeave={onDragLeave}
          >
            <div style={{
              display: "flex",
              justifyContent: "space-between",
              alignItems: "center",
              marginBottom: "1rem",
            }}>
              <div style={{ display: "flex", alignItems: "center", gap: "0.625rem" }}>
                <h3 style={{ margin: 0, fontSize: "1.125rem" }}>In-Flight Assets</h3>
                <span class="status-badge" style={{ fontVariantNumeric: "tabular-nums" }}>
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

            {anyUploading && (
              <div style={{ marginBottom: "1rem" }}>
                <div style={{
                  display: "flex",
                  justifyContent: "space-between",
                  fontSize: "0.75rem",
                  color: "var(--color-text-secondary)",
                  marginBottom: "0.375rem",
                }}>
                  <span>Uploading {doneCount} of {totalFiles} files…</span>
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
                    background: "#1877F2",
                    transition: "width 0.3s ease",
                    borderRadius: "2px",
                  }} />
                </div>
              </div>
            )}

            {activeFilters.size > 0 && (
              <div style={{
                display: "flex",
                alignItems: "center",
                gap: "0.5rem",
                marginBottom: "0.75rem",
                fontSize: "0.75rem",
                color: "var(--color-text-secondary)",
              }}>
                <span>Showing {filteredLifecycle.length} of {totalFiles}</span>
              </div>
            )}

            <div style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fill, minmax(var(--grid-card-sm), 1fr))",
              gap: "0.75rem",
              marginBottom: "1rem",
            }}>
              {filteredLifecycle.map((f) => (
                <FileCard key={f.name} f={f} />
              ))}
            </div>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
            <div class="sidebar-panel">
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                <h3 style={{ margin: 0 }}>Upload Summary</h3>
                {activeFilters.size > 0 && (
                  <button
                    onClick={() => { statusFilter.value = new Set(); }}
                    style={{
                      background: "none",
                      border: "none",
                      color: "var(--color-primary)",
                      fontSize: "0.75rem",
                      cursor: "pointer",
                      padding: "0.125rem 0.25rem",
                    }}
                  >
                    Clear
                  </button>
                )}
              </div>
              <StatusRow label="Uploading" count={uploading.length} color="var(--color-primary)" status="uploading" />
              <StatusRow label="Thumbnail" count={processing.length} color="var(--color-warning)" status="processing" />
              <StatusRow label="Downsizing" count={thumbnailed.length} color="var(--color-info, #0891b2)" status="thumbnailed" />
              <StatusRow label="Ready" count={ready.length} color="var(--color-success)" status="ready" />
              <StatusRow label="Error" count={errored.length} color="var(--color-danger)" status="error" />
              {uploading.length === 0 && processing.length === 0 && thumbnailed.length === 0 && ready.length === 0 && errored.length === 0 && (
                <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)", padding: "0.25rem 0" }}>
                  Waiting…
                </div>
              )}
            </div>

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
                {anyUploading && uploadSpeed.value > 0 && (
                  <div style={{ display: "flex", justifyContent: "space-between" }}>
                    <span style={{ color: "var(--color-text-secondary)" }}>Upload speed</span>
                    <span style={{ fontWeight: 600 }}>{formatSpeed(uploadSpeed.value)}</span>
                  </div>
                )}
                {anyUploading && (
                  <div style={{ display: "flex", justifyContent: "space-between" }}>
                    <span style={{ color: "var(--color-text-secondary)" }}>ETA</span>
                    <span style={{ fontWeight: 600, fontVariantNumeric: "tabular-nums", color: etaSeconds != null ? "var(--color-text)" : "var(--color-text-secondary)" }}>
                      {etaSeconds != null ? formatElapsed(etaSeconds) : "\u2014"}
                    </span>
                  </div>
                )}
              </div>
            </div>

            {allDone && doneCount > 0 && (
              <button
                class="primary"
                onClick={proceedToFBPrep}
                style={{ width: "100%", background: "#1877F2", borderColor: "#1877F2" }}
              >
                Continue to Facebook Prep →
              </button>
            )}

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
