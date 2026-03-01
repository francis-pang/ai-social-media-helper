/**
 * FBPrepUploader — upload step for the Facebook Prep workflow (DDR-080).
 *
 * Reuses the shared upload engine for S3 upload orchestration.
 * Shows the same 2-column layout as FileUploader but without the triage pipeline:
 * - No initTriage / updateTriageFiles / finalizeTriageUploads calls
 * - No server-side processing status polling
 * - "Continue to Facebook Prep" button appears when all uploads are done
 */
import { useState } from "preact/hooks";
import { createUploadEngine } from "../upload/uploadEngine";
import { fbPrepMediaKeys } from "./FBPrepView";
import { uploadSessionId, navigateToStep } from "../app";
import { syncUrlToStep } from "../router";
import { getFilesFromDataTransfer } from "../utils/fileSystem";
import { formatBytes, formatSpeed } from "../utils/format";
import { formatElapsed } from "../hooks/useElapsedTimer";
import { generateThumbnail } from "./media-uploader/thumbnailGenerator";

const ACCEPT =
  "image/jpeg,image/png,image/gif,image/webp,image/heic,image/heif," +
  "video/mp4,video/quicktime,video/x-msvideo,video/webm,video/x-matroska";

// Engine per workflow — isolated from FileUploader's engine (DDR-080)
const engine = createUploadEngine({ enableDedup: true, enableSpeedTracking: true });
const files = engine.files;
const error = engine.error;
const uploadSpeed = engine.uploadSpeed;

function generateSessionId(): string {
  return crypto.randomUUID();
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

  // Generate client-side thumbnails for preview (best-effort)
  for (const file of newFiles) {
    generateThumbnail(file).then((dataUrl) => {
      if (dataUrl) engine.updateFile(file.name, { thumbnailDataUrl: dataUrl });
    });
  }
}

function clearAll() {
  engine.clearAll();
  uploadSessionId.value = null;
}

/** Reset state when navigating back to landing (DDR-080). */
export function resetFBPrepUploaderState() {
  engine.resetState();
  uploadSessionId.value = null;
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

function proceedToFBPrep() {
  const doneFiles = files.value.filter((f) => f.status === "done");
  if (doneFiles.length === 0) return;

  fbPrepMediaKeys.value = doneFiles.map((f) => f.key);
  navigateToStep("fb-prep");
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function FileCard({ f }: { f: ReturnType<typeof files.value>[number] }) {
  const isUploading = f.status === "uploading" || f.status === "pending";
  const isDone = f.status === "done";
  const isError = f.status === "error";

  const dotColor = isError
    ? "var(--color-danger)"
    : isDone
    ? "var(--color-success)"
    : "var(--color-primary)";

  return (
    <div
      style={{
        background: "var(--color-surface)",
        border: "1px solid var(--color-border)",
        borderRadius: "var(--radius)",
        overflow: "hidden",
      }}
    >
      {/* Thumbnail area */}
      <div
        style={{
          aspectRatio: "1",
          background: "var(--color-surface-alt)",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          position: "relative",
          overflow: "hidden",
        }}
      >
        {f.thumbnailDataUrl ? (
          <img
            src={f.thumbnailDataUrl}
            alt=""
            loading="lazy"
            style={{ width: "100%", height: "100%", objectFit: "cover" }}
            onError={(e) => {
              (e.target as HTMLImageElement).style.display = "none";
            }}
          />
        ) : (
          <span style={{ fontSize: "2rem", opacity: 0.35 }}>📄</span>
        )}
        {/* Status dot */}
        <span
          style={{
            position: "absolute",
            top: "0.5rem",
            right: "0.5rem",
            width: "0.625rem",
            height: "0.625rem",
            borderRadius: "50%",
            background: dotColor,
            boxShadow: "0 0 0 2px var(--color-surface)",
            animation: isUploading ? "pulse-ring 1.5s ease-in-out infinite" : "none",
          }}
        />
        {/* Two-step pipeline: Upload → Done */}
        <div class="mini-pipeline">
          {[
            { label: "Uploading", stage: isDone || (!isUploading && !isError) ? "done" : isUploading ? "active" : "pending" },
            { label: "Done", stage: isDone ? "done" : "pending" },
          ].flatMap((step, i, arr) => {
            const els = [
              <div class={`mini-pipeline__step mini-pipeline__step--${step.stage}`} key={`s-${i}`}>
                <div class="mini-pipeline__circle">{step.stage === "done" ? "✓" : ""}</div>
                <div class="mini-pipeline__label">{step.label}</div>
              </div>,
            ];
            if (i < arr.length - 1) {
              els.push(
                <div
                  class={`mini-pipeline__connector${step.stage === "done" ? " mini-pipeline__connector--done" : ""}`}
                  key={`c-${i}`}
                />,
              );
            }
            return els;
          })}
        </div>
      </div>

      {/* File info */}
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
        <div
          style={{
            fontSize: "0.6875rem",
            color: "var(--color-text-secondary)",
            marginTop: "0.125rem",
          }}
        >
          {isUploading
            ? `${formatBytes(f.loaded)} / ${formatBytes(f.size)}`
            : formatBytes(f.size)}
        </div>
        <div
          style={{
            fontSize: "0.625rem",
            fontWeight: 600,
            letterSpacing: "0.05em",
            color: dotColor,
            marginTop: "0.25rem",
          }}
        >
          {isError ? "ERROR" : isDone ? "READY" : "UPLOADING…"}
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

  const allDone =
    files.value.length > 0 &&
    files.value.every((f) => f.status === "done" || f.status === "error");
  const doneCount = files.value.filter((f) => f.status === "done").length;
  const anyUploading = files.value.some((f) => f.status === "uploading");
  const totalFiles = files.value.length;
  const uploading = files.value.filter((f) => f.status === "uploading" || f.status === "pending");
  const done = files.value.filter((f) => f.status === "done");
  const errored = files.value.filter((f) => f.status === "error");

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
        <div
          style={{
            color: "var(--color-danger)",
            marginBottom: "1rem",
            fontSize: "0.875rem",
            padding: "0.75rem 1rem",
            background: "var(--color-primary-light)",
            borderRadius: "var(--radius)",
            borderLeft: "3px solid var(--color-danger)",
          }}
        >
          {error.value}
        </div>
      )}

      {!hasFiles ? (
        /* ── Empty state ── */
        <div class="layout-sidebar">
          <div
            class={`drop-zone${dragActive ? " drop-zone--active" : ""}`}
            onDrop={onDropWrapped}
            onDragOver={handleDragOver}
            onDragEnter={onDragEnter}
            onDragLeave={onDragLeave}
          >
            <span class="drop-zone__icon">🌐</span>
            <span class="drop-zone__title">Drop your photos &amp; videos here</span>
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
            <p
              style={{
                fontSize: "0.75rem",
                color: "var(--color-text-secondary)",
                marginTop: "1rem",
                marginBottom: 0,
              }}
            >
              Files are processed securely and not stored permanently
            </p>
          </div>

          <div class="sidebar-panel">
            <h3>What Facebook Prep Does</h3>
            <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
              <div style={{ display: "flex", gap: "0.75rem", alignItems: "flex-start" }}>
                <span style={{ fontSize: "1.25rem", flexShrink: 0 }}>✍️</span>
                <div>
                  <div style={{ fontWeight: 600, marginBottom: "0.125rem" }}>Unique Captions</div>
                  <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)" }}>
                    AI generates captions for each photo and video as part of a coherent narrative
                  </div>
                </div>
              </div>
              <div style={{ display: "flex", gap: "0.75rem", alignItems: "flex-start" }}>
                <span style={{ fontSize: "1.25rem", flexShrink: 0 }}>📍</span>
                <div>
                  <div style={{ fontWeight: 600, marginBottom: "0.125rem" }}>Location Tags</div>
                  <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)" }}>
                    Verified location names from Google Maps for Facebook Memories
                  </div>
                </div>
              </div>
              <div style={{ display: "flex", gap: "0.75rem", alignItems: "flex-start" }}>
                <span style={{ fontSize: "1.25rem", flexShrink: 0 }}>📅</span>
                <div>
                  <div style={{ fontWeight: 600, marginBottom: "0.125rem" }}>Date &amp; Time</div>
                  <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)" }}>
                    EXIF dates extracted automatically for each media item
                  </div>
                </div>
              </div>
              <div style={{ display: "flex", gap: "0.75rem", alignItems: "flex-start" }}>
                <span style={{ fontSize: "1.25rem", flexShrink: 0 }}>💡</span>
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
        /* ── In-flight state ── */
        <div class="layout-sidebar">
          <div
            onDrop={onDropWrapped}
            onDragOver={handleDragOver}
            onDragEnter={onDragEnter}
            onDragLeave={onDragLeave}
          >
            {/* Header */}
            <div
              style={{
                display: "flex",
                justifyContent: "space-between",
                alignItems: "center",
                marginBottom: "1rem",
              }}
            >
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

            {/* Upload progress bar */}
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
                      background: "#1877F2",
                      transition: "width 0.3s ease",
                      borderRadius: "2px",
                    }}
                  />
                </div>
              </div>
            )}

            {/* Thumbnail grid */}
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "repeat(auto-fill, minmax(var(--grid-card-sm), 1fr))",
                gap: "0.75rem",
                marginBottom: "1rem",
              }}
            >
              {files.value.map((f) => (
                <FileCard key={f.name} f={f} />
              ))}
            </div>
          </div>

          {/* Right sidebar */}
          <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
            {/* Upload Summary */}
            <div class="sidebar-panel">
              <h3>Upload Summary</h3>
              {uploading.length > 0 && (
                <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "0.375rem 0" }}>
                  <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
                    <span style={{ width: "0.5rem", height: "0.5rem", borderRadius: "50%", background: "var(--color-primary)", flexShrink: 0 }} />
                    <span style={{ fontSize: "0.875rem", color: "var(--color-text)" }}>Uploading</span>
                  </div>
                  <span style={{ fontSize: "0.875rem", fontWeight: 600, fontVariantNumeric: "tabular-nums" }}>{uploading.length}</span>
                </div>
              )}
              {done.length > 0 && (
                <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "0.375rem 0" }}>
                  <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
                    <span style={{ width: "0.5rem", height: "0.5rem", borderRadius: "50%", background: "var(--color-success)", flexShrink: 0 }} />
                    <span style={{ fontSize: "0.875rem", color: "var(--color-text)" }}>Ready</span>
                  </div>
                  <span style={{ fontSize: "0.875rem", fontWeight: 600, fontVariantNumeric: "tabular-nums" }}>{done.length}</span>
                </div>
              )}
              {errored.length > 0 && (
                <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "0.375rem 0" }}>
                  <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
                    <span style={{ width: "0.5rem", height: "0.5rem", borderRadius: "50%", background: "var(--color-danger)", flexShrink: 0 }} />
                    <span style={{ fontSize: "0.875rem", color: "var(--color-text)" }}>Error</span>
                  </div>
                  <span style={{ fontSize: "0.875rem", fontWeight: 600, fontVariantNumeric: "tabular-nums" }}>{errored.length}</span>
                </div>
              )}
              {uploading.length === 0 && done.length === 0 && errored.length === 0 && (
                <div style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)", padding: "0.25rem 0" }}>
                  Waiting…
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
                      {etaSeconds != null ? formatElapsed(etaSeconds) : "—"}
                    </span>
                  </div>
                )}
              </div>
            </div>

            {/* Primary CTA */}
            {allDone && doneCount > 0 && (
              <button
                class="primary"
                onClick={proceedToFBPrep}
                style={{ width: "100%", background: "#1877F2", borderColor: "#1877F2" }}
              >
                Continue to Facebook Prep →
              </button>
            )}

            {/* Cancel */}
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
