import { signal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { createPoller } from "../hooks/usePolling";
import { formatBytes } from "../utils/format";
import { triageJobId, selectedPaths, uploadSessionId, navigateToLanding, navigateBack, setStep } from "../app";
import { ProcessingIndicator } from "./ProcessingIndicator";
import {
  getTriageResults,
  confirmTriage,
  isCloudMode,
} from "../api/client";
import { ActionBar } from "./shared/ActionBar";
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
const error = signal<string | null>(null);

function pollResults(id: string) {
  const sessionId = isCloudMode ? uploadSessionId.value ?? undefined : undefined;
  const { promise, abort } = createPoller<TriageResults>({
    fn: () => getTriageResults(id, sessionId),
    intervalMs: 2000,
    timeoutMs: 15 * 60 * 1000,
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
          error: "Processing timed out after 15 minutes",
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
      <ActionBar
        left={
          <span style={{ fontSize: "0.875rem" }}>
            {selectedForDeletion.value.size} of {discard.length} marked for
            deletion
          </span>
        }
        right={
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
        }
      />
    </div>
  );
}
