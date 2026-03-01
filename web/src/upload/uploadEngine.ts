/**
 * Shared S3 upload engine factory (DDR-080).
 *
 * Creates isolated per-instance signal state for upload orchestration.
 * Consumers (FileUploader, MediaUploader, FBPrepUploader) instantiate their own
 * engine and layer workflow-specific logic on top.
 *
 * Features enabled per config:
 *   enableDedup      — DDR-067 content fingerprinting via quickFingerprint/fullHash
 *   enableSpeedTracking — bytes/sec upload speed calculation
 */
import { signal } from "@preact/signals";
import type { Signal } from "@preact/signals";
import {
  getUploadUrl,
  uploadToS3,
  uploadToS3Multipart,
  MULTIPART_THRESHOLD,
} from "../api/client";
import { quickFingerprint, fullHash } from "../utils/fileHash";

export interface UploadedFile {
  name: string;
  size: number;
  /** S3 key assigned by the backend (e.g. `{sessionId}/{filename}`). */
  key: string;
  status: "pending" | "uploading" | "done" | "error";
  /** Upload progress 0–100. */
  progress: number;
  /** Bytes uploaded so far (for speed / ETA calculation). */
  loaded: number;
  error?: string;
  /** Client-side thumbnail data URL (optional, set by consumer). */
  thumbnailDataUrl?: string;
}

export interface UploadEngineConfig {
  /** Enable DDR-067 content-based deduplication (default: false). */
  enableDedup?: boolean;
  /** Enable bytes/sec upload speed tracking (default: false). */
  enableSpeedTracking?: boolean;
}

export interface UploadEngine {
  // --- Reactive state ---
  readonly files: Signal<UploadedFile[]>;
  readonly error: Signal<string | null>;
  /** Aggregate upload speed in bytes/sec (0 when not tracking or idle). */
  readonly uploadSpeed: Signal<number>;

  // --- Actions ---
  /**
   * Add and begin uploading files.
   * @returns Count of files actually queued (dedup may reduce this).
   */
  addFiles(sessionId: string, newFiles: File[]): Promise<number>;
  /** Partially update a file entry (e.g. to add thumbnailDataUrl). */
  updateFile(name: string, updates: Partial<UploadedFile>): void;
  /** Remove a file from the list (only when not uploading). */
  removeFile(name: string): void;
  /** Clear all files and reset session (does NOT reset error). */
  clearAll(): void;
  /** Full reset — clears files, error, speed, dedup map. */
  resetState(): void;
  /** Sum of `loaded` bytes across all files (for ETA calculation). */
  getTotalLoaded(): number;
}

export function createUploadEngine(config: UploadEngineConfig = {}): UploadEngine {
  const { enableDedup = false, enableSpeedTracking = false } = config;

  // --- Per-instance signal state ---
  const files = signal<UploadedFile[]>([]);
  const error = signal<string | null>(null);
  const uploadSpeed = signal<number>(0);

  // --- Dedup state (DDR-067) ---
  const fingerprintMap = new Map<string, string>(); // fingerprint → filename

  // --- Speed tracking state ---
  let speedTimer: ReturnType<typeof setInterval> | null = null;
  let prevSpeedBytes = 0;
  let prevSpeedTime = 0;

  function getTotalLoaded(): number {
    return files.value.reduce((sum, f) => sum + f.loaded, 0);
  }

  function startSpeedTracking() {
    if (!enableSpeedTracking || speedTimer) return;
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

  function updateFile(name: string, updates: Partial<UploadedFile>) {
    files.value = files.value.map((f) =>
      f.name === name ? { ...f, ...updates } : f,
    );
  }

  function removeFile(name: string) {
    files.value = files.value.filter((f) => f.name !== name);
  }

  function clearAll() {
    files.value = [];
    if (enableSpeedTracking) stopSpeedTracking();
  }

  function resetState() {
    files.value = [];
    error.value = null;
    fingerprintMap.clear();
    if (enableSpeedTracking) stopSpeedTracking();
  }

  async function uploadFile(sessionId: string, filename: string, file: File) {
    updateFile(filename, { status: "uploading", progress: 0, loaded: 0 });
    if (enableSpeedTracking) startSpeedTracking();

    try {
      let key: string;

      if (file.size > MULTIPART_THRESHOLD) {
        key = await uploadToS3Multipart(sessionId, file, (loaded, total) => {
          updateFile(filename, {
            progress: Math.round((loaded / total) * 100),
            loaded,
          });
        });
      } else {
        const res = await getUploadUrl(sessionId, filename, file.type);
        key = res.key;

        await uploadToS3(res.uploadUrl, file, (loaded, total) => {
          updateFile(filename, {
            progress: Math.round((loaded / total) * 100),
            loaded,
          });
        });
      }

      updateFile(filename, {
        status: "done",
        progress: 100,
        loaded: file.size,
        key,
      });
    } catch (e) {
      updateFile(filename, {
        status: "error",
        error: e instanceof Error ? e.message : "Upload failed",
      });
    }
  }

  async function addFiles(sessionId: string, newFiles: File[]): Promise<number> {
    const existing = new Set(files.value.map((f) => f.name));

    const toAdd: UploadedFile[] = [];
    const fileMap = new Map<string, File>();
    let skippedDuplicates = 0;

    for (const file of newFiles) {
      if (existing.has(file.name)) continue;

      if (enableDedup) {
        try {
          const fp = await quickFingerprint(file);
          const existingName = fingerprintMap.get(fp);
          if (existingName) {
            const existingFile =
              fileMap.get(existingName) ??
              newFiles.find((f) => f.name === existingName);
            if (existingFile) {
              const fh1 = await fullHash(file);
              const fh2 = await fullHash(existingFile);
              if (fh1 === fh2) {
                skippedDuplicates++;
                console.info(
                  `DDR-067: Skipping duplicate: ${file.name} matches ${existingName}`,
                );
                continue;
              }
            }
          }
          fingerprintMap.set(fp, file.name);
        } catch {
          // Fingerprinting failed — proceed with upload (dedup is best-effort)
        }
      }

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

    if (skippedDuplicates > 0) {
      console.info(`DDR-067: Skipped ${skippedDuplicates} duplicate file(s)`);
    }

    if (toAdd.length === 0) return 0;

    files.value = [...files.value, ...toAdd];

    // Start uploads in parallel (fire-and-forget per file)
    for (const entry of toAdd) {
      uploadFile(sessionId, entry.name, fileMap.get(entry.name)!);
    }

    return toAdd.length;
  }

  return {
    files,
    error,
    uploadSpeed,
    addFiles,
    updateFile,
    removeFile,
    clearAll,
    resetState,
    getTotalLoaded,
  };
}
