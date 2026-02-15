import type {
  BrowseResponse,
  PickRequest,
  PickResponse,
  TriageStartRequest,
  TriageStartResponse,
  TriageInitRequest,
  TriageInitResponse,
  TriageUpdateFilesRequest,
  TriageResults,
  TriageConfirmRequest,
  TriageConfirmResponse,
  UploadUrlResponse,
  FullImageResponse,
  SelectionStartRequest,
  SelectionStartResponse,
  SelectionResults,
  EnhancementStartRequest,
  EnhancementStartResponse,
  EnhancementResults,
  EnhancementFeedbackRequest,
  EnhancementFeedbackResponse,
  DownloadStartRequest,
  DownloadStartResponse,
  DownloadResults,
  DescriptionGenerateRequest,
  DescriptionGenerateResponse,
  DescriptionResults,
  DescriptionFeedbackRequest,
  DescriptionFeedbackResponse,
  PublishStartRequest,
  PublishStartResponse,
  PublishStatus,
  MultipartInitRequest,
  MultipartInitResponse,
  MultipartCompleteRequest,
  MultipartCompleteResponse,
  MultipartAbortRequest,
  MultipartCompletedPart,
} from "../types/api";
import { getIdToken } from "../auth/cognito";

/**
 * Whether we're running in cloud mode (served from CloudFront with API Gateway).
 * In local mode, the Vite dev server proxies /api to the Go web server.
 * In cloud mode, CloudFront proxies /api to API Gateway (same-origin).
 *
 * Detection: if VITE_CLOUD_MODE is set, we're in cloud mode. Otherwise local.
 */
export const isCloudMode: boolean = !!import.meta.env.VITE_CLOUD_MODE;

const BASE = "";

async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
  // Attach Cognito JWT token for authenticated API calls (DDR-028)
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...((init?.headers as Record<string, string>) || {}),
  };
  const token = await getIdToken();
  if (token) {
    headers["Authorization"] = `Bearer ${token}`;
  }

  const res = await fetch(`${BASE}${url}`, {
    ...init,
    headers,
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`${res.status}: ${body}`);
  }

  // Guard against CloudFront error-response masking: when the API origin
  // returns 403/404, CloudFront's custom errorResponses may convert it to
  // 200 + index.html (SPA fallback).  Detect this before calling res.json()
  // so the caller gets a meaningful error instead of a cryptic SyntaxError.
  const contentType = res.headers.get("content-type") || "";
  if (!contentType.includes("application/json")) {
    const body = await res.text();
    // HTML body strongly suggests CloudFront served the SPA fallback page
    if (body.trimStart().startsWith("<!DOCTYPE") || body.trimStart().startsWith("<html")) {
      throw new Error(
        `API request to ${url} was intercepted by the CDN — the endpoint may not exist or returned an auth error`,
      );
    }
    throw new Error(
      `Expected JSON from ${url} but received ${contentType || "unknown content-type"}`,
    );
  }

  return res.json() as Promise<T>;
}

// --- Phase 1 (local mode) APIs ---

/** List files and directories at the given path (local mode only). */
export function browse(path: string): Promise<BrowseResponse> {
  return fetchJSON<BrowseResponse>(
    `/api/browse?path=${encodeURIComponent(path)}`,
  );
}

/** Open a native OS file/directory picker dialog (local mode only). */
export function pick(req: PickRequest): Promise<PickResponse> {
  return fetchJSON<PickResponse>("/api/pick", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

// --- Phase 2 (cloud mode) APIs ---

/** Get a presigned S3 PUT URL for uploading a file (cloud mode only). */
export function getUploadUrl(
  sessionId: string,
  filename: string,
  contentType: string,
): Promise<UploadUrlResponse> {
  const params = new URLSearchParams({ sessionId, filename, contentType });
  return fetchJSON<UploadUrlResponse>(`/api/upload-url?${params}`);
}

/** Upload a file directly to S3 using a presigned PUT URL. */
export async function uploadToS3(
  uploadUrl: string,
  file: File,
  onProgress?: (loaded: number, total: number) => void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", uploadUrl, true);
    xhr.setRequestHeader("Content-Type", file.type);

    if (onProgress) {
      xhr.upload.addEventListener("progress", (e) => {
        if (e.lengthComputable) onProgress(e.loaded, e.total);
      });
    }

    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
      } else {
        reject(new Error(`Upload failed: ${xhr.status} ${xhr.statusText}`));
      }
    };
    xhr.onerror = () => reject(new Error("Upload failed: network error"));
    xhr.send(file);
  });
}

// --- S3 Multipart Upload (DDR-054) ---

/** Threshold in bytes: files larger than this use multipart upload. */
export const MULTIPART_THRESHOLD = 10 * 1024 * 1024; // 10 MB

/** Default chunk size for multipart uploads. */
export const MULTIPART_CHUNK_SIZE = 10 * 1024 * 1024; // 10 MB

/** Maximum concurrent chunk uploads (matches browser per-origin connection limit). */
const MULTIPART_CONCURRENCY = 6;

/** Initialize a multipart upload and get presigned part URLs. */
export function initMultipartUpload(
  req: MultipartInitRequest,
): Promise<MultipartInitResponse> {
  return fetchJSON<MultipartInitResponse>("/api/upload-multipart/init", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/** Complete a multipart upload by assembling parts with their ETags. */
export function completeMultipartUpload(
  req: MultipartCompleteRequest,
): Promise<MultipartCompleteResponse> {
  return fetchJSON<MultipartCompleteResponse>(
    "/api/upload-multipart/complete",
    {
      method: "POST",
      body: JSON.stringify(req),
    },
  );
}

/** Abort a multipart upload, cleaning up uploaded parts. */
export function abortMultipartUpload(
  req: MultipartAbortRequest,
): Promise<void> {
  return fetchJSON<void>("/api/upload-multipart/abort", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/**
 * Upload a single chunk to S3 using a presigned PUT URL.
 * Returns the ETag from the response headers (required for CompleteMultipartUpload).
 */
function uploadChunk(
  url: string,
  blob: Blob,
  onProgress?: (loaded: number) => void,
): Promise<string> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", url, true);

    if (onProgress) {
      xhr.upload.addEventListener("progress", (e) => {
        if (e.lengthComputable) onProgress(e.loaded);
      });
    }

    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        const etag = xhr.getResponseHeader("ETag");
        if (!etag) {
          reject(
            new Error(
              "Upload succeeded but ETag header missing (check S3 CORS exposedHeaders)",
            ),
          );
          return;
        }
        resolve(etag);
      } else {
        reject(new Error(`Chunk upload failed: ${xhr.status} ${xhr.statusText}`));
      }
    };
    xhr.onerror = () => reject(new Error("Chunk upload failed: network error"));
    xhr.send(blob);
  });
}

/**
 * Upload a large file to S3 using multipart upload with parallel chunks (DDR-054).
 *
 * 1. Calls init to create the multipart upload and get presigned part URLs.
 * 2. Slices the file into chunks and uploads them in parallel (up to MULTIPART_CONCURRENCY).
 * 3. Tracks aggregate progress across all chunks.
 * 4. On success, calls complete with all ETags.
 * 5. On failure, calls abort to clean up orphaned parts.
 */
export async function uploadToS3Multipart(
  sessionId: string,
  file: File,
  onProgress?: (loaded: number, total: number) => void,
): Promise<string> {
  const chunkSize = MULTIPART_CHUNK_SIZE;
  const fileSize = file.size;

  // 1. Initialize multipart upload and get presigned URLs for all parts.
  const initRes = await initMultipartUpload({
    sessionId,
    filename: file.name,
    contentType: file.type,
    fileSize,
    chunkSize,
  });

  const { uploadId, key, partUrls } = initRes;
  const completedParts: MultipartCompletedPart[] = [];

  // Track bytes uploaded per chunk for aggregate progress reporting.
  const chunkProgress = new Array<number>(partUrls.length).fill(0);

  function reportProgress() {
    if (onProgress) {
      const totalLoaded = chunkProgress.reduce((sum, v) => sum + v, 0);
      onProgress(totalLoaded, fileSize);
    }
  }

  try {
    // 2. Upload chunks in parallel with a concurrency pool.
    let nextIndex = 0;

    async function uploadNext(): Promise<void> {
      while (nextIndex < partUrls.length) {
        const idx = nextIndex++;
        const part = partUrls[idx];
        if (!part) continue;
        const start = idx * chunkSize;
        const end = Math.min(start + chunkSize, fileSize);
        const blob = file.slice(start, end);

        const etag = await uploadChunk(part.url, blob, (loaded) => {
          chunkProgress[idx] = loaded;
          reportProgress();
        });

        // Mark chunk as fully uploaded for progress.
        chunkProgress[idx] = end - start;
        reportProgress();

        completedParts.push({
          partNumber: part.partNumber,
          etag,
        });
      }
    }

    // Launch concurrent workers.
    const workers = Array.from(
      { length: Math.min(MULTIPART_CONCURRENCY, partUrls.length) },
      () => uploadNext(),
    );
    await Promise.all(workers);

    // 3. Sort parts by part number (S3 requires ascending order).
    completedParts.sort((a, b) => a.partNumber - b.partNumber);

    // 4. Complete the multipart upload.
    await completeMultipartUpload({
      sessionId,
      key,
      uploadId,
      parts: completedParts,
    });

    return key;
  } catch (err) {
    // 5. On failure, abort to clean up orphaned parts.
    try {
      await abortMultipartUpload({ sessionId, key, uploadId });
    } catch (abortErr) {
      // eslint-disable-next-line no-console
      console.error("Failed to abort multipart upload:", abortErr);
    }
    throw err;
  }
}

// --- Common APIs (work in both modes) ---

/** Start a triage job for the given file paths or session ID. */
export function startTriage(
  req: TriageStartRequest,
): Promise<TriageStartResponse> {
  return fetchJSON<TriageStartResponse>("/api/triage/start", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/** Initialize a triage job before file uploads complete (DDR-061). */
export function initTriage(
  req: TriageInitRequest,
): Promise<TriageInitResponse> {
  return fetchJSON<TriageInitResponse>("/api/triage/init", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/** Update the expected file count during upload (DDR-061). */
export function updateTriageFiles(
  req: TriageUpdateFilesRequest,
): Promise<{ expectedFileCount: number }> {
  return fetchJSON<{ expectedFileCount: number }>("/api/triage/update-files", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/** Get triage results (poll until status is "complete" or "error"). */
export function getTriageResults(id: string, sessionId?: string): Promise<TriageResults> {
  // In cloud mode, pass sessionId for ownership verification (DDR-028)
  const params = sessionId ? `?sessionId=${encodeURIComponent(sessionId)}` : '';
  return fetchJSON<TriageResults>(`/api/triage/${id}/results${params}`);
}

/** Confirm deletion of selected files. */
export function confirmTriage(
  id: string,
  req: TriageConfirmRequest,
): Promise<TriageConfirmResponse> {
  return fetchJSON<TriageConfirmResponse>(`/api/triage/${id}/confirm`, {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/** Get thumbnail URL for a media file. */
export function thumbnailUrl(pathOrKey: string): string {
  if (isCloudMode) {
    return `${BASE}/api/media/thumbnail?key=${encodeURIComponent(pathOrKey)}`;
  }
  return `${BASE}/api/media/thumbnail?path=${encodeURIComponent(pathOrKey)}`;
}

/** Get full-resolution URL for a media file. */
export function fullImageUrl(pathOrKey: string): string {
  if (isCloudMode) {
    return `${BASE}/api/media/full?key=${encodeURIComponent(pathOrKey)}`;
  }
  return `${BASE}/api/media/full?path=${encodeURIComponent(pathOrKey)}`;
}

/**
 * Resolve the full-resolution URL for a media file.
 * For videos in cloud mode, checks for compressed WebM version first.
 * In cloud mode, fetches a presigned S3 URL from the backend.
 * In local mode, returns the direct URL synchronously (wrapped in a Promise).
 */
export async function getFullMediaUrl(
  pathOrKey: string,
  mediaType?: "Photo" | "Video",
): Promise<string> {
  if (isCloudMode) {
    // For videos, try compressed endpoint first
    if (mediaType === "Video") {
      try {
        const res = await fetchJSON<FullImageResponse>(
          `/api/media/compressed?key=${encodeURIComponent(pathOrKey)}`,
        );
        return res.url;
      } catch (err) {
        // Fallback to original if compressed endpoint fails
        console.warn("Failed to get compressed video, falling back to original", err);
      }
    }
    // For photos or if compressed failed, use full endpoint
    const res = await fetchJSON<FullImageResponse>(
      `/api/media/full?key=${encodeURIComponent(pathOrKey)}`,
    );
    return res.url;
  }
  return fullImageUrl(pathOrKey);
}

/**
 * Open a full image in a new browser tab. In cloud mode, fetches a presigned URL first.
 * In local mode, opens the direct URL.
 */
export async function openFullImage(pathOrKey: string): Promise<void> {
  const url = await getFullMediaUrl(pathOrKey);
  window.open(url, "_blank");
}

/**
 * Check if a filename refers to a video file based on its extension.
 * Used by components that lack an explicit media type field (e.g., TriageView).
 */
export function isVideoFile(filename: string): boolean {
  const ext = filename.split(".").pop()?.toLowerCase() ?? "";
  return ["mp4", "mov", "avi", "mkv", "webm", "m4v", "3gp"].includes(ext);
}

// --- Selection APIs (DDR-030) ---

/** Start a media selection job for the given session. */
export function startSelection(
  req: SelectionStartRequest,
): Promise<SelectionStartResponse> {
  return fetchJSON<SelectionStartResponse>("/api/selection/start", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/** Get selection results (poll until status is "complete" or "error"). */
export function getSelectionResults(
  id: string,
  sessionId: string,
): Promise<SelectionResults> {
  return fetchJSON<SelectionResults>(
    `/api/selection/${id}/results?sessionId=${encodeURIComponent(sessionId)}`,
  );
}

// --- Enhancement APIs (DDR-031) ---

/** Start a photo enhancement job for the given media keys. */
export function startEnhancement(
  req: EnhancementStartRequest,
): Promise<EnhancementStartResponse> {
  return fetchJSON<EnhancementStartResponse>("/api/enhance/start", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/** Get enhancement results (poll until status is "complete" or "error"). */
export function getEnhancementResults(
  id: string,
  sessionId: string,
): Promise<EnhancementResults> {
  return fetchJSON<EnhancementResults>(
    `/api/enhance/${id}/results?sessionId=${encodeURIComponent(sessionId)}`,
  );
}

/** Submit feedback for a specific photo in an enhancement job. */
export function submitEnhancementFeedback(
  id: string,
  req: EnhancementFeedbackRequest,
): Promise<EnhancementFeedbackResponse> {
  return fetchJSON<EnhancementFeedbackResponse>(
    `/api/enhance/${id}/feedback`,
    {
      method: "POST",
      body: JSON.stringify(req),
    },
  );
}

// --- Download APIs (DDR-034) ---

/** Start a download job to create ZIP bundles for a post group. */
export function startDownload(
  req: DownloadStartRequest,
): Promise<DownloadStartResponse> {
  return fetchJSON<DownloadStartResponse>("/api/download/start", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/** Get download results (poll until status is "complete" or "error"). */
export function getDownloadResults(
  id: string,
  sessionId: string,
): Promise<DownloadResults> {
  return fetchJSON<DownloadResults>(
    `/api/download/${id}/results?sessionId=${encodeURIComponent(sessionId)}`,
  );
}

// --- Description APIs (DDR-036) ---

/** Generate an AI Instagram caption for a post group. */
export function generateDescription(
  req: DescriptionGenerateRequest,
): Promise<DescriptionGenerateResponse> {
  return fetchJSON<DescriptionGenerateResponse>("/api/description/generate", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/** Get description generation results (poll until status is "complete" or "error"). */
export function getDescriptionResults(
  id: string,
  sessionId: string,
): Promise<DescriptionResults> {
  return fetchJSON<DescriptionResults>(
    `/api/description/${id}/results?sessionId=${encodeURIComponent(sessionId)}`,
  );
}

/** Submit feedback to regenerate a caption. */
export function submitDescriptionFeedback(
  id: string,
  req: DescriptionFeedbackRequest,
): Promise<DescriptionFeedbackResponse> {
  return fetchJSON<DescriptionFeedbackResponse>(
    `/api/description/${id}/feedback`,
    {
      method: "POST",
      body: JSON.stringify(req),
    },
  );
}

// --- Publish APIs (DDR-040) ---

/** Response from GET /api/health. */
export interface HealthResponse {
  status: string;
  service: string;
  instagramConfigured: boolean;
}

/** Check whether Instagram publishing is configured on the backend. */
export function getHealth(): Promise<HealthResponse> {
  return fetchJSON<HealthResponse>("/api/health");
}

/** Start publishing a post group to Instagram. */
export function startPublish(
  req: PublishStartRequest,
): Promise<PublishStartResponse> {
  return fetchJSON<PublishStartResponse>("/api/publish/start", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/** Get publishing status (poll until status is "published" or "error"). */
export function getPublishStatus(
  id: string,
  sessionId: string,
): Promise<PublishStatus> {
  return fetchJSON<PublishStatus>(
    `/api/publish/${id}/status?sessionId=${encodeURIComponent(sessionId)}`,
  );
}

// --- Session Invalidation API (DDR-037) ---

/** Request body for POST /api/session/invalidate. */
export interface InvalidateRequest {
  sessionId: string;
  /** The step from which to invalidate (all downstream state is cleared). */
  fromStep: "selection" | "enhancement" | "grouping" | "download" | "description" | "publish";
}

/** Response from POST /api/session/invalidate. */
export interface InvalidateResponse {
  invalidated: string[];
}

/**
 * Invalidate downstream state when a user navigates back and re-processes.
 * Clears in-memory jobs and optionally S3 artifacts for steps after fromStep.
 */
export function invalidateSession(
  req: InvalidateRequest,
): Promise<InvalidateResponse> {
  return fetchJSON<InvalidateResponse>("/api/session/invalidate", {
    method: "POST",
    body: JSON.stringify(req),
  });
}
