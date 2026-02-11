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
import { ProcessingIndicator } from "./ProcessingIndicator";
import {
  startEnhancement,
  getEnhancementResults,
  submitEnhancementFeedback,
  thumbnailUrl,
} from "../api/client";
import { openMediaPlayer } from "./MediaPlayer";
import { groupableMedia } from "./PostGrouper";
import type { EnhancementItem, EnhancementResults } from "../types/api";

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
  const interval = setInterval(async () => {
    try {
      const res = await getEnhancementResults(id, sessionId);
      results.value = res;
      if (res.status === "complete" || res.status === "error") {
        clearInterval(interval);
        if (res.status === "complete") {
          setStep("review-enhanced");
        }
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : "Failed to poll results";
      clearInterval(interval);
    }
  }, 3000);
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

// --- Helper functions ---

function getPhaseLabel(phase: string): string {
  switch (phase) {
    case "initial":
      return "Queued";
    case "phase1":
      return "Global Enhancement";
    case "phase2":
      return "Analyzing";
    case "phase3":
      return "Surgical Edits";
    case "feedback":
      return "Feedback Applied";
    case "complete":
      return "Done";
    case "error":
      return "Error";
    default:
      return phase;
  }
}

function getPhaseColor(phase: string): string {
  switch (phase) {
    case "complete":
    case "feedback":
      return "var(--color-success)";
    case "error":
      return "var(--color-danger)";
    case "initial":
      return "var(--color-text-secondary)";
    default:
      return "var(--color-primary)";
  }
}

// --- Sub-components ---

function EnhancementCard({
  item,
  isSelected,
  onClick,
}: {
  item: EnhancementItem;
  isSelected: boolean;
  onClick: () => void;
}) {
  const hasEnhanced = !!item.enhancedThumbKey;
  const thumbSrc = hasEnhanced
    ? thumbnailUrl(item.enhancedThumbKey)
    : thumbnailUrl(item.originalThumbKey || item.key);

  return (
    <div
      onClick={onClick}
      style={{
        background: isSelected
          ? "rgba(108, 140, 255, 0.08)"
          : "var(--color-bg)",
        borderRadius: "var(--radius)",
        overflow: "hidden",
        border: isSelected
          ? "2px solid var(--color-primary)"
          : "2px solid transparent",
        cursor: "pointer",
        transition: "border-color 0.15s",
      }}
    >
      {/* Thumbnail */}
      <div
        style={{
          width: "100%",
          aspectRatio: "1",
          background: "var(--color-surface-hover)",
          position: "relative",
        }}
      >
        <img
          src={thumbSrc}
          alt={item.filename}
          loading="lazy"
          style={{
            width: "100%",
            height: "100%",
            objectFit: "cover",
          }}
          onError={(e) => {
            (e.target as HTMLImageElement).style.display = "none";
          }}
        />
        {/* Phase badge */}
        <span
          style={{
            position: "absolute",
            top: "0.375rem",
            right: "0.375rem",
            fontSize: "0.5625rem",
            padding: "0.125rem 0.375rem",
            borderRadius: "4px",
            background: getPhaseColor(item.phase),
            color: "#fff",
            fontWeight: 600,
          }}
        >
          {getPhaseLabel(item.phase)}
        </span>
        {/* Score badge (if analysis available) */}
        {item.analysis && (
          <span
            style={{
              position: "absolute",
              top: "0.375rem",
              left: "0.375rem",
              fontSize: "0.6875rem",
              fontWeight: 700,
              padding: "0.125rem 0.375rem",
              borderRadius: "4px",
              background:
                item.analysis.professionalScore >= 8.5
                  ? "rgba(81, 207, 102, 0.9)"
                  : "rgba(255, 193, 7, 0.9)",
              color: "#fff",
            }}
          >
            {item.analysis.professionalScore.toFixed(1)}
          </span>
        )}
        {/* Imagen edits indicator */}
        {item.imagenEdits > 0 && (
          <span
            style={{
              position: "absolute",
              bottom: "0.375rem",
              left: "0.375rem",
              fontSize: "0.5625rem",
              padding: "0.125rem 0.375rem",
              borderRadius: "4px",
              background: "rgba(108, 140, 255, 0.85)",
              color: "#fff",
              fontWeight: 600,
            }}
          >
            +{item.imagenEdits} surgical
          </span>
        )}
      </div>

      {/* Info */}
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
        {item.error && (
          <div
            style={{
              fontSize: "0.625rem",
              color: "var(--color-danger)",
              marginTop: "0.25rem",
            }}
          >
            {item.error}
          </div>
        )}
      </div>
    </div>
  );
}

function SideBySideComparison({ item }: { item: EnhancementItem }) {
  const originalThumb = thumbnailUrl(item.originalThumbKey || item.key);
  const enhancedThumb = item.enhancedThumbKey
    ? thumbnailUrl(item.enhancedThumbKey)
    : null;

  return (
    <div class="card" style={{ marginBottom: "1.5rem" }}>
      <h3
        style={{
          marginBottom: "1rem",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
        }}
      >
        <span>{item.filename}</span>
        <span
          style={{
            fontSize: "0.75rem",
            color: getPhaseColor(item.phase),
            fontWeight: 600,
          }}
        >
          {getPhaseLabel(item.phase)}
        </span>
      </h3>

      {/* Side-by-side images */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: enhancedThumb ? "1fr 1fr" : "1fr",
          gap: "1rem",
          marginBottom: "1rem",
        }}
      >
        {/* Original */}
        <div>
          <div
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              marginBottom: "0.375rem",
              textAlign: "center",
            }}
          >
            Original
          </div>
          <div
            style={{
              background: "var(--color-surface-hover)",
              borderRadius: "var(--radius)",
              overflow: "hidden",
              aspectRatio: "4/3",
            }}
          >
            <img
              src={originalThumb}
              alt={`Original: ${item.filename}`}
              onClick={() => openMediaPlayer(item.originalKey, "Photo", item.filename)}
              style={{
                width: "100%",
                height: "100%",
                objectFit: "contain",
                cursor: "zoom-in",
              }}
              onError={(e) => {
                (e.target as HTMLImageElement).style.display = "none";
              }}
            />
          </div>
        </div>

        {/* Enhanced */}
        {enhancedThumb && (
          <div>
            <div
              style={{
                fontSize: "0.75rem",
                color: "var(--color-text-secondary)",
                marginBottom: "0.375rem",
                textAlign: "center",
              }}
            >
              Enhanced
              {item.analysis && (
                <span
                  style={{
                    marginLeft: "0.375rem",
                    color:
                      item.analysis.professionalScore >= 8.5
                        ? "var(--color-success)"
                        : "var(--color-warning, #ffc107)",
                  }}
                >
                  (Score: {item.analysis.professionalScore.toFixed(1)})
                </span>
              )}
            </div>
            <div
              style={{
                background: "var(--color-surface-hover)",
                borderRadius: "var(--radius)",
                overflow: "hidden",
                aspectRatio: "4/3",
              }}
            >
              <img
                src={enhancedThumb}
                alt={`Enhanced: ${item.filename}`}
                onClick={() => openMediaPlayer(item.enhancedKey, "Photo", `${item.filename} (enhanced)`)}
                style={{
                  width: "100%",
                  height: "100%",
                  objectFit: "contain",
                  cursor: "zoom-in",
                }}
                onError={(e) => {
                  (e.target as HTMLImageElement).style.display = "none";
                }}
              />
            </div>
          </div>
        )}
      </div>

      {/* Enhancement details */}
      {item.phase1Text && (
        <div style={{ marginBottom: "0.75rem" }}>
          <div
            style={{
              fontSize: "0.75rem",
              fontWeight: 600,
              color: "var(--color-text-secondary)",
              marginBottom: "0.25rem",
            }}
          >
            Changes Applied:
          </div>
          <div
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              lineHeight: 1.5,
              background: "var(--color-bg)",
              padding: "0.5rem 0.75rem",
              borderRadius: "var(--radius)",
            }}
          >
            {item.phase1Text}
          </div>
        </div>
      )}

      {/* Analysis details */}
      {item.analysis && !item.analysis.noFurtherEditsNeeded && (
        <div style={{ marginBottom: "0.75rem" }}>
          <div
            style={{
              fontSize: "0.75rem",
              fontWeight: 600,
              color: "var(--color-text-secondary)",
              marginBottom: "0.25rem",
            }}
          >
            Analysis:
          </div>
          <div
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              lineHeight: 1.5,
              background: "var(--color-bg)",
              padding: "0.5rem 0.75rem",
              borderRadius: "var(--radius)",
            }}
          >
            <div>{item.analysis.overallAssessment}</div>
            {item.analysis.remainingImprovements.length > 0 && (
              <ul
                style={{
                  margin: "0.375rem 0 0 0",
                  paddingLeft: "1.25rem",
                }}
              >
                {item.analysis.remainingImprovements.map((imp, i) => (
                  <li key={i} style={{ marginBottom: "0.25rem" }}>
                    <span
                      style={{
                        fontWeight: 600,
                        color:
                          imp.impact === "high"
                            ? "var(--color-danger)"
                            : "var(--color-text-secondary)",
                      }}
                    >
                      [{imp.impact}]
                    </span>{" "}
                    {imp.description}
                    {imp.imagenSuitable && (
                      <span
                        style={{
                          fontSize: "0.625rem",
                          color: "var(--color-primary)",
                          marginLeft: "0.375rem",
                        }}
                      >
                        (surgical edit)
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}

      {/* Feedback history */}
      {item.feedbackHistory && item.feedbackHistory.length > 0 && (
        <div style={{ marginBottom: "0.75rem" }}>
          <div
            style={{
              fontSize: "0.75rem",
              fontWeight: 600,
              color: "var(--color-text-secondary)",
              marginBottom: "0.25rem",
            }}
          >
            Feedback History:
          </div>
          {item.feedbackHistory.map((fb, i) => (
            <div
              key={i}
              style={{
                fontSize: "0.75rem",
                background: "var(--color-bg)",
                padding: "0.375rem 0.75rem",
                borderRadius: "var(--radius)",
                marginBottom: "0.375rem",
                borderLeft: `3px solid ${fb.success ? "var(--color-success)" : "var(--color-danger)"}`,
              }}
            >
              <div style={{ fontWeight: 600 }}>You: {fb.userFeedback}</div>
              <div style={{ color: "var(--color-text-secondary)" }}>
                AI ({fb.method}): {fb.modelResponse}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Feedback input */}
      {(item.phase === "complete" || item.phase === "feedback") && (
        <div
          style={{
            display: "flex",
            gap: "0.5rem",
            alignItems: "flex-start",
          }}
        >
          <textarea
            value={feedbackText.value}
            onInput={(e) => {
              feedbackText.value = (e.target as HTMLTextAreaElement).value;
            }}
            placeholder='Give feedback, e.g., "make the sky more blue", "remove the trash can on the right"...'
            style={{
              flex: 1,
              minHeight: "2.5rem",
              resize: "vertical",
              fontSize: "0.8125rem",
            }}
            disabled={feedbackLoading.value}
          />
          <button
            class="primary"
            onClick={handleFeedback}
            disabled={!feedbackText.value.trim() || feedbackLoading.value}
            style={{ whiteSpace: "nowrap" }}
          >
            {feedbackLoading.value ? "Processing..." : "Apply Feedback"}
          </button>
        </div>
      )}
    </div>
  );
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
              gridTemplateColumns: "repeat(auto-fill, minmax(120px, 1fr))",
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
                  fontSize: "0.6875rem",
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
            gridTemplateColumns: "repeat(auto-fill, minmax(140px, 1fr))",
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
      {selectedItem && <SideBySideComparison item={selectedItem} />}

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
          {completedItems.length} photo(s) ready for grouping
        </span>
        <div style={{ display: "flex", gap: "0.75rem" }}>
          <button class="outline" onClick={handleBack}>
            Back to Selection
          </button>
          <button
            class="primary"
            onClick={handleProceed}
            disabled={completedItems.length === 0}
          >
            Proceed to Grouping ({completedItems.length})
          </button>
        </div>
      </div>
    </div>
  );
}
