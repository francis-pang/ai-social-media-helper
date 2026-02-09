import type {
  BrowseResponse,
  PickRequest,
  PickResponse,
  TriageStartRequest,
  TriageStartResponse,
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
 * In cloud mode, fetches a presigned S3 URL from the backend.
 * In local mode, returns the direct URL synchronously (wrapped in a Promise).
 */
export async function getFullMediaUrl(pathOrKey: string): Promise<string> {
  if (isCloudMode) {
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

// --- Session Invalidation API (DDR-037) ---

/** Request body for POST /api/session/invalidate. */
export interface InvalidateRequest {
  sessionId: string;
  /** The step from which to invalidate (all downstream state is cleared). */
  fromStep: "selection" | "enhancement" | "grouping" | "download" | "description";
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
