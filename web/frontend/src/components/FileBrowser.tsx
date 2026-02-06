import { signal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { browse } from "../api/client";
import { currentStep, selectedPaths } from "../app";
import type { FileEntry } from "../types/api";

const currentPath = signal<string>("");
const entries = signal<FileEntry[]>([]);
const parentPath = signal<string>("");
const selected = signal<Set<string>>(new Set());
const loading = signal(false);
const error = signal<string | null>(null);

/** Media file extensions the backend supports. */
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

function isMediaFile(entry: FileEntry): boolean {
  const ext = entry.name.slice(entry.name.lastIndexOf(".")).toLowerCase();
  return !entry.isDir && MEDIA_EXTENSIONS.has(ext);
}

async function loadDirectory(path: string) {
  loading.value = true;
  error.value = null;
  try {
    const res = await browse(path);
    currentPath.value = res.path;
    parentPath.value = res.parent;
    entries.value = res.entries;
    selected.value = new Set();
  } catch (e) {
    error.value = e instanceof Error ? e.message : "Failed to browse";
  } finally {
    loading.value = false;
  }
}

function toggleSelect(path: string) {
  const next = new Set(selected.value);
  if (next.has(path)) {
    next.delete(path);
  } else {
    next.add(path);
  }
  selected.value = next;
}

function selectAllMedia() {
  const mediaPaths = entries.value
    .filter(isMediaFile)
    .map((e) => e.path);
  selected.value = new Set(mediaPaths);
}

function selectDirectory() {
  selected.value = new Set([currentPath.value]);
}

function proceedWithSelection() {
  selectedPaths.value = Array.from(selected.value);
  currentStep.value = "confirm-files";
}

function formatSize(bytes: number): string {
  if (bytes === 0) return "";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function FileBrowser() {
  useEffect(() => {
    loadDirectory(currentPath.value);
  }, []);

  const mediaCount = entries.value.filter(isMediaFile).length;
  const dirCount = entries.value.filter((e) => e.isDir).length;

  return (
    <div class="card">
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "0.75rem",
          marginBottom: "1rem",
        }}
      >
        <code style={{ flex: 1, padding: "0.5rem", fontSize: "0.8125rem" }}>
          {currentPath.value || "~"}
        </code>
      </div>

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

      <div
        style={{
          display: "flex",
          gap: "0.5rem",
          marginBottom: "1rem",
          flexWrap: "wrap",
        }}
      >
        {parentPath.value !== "" && (
          <button class="outline" onClick={() => loadDirectory(parentPath.value)}>
            .. (up)
          </button>
        )}
        {mediaCount > 0 && (
          <button class="outline" onClick={selectAllMedia}>
            Select all media ({mediaCount})
          </button>
        )}
        <button class="outline" onClick={selectDirectory}>
          Select entire directory
        </button>
      </div>

      {loading.value ? (
        <p style={{ color: "var(--color-text-secondary)" }}>Loading...</p>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
          {entries.value.map((entry) => {
            const isMedia = isMediaFile(entry);
            const isSelected = entry.isDir
              ? false
              : selected.value.has(entry.path);

            return (
              <div
                key={entry.path}
                onClick={() => {
                  if (entry.isDir) {
                    loadDirectory(entry.path);
                  } else if (isMedia) {
                    toggleSelect(entry.path);
                  }
                }}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "0.75rem",
                  padding: "0.5rem 0.75rem",
                  borderRadius: "var(--radius)",
                  cursor: entry.isDir || isMedia ? "pointer" : "default",
                  background: isSelected
                    ? "rgba(108, 140, 255, 0.15)"
                    : "transparent",
                  opacity: !entry.isDir && !isMedia ? 0.4 : 1,
                }}
              >
                <span style={{ width: "1.25rem", textAlign: "center" }}>
                  {entry.isDir ? "📁" : isMedia ? "🖼" : "📄"}
                </span>
                {isMedia && (
                  <input
                    type="checkbox"
                    checked={isSelected}
                    onClick={(e) => e.stopPropagation()}
                    onChange={() => toggleSelect(entry.path)}
                  />
                )}
                <span style={{ flex: 1, fontSize: "0.875rem" }}>
                  {entry.name}
                </span>
                <span
                  style={{
                    fontSize: "0.75rem",
                    color: "var(--color-text-secondary)",
                  }}
                >
                  {entry.isDir
                    ? "dir"
                    : formatSize(entry.size)}
                </span>
              </div>
            );
          })}
          {entries.value.length === 0 && !loading.value && (
            <p
              style={{
                color: "var(--color-text-secondary)",
                padding: "1rem",
                textAlign: "center",
              }}
            >
              {dirCount === 0 && mediaCount === 0
                ? "Empty directory"
                : "No entries"}
            </p>
          )}
        </div>
      )}

      {selected.value.size > 0 && (
        <div
          style={{
            marginTop: "1.5rem",
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
          }}
        >
          <span style={{ fontSize: "0.875rem", color: "var(--color-text-secondary)" }}>
            {selected.value.size} item(s) selected
          </span>
          <button class="primary" onClick={proceedWithSelection}>
            Continue
          </button>
        </div>
      )}
    </div>
  );
}
