import type {
  BrowseResponse,
  TriageStartRequest,
  TriageStartResponse,
  TriageResults,
  TriageConfirmRequest,
  TriageConfirmResponse,
} from "../types/api";

/**
 * API base URL. In development, Vite proxies /api to localhost:8080.
 * In production (Phase 2), this can be overridden to a remote endpoint.
 */
const BASE = "";

async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${url}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...init?.headers,
    },
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`${res.status}: ${body}`);
  }
  return res.json() as Promise<T>;
}

/** List files and directories at the given path. */
export function browse(path: string): Promise<BrowseResponse> {
  return fetchJSON<BrowseResponse>(
    `/api/browse?path=${encodeURIComponent(path)}`,
  );
}

/** Start a triage job for the given file paths. */
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
export function thumbnailUrl(path: string): string {
  return `${BASE}/api/media/thumbnail?path=${encodeURIComponent(path)}`;
}
