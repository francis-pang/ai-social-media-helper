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
  /** Local filesystem path (Phase 1 local mode). */
  path: string;
  /** S3 object key (Phase 2 cloud mode). */
  key?: string;
  saveable: boolean;
  reason: string;
  /** Thumbnail URL: /api/media/thumbnail?path=... or ?key=... */
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

/** Request body for POST /api/pick. */
export interface PickRequest {
  mode: "files" | "directory";
}

/** Response from POST /api/pick. */
export interface PickResponse {
  paths: string[];
  canceled: boolean;
}

/** Request body for POST /api/triage/start. */
export interface TriageStartRequest {
  /** Local filesystem paths (Phase 1). */
  paths?: string[];
  /** S3 session ID (Phase 2 — Lambda lists objects with this prefix). */
  sessionId?: string;
  model?: string;
}

/** Response from POST /api/triage/start. */
export interface TriageStartResponse {
  id: string;
}

/** Request body for POST /api/triage/:id/confirm. */
export interface TriageConfirmRequest {
  /** Paths of files the user confirmed for deletion (Phase 1). */
  deletePaths?: string[];
  /** S3 keys of files the user confirmed for deletion (Phase 2). */
  deleteKeys?: string[];
}

/** Response from POST /api/triage/:id/confirm. */
export interface TriageConfirmResponse {
  deleted: number;
  errors: string[];
  reclaimedBytes: number;
}

/** Response from GET /api/upload-url (Phase 2 only). */
export interface UploadUrlResponse {
  uploadUrl: string;
  key: string;
}

/** Response from GET /api/media/full when returning presigned URL (Phase 2). */
export interface FullImageResponse {
  url: string;
}
