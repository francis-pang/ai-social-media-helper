import { signal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { createPoller } from "../hooks/usePolling";
import { formatBytes } from "../utils/format";
import { triageJobId, selectedPaths, uploadSessionId, fileHandles, navigateToLanding, navigateBack, setStep } from "../app";
import { resetFileBrowserState } from "./FileBrowser";
import { ProcessingIndicator } from "./ProcessingIndicator";
import {
  getTriageResults,
  confirmTriage,
  isCloudMode,
  isVideoFile,
  thumbnailUrl,
} from "../api/client";
import { MediaReviewModal } from "./MediaReviewModal";
import { MediaCard, itemId } from "./TriageMediaCard";
import type { TriageResults } from "../types/api";

const results = signal<TriageResults | null>(null);
const selectedForDeletion = signal<Set<string>>(new Set());
const confirmLoading = signal(false);
const confirmResult = signal<{
  deleted: number;
  errors: string[];
  reclaimedBytes: number;
} | null>(null);
/** DDR-074: Local file deletion results (separate from S3 cleanup). */
const localDeleteResult = signal<{
  deleted: number;
  errors: string[];
} | null>(null);
const error = signal<string | null>(null);
const reasonFilter = signal("all");
const keepExpanded = signal(false);
const reviewModalIndex = signal<number | null>(null);

function pollResults(id: string) {
  const sessionId = isCloudMode ? uploadSessionId.value ?? undefined : undefined;
  const { promise, abort } = createPoller<TriageResults>({
    fn: () => getTriageResults(id, sessionId),
    intervalMs: 2000,
    timeoutMs: 30 * 60 * 1000,
    immediate: true, // pick up already-complete results without a 2s delay
    isDone: (res) => res.status === "complete" || res.status === "error",
    onPoll: (res) => {
      results.value = res;
    },
    onPollError: (e) => {
      error.value = e instanceof Error ? e.message : "Failed to poll results";
      return false;
    },
  });
  promise
    .then((res) => {
      setStep("results");
      if (res.discard && res.discard.length > 0) {
        selectedForDeletion.value = new Set(
          res.discard.map((item) => itemId(item)),
        );
      }
    })
    .catch((err) => {
      if (err instanceof Error && err.message === "Polling timed out") {
        error.value =
          "Processing is taking too long. Please try again or check your connection.";
        results.value = {
          id,
          status: "error",
          error: "Processing timed out after 30 minutes",
          keep: [],
          discard: [],
        };
      }
    });
  return abort;
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
  const discard = results.value.discard ?? [];
  selectedForDeletion.value = new Set(
    discard.map((item) => itemId(item)),
  );
}

function deselectAll() {
  selectedForDeletion.value = new Set();
}

async function handleConfirmDeletion() {
  if (!triageJobId.value) return;
  confirmLoading.value = true;
  localDeleteResult.value = null;
  try {
    const ids = Array.from(selectedForDeletion.value);
    const req = isCloudMode
      ? { deleteKeys: ids, sessionId: uploadSessionId.value }
      : { deletePaths: ids };
    const res = await confirmTriage(triageJobId.value, req);
    confirmResult.value = res;

    // DDR-074: Delete from local filesystem using stored file handles
    if (isCloudMode && fileHandles.value.size > 0) {
      let localDeleted = 0;
      const localErrors: string[] = [];

      for (const id of ids) {
        const filename = id.includes("/") ? id.split("/").pop()! : id;
        const handle = fileHandles.value.get(filename);
        if (!handle) continue;

        try {
          const perm = await handle.requestPermission({ mode: "readwrite" });
          if (perm !== "granted") {
            localErrors.push(`Permission denied: ${filename}`);
            continue;
          }
          await handle.remove();
          localDeleted++;
        } catch (e) {
          const msg = e instanceof Error ? e.message : "Unknown error";
          localErrors.push(`${filename}: ${msg}`);
        }
      }

      localDeleteResult.value = { deleted: localDeleted, errors: localErrors };
    }
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
  localDeleteResult.value = null;
  error.value = null;
  triageJobId.value = null;
  selectedPaths.value = [];
  uploadSessionId.value = null;
  // In cloud mode, go back to landing page; in local mode, go back to file browser (DDR-042)
  if (isCloudMode) {
    navigateToLanding();
  } else {
    resetFileBrowserState();
    setStep("browse");
  }
}

function handleBack() {
  results.value = null;
  selectedForDeletion.value = new Set();
  confirmResult.value = null;
  localDeleteResult.value = null;
  error.value = null;
  triageJobId.value = null;
  selectedPaths.value = [];
  uploadSessionId.value = null;
  navigateBack();
}

export function TriageView() {
  useEffect(() => {
    if (triageJobId.value && !results.value) {
      return pollResults(triageJobId.value);
    }
  }, []);

  // Show deletion confirmation result
  if (confirmResult.value) {
    const serverErrors = confirmResult.value.errors ?? [];
    const localResult = localDeleteResult.value;
    const localErrors = localResult?.errors ?? [];
    const allErrors = [...serverErrors, ...localErrors];
    const hasLocalDelete = localResult !== null;

    return (
      <div class="card" style={{ textAlign: "center" }}>
        <div style={{ fontSize: "2rem", marginBottom: "0.5rem" }}>
          {allErrors.length === 0 ? "Done" : "Completed with errors"}
        </div>
        {hasLocalDelete ? (
          <p>
            Deleted {localResult.deleted} file(s) from your device.
          </p>
        ) : (
          <p>
            Deleted {confirmResult.value.deleted} file(s)
            {!isCloudMode && confirmResult.value.reclaimedBytes > 0 &&
              `, reclaimed ${formatBytes(confirmResult.value.reclaimedBytes)}`
            }.
          </p>
        )}
        {allErrors.length > 0 && (
          <div
            style={{
              color: "var(--color-danger)",
              marginTop: "1rem",
              fontSize: "0.875rem",
            }}
          >
            {allErrors.map((err) => (
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

  // Show processing state — Gemini-only phases (DDR-056, DDR-063)
  if (!results.value || results.value.status === "pending" || results.value.status === "processing") {
    const phase = results.value?.phase;

    // DDR-063: Only show Gemini-specific phases here.
    // File-level processing is now shown on the upload screen (FileUploader).
    let title = "AI Analysis";
    let description = "Preparing media for Gemini AI evaluation";
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
  const reasons = [...new Set(discard.map((i) => i.reason))];
  const filteredDiscard =
    reasonFilter.value === "all"
      ? discard
      : discard.filter((i) => i.reason === reasonFilter.value);

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
        <h2 style={{ display: "flex", alignItems: "center", gap: "0.5rem", marginBottom: "1rem" }}>
          <span>✕ Discard Candidates</span>
          <span
            style={{
              background: "rgba(239, 68, 68, 0.1)",
              color: "var(--color-danger)",
              borderRadius: "999px",
              padding: "0.125rem 0.5rem",
              fontSize: "0.75rem",
              fontWeight: "normal",
            }}
          >
            {discard.length}
          </span>
        </h2>

        <div class="reason-filters" style={{ justifyContent: "space-between", alignItems: "center", marginBottom: "1rem" }}>
          <div style={{ display: "flex", gap: "0.5rem", flexWrap: "wrap" }}>
            <button
              class={reasonFilter.value === "all" ? "reason-pill reason-pill--active" : "reason-pill"}
              onClick={() => { reasonFilter.value = "all"; }}
            >
              All
            </button>
            {reasons.map((r) => (
              <button
                key={r}
                class={reasonFilter.value === r ? "reason-pill reason-pill--active" : "reason-pill"}
                onClick={() => { reasonFilter.value = r; }}
              >
                {r}
              </button>
            ))}
          </div>
          <div style={{ display: "flex", gap: "0.75rem", alignItems: "center", flexShrink: 0 }}>
            <span
              onClick={selectAllDiscard}
              style={{ color: "var(--color-primary)", cursor: "pointer", fontSize: "0.8rem" }}
            >
              Select All
            </span>
            <span style={{ color: "var(--color-text-secondary)" }}>|</span>
            <span
              onClick={deselectAll}
              style={{ color: "var(--color-primary)", cursor: "pointer", fontSize: "0.8rem" }}
            >
              Deselect All
            </span>
          </div>
        </div>

        <div class="masonry-grid">
          {filteredDiscard.map((item, idx) => (
            <div key={itemId(item)} style={{ breakInside: "avoid", marginBottom: "1rem" }}>
              <MediaCard
                item={item}
                selectable={true}
                isSelected={selectedForDeletion.value.has(itemId(item))}
                onToggle={() => toggleDeletion(itemId(item))}
                onReview={() => { reviewModalIndex.value = idx; }}
              />
            </div>
          ))}
        </div>
      </div>

      {/* Keep section */}
      <div class="card" style={{ marginBottom: "1.5rem" }}>
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <h2 style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
            <span>✓ Keep</span>
            <span
              style={{
                background: "rgba(34, 197, 94, 0.1)",
                color: "var(--color-success)",
                borderRadius: "999px",
                padding: "0.125rem 0.5rem",
                fontSize: "0.75rem",
                fontWeight: "normal",
              }}
            >
              {keep.length}
            </span>
          </h2>
          <button
            class="outline"
            onClick={() => { keepExpanded.value = !keepExpanded.value; }}
            style={{ fontSize: "0.8rem" }}
          >
            {keepExpanded.value ? "Collapse" : "Expand"}
          </button>
        </div>
        {!keepExpanded.value ? (
          <p style={{ color: "var(--color-text-secondary)", fontSize: "0.875rem", marginTop: "0.5rem" }}>
            {keep.length} items will be kept
          </p>
        ) : (
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fill, minmax(var(--grid-card-sm), 1fr))",
              gap: "0.75rem",
              marginTop: "1rem",
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
        )}
      </div>

      {/* Sticky action bar */}
      <div class="sticky-action-bar">
        <span style={{ color: "var(--color-text-secondary)", fontSize: "0.875rem" }}>
          Selection Summary: {selectedForDeletion.value.size} items selected for deletion
        </span>
        <span style={{ display: "flex", alignItems: "center", gap: "0.375rem", fontSize: "0.875rem" }}>
          <span
            style={{
              display: "inline-block",
              width: "0.5rem",
              height: "0.5rem",
              borderRadius: "50%",
              background: "var(--color-success)",
            }}
          />
          {keep.length} items will be kept
        </span>
        <div style={{ display: "flex", gap: "0.75rem" }}>
          <button class="outline" onClick={handleBack}>
            Back
          </button>
          <button
            class="danger"
            onClick={handleConfirmDeletion}
            disabled={selectedForDeletion.value.size === 0 || confirmLoading.value}
          >
            {confirmLoading.value ? "Deleting..." : `Delete ${selectedForDeletion.value.size} Selected`}
          </button>
          <button
            class="primary"
            onClick={handleConfirmDeletion}
            disabled={selectedForDeletion.value.size === 0 || confirmLoading.value}
          >
            Confirm & Archive
          </button>
        </div>
      </div>

      {/* Media review modal */}
      {reviewModalIndex.value !== null && (
        <MediaReviewModal
          items={filteredDiscard.map((item) => ({
            id: itemId(item),
            url: item.thumbnailUrl || thumbnailUrl(isCloudMode && item.key ? item.key : item.path),
            filename: item.filename,
            type: (isVideoFile(item.filename) ? "video" : "image") as "image" | "video",
            reason: item.reason,
          }))}
          initialIndex={reviewModalIndex.value}
          onClose={() => { reviewModalIndex.value = null; }}
          onKeep={(id: string) => {
            const next = new Set(selectedForDeletion.value);
            next.delete(id);
            selectedForDeletion.value = next;
          }}
          onDelete={(id: string) => {
            const next = new Set(selectedForDeletion.value);
            next.add(id);
            selectedForDeletion.value = next;
          }}
        />
      )}
    </div>
  );
}
