import { signal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { createPoller } from "../hooks/usePolling";
import { ProcessingIndicator } from "./ProcessingIndicator";
import { ActionBar } from "./shared/ActionBar";
import {
  startFBPrep,
  getFBPrepResults,
  submitFBPrepFeedback,
  thumbnailUrl,
} from "../api/client";
import { navigateBack, uploadSessionId, economyMode } from "../app";
import type { FBPrepJob, FBPrepItem } from "../types/api";

// --- State ---

/** Media keys to process (set by parent before navigating to FB Prep). */
export const fbPrepMediaKeys = signal<string[]>([]);

const jobId = signal<string | null>(null);
const results = signal<FBPrepJob | null>(null);
const error = signal<string | null>(null);

/** Item index for which we're showing the feedback form. */
const selectedItemIndex = signal<number | null>(null);

/** Feedback text input for regeneration. */
const feedbackText = signal("");

/** Whether feedback is being processed. */
const feedbackLoading = signal(false);

/** Copy status: which field was just copied (for "Copied!" display). */
const copyStatus = signal<{ itemIndex: number; field: string } | null>(null);

/**
 * Reset all FB prep state.
 */
export function resetFBPrepState() {
  jobId.value = null;
  results.value = null;
  error.value = null;
  selectedItemIndex.value = null;
  feedbackText.value = "";
  feedbackLoading.value = false;
  copyStatus.value = null;
  fbPrepMediaKeys.value = [];
}

// --- Polling ---

function pollResults(id: string, sessionId: string) {
  createPoller({
    fn: () => getFBPrepResults(id, sessionId),
    intervalMs: 2000,
    timeoutMs: 60000,
    immediate: true,
    isDone: (res) => res.status === "complete" || res.status === "error",
    onPoll: (res) => {
      results.value = res;
    },
    onPollError: (e) => {
      error.value = e instanceof Error ? e.message : "Failed to poll results";
      return false;
    },
  }).promise.then((res) => {
    if (res.status === "error") {
      error.value = res.error ?? "Processing failed";
    }
    feedbackLoading.value = false;
  });
}

// --- Copy ---

function copyToClipboard(itemIndex: number, field: string, text: string) {
  navigator.clipboard.writeText(text).then(() => {
    copyStatus.value = { itemIndex, field };
    setTimeout(() => {
      copyStatus.value = null;
    }, 2000);
  });
}

// --- Feedback ---

async function handleRegenerate() {
  const idx = selectedItemIndex.value;
  const sessionId = uploadSessionId.value;
  const jid = jobId.value;
  if (idx === null || !feedbackText.value.trim() || !sessionId || !jid) return;

  feedbackLoading.value = true;
  try {
    await submitFBPrepFeedback(jid, {
      sessionId,
      itemIndex: idx,
      feedback: feedbackText.value.trim(),
    });
    feedbackText.value = "";
    selectedItemIndex.value = null;

    // Poll for regenerated results
    pollResults(jid, sessionId);
  } catch (err) {
    error.value = err instanceof Error ? err.message : "Feedback failed";
    feedbackLoading.value = false;
  }
}

// --- Helpers ---

function getConfidenceColor(confidence: string): string {
  const c = confidence.toLowerCase();
  if (c === "high") return "var(--color-success)";
  if (c === "medium") return "var(--color-warning)";
  return "var(--color-text-secondary)";
}

function CopyButton({
  itemIndex,
  field,
  text,
}: {
  itemIndex: number;
  field: string;
  text: string;
}) {
  const isCopied =
    copyStatus.value?.itemIndex === itemIndex && copyStatus.value?.field === field;
  return (
    <button
      class="outline"
      style={{ fontSize: "0.75rem", padding: "0.25rem 0.5rem" }}
      onClick={() => copyToClipboard(itemIndex, field, text)}
      disabled={!text}
    >
      {isCopied ? "Copied!" : "Copy"}
    </button>
  );
}

// --- Sub-components ---

function FBPrepItemCard({
  item,
  onRegenerateClick,
}: {
  item: FBPrepItem;
  onRegenerateClick: () => void;
}) {
  const key = item.s3_key || (item as unknown as { key?: string }).key;
  const thumbKey = key ?? item.s3_key;

  return (
    <div
      class="card"
      style={{
        marginBottom: "1rem",
        display: "grid",
        gridTemplateColumns: "minmax(0, 10rem) 1fr",
        gap: "1rem",
        alignItems: "start",
      }}
    >
      {/* Thumbnail */}
      <div
        style={{
          aspectRatio: "1",
          borderRadius: "var(--radius)",
          overflow: "hidden",
          background: "var(--color-surface-hover)",
        }}
      >
        <img
          src={thumbnailUrl(thumbKey)}
          alt=""
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
      </div>

      {/* Content */}
      <div style={{ minWidth: 0 }}>
        {/* Caption */}
        <div style={{ marginBottom: "0.75rem" }}>
          <div
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              marginBottom: "0.25rem",
            }}
          >
            Caption
          </div>
          <div
            style={{
              display: "flex",
              alignItems: "flex-start",
              gap: "0.5rem",
            }}
          >
            <div
              class="text-block"
              style={{
                flex: 1,
                fontSize: "0.875rem",
                lineHeight: 1.5,
                whiteSpace: "pre-wrap",
              }}
            >
              {item.caption || "—"}
            </div>
            <CopyButton
              itemIndex={item.item_index}
              field="caption"
              text={item.caption}
            />
          </div>
        </div>

        {/* Location */}
        <div style={{ marginBottom: "0.75rem" }}>
          <div
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              marginBottom: "0.25rem",
            }}
          >
            Location
          </div>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "0.5rem",
            }}
          >
            <span style={{ fontSize: "0.875rem" }}>
              {item.location_tag || "—"}
            </span>
            <CopyButton
              itemIndex={item.item_index}
              field="location"
              text={item.location_tag}
            />
          </div>
        </div>

        {/* Date/Time */}
        <div style={{ marginBottom: "0.75rem" }}>
          <div
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              marginBottom: "0.25rem",
            }}
          >
            Date/Time
          </div>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "0.5rem",
            }}
          >
            <span style={{ fontSize: "0.875rem" }}>
              {item.date_timestamp || "—"}
            </span>
            <CopyButton
              itemIndex={item.item_index}
              field="datetime"
              text={item.date_timestamp}
            />
          </div>
        </div>

        {/* Location Confidence */}
        {item.location_confidence && (
          <div
            style={{
              marginBottom: "0.75rem",
              fontSize: "0.75rem",
              color: getConfidenceColor(item.location_confidence),
            }}
          >
            Location confidence: {item.location_confidence}
          </div>
        )}

        {/* Regenerate */}
        <button
          class="outline"
          style={{ fontSize: "0.875rem" }}
          onClick={onRegenerateClick}
        >
          Regenerate
        </button>
      </div>
    </div>
  );
}

// --- Main Component ---

export function FBPrepView() {
  const keys = fbPrepMediaKeys.value;
  const sessionId = uploadSessionId.value;
  const job = results.value;
  const err = error.value;

  // Start job when mounting with keys and no job yet
  useEffect(() => {
    if (keys.length === 0 || !sessionId) return;
    if (jobId.value) return;

    error.value = null;
    startFBPrep({
      sessionId,
      mediaItems: keys.map((k) => ({ key: k })),
      economyMode: economyMode.value,
    })
      .then((res) => {
        jobId.value = res.id;
        pollResults(res.id, sessionId);
      })
      .catch((e) => {
        error.value =
          e instanceof Error ? e.message : "Failed to start FB prep";
      });
  }, [keys.length, sessionId]);

  // No media
  if (keys.length === 0) {
    return (
      <div class="card" style={{ textAlign: "center", padding: "2rem" }}>
        <p style={{ color: "var(--color-text-secondary)" }}>
          No media selected for Facebook prep.
        </p>
        <button class="outline" onClick={() => navigateBack()}>
          Back
        </button>
      </div>
    );
  }

  // Loading state
  if (
    !job ||
    job.status === "pending" ||
    job.status === "processing"
  ) {
    const itemCount = job?.items?.length ?? 0;
    const totalCount = keys.length;

    return (
      <ProcessingIndicator
        title="Preparing for Facebook"
        description="Generating captions, location tags, and timestamps for each item."
        status={job?.status ?? "pending"}
        jobId={jobId.value ?? undefined}
        sessionId={sessionId ?? undefined}
        pollIntervalMs={2000}
        completedCount={itemCount}
        totalCount={totalCount}
        onCancel={() => navigateBack()}
      />
    );
  }

  // Error state
  if (job.status === "error" || err) {
    return (
      <div class="card" style={{ marginBottom: "1rem" }}>
        <div
          style={{
            color: "var(--color-danger)",
            marginBottom: "1rem",
            fontSize: "0.875rem",
          }}
        >
          {job.error ?? err ?? "Processing failed"}
        </div>
        <button
          class="outline"
          onClick={() => {
            jobId.value = null;
            results.value = null;
            error.value = null;
          }}
        >
          Try Again
        </button>
      </div>
    );
  }

  // Complete state — per-item cards
  const items = job.items ?? [];
  const selectedIdx = selectedItemIndex.value;

  return (
    <div>
      {/* Session header */}
      <div
        class="card"
        style={{
          marginBottom: "1.5rem",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
        }}
      >
        <span style={{ fontSize: "0.875rem" }}>
          <strong>{items.length}</strong> item{items.length !== 1 ? "s" : ""}{" "}
          ready
        </span>
        <span
          style={{
            fontSize: "0.75rem",
            color: "var(--color-text-secondary)",
          }}
        >
          Status: {job.status}
        </span>
      </div>

      {/* Per-item cards */}
      {items.map((item) => (
        <div key={item.item_index}>
          <FBPrepItemCard
            item={item}
            onRegenerateClick={() => {
              selectedItemIndex.value =
                selectedItemIndex.value === item.item_index
                  ? null
                  : item.item_index;
              feedbackText.value = "";
            }}
          />

          {/* Inline feedback form when Regenerate clicked */}
          {selectedIdx === item.item_index && (
            <div
              class="card"
              style={{
                marginBottom: "1rem",
                marginLeft: "11rem",
                background: "var(--color-bg)",
              }}
            >
              <div
                style={{
                  display: "flex",
                  gap: "0.5rem",
                  alignItems: "flex-end",
                }}
              >
                <textarea
                  value={feedbackText.value}
                  onInput={(e) => {
                    feedbackText.value = (
                      e.target as HTMLTextAreaElement
                    ).value;
                  }}
                  placeholder='e.g., "make it shorter", "more casual"'
                  style={{
                    flex: 1,
                    minHeight: "3rem",
                    padding: "0.5rem 0.75rem",
                    fontSize: "0.875rem",
                    border: "1px solid var(--color-border)",
                    borderRadius: "var(--radius)",
                    background: "var(--color-surface)",
                    color: "var(--color-text)",
                    resize: "vertical",
                    fontFamily: "inherit",
                  }}
                />
                <button
                  class="primary"
                  disabled={!feedbackText.value.trim() || feedbackLoading.value}
                  onClick={() => handleRegenerate()}
                  style={{ fontSize: "0.875rem", flexShrink: 0 }}
                >
                  {feedbackLoading.value ? "Regenerating…" : "Regenerate"}
                </button>
              </div>
            </div>
          )}
        </div>
      ))}

      <ActionBar
        left={
          <span style={{ fontSize: "0.875rem" }}>
            {items.length} item{items.length !== 1 ? "s" : ""} prepared
          </span>
        }
        right={
          <button class="outline" onClick={() => navigateBack()}>
            Back
          </button>
        }
      />
    </div>
  );
}
