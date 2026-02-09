import { signal } from "@preact/signals";
import { getUploadUrl, uploadToS3, startTriage } from "../api/client";
import { selectedPaths, uploadSessionId, triageJobId, navigateToStep } from "../app";

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
  error?: string;
}

const files = signal<UploadedFile[]>([]);
const error = signal<string | null>(null);

function generateSessionId(): string {
  return crypto.randomUUID();
}

function handleFileSelect(e: Event) {
  const input = e.target as HTMLInputElement;
  if (!input.files || input.files.length === 0) return;
  addFiles(Array.from(input.files));
  input.value = ""; // Reset so same files can be re-selected
}

function handleDrop(e: DragEvent) {
  e.preventDefault();
  e.stopPropagation();
  if (!e.dataTransfer?.files) return;
  addFiles(Array.from(e.dataTransfer.files));
}

function handleDragOver(e: DragEvent) {
  e.preventDefault();
  e.stopPropagation();
}

function addFiles(newFiles: File[]) {
  // Ensure we have a session ID
  if (!uploadSessionId.value) {
    uploadSessionId.value = generateSessionId();
  }

  const sessionId = uploadSessionId.value;
  const existing = new Set(files.value.map((f) => f.name));

  // Filter to supported media types and deduplicate
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
    });
    fileMap.set(file.name, file);
  }

  if (toAdd.length === 0) return;

  files.value = [...files.value, ...toAdd];

  // Start uploading each file
  for (const entry of toAdd) {
    uploadFile(sessionId, entry.name, fileMap.get(entry.name)!);
  }
}

async function uploadFile(sessionId: string, filename: string, file: File) {
  updateFile(filename, { status: "uploading", progress: 0 });

  try {
    const { uploadUrl, key } = await getUploadUrl(
      sessionId,
      filename,
      file.type,
    );

    await uploadToS3(uploadUrl, file, (loaded, total) => {
      updateFile(filename, { progress: Math.round((loaded / total) * 100) });
    });

    updateFile(filename, { status: "done", progress: 100, key });
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

function clearAll() {
  files.value = [];
  uploadSessionId.value = null;
}

/** Reset FileUploader state (called from navigateToLanding — DDR-042). */
export function resetFileUploaderState() {
  files.value = [];
  error.value = null;
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
    // Cloud triage uses sessionId — Lambda lists S3 objects with this prefix
    const res = await startTriage({ sessionId });
    triageJobId.value = res.id;
    navigateToStep("processing");
  } catch (e) {
    error.value = e instanceof Error ? e.message : "Failed to start triage";
  }
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function FileUploader() {
  const allDone = files.value.length > 0 && files.value.every(
    (f) => f.status === "done" || f.status === "error",
  );
  const doneCount = files.value.filter((f) => f.status === "done").length;
  const anyUploading = files.value.some((f) => f.status === "uploading");

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

      {/* Drop zone */}
      <div
        onDrop={handleDrop}
        onDragOver={handleDragOver}
        style={{
          border: "2px dashed var(--color-border)",
          borderRadius: "var(--radius)",
          padding: "2rem",
          textAlign: "center",
          marginBottom: "1.5rem",
          cursor: "pointer",
          transition: "border-color 0.2s",
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
            fontSize: "1.25rem",
            marginBottom: "0.5rem",
            color: "var(--color-text-secondary)",
          }}
        >
          Drop files here
        </div>
        <div style={{ fontSize: "0.8125rem", color: "var(--color-text-secondary)" }}>
          or click to browse — JPEG, PNG, GIF, WebP, HEIC, MP4, MOV
        </div>
      </div>

      {/* File list */}
      {files.value.length > 0 && (
        <div
          style={{
            background: "var(--color-bg)",
            borderRadius: "var(--radius)",
            padding: "0.5rem",
            maxHeight: "400px",
            overflowY: "auto",
            marginBottom: "1.5rem",
          }}
        >
          {files.value.map((f) => (
            <div
              key={f.name}
              style={{
                display: "flex",
                alignItems: "center",
                gap: "0.75rem",
                padding: "0.375rem 0.5rem",
                borderBottom: "1px solid var(--color-border)",
              }}
            >
              {/* Status indicator */}
              <span
                style={{
                  width: "0.5rem",
                  height: "0.5rem",
                  borderRadius: "50%",
                  flexShrink: 0,
                  background:
                    f.status === "done"
                      ? "var(--color-success)"
                      : f.status === "error"
                        ? "var(--color-danger)"
                        : f.status === "uploading"
                          ? "var(--color-accent)"
                          : "var(--color-text-secondary)",
                }}
              />

              {/* Filename */}
              <span
                style={{
                  flex: 1,
                  fontSize: "0.8125rem",
                  fontFamily: "var(--font-mono)",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
                title={f.name}
              >
                {f.name}
              </span>

              {/* Size */}
              <span
                style={{
                  fontSize: "0.6875rem",
                  color: "var(--color-text-secondary)",
                  flexShrink: 0,
                }}
              >
                {formatSize(f.size)}
              </span>

              {/* Progress / Status */}
              <span
                style={{
                  fontSize: "0.6875rem",
                  color:
                    f.status === "error"
                      ? "var(--color-danger)"
                      : "var(--color-text-secondary)",
                  flexShrink: 0,
                  minWidth: "3.5rem",
                  textAlign: "right",
                }}
              >
                {f.status === "uploading"
                  ? `${f.progress}%`
                  : f.status === "done"
                    ? "Uploaded"
                    : f.status === "error"
                      ? "Failed"
                      : "Pending"}
              </span>

              {/* Remove button */}
              <button
                class="outline"
                onClick={() => removeFile(f.name)}
                disabled={f.status === "uploading"}
                style={{
                  padding: "0.125rem 0.5rem",
                  fontSize: "0.75rem",
                  flexShrink: 0,
                }}
              >
                Remove
              </button>
            </div>
          ))}
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

      {/* Actions */}
      {files.value.length > 0 && (
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
          }}
        >
          <span
            style={{
              fontSize: "0.875rem",
              color: "var(--color-text-secondary)",
            }}
          >
            {doneCount} of {files.value.length} file(s) uploaded
            {anyUploading && " — uploading..."}
          </span>
          <div style={{ display: "flex", gap: "0.75rem" }}>
            <button
              class="outline"
              onClick={clearAll}
              disabled={anyUploading}
            >
              Clear all
            </button>
            <button
              class="primary"
              onClick={proceedToTriage}
              disabled={!allDone || doneCount === 0}
            >
              Continue to Triage
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
