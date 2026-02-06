/** A file or directory entry returned by the browse API. */
export interface FileEntry {
  name: string;
  path: string;
  isDir: boolean;
  size: number;
  /** MIME type for files, empty for directories. */
  mimeType: string;
}

/** Response from GET /api/browse. */
export interface BrowseResponse {
  path: string;
  parent: string;
  entries: FileEntry[];
}

/** A single triage verdict from the AI. */
export interface TriageItem {
  media: number;
  filename: string;
  path: string;
  saveable: boolean;
  reason: string;
  /** Thumbnail URL: /api/media/thumbnail?path=... */
  thumbnailUrl: string;
}

/** Response from GET /api/triage/:id/results. */
export interface TriageResults {
  id: string;
  status: "pending" | "processing" | "complete" | "error";
  keep: TriageItem[];
  discard: TriageItem[];
  error?: string;
}

/** Request body for POST /api/triage/start. */
export interface TriageStartRequest {
  paths: string[];
  model?: string;
}

/** Response from POST /api/triage/start. */
export interface TriageStartResponse {
  id: string;
}

/** Request body for POST /api/triage/:id/confirm. */
export interface TriageConfirmRequest {
  /** Paths of files the user confirmed for deletion. */
  deletePaths: string[];
}

/** Response from POST /api/triage/:id/confirm. */
export interface TriageConfirmResponse {
  deleted: number;
  errors: string[];
  reclaimedBytes: number;
}
