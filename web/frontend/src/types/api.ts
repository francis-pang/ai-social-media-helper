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
  /** Current sub-phase: uploading | gemini_processing | analyzing */
  phase?: "uploading" | "gemini_processing" | "analyzing";
  /** Total number of supported media files being processed. */
  totalFiles?: number;
  /** Number of files uploaded to Gemini so far (during uploading phase). */
  uploadedFiles?: number;
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

// --- Multipart Upload types (DDR-054) ---

/** Request body for POST /api/upload-multipart/init. */
export interface MultipartInitRequest {
  sessionId: string;
  filename: string;
  contentType: string;
  fileSize: number;
  chunkSize: number;
}

/** A single presigned part URL returned by the init endpoint. */
export interface MultipartPartUrl {
  partNumber: number;
  url: string;
}

/** Response from POST /api/upload-multipart/init. */
export interface MultipartInitResponse {
  uploadId: string;
  key: string;
  partUrls: MultipartPartUrl[];
}

/** A completed part with its ETag, used in the complete request. */
export interface MultipartCompletedPart {
  partNumber: number;
  etag: string;
}

/** Request body for POST /api/upload-multipart/complete. */
export interface MultipartCompleteRequest {
  sessionId: string;
  key: string;
  uploadId: string;
  parts: MultipartCompletedPart[];
}

/** Response from POST /api/upload-multipart/complete. */
export interface MultipartCompleteResponse {
  key: string;
}

/** Request body for POST /api/upload-multipart/abort. */
export interface MultipartAbortRequest {
  sessionId: string;
  key: string;
  uploadId: string;
}

/** Response from GET /api/media/full when returning presigned URL (Phase 2). */
export interface FullImageResponse {
  url: string;
}

// --- Selection types (DDR-030) ---

/** Request body for POST /api/selection/start. */
export interface SelectionStartRequest {
  sessionId: string;
  tripContext: string;
  model?: string;
}

/** Response from POST /api/selection/start. */
export interface SelectionStartResponse {
  id: string;
}

/** A media item selected by the AI. */
export interface SelectionItem {
  rank: number;
  media: number;
  filename: string;
  key: string;
  type: "Photo" | "Video";
  scene: string;
  justification: string;
  comparisonNote?: string;
  thumbnailUrl: string;
}

/** A media item excluded by the AI, with a reason. */
export interface ExcludedItem {
  media: number;
  filename: string;
  key: string;
  reason: string;
  category: "near-duplicate" | "quality-issue" | "content-mismatch" | "redundant-scene";
  duplicateOf?: string;
  thumbnailUrl: string;
}

/** A scene group detected by the AI. */
export interface SelectionSceneGroup {
  name: string;
  gps?: string;
  timeRange?: string;
  items: SelectionSceneGroupItem[];
}

/** A media item within a scene group. */
export interface SelectionSceneGroupItem {
  media: number;
  filename: string;
  key: string;
  type: "Photo" | "Video";
  selected: boolean;
  description: string;
  thumbnailUrl: string;
}

/** Response from GET /api/selection/{id}/results. */
export interface SelectionResults {
  id: string;
  status: "pending" | "processing" | "complete" | "error";
  selected: SelectionItem[] | null;
  excluded: ExcludedItem[] | null;
  sceneGroups: SelectionSceneGroup[] | null;
  error?: string;
}

// --- Enhancement types (DDR-031) ---

/** Request body for POST /api/enhance/start. */
export interface EnhancementStartRequest {
  sessionId: string;
  keys: string[];
}

/** Response from POST /api/enhance/start. */
export interface EnhancementStartResponse {
  id: string;
}

/** Analysis of what further improvements are needed (Phase 2 output). */
export interface AnalysisResult {
  overallAssessment: string;
  remainingImprovements: ImprovementItem[];
  professionalScore: number;
  targetScore: number;
  noFurtherEditsNeeded: boolean;
}

/** A single improvement recommendation from analysis. */
export interface ImprovementItem {
  type: string;
  description: string;
  region: string;
  impact: "high" | "medium" | "low";
  imagenSuitable: boolean;
  editInstruction: string;
}

/** A feedback entry recording one round of user feedback. */
export interface FeedbackEntry {
  userFeedback: string;
  modelResponse: string;
  method: "gemini" | "imagen";
  success: boolean;
}

/** A single photo enhancement result item. */
export interface EnhancementItem {
  key: string;
  filename: string;
  phase: "initial" | "phase1" | "phase2" | "phase3" | "feedback" | "complete" | "error";
  originalKey: string;
  enhancedKey: string;
  originalThumbKey: string;
  enhancedThumbKey: string;
  phase1Text: string;
  analysis?: AnalysisResult;
  imagenEdits: number;
  feedbackHistory: FeedbackEntry[];
  error?: string;
}

/** Response from GET /api/enhance/{id}/results. */
export interface EnhancementResults {
  id: string;
  status: "pending" | "processing" | "complete" | "error";
  items: EnhancementItem[] | null;
  totalCount: number;
  completedCount: number;
  error?: string;
}

/** Request body for POST /api/enhance/{id}/feedback. */
export interface EnhancementFeedbackRequest {
  sessionId: string;
  key: string;
  feedback: string;
}

/** Response from POST /api/enhance/{id}/feedback. */
export interface EnhancementFeedbackResponse {
  status: string;
}

// --- Download types (DDR-034) ---

/** Request body for POST /api/download/start. */
export interface DownloadStartRequest {
  sessionId: string;
  keys: string[];
  groupLabel: string;
}

/** Response from POST /api/download/start. */
export interface DownloadStartResponse {
  id: string;
}

/** A single ZIP bundle in a download job. */
export interface DownloadBundle {
  /** Bundle type: "images" or "videos". */
  type: "images" | "videos";
  /** Display filename: e.g., "Tokyo Day 1-images.zip". */
  name: string;
  /** S3 key of the created ZIP (populated on completion). */
  zipKey: string;
  /** Presigned GET URL for downloading (populated on completion). */
  downloadUrl: string;
  /** Number of files in this bundle. */
  fileCount: number;
  /** Total size of original files in bytes. */
  totalSize: number;
  /** Size of the ZIP file in bytes (populated on completion). */
  zipSize: number;
  /** Bundle creation status. */
  status: "pending" | "processing" | "complete" | "error";
  /** Error message if status is "error". */
  error?: string;
}

/** Response from GET /api/download/{id}/results. */
export interface DownloadResults {
  id: string;
  status: "pending" | "processing" | "complete" | "error";
  bundles: DownloadBundle[] | null;
  error?: string;
}

// --- Description types (DDR-036) ---

/** Request body for POST /api/description/generate. */
export interface DescriptionGenerateRequest {
  sessionId: string;
  keys: string[];
  groupLabel: string;
  tripContext: string;
}

/** Response from POST /api/description/generate. */
export interface DescriptionGenerateResponse {
  id: string;
}

/** Response from GET /api/description/{id}/results. */
export interface DescriptionResults {
  id: string;
  status: "pending" | "processing" | "complete" | "error";
  caption?: string;
  hashtags?: string[];
  locationTag?: string;
  feedbackRound: number;
  error?: string;
}

/** Request body for POST /api/description/{id}/feedback. */
export interface DescriptionFeedbackRequest {
  sessionId: string;
  feedback: string;
}

/** Response from POST /api/description/{id}/feedback. */
export interface DescriptionFeedbackResponse {
  status: string;
}

// --- Publish types (DDR-040) ---

/** Request body for POST /api/publish/start. */
export interface PublishStartRequest {
  sessionId: string;
  groupId: string;
  keys: string[];
  caption: string;
  hashtags: string[];
}

/** Response from POST /api/publish/start. */
export interface PublishStartResponse {
  id: string;
}

/** Progress info for a publish job. */
export interface PublishProgress {
  completed: number;
  total: number;
}

/** Response from GET /api/publish/{id}/status. */
export interface PublishStatus {
  id: string;
  status: "pending" | "creating_containers" | "processing_videos" | "creating_carousel" | "publishing" | "published" | "error";
  phase: string;
  progress: PublishProgress;
  instagramPostId?: string;
  error?: string;
}

// --- Post Grouping types (DDR-033) ---

/** A post group — a collection of media items destined for one Instagram carousel or download bundle. */
export interface PostGroup {
  /** Unique identifier for this group. */
  id: string;
  /** Descriptive label for the group — used for organization and as context for AI caption generation. */
  label: string;
  /** S3 keys of enhanced media items in this group. */
  keys: string[];
}

/** A media item available for grouping — carries display info from enhancement results. */
export interface GroupableMediaItem {
  /** S3 key (enhanced version if available, otherwise original). */
  key: string;
  /** Original filename for display. */
  filename: string;
  /** Thumbnail S3 key for display. */
  thumbnailKey: string;
  /** Media type. */
  type: "Photo" | "Video";
}
