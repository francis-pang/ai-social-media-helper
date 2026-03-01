import { signal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import {
  currentStep,
  navigateBack,
  navigateToStep,
  invalidateDownstream,
  uploadSessionId,
  setStep,
} from "../app";
import { createPoller } from "../hooks/usePolling";
import { ProcessingIndicator } from "./ProcessingIndicator";
import { ActionBar } from "./shared/ActionBar";
import {
  startEnhancement,
  getEnhancementResults,
  submitEnhancementFeedback,
} from "../api/client";
import { groupableMedia } from "./PostGrouper";
import { EnhancementCard, getPhaseLabel, getPhaseColor } from "./enhancement/EnhancementCard";
import { SideBySideComparison } from "./enhancement/SideBySideComparison";
import type { EnhancementResults } from "../types/api";

// --- State ---

const enhancementJobId = signal<string | null>(null);
const results = signal<EnhancementResults | null>(null);
const error = signal<string | null>(null);

/** Currently selected item for side-by-side comparison. */
const selectedItemKey = signal<string | null>(null);

/** Feedback text input for the selected item. */
const feedbackText = signal("");

/** Whether feedback is being processed. */
const feedbackLoading = signal(false);

/** Enhancement keys to process (carried from selection step). */
export const enhancementKeys = signal<string[]>([]);

/**
 * Reset all enhancement state to initial values (DDR-037).
 * Called by the invalidation cascade when a previous step changes.
 */
export function resetEnhancementState() {
  enhancementJobId.value = null;
  results.value = null;
  error.value = null;
  selectedItemKey.value = null;
  feedbackText.value = "";
  feedbackLoading.value = false;
  enhancementKeys.value = [];
}

// --- Polling ---

function startEnhancementJob() {
  const sessionId = uploadSessionId.value;
  if (!sessionId) {
    error.value = "No upload session found. Please upload media first.";
    return;
  }

  const keys = enhancementKeys.value;
  if (keys.length === 0) {
    error.value = "No photos selected for enhancement.";
    return;
  }

  // Invalidate all downstream state (grouping, download, description)
  // when re-running enhancement (DDR-037).
  invalidateDownstream("grouping");

  error.value = null;
  startEnhancement({ sessionId, keys })
    .then((res) => {
      enhancementJobId.value = res.id;
      pollResults(res.id, sessionId);
    })
    .catch((e) => {
      error.value =
        e instanceof Error ? e.message : "Failed to start enhancement";
    });
}

function pollResults(id: string, sessionId: string) {
  createPoller({
    fn: () => getEnhancementResults(id, sessionId),
    intervalMs: 3000,
    isDone: (res) => res.status === "complete" || res.status === "error",
    onPoll: (res) => { results.value = res; },
    onPollError: (e) => {
      error.value = e instanceof Error ? e.message : "Failed to poll results";
      return false;
    },
  }).promise.then((res) => {
    if (res.status === "complete") { setStep("review-enhanced"); }
  });
}

// --- Feedback ---

async function handleFeedback() {
  if (!selectedItemKey.value || !feedbackText.value.trim()) return;

  const sessionId = uploadSessionId.value;
  const jobId = enhancementJobId.value;
  if (!sessionId || !jobId) return;

  feedbackLoading.value = true;
  try {
    await submitEnhancementFeedback(jobId, {
      sessionId,
      key: selectedItemKey.value,
      feedback: feedbackText.value.trim(),
    });
    feedbackText.value = "";

    // Poll for updated results after feedback
    setTimeout(async () => {
      try {
        const res = await getEnhancementResults(jobId, sessionId);
        results.value = res;
      } catch {
        // Ignore polling errors
      }
      feedbackLoading.value = false;
    }, 5000); // Wait 5s for feedback processing
  } catch (e) {
    error.value = e instanceof Error ? e.message : "Feedback submission failed";
    feedbackLoading.value = false;
  }
}

// --- Navigation ---

function handleProceed() {
  // Populate groupable media from enhancement results (DDR-033)
  const items = results.value?.items ?? [];
  groupableMedia.value = items
    .filter((i) => i.phase === "complete" || i.phase === "feedback")
    .map((i) => ({
      key: i.enhancedKey || i.key,
      filename: i.filename,
      thumbnailKey: i.enhancedThumbKey || i.originalThumbKey || i.key,
      type: "Photo" as const,
    }));
  navigateToStep("group-posts");
}

function handleBack() {
  results.value = null;
  enhancementJobId.value = null;
  selectedItemKey.value = null;
  feedbackText.value = "";
  error.value = null;
  navigateBack();
}

// --- Main Component ---

export function EnhancementView() {
  // Start enhancement job when mounting in "enhancing" step
  useEffect(() => {
    if (currentStep.value === "enhancing" && !enhancementJobId.value) {
      startEnhancementJob();
    }
  }, []);

  // --- Processing State (Step 4) — DDR-056 ProcessingIndicator ---
  if (
    currentStep.value === "enhancing" &&
    (!results.value ||
      results.value.status === "pending" ||
      results.value.status === "processing")
  ) {
    const completedCount = results.value?.completedCount ?? 0;
    const totalCount = results.value?.totalCount ?? enhancementKeys.value.length;

    return (
      <ProcessingIndicator
        title="Enhancing Photos"
        description="Each photo goes through a multi-step AI enhancement pipeline: global enhancement, professional quality analysis, and surgical edits."
        status={results.value?.status ?? "pending"}
        jobId={enhancementJobId.value ?? undefined}
        sessionId={uploadSessionId.value ?? undefined}
        pollIntervalMs={3000}
        completedCount={completedCount}
        totalCount={totalCount}
        onCancel={handleBack}
      >
        {/* Per-item status */}
        {results.value?.items && results.value.items.length > 0 && (
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fill, minmax(var(--grid-card-sm), 1fr))",
              gap: "0.5rem",
              maxWidth: "40rem",
              margin: "1rem auto 0",
              textAlign: "left",
            }}
          >
            {results.value.items.map((item) => (
              <div
                key={item.key}
                style={{
                  fontSize: "0.75rem",
                  padding: "0.25rem 0.5rem",
                  borderRadius: "var(--radius)",
                  background: "var(--color-bg)",
                  color: getPhaseColor(item.phase),
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
                title={`${item.filename}: ${getPhaseLabel(item.phase)}`}
              >
                {item.filename}: {getPhaseLabel(item.phase)}
              </div>
            ))}
          </div>
        )}

        {error.value && (
          <div
            style={{
              color: "var(--color-danger)",
              marginTop: "1rem",
              fontSize: "0.875rem",
            }}
          >
            {error.value}
          </div>
        )}
      </ProcessingIndicator>
    );
  }

  // --- Error State ---
  if (results.value?.status === "error") {
    return (
      <div class="card">
        <p style={{ color: "var(--color-danger)" }}>
          Enhancement failed: {results.value.error}
        </p>
        <button
          class="outline"
          onClick={handleBack}
          style={{ marginTop: "1rem" }}
        >
          Back to Selection
        </button>
      </div>
    );
  }

  // --- Review State (Step 5) ---
  const items = results.value?.items || [];
  const completedItems = items.filter(
    (i) => i.phase === "complete" || i.phase === "feedback",
  );
  const errorItems = items.filter((i) => i.phase === "error");
  const selectedItem = items.find((i) => i.key === selectedItemKey.value);

  // Auto-select first item if none selected
  if (!selectedItem && items.length > 0 && !selectedItemKey.value) {
    selectedItemKey.value = items[0]!.key;
  }

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

      {/* Summary bar */}
      <div
        class="card"
        style={{
          marginBottom: "1.5rem",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
        }}
      >
        <div>
          <span style={{ fontSize: "0.875rem" }}>
            <strong style={{ color: "var(--color-success)" }}>
              {completedItems.length} enhanced
            </strong>
            {errorItems.length > 0 && (
              <>
                {" / "}
                <span style={{ color: "var(--color-danger)" }}>
                  {errorItems.length} failed
                </span>
              </>
            )}
            {" / "}
            <span style={{ color: "var(--color-text-secondary)" }}>
              {items.length} total
            </span>
          </span>
        </div>
        <div>
          <span
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
            }}
          >
            Click a photo to view comparison. Give feedback to request changes.
          </span>
        </div>
      </div>

      {/* Photo grid */}
      <div class="card" style={{ marginBottom: "1.5rem" }}>
        <h2 style={{ marginBottom: "1rem" }}>Enhanced Photos</h2>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill, minmax(var(--grid-card-md), 1fr))",
            gap: "0.75rem",
          }}
        >
          {items.map((item) => (
            <EnhancementCard
              key={item.key}
              item={item}
              isSelected={item.key === selectedItemKey.value}
              onClick={() => {
                selectedItemKey.value = item.key;
                feedbackText.value = "";
              }}
            />
          ))}
        </div>
      </div>

      {/* Side-by-side comparison for selected item */}
      {selectedItem && (
        <SideBySideComparison
          item={selectedItem}
          feedbackText={feedbackText.value}
          onFeedbackInput={(text) => { feedbackText.value = text; }}
          feedbackLoading={feedbackLoading.value}
          onSubmitFeedback={handleFeedback}
        />
      )}

      <ActionBar
        left={
          <span style={{ fontSize: "0.875rem" }}>
            {completedItems.length} photo(s) ready for grouping
          </span>
        }
        right={
          <div style={{ display: "flex", gap: "0.75rem" }}>
            <button class="outline" onClick={handleBack}>Back to Selection</button>
            <button class="primary" onClick={handleProceed} disabled={completedItems.length === 0}>
              Proceed to Grouping ({completedItems.length})
            </button>
          </div>
        }
      />
    </div>
  );
}
