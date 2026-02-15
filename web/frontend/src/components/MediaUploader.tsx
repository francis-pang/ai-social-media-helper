import { signal } from "@preact/signals";
import { getUploadUrl, uploadToS3, uploadToS3Multipart, MULTIPART_THRESHOLD } from "../api/client";
import {
  navigateToStep,
  selectedPaths,
  tripContext,
  uploadSessionId,
} from "../app";
import { getFilesFromDataTransfer } from "../utils/fileSystem";
import { formatBytes } from "../utils/format";
import { badgeClass, badgeLabel } from "../utils/statusBadge";
import { generateThumbnail } from "./media-uploader/thumbnailGenerator";

/** Supported media file extensions (lower-case, with leading dot). */
const MEDIA_EXTENSIONS = new Set([
  ".jpg",
  ".jpeg",
  ".png",
  ".gif",
  ".webp",
  ".heic",
  ".heif",
  ".mp4",
  ".mov",
  ".avi",
  ".webm",
  ".mkv",
]);

/** File System Access API accept types for the file picker dialog. */
const MEDIA_ACCEPT_TYPES: FilePickerAcceptType[] = [
  {
    description: "Media files",
    accept: {
      "image/*": [".jpg", ".jpeg", ".png", ".gif", ".webp", ".heic", ".heif"],
      "video/*": [".mp4", ".mov", ".avi", ".webm", ".mkv"],
    },
  },
];

interface MediaFile {
  name: string;
  size: number;
  key: string;
  status: "pending" | "uploading" | "done" | "error";
  progress: number;
  error?: string;
  thumbnailDataUrl?: string;
  mediaType: "image" | "video";
}

const files = signal<MediaFile[]>([]);
const error = signal<string | null>(null);

function generateSessionId(): string {
  return crypto.randomUUID();
}

/** Check whether a File is a supported media type based on extension. */
function isMediaFile(file: File): boolean {
  const dot = file.name.lastIndexOf(".");
  if (dot === -1) return false;
  const ext = file.name.slice(dot).toLowerCase();
  return MEDIA_EXTENSIONS.has(ext);
}

/** Determine whether a file is an image or video based on MIME type. */
function getMediaType(file: File): "image" | "video" {
  return file.type.startsWith("video/") ? "video" : "image";
}

// ---------------------------------------------------------------------------
// File System Access API pickers (DDR-029)
// ---------------------------------------------------------------------------

/** Recursively collect media files from a directory handle. */
async function collectMediaFiles(
  dirHandle: FileSystemDirectoryHandle,
): Promise<File[]> {
  const result: File[] = [];
  for await (const entry of dirHandle.values()) {
    if (entry.kind === "file") {
      const file = await (entry as FileSystemFileHandle).getFile();
      if (isMediaFile(file)) {
        result.push(file);
      }
    } else if (entry.kind === "directory") {
      const nested = await collectMediaFiles(
        entry as FileSystemDirectoryHandle,
      );
      result.push(...nested);
    }
  }
  return result;
}

/** Open the File System Access API file picker for individual files. */
async function chooseFiles() {
  try {
    const handles = await window.showOpenFilePicker({
      multiple: true,
      types: MEDIA_ACCEPT_TYPES,
    });
    const mediaFiles: File[] = [];
    for (const handle of handles) {
      mediaFiles.push(await handle.getFile());
    }
    if (mediaFiles.length > 0) {
      addFiles(mediaFiles);
    }
  } catch (e) {
    // User cancelled the picker — not an error
    if (e instanceof DOMException && e.name === "AbortError") return;
    error.value = e instanceof Error ? e.message : "Failed to open file picker";
  }
}

/** Open the File System Access API directory picker and collect media files. */
async function chooseFolder() {
  try {
    const dirHandle = await window.showDirectoryPicker();
    const mediaFiles = await collectMediaFiles(dirHandle);
    if (mediaFiles.length === 0) {
      error.value = "No media files found in the selected folder.";
      return;
    }
    error.value = null;
    addFiles(mediaFiles);
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") return;
    error.value =
      e instanceof Error ? e.message : "Failed to open folder picker";
  }
}

// ---------------------------------------------------------------------------
// File management
// ---------------------------------------------------------------------------

function addFiles(newFiles: File[]) {
  if (!uploadSessionId.value) {
    uploadSessionId.value = generateSessionId();
  }

  const sessionId = uploadSessionId.value;
  const existing = new Set(files.value.map((f) => f.name));

  const toAdd: MediaFile[] = [];
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
      mediaType: getMediaType(file),
    });
    fileMap.set(file.name, file);
  }

  if (toAdd.length === 0) return;
  files.value = [...files.value, ...toAdd];

  // Generate thumbnails and start uploads concurrently
  for (const entry of toAdd) {
    const file = fileMap.get(entry.name)!;

    // Thumbnail generation (non-blocking, best-effort)
    generateThumbnail(file).then((dataUrl) => {
      if (dataUrl) {
        updateFile(entry.name, { thumbnailDataUrl: dataUrl });
      }
    });

    // S3 upload
    uploadFile(sessionId, entry.name, file);
  }
}

async function uploadFile(sessionId: string, filename: string, file: File) {
  updateFile(filename, { status: "uploading", progress: 0 });

  try {
    let key: string;

    if (file.size > MULTIPART_THRESHOLD) {
      // Large file: use S3 multipart upload with parallel chunks (DDR-054)
      key = await uploadToS3Multipart(sessionId, file, (loaded, total) => {
        updateFile(filename, { progress: Math.round((loaded / total) * 100) });
      });
    } else {
      // Small file: use single presigned PUT (existing path)
      const res = await getUploadUrl(sessionId, filename, file.type);
      key = res.key;

      await uploadToS3(res.uploadUrl, file, (loaded, total) => {
        updateFile(filename, { progress: Math.round((loaded / total) * 100) });
      });
    }

    updateFile(filename, { status: "done", progress: 100, key });
  } catch (e) {
    updateFile(filename, {
      status: "error",
      error: e instanceof Error ? e.message : "Upload failed",
    });
  }
}

function updateFile(filename: string, updates: Partial<MediaFile>) {
  files.value = files.value.map((f) =>
    f.name === filename ? { ...f, ...updates } : f,
  );
}

function removeFile(filename: string) {
  files.value = files.value.filter((f) => f.name !== filename);
}

function clearAll() {
  files.value = [];
  uploadSessionId.value = null;
}

// ---------------------------------------------------------------------------
// Drag-and-drop (supplementary input method)
// ---------------------------------------------------------------------------

async function handleDrop(e: DragEvent) {
  e.preventDefault();
  e.stopPropagation();
  if (!e.dataTransfer) return;

  const allFiles = await getFilesFromDataTransfer(e.dataTransfer);
  const mediaFiles = allFiles.filter(isMediaFile);
  if (mediaFiles.length > 0) {
    addFiles(mediaFiles);
  }
}

function handleDragOver(e: DragEvent) {
  e.preventDefault();
  e.stopPropagation();
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

function proceedToSelection() {
  const uploadedKeys = files.value
    .filter((f) => f.status === "done")
    .map((f) => f.key);

  if (uploadedKeys.length === 0) return;

  if (!tripContext.value.trim()) {
    error.value =
      "Please enter a trip or event description before continuing.";
    return;
  }

  error.value = null;
  selectedPaths.value = uploadedKeys;
  navigateToStep("selecting");
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function MediaUploader() {
  const allDone =
    files.value.length > 0 &&
    files.value.every((f) => f.status === "done" || f.status === "error");
  const doneCount = files.value.filter((f) => f.status === "done").length;
  const anyUploading = files.value.some((f) => f.status === "uploading");
  const totalFiles = files.value.length;

  const overallProgress =
    totalFiles > 0
      ? Math.round(
          files.value.reduce((sum, f) => sum + f.progress, 0) / totalFiles,
        )
      : 0;

  return (
    <div class="card">
      <p
        style={{
          color: "var(--color-text-secondary)",
          fontSize: "0.875rem",
          marginBottom: "1.5rem",
        }}
      >
        Upload media files for AI selection. Choose individual files, select a
        folder, or drag and drop.
      </p>

      {/* Picker buttons (File System Access API — DDR-029) — shown when no files yet */}
      {files.value.length === 0 && (
        <div
          style={{
            display: "flex",
            gap: "0.75rem",
            marginBottom: "1rem",
          }}
        >
          <button class="primary" onClick={chooseFiles}>
            Choose Files
          </button>
          <button class="outline" onClick={chooseFolder}>
            Choose Folder
          </button>
        </div>
      )}

      {/* Drop zone (DDR-058: compact secondary when files exist) */}
      <div
        onDrop={handleDrop}
        onDragOver={handleDragOver}
        style={{
          border: "2px dashed var(--color-border)",
          borderRadius: "var(--radius)",
          padding: files.value.length > 0 ? "0.75rem" : "1.5rem",
          textAlign: "center",
          marginBottom: "1.5rem",
          transition: "border-color 0.2s, padding 0.2s",
        }}
      >
        <div
          style={{
            fontSize: files.value.length > 0 ? "0.875rem" : "1rem",
            marginBottom: files.value.length > 0 ? "0" : "0.375rem",
            color: "var(--color-text-secondary)",
          }}
        >
          {files.value.length > 0 ? "Drop more files here" : "or drop files here"}
        </div>
        {files.value.length === 0 && (
          <div
            style={{ fontSize: "0.75rem", color: "var(--color-text-secondary)" }}
          >
            JPEG, PNG, GIF, WebP, HEIC, MP4, MOV, AVI, WebM, MKV
          </div>
        )}
      </div>

      {/* File list (DDR-058: card-based rows with thumbnails) */}
      {files.value.length > 0 && (
        <div class="file-list">
          {files.value.map((f) => (
            <div class="file-row" key={f.name}>
              {/* Thumbnail */}
              <div
                style={{
                  width: "40px",
                  height: "40px",
                  borderRadius: "4px",
                  overflow: "hidden",
                  flexShrink: 0,
                  background: "var(--color-surface-hover)",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                }}
              >
                {f.thumbnailDataUrl ? (
                  <img
                    src={f.thumbnailDataUrl}
                    alt={f.name}
                    style={{
                      width: "100%",
                      height: "100%",
                      objectFit: "cover",
                    }}
                  />
                ) : (
                  <span
                    style={{
                      fontSize: "0.75rem",
                      color: "var(--color-text-secondary)",
                      textTransform: "uppercase",
                    }}
                  >
                    {f.mediaType === "video" ? "VID" : "IMG"}
                  </span>
                )}
              </div>

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

              {/* Media type badge */}
              <span
                style={{
                  fontSize: "0.75rem",
                  padding: "0.125rem 0.375rem",
                  borderRadius: "999px",
                  background:
                    f.mediaType === "video"
                      ? "rgba(108, 140, 255, 0.15)"
                      : "rgba(81, 207, 102, 0.15)",
                  color:
                    f.mediaType === "video"
                      ? "var(--color-primary)"
                      : "var(--color-success)",
                  flexShrink: 0,
                  textTransform: "uppercase",
                  fontWeight: 600,
                }}
              >
                {f.mediaType === "video" ? "Video" : "Photo"}
              </span>

              {/* Size */}
              <span
                style={{
                  fontSize: "0.75rem",
                  color: "var(--color-text-secondary)",
                  flexShrink: 0,
                }}
              >
                {formatBytes(f.size)}
              </span>

              {/* Status badge (DDR-058) */}
              <span
                class={badgeClass(f.status)}
                style={f.status === "uploading" ? {
                  background: `linear-gradient(to right, rgba(108, 140, 255, 0.3) ${f.progress}%, rgba(108, 140, 255, 0.1) ${f.progress}%)`,
                } : undefined}
                title={f.status === "error" ? (f.error || "Upload failed") : undefined}
              >
                {badgeLabel(f.status, f.progress)}
              </span>

              {/* Remove button (DDR-058: compact X) */}
              <button
                class="btn-remove"
                onClick={() => removeFile(f.name)}
                disabled={f.status === "uploading"}
                title="Remove file"
              >
                ✕
              </button>

              {/* Per-file progress bar (DDR-058) */}
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
            <span>{overallProgress}%</span>
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

      {/* Error message */}
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
            padding: "1.5rem 1rem",
            textAlign: "center",
            fontSize: "0.875rem",
          }}
        >
          No files selected yet. Use the buttons above or drag and drop media
          files.
        </p>
      )}

      {/* Trip / Event context */}
      <div style={{ marginBottom: "1.5rem" }}>
        <label
          style={{
            display: "block",
            fontSize: "0.875rem",
            fontWeight: 500,
            marginBottom: "0.5rem",
          }}
        >
          Trip / Event Context
        </label>
        <input
          type="text"
          placeholder='e.g., "3-day trip to Tokyo, Oct 2025"'
          value={tripContext.value}
          onInput={(e) => {
            tripContext.value = (e.target as HTMLInputElement).value;
            if (error.value?.includes("trip or event")) {
              error.value = null;
            }
          }}
          style={{
            width: "100%",
            padding: "0.625rem 0.75rem",
            fontSize: "0.875rem",
            background: "var(--color-bg)",
            border: "1px solid var(--color-border)",
            borderRadius: "var(--radius)",
            color: "var(--color-text)",
            outline: "none",
          }}
        />
        <p
          style={{
            fontSize: "0.75rem",
            color: "var(--color-text-secondary)",
            marginTop: "0.375rem",
          }}
        >
          Describe the trip or event to help the AI understand context for media
          selection.
        </p>
      </div>

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
          <div style={{ display: "flex", gap: "0.5rem" }}>
            <button class="btn-add-more" onClick={chooseFiles}>
              + Choose Files
            </button>
            <button class="btn-add-more" onClick={chooseFolder}>
              + Choose Folder
            </button>
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: "0.75rem" }}>
            <span
              style={{
                fontSize: "0.875rem",
                color: "var(--color-text-secondary)",
              }}
            >
              {doneCount}/{totalFiles} uploaded
            </span>
            <button class="outline" onClick={clearAll} disabled={anyUploading}>
              Clear all
            </button>
            <button
              class="primary"
              onClick={proceedToSelection}
              disabled={!allDone || doneCount === 0}
            >
              Continue to Selection
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
