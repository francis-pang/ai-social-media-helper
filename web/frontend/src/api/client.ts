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
export function getTriageResults(id: string): Promise<TriageResults> {
  return fetchJSON<TriageResults>(`/api/triage/${id}/results`);
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
 * Open a full image. In cloud mode, fetches a presigned URL first.
 * In local mode, opens the direct URL.
 */
export async function openFullImage(pathOrKey: string): Promise<void> {
  if (isCloudMode) {
    const res = await fetchJSON<FullImageResponse>(
      `/api/media/full?key=${encodeURIComponent(pathOrKey)}`,
    );
    window.open(res.url, "_blank");
  } else {
    window.open(fullImageUrl(pathOrKey), "_blank");
  }
}
