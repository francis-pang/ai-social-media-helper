import { signal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { triageJobId, selectedPaths, uploadSessionId, navigateToLanding, navigateBack, setStep } from "../app";
import { ProcessingIndicator } from "./ProcessingIndicator";
import {
  getTriageResults,
  confirmTriage,
  thumbnailUrl,
  isCloudMode,
  isVideoFile,
} from "../api/client";
import { openMediaPlayer } from "./MediaPlayer";
import type { TriageItem, TriageResults } from "../types/api";

const results = signal<TriageResults | null>(null);
const selectedForDeletion = signal<Set<string>>(new Set());
const confirmLoading = signal(false);
const confirmResult = signal<{
  deleted: number;
  errors: string[];
  reclaimedBytes: number;
} | null>(null);
const error = signal<string | null>(null);

/** Get the identifier for a triage item (key in cloud mode, path in local). */
function itemId(item: TriageItem): string {
  return isCloudMode ? (item.key ?? item.path) : item.path;
}

/** Get the thumbnail source for a triage item. */
function itemThumb(item: TriageItem): string {
  if (isCloudMode && item.key) {
    return thumbnailUrl(item.key);
  }
  return thumbnailUrl(item.path);
}

/** Maximum time (ms) to poll before giving up — 15 minutes. */
const POLL_TIMEOUT_MS = 15 * 60 * 1000;

function pollResults(id: string) {
  // Pass sessionId for ownership verification in cloud mode (DDR-028)
  const sessionId = isCloudMode ? uploadSessionId.value ?? undefined : undefined;
  const startTime = Date.now();
  const interval = setInterval(async () => {
    // Give up if we've been polling longer than the timeout.
    if (Date.now() - startTime > POLL_TIMEOUT_MS) {
      clearInterval(interval);
      error.value =
        "Processing is taking too long. Please try again or check your connection.";
      results.value = {
        id,
        status: "error",
        error: "Processing timed out after 15 minutes",
        keep: [],
        discard: [],
      };
      return;
    }
    try {
      const res = await getTriageResults(id, sessionId);
      results.value = res;
      if (res.status === "complete" || res.status === "error") {
        clearInterval(interval);
        setStep("results");
        if (res.discard && res.discard.length > 0) {
          selectedForDeletion.value = new Set(
            res.discard.map((item) => itemId(item)),
          );
        }
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : "Failed to poll results";
      clearInterval(interval);
    }
  }, 2000);

  return () => clearInterval(interval);
}

function toggleDeletion(id: string) {
  const next = new Set(selectedForDeletion.value);
  if (next.has(id)) {
    next.delete(id);
  } else {
    next.add(id);
  }
  selectedForDeletion.value = next;
}

function selectAllDiscard() {
  if (!results.value) return;
  selectedForDeletion.value = new Set(
    results.value.discard.map((item) => itemId(item)),
  );
}

function deselectAll() {
  selectedForDeletion.value = new Set();
}

async function handleConfirmDeletion() {
  if (!triageJobId.value) return;
  confirmLoading.value = true;
  try {
    const ids = Array.from(selectedForDeletion.value);
    // Include sessionId for ownership verification in cloud mode (DDR-028)
    const req = isCloudMode
      ? { deleteKeys: ids, sessionId: uploadSessionId.value }
      : { deletePaths: ids };
    const res = await confirmTriage(triageJobId.value, req);
    confirmResult.value = res;
  } catch (e) {
    error.value = e instanceof Error ? e.message : "Failed to confirm deletion";
  } finally {
    confirmLoading.value = false;
  }
}

function startOver() {
  results.value = null;
  selectedForDeletion.value = new Set();
  confirmResult.value = null;
  error.value = null;
  triageJobId.value = null;
  selectedPaths.value = [];
  uploadSessionId.value = null;
  // In cloud mode, go back to landing page; in local mode, go back to file browser (DDR-042)
  if (isCloudMode) {
    navigateToLanding();
  } else {
    setStep("browse");
  }
}

function handleBack() {
  results.value = null;
  selectedForDeletion.value = new Set();
  confirmResult.value = null;
  error.value = null;
  triageJobId.value = null;
  selectedPaths.value = [];
  uploadSessionId.value = null;
  navigateBack();
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function MediaCard({
  item,
  selectable,
  isSelected,
  onToggle,
}: {
  item: TriageItem;
  selectable: boolean;
  isSelected: boolean;
  onToggle?: () => void;
}) {
  return (
    <div
      onClick={selectable ? onToggle : undefined}
      style={{
        background: isSelected
          ? "rgba(255, 107, 107, 0.1)"
          : "var(--color-bg)",
        border: isSelected
          ? "2px solid var(--color-danger)"
          : "2px solid transparent",
        borderRadius: "var(--radius)",
        overflow: "hidden",
        cursor: selectable ? "pointer" : "default",
      }}
    >
      <div
        onClick={(e) => {
          e.stopPropagation();
          openMediaPlayer(
            itemId(item),
            isVideoFile(item.filename) ? "Video" : "Photo",
            item.filename,
          );
        }}
        style={{
          width: "100%",
          aspectRatio: "1",
          background: "var(--color-surface-hover)",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          position: "relative",
          cursor: "zoom-in",
        }}
      >
        {/* For photos: load thumbnail image; for videos: skip (no ffmpeg in triage pipeline) */}
        {!isVideoFile(item.filename) ? (
          <img
            src={itemThumb(item)}
            alt={item.filename}
            title={item.filename}
            loading="lazy"
            style={{
              width: "100%",
              height: "100%",
              objectFit: "cover",
            }}
            onError={(e) => {
              const img = e.target as HTMLImageElement;
              img.style.display = "none";
              const fallback = img.nextElementSibling as HTMLElement | null;
              if (fallback) fallback.style.display = "flex";
            }}
          />
        ) : null}
        {/* Fallback placeholder: always visible for videos, hidden until onError for photos */}
        <div
          style={{
            display: isVideoFile(item.filename) ? "flex" : "none",
            position: "absolute",
            inset: 0,
            flexDirection: "column",
            alignItems: "center",
            justifyContent: "center",
            color: "var(--color-text-secondary)",
            gap: "0.5rem",
          }}
        >
          <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
            {isVideoFile(item.filename) ? (
              <path d="M15 10l4.553-2.276A1 1 0 0121 8.618v6.764a1 1 0 01-1.447.894L15 14M5 18h8a2 2 0 002-2V8a2 2 0 00-2-2H5a2 2 0 00-2 2v8a2 2 0 002 2z" />
            ) : (
              <path d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
            )}
          </svg>
          <span style={{ fontSize: "0.75rem" }}>
            {isVideoFile(item.filename) ? item.filename : "No preview"}
          </span>
        </div>
        {/* Video play icon overlay */}
        {isVideoFile(item.filename) && (
          <div
            style={{
              position: "absolute",
              bottom: "0.5rem",
              right: "0.5rem",
              background: "rgba(0,0,0,0.65)",
              borderRadius: "4px",
              padding: "0.25rem 0.4rem",
              display: "flex",
              alignItems: "center",
              gap: "0.25rem",
              color: "#fff",
              fontSize: "0.75rem",
              pointerEvents: "none",
            }}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <polygon points="6,4 20,12 6,20" />
            </svg>
            Video
          </div>
        )}
        {selectable && (
          <input
            type="checkbox"
            checked={isSelected}
            onClick={(e) => e.stopPropagation()}
            onChange={onToggle}
            style={{
              position: "absolute",
              top: "0.5rem",
              left: "0.5rem",
              width: "1.25rem",
              height: "1.25rem",
            }}
          />
        )}
      </div>
      <div style={{ padding: "0.5rem" }}>
        <div
          title={item.filename}
          style={{
            fontSize: "0.75rem",
            fontFamily: "var(--font-mono)",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {item.filename}
        </div>
        <div
          style={{
            fontSize: "0.75rem",
            color: "var(--color-text-secondary)",
            marginTop: "0.25rem",
          }}
        >
          {item.reason}
        </div>
      </div>
    </div>
  );
}

export function TriageView() {
  useEffect(() => {
    if (triageJobId.value && !results.value) {
      return pollResults(triageJobId.value);
    }
  }, []);

  // Show deletion confirmation result
  if (confirmResult.value) {
    return (
      <div class="card" style={{ textAlign: "center" }}>
        <div style={{ fontSize: "2rem", marginBottom: "0.5rem" }}>
          {confirmResult.value.errors.length === 0 ? "Done" : "Completed with errors"}
        </div>
        <p>
          Deleted {confirmResult.value.deleted} file(s)
          {!isCloudMode && confirmResult.value.reclaimedBytes > 0 &&
            `, reclaimed ${formatBytes(confirmResult.value.reclaimedBytes)}`
          }.
        </p>
        {confirmResult.value.errors.length > 0 && (
          <div
            style={{
              color: "var(--color-danger)",
              marginTop: "1rem",
              fontSize: "0.875rem",
            }}
          >
            {confirmResult.value.errors.map((err) => (
              <div key={err}>{err}</div>
            ))}
          </div>
        )}
        <button class="primary" onClick={startOver} style={{ marginTop: "1.5rem" }}>
          Start Over
        </button>
      </div>
    );
  }

  // Show processing state (DDR-056 — ProcessingIndicator)
  if (!results.value || results.value.status === "pending" || results.value.status === "processing") {
    const phase = results.value?.phase;

    // Phase-specific title, description, and status label
    let title = "Analyzing Media with AI";
    let description = "Evaluating each media file for quality, duplicates, and content issues";
    let statusLabel: string = results.value?.status ?? "pending";

    if (phase === "uploading") {
      title = "Uploading Media to Gemini";
      description = "Preparing and uploading each media file to the Gemini API for analysis";
      statusLabel = "uploading";
    } else if (phase === "gemini_processing") {
      title = "Processing Videos";
      description = "Gemini is processing uploaded video files — this may take a moment";
      statusLabel = "processing videos";
    } else if (phase === "analyzing") {
      title = "Analyzing Media with AI";
      description = "Sending query to Gemini and waiting for the AI to evaluate your media";
      statusLabel = "analyzing";
    }

    // Upload progress (only during uploading phase)
    const showUploadProgress =
      phase === "uploading" &&
      results.value?.totalFiles != null &&
      results.value.totalFiles > 0;

    return (
      <ProcessingIndicator
        title={title}
        description={description}
        status={statusLabel}
        jobId={triageJobId.value ?? undefined}
        sessionId={uploadSessionId.value ?? undefined}
        pollIntervalMs={2000}
        fileCount={selectedPaths.value.length}
        completedCount={showUploadProgress ? (results.value?.uploadedFiles ?? 0) : undefined}
        totalCount={showUploadProgress ? results.value?.totalFiles : undefined}
        onCancel={startOver}
      />
    );
  }

  // Show error
  if (results.value.status === "error") {
    return (
      <div class="card">
        <p style={{ color: "var(--color-danger)" }}>
          Triage failed: {results.value.error}
        </p>
        <button class="outline" onClick={startOver} style={{ marginTop: "1rem" }}>
          Start Over
        </button>
      </div>
    );
  }

  // Show results (guard against null arrays from Go nil-slice JSON encoding)
  const keep = results.value.keep ?? [];
  const discard = results.value.discard ?? [];

  return (
    <div>
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

      {/* Discard section */}
      <div class="card" style={{ marginBottom: "1.5rem" }}>
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            marginBottom: "1rem",
          }}
        >
          <h2 style={{ color: "var(--color-danger)" }}>
            Discard ({discard.length})
          </h2>
          <div style={{ display: "flex", gap: "0.5rem" }}>
            <button class="outline" onClick={selectAllDiscard}>
              Select all
            </button>
            <button class="outline" onClick={deselectAll}>
              Deselect all
            </button>
          </div>
        </div>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill, minmax(var(--grid-card-md), 1fr))",
            gap: "0.75rem",
          }}
        >
          {discard.map((item) => (
            <MediaCard
              key={itemId(item)}
              item={item}
              selectable={true}
              isSelected={selectedForDeletion.value.has(itemId(item))}
              onToggle={() => toggleDeletion(itemId(item))}
            />
          ))}
        </div>
      </div>

      {/* Keep section */}
      <div class="card" style={{ marginBottom: "1.5rem" }}>
        <h2 style={{ color: "var(--color-success)", marginBottom: "1rem" }}>
          Keep ({keep.length})
        </h2>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill, minmax(var(--grid-card-md), 1fr))",
            gap: "0.75rem",
          }}
        >
          {keep.map((item) => (
            <MediaCard
              key={itemId(item)}
              item={item}
              selectable={false}
              isSelected={false}
            />
          ))}
        </div>
      </div>

      {/* Action bar */}
      <div
        style={{
          position: "sticky",
          bottom: "1rem",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          padding: "1rem 1.5rem",
          background: "var(--color-surface)",
          borderRadius: "var(--radius-lg)",
          border: "1px solid var(--color-border)",
        }}
      >
        <span style={{ fontSize: "0.875rem" }}>
          {selectedForDeletion.value.size} of {discard.length} marked for
          deletion
        </span>
        <div style={{ display: "flex", gap: "0.75rem" }}>
          <button class="outline" onClick={handleBack}>
            Back
          </button>
          <button
            class="danger"
            onClick={handleConfirmDeletion}
            disabled={
              selectedForDeletion.value.size === 0 || confirmLoading.value
            }
          >
            {confirmLoading.value
              ? "Deleting..."
              : `Delete ${selectedForDeletion.value.size} file(s)`}
          </button>
        </div>
      </div>
    </div>
  );
}
