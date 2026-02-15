/**
 * Shared status badge helpers for file upload UI.
 *
 * Replaces duplicated `badgeClass` / `badgeLabel` in MediaUploader, FileUploader,
 * and ProcessingIndicator.
 */

export type UploadStatus = "pending" | "uploading" | "done" | "error";

/** Map a file upload status to a CSS class for the status badge. */
export function badgeClass(status: UploadStatus): string {
  return `status-badge status-badge--${status}`;
}

/** Map a file upload status to a human-readable label. */
export function badgeLabel(status: UploadStatus, progress: number): string {
  switch (status) {
    case "uploading":
      return `Uploading ${progress}%`;
    case "done":
      return "Uploaded";
    case "error":
      return "Failed";
    default:
      return "Waiting";
  }
}
