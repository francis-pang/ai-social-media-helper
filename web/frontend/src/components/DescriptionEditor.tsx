import { signal, computed } from "@preact/signals";
import { useEffect } from "preact/hooks";
import {
  navigateBack,
  navigateToStep,
  uploadSessionId,
  tripContext,
} from "../app";
import { ProcessingIndicator } from "./ProcessingIndicator";
import {
  generateDescription,
  getDescriptionResults,
  submitDescriptionFeedback,
  thumbnailUrl,
} from "../api/client";
import { postGroups, groupableMedia } from "./PostGrouper";
import { setGroupCaption } from "./PublishView";
import type { PostGroup, GroupableMediaItem } from "../types/api";

// --- State ---

/** Index of the post group currently being described. */
const currentGroupIndex = signal(0);

/** Description generation state. */
interface DescriptionState {
  jobId: string | null;
  status: "idle" | "generating" | "complete" | "error";
  caption: string;
  hashtags: string[];
  locationTag: string;
  feedbackRound: number;
  error: string | null;
}

const descriptionState = signal<DescriptionState>({
  jobId: null,
  status: "idle",
  caption: "",
  hashtags: [],
  locationTag: "",
  feedbackRound: 0,
  error: null,
});

/** User's feedback text input. */
const feedbackText = signal("");

/** Whether the caption textarea is being edited manually. */
const isEditing = signal(false);

/** Copy-to-clipboard status. */
const copyStatus = signal<"idle" | "copied">("idle");

/**
 * Reset all description editor state to initial values (DDR-037).
 * Called by the invalidation cascade when a previous step changes.
 */
export function resetDescriptionState() {
  currentGroupIndex.value = 0;
  descriptionState.value = {
    jobId: null,
    status: "idle",
    caption: "",
    hashtags: [],
    locationTag: "",
    feedbackRound: 0,
    error: null,
  };
  feedbackText.value = "";
  isEditing.value = false;
  copyStatus.value = "idle";
}

// --- Derived state ---

const currentGroup = computed<PostGroup | null>(() => {
  const groups = postGroups.value;
  const idx = currentGroupIndex.value;
  return idx < groups.length ? groups[idx]! : null;
});

const hasMoreGroups = computed(
  () => currentGroupIndex.value < postGroups.value.length - 1,
);

const totalGroups = computed(() => postGroups.value.length);

// --- Actions ---

async function startGeneration() {
  const group = currentGroup.value;
  const sessionId = uploadSessionId.value;
  if (!group || !sessionId) return;

  descriptionState.value = {
    jobId: null,
    status: "generating",
    caption: "",
    hashtags: [],
    locationTag: "",
    feedbackRound: 0,
    error: null,
  };

  try {
    const { id } = await generateDescription({
      sessionId,
      keys: group.keys,
      groupLabel: group.label,
      tripContext: tripContext.value,
    });

    descriptionState.value = {
      ...descriptionState.value,
      jobId: id,
    };

    // Poll for results
    await pollForResults(id, sessionId);
  } catch (err) {
    descriptionState.value = {
      ...descriptionState.value,
      status: "error",
      error: err instanceof Error ? err.message : "Generation failed",
    };
  }
}

async function pollForResults(jobId: string, sessionId: string) {
  const pollInterval = 2000;
  const maxPolls = 30; // 60 seconds max

  for (let i = 0; i < maxPolls; i++) {
    await new Promise((resolve) => setTimeout(resolve, pollInterval));

    try {
      const results = await getDescriptionResults(jobId, sessionId);

      if (results.status === "complete") {
        descriptionState.value = {
          jobId,
          status: "complete",
          caption: results.caption ?? "",
          hashtags: results.hashtags ?? [],
          locationTag: results.locationTag ?? "",
          feedbackRound: results.feedbackRound,
          error: null,
        };
        return;
      }

      if (results.status === "error") {
        descriptionState.value = {
          jobId,
          status: "error",
          caption: "",
          hashtags: [],
          locationTag: "",
          feedbackRound: 0,
          error: results.error ?? "Generation failed",
        };
        return;
      }
    } catch (err) {
      // Continue polling on transient errors
      // eslint-disable-next-line no-console
      console.warn("Poll error:", err);
    }
  }

  descriptionState.value = {
    ...descriptionState.value,
    status: "error",
    error: "Generation timed out",
  };
}

async function submitFeedback() {
  const state = descriptionState.value;
  const sessionId = uploadSessionId.value;
  if (!state.jobId || !sessionId || !feedbackText.value.trim()) return;

  const feedback = feedbackText.value.trim();
  feedbackText.value = "";

  descriptionState.value = {
    ...descriptionState.value,
    status: "generating",
  };

  try {
    await submitDescriptionFeedback(state.jobId, {
      sessionId,
      feedback,
    });

    // Poll for regenerated results
    await pollForResults(state.jobId, sessionId);
  } catch (err) {
    descriptionState.value = {
      ...descriptionState.value,
      status: "error",
      error: err instanceof Error ? err.message : "Feedback failed",
    };
  }
}

function copyToClipboard() {
  const state = descriptionState.value;
  const hashtagStr = state.hashtags.map((h) => `#${h}`).join(" ");
  const fullText = `${state.caption}\n\n${hashtagStr}`;

  navigator.clipboard.writeText(fullText).then(() => {
    copyStatus.value = "copied";
    setTimeout(() => {
      copyStatus.value = "idle";
    }, 2000);
  });
}

function acceptAndContinue() {
  // Save caption data for the current group (used by PublishView)
  const group = currentGroup.value;
  const state = descriptionState.value;
  if (group && state.status === "complete") {
    setGroupCaption(group.id, state.caption, state.hashtags);
  }

  if (hasMoreGroups.value) {
    // Move to the next group
    currentGroupIndex.value = currentGroupIndex.value + 1;
    descriptionState.value = {
      jobId: null,
      status: "idle",
      caption: "",
      hashtags: [],
      locationTag: "",
      feedbackRound: 0,
      error: null,
    };
    feedbackText.value = "";
    isEditing.value = false;
  } else {
    // All groups done — proceed to Instagram publishing (DDR-040)
    navigateToStep("instagram-publish");
  }
}

// --- Sub-components ---

function MediaPreviewStrip({ group }: { group: PostGroup }) {
  const items = group.keys
    .slice(0, 8)
    .map((key) => groupableMedia.value.find((m) => m.key === key))
    .filter((m): m is GroupableMediaItem => m !== undefined);

  return (
    <div
      style={{
        display: "flex",
        gap: "4px",
        overflowX: "auto",
        padding: "0.5rem 0",
      }}
    >
      {items.map((item) => (
        <div
          key={item.key}
          style={{
            width: "3.5rem",
            height: "3.5rem",
            borderRadius: "var(--radius)",
            overflow: "hidden",
            flexShrink: 0,
            background: "var(--color-surface-hover)",
            position: "relative",
          }}
        >
          <img
            src={thumbnailUrl(item.thumbnailKey)}
            alt={item.filename}
            style={{
              width: "100%",
              height: "100%",
              objectFit: "cover",
              display: "block",
            }}
            onError={(e) => {
              (e.target as HTMLImageElement).style.display = "none";
            }}
          />
          {item.type === "Video" && (
            <span
              style={{
                position: "absolute",
                bottom: "2px",
                right: "2px",
                fontSize: "0.75rem",
                background: "rgba(0,0,0,0.6)",
                color: "#fff",
                padding: "1px 3px",
                borderRadius: "2px",
              }}
            >
              VID
            </span>
          )}
        </div>
      ))}
      {group.keys.length > 8 && (
        <div
          style={{
            width: "3.5rem",
            height: "3.5rem",
            borderRadius: "var(--radius)",
            background: "var(--color-surface-hover)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            fontSize: "0.75rem",
            color: "var(--color-text-secondary)",
            flexShrink: 0,
          }}
        >
          +{group.keys.length - 8}
        </div>
      )}
    </div>
  );
}

function HashtagList({
  hashtags,
  onRemove,
}: {
  hashtags: string[];
  onRemove: (index: number) => void;
}) {
  return (
    <div
      style={{
        display: "flex",
        flexWrap: "wrap",
        gap: "0.375rem",
      }}
    >
      {hashtags.map((tag, i) => (
        <span
          key={`${tag}-${i}`}
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: "0.25rem",
            padding: "0.25rem 0.5rem",
            fontSize: "0.75rem",
            background: "rgba(108, 140, 255, 0.1)",
            color: "var(--color-primary)",
            borderRadius: "var(--radius)",
            cursor: "pointer",
            transition: "background 0.15s",
          }}
          onClick={() => onRemove(i)}
          title="Click to remove"
        >
          #{tag}
          <span
            style={{
              fontSize: "0.75rem",
              opacity: 0.6,
              marginLeft: "0.125rem",
            }}
          >
            x
          </span>
        </span>
      ))}
    </div>
  );
}

// GeneratingSpinner replaced by ProcessingIndicator (DDR-056)

// --- Main Component ---

export function DescriptionEditor() {
  const group = currentGroup.value;
  const state = descriptionState.value;
  const groups = postGroups.value;

  // Auto-start generation when component mounts or group changes
  useEffect(() => {
    if (group && state.status === "idle") {
      startGeneration();
    }
  }, [currentGroupIndex.value]);

  if (!group) {
    return (
      <div class="card" style={{ textAlign: "center", padding: "2rem" }}>
        <p style={{ color: "var(--color-text-secondary)" }}>
          No post groups to generate descriptions for.
        </p>
        <button class="outline" onClick={() => navigateBack()}>
          Back
        </button>
      </div>
    );
  }

  return (
    <div>
      {/* Progress bar — which group we're on */}
      {groups.length > 1 && (
        <div
          class="card"
          style={{
            marginBottom: "1rem",
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
          }}
        >
          <span style={{ fontSize: "0.875rem" }}>
            Post{" "}
            <strong>
              {currentGroupIndex.value + 1} of {totalGroups.value}
            </strong>
          </span>
          <div
            style={{
              display: "flex",
              gap: "4px",
            }}
          >
            {groups.map((_, i) => (
              <div
                key={i}
                style={{
                  width: "1.5rem",
                  height: "4px",
                  borderRadius: "2px",
                  background:
                    i < currentGroupIndex.value
                      ? "var(--color-success)"
                      : i === currentGroupIndex.value
                        ? "var(--color-primary)"
                        : "var(--color-border)",
                }}
              />
            ))}
          </div>
        </div>
      )}

      {/* Group header */}
      <div class="card" style={{ marginBottom: "1rem" }}>
        <h3
          style={{
            margin: "0 0 0.5rem",
            fontSize: "1rem",
            color: group.label
              ? "var(--color-text)"
              : "var(--color-text-secondary)",
          }}
        >
          {group.label || "Untitled group"}
        </h3>
        <div
          style={{
            fontSize: "0.75rem",
            color: "var(--color-text-secondary)",
            marginBottom: "0.5rem",
          }}
        >
          {group.keys.length} item{group.keys.length !== 1 ? "s" : ""} in this
          post
        </div>
        <MediaPreviewStrip group={group} />
      </div>

      {/* Generating state — DDR-056 ProcessingIndicator */}
      {state.status === "generating" && (
        <div style={{ marginBottom: "1rem" }}>
          <ProcessingIndicator
            title="Generating Caption"
            description="Analyzing your media and crafting an engaging Instagram caption"
            sessionId={uploadSessionId.value ?? undefined}
            jobId={state.jobId ?? undefined}
            pollIntervalMs={2000}
          />
        </div>
      )}

      {/* Error state */}
      {state.status === "error" && (
        <div class="card" style={{ marginBottom: "1rem" }}>
          <div
            style={{
              color: "var(--color-danger)",
              marginBottom: "1rem",
              fontSize: "0.875rem",
            }}
          >
            {state.error}
          </div>
          <button class="primary" onClick={() => startGeneration()}>
            Try Again
          </button>
        </div>
      )}

      {/* Complete state — show caption editor */}
      {state.status === "complete" && (
        <>
          {/* Caption */}
          <div class="card" style={{ marginBottom: "1rem" }}>
            <div
              style={{
                display: "flex",
                justifyContent: "space-between",
                alignItems: "center",
                marginBottom: "0.75rem",
              }}
            >
              <h3 style={{ margin: 0, fontSize: "1rem" }}>Caption</h3>
              <div style={{ display: "flex", gap: "0.5rem" }}>
                {state.feedbackRound > 0 && (
                  <span
                    style={{
                      fontSize: "0.75rem",
                      color: "var(--color-text-secondary)",
                      padding: "0.125rem 0.375rem",
                      background: "var(--color-surface-hover)",
                      borderRadius: "3px",
                    }}
                  >
                    v{state.feedbackRound + 1}
                  </span>
                )}
                <button
                  class="outline"
                  style={{ fontSize: "0.75rem", padding: "0.25rem 0.5rem" }}
                  onClick={() => {
                    isEditing.value = !isEditing.value;
                  }}
                >
                  {isEditing.value ? "Done" : "Edit"}
                </button>
              </div>
            </div>

            {isEditing.value ? (
              <textarea
                value={state.caption}
                onInput={(e) => {
                  descriptionState.value = {
                    ...descriptionState.value,
                    caption: (e.target as HTMLTextAreaElement).value,
                  };
                }}
                style={{
                  width: "100%",
                  minHeight: "8rem",
                  padding: "0.75rem",
                  fontSize: "0.875rem",
                  lineHeight: "1.5",
                  border: "1px solid var(--color-border)",
                  borderRadius: "var(--radius)",
                  background: "var(--color-bg)",
                  color: "var(--color-text)",
                  resize: "vertical",
                  fontFamily: "inherit",
                }}
              />
            ) : (
              <div
                style={{
                  padding: "0.75rem",
                  background: "var(--color-bg)",
                  borderRadius: "var(--radius)",
                  border: "1px solid var(--color-border)",
                  fontSize: "0.875rem",
                  lineHeight: "1.6",
                  whiteSpace: "pre-wrap",
                }}
              >
                {state.caption}
              </div>
            )}
          </div>

          {/* Location tag */}
          {state.locationTag && (
            <div
              class="card"
              style={{
                marginBottom: "1rem",
                display: "flex",
                alignItems: "center",
                gap: "0.5rem",
              }}
            >
              <span
                style={{
                  fontSize: "0.875rem",
                  color: "var(--color-text-secondary)",
                }}
              >
                Location:
              </span>
              <span
                style={{
                  fontSize: "0.875rem",
                  fontWeight: 500,
                  color: "var(--color-primary)",
                }}
              >
                {state.locationTag}
              </span>
            </div>
          )}

          {/* Hashtags */}
          <div class="card" style={{ marginBottom: "1rem" }}>
            <h3
              style={{
                margin: "0 0 0.75rem",
                fontSize: "1rem",
              }}
            >
              Hashtags
              <span
                style={{
                  fontSize: "0.75rem",
                  color: "var(--color-text-secondary)",
                  fontWeight: 400,
                  marginLeft: "0.5rem",
                }}
              >
                ({state.hashtags.length})
              </span>
            </h3>
            <HashtagList
              hashtags={state.hashtags}
              onRemove={(index) => {
                descriptionState.value = {
                  ...descriptionState.value,
                  hashtags: state.hashtags.filter((_, i) => i !== index),
                };
              }}
            />
            <div
              style={{
                marginTop: "0.5rem",
                fontSize: "0.75rem",
                color: "var(--color-text-secondary)",
              }}
            >
              Click a hashtag to remove it
            </div>
          </div>

          {/* Feedback input */}
          <div class="card" style={{ marginBottom: "1rem" }}>
            <h3 style={{ margin: "0 0 0.5rem", fontSize: "1rem" }}>
              Want changes?
            </h3>
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
                placeholder='e.g., "make it shorter", "more casual", "add more food hashtags"'
                style={{
                  flex: 1,
                  minHeight: "3rem",
                  maxHeight: "6rem",
                  padding: "0.5rem 0.75rem",
                  fontSize: "0.875rem",
                  border: "1px solid var(--color-border)",
                  borderRadius: "var(--radius)",
                  background: "var(--color-bg)",
                  color: "var(--color-text)",
                  resize: "vertical",
                  fontFamily: "inherit",
                }}
              />
              <button
                class="primary"
                disabled={!feedbackText.value.trim()}
                onClick={() => submitFeedback()}
                style={{ fontSize: "0.875rem", flexShrink: 0 }}
              >
                Regenerate
              </button>
            </div>
          </div>
        </>
      )}

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
        <button class="outline" onClick={() => navigateBack()}>
          Back
        </button>

        <div style={{ display: "flex", gap: "0.5rem" }}>
          {state.status === "complete" && (
            <>
              <button
                class="outline"
                onClick={() => copyToClipboard()}
                style={{ fontSize: "0.875rem" }}
              >
                {copyStatus.value === "copied"
                  ? "Copied!"
                  : "Copy to Clipboard"}
              </button>

              <button
                class="primary"
                onClick={() => acceptAndContinue()}
                style={{ fontSize: "0.875rem" }}
              >
                {hasMoreGroups.value
                  ? "Accept & Next Group"
                  : "Done"}
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
