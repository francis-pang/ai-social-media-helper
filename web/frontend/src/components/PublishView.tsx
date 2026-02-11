import { signal, computed } from "@preact/signals";
import { useEffect } from "preact/hooks";
import {
  navigateBack,
  navigateToStep,
  uploadSessionId,
} from "../app";
import { ElapsedTimer } from "./ProcessingIndicator";
import {
  startPublish,
  getPublishStatus,
  getHealth,
  thumbnailUrl,
} from "../api/client";
import { postGroups, groupableMedia } from "./PostGrouper";
import type { PostGroup, GroupableMediaItem, PublishStatus } from "../types/api";

// --- State ---

/** Track publish state per group. */
interface GroupPublishState {
  jobId: string | null;
  status: "idle" | "publishing" | "published" | "error";
  phase: string;
  progress: { completed: number; total: number };
  instagramPostId: string | null;
  error: string | null;
  /** Caption and hashtags from the description step (stored for the publish request). */
  caption: string;
  hashtags: string[];
}

const publishStates = signal<Record<string, GroupPublishState>>({});

/** Whether the backend has Instagram credentials configured. */
const instagramConfigured = signal<boolean | null>(null); // null = not yet checked

/**
 * Reset all publish state to initial values (DDR-037).
 * Called by the invalidation cascade when a previous step changes.
 */
export function resetPublishState() {
  publishStates.value = {};
}

/** Check if Instagram is configured on the backend (called once on mount). */
async function checkInstagramStatus() {
  if (instagramConfigured.value !== null) return; // already checked
  try {
    const health = await getHealth();
    instagramConfigured.value = health.instagramConfigured;
  } catch {
    instagramConfigured.value = false;
  }
}

/**
 * Set caption data for a group from the description step.
 * Called by DescriptionEditor when a caption is accepted.
 */
export function setGroupCaption(
  groupId: string,
  caption: string,
  hashtags: string[],
) {
  const current = getGroupState(groupId);
  publishStates.value = {
    ...publishStates.value,
    [groupId]: { ...current, caption, hashtags },
  };
}

// --- Helpers ---

function getGroupState(groupId: string): GroupPublishState {
  return (
    publishStates.value[groupId] ?? {
      jobId: null,
      status: "idle",
      phase: "",
      progress: { completed: 0, total: 0 },
      instagramPostId: null,
      error: null,
      caption: "",
      hashtags: [],
    }
  );
}

function setGroupState(groupId: string, state: GroupPublishState) {
  publishStates.value = {
    ...publishStates.value,
    [groupId]: state,
  };
}

// --- Derived state ---

const totalGroups = computed(() => postGroups.value.length);
const publishedGroups = computed(
  () =>
    Object.values(publishStates.value).filter((s) => s.status === "published")
      .length,
);

// --- Phase display ---

function phaseLabel(phase: string): string {
  switch (phase) {
    case "creating_containers":
      return "Uploading media to Instagram...";
    case "processing_videos":
      return "Processing videos on Instagram...";
    case "creating_carousel":
      return "Creating carousel post...";
    case "publishing":
      return "Publishing...";
    case "published":
      return "Published!";
    default:
      return "Preparing...";
  }
}

// --- Actions ---

async function handlePublish(group: PostGroup) {
  const sessionId = uploadSessionId.value;
  if (!sessionId) return;

  const state = getGroupState(group.id);

  setGroupState(group.id, {
    ...state,
    jobId: null,
    status: "publishing",
    phase: "pending",
    progress: { completed: 0, total: group.keys.length },
    instagramPostId: null,
    error: null,
  });

  try {
    const { id } = await startPublish({
      sessionId,
      groupId: group.id,
      keys: group.keys,
      caption: state.caption,
      hashtags: state.hashtags,
    });

    setGroupState(group.id, {
      ...getGroupState(group.id),
      jobId: id,
    });

    // Poll for status
    const pollInterval = 3000;
    const maxPolls = 200; // 10 minutes max

    for (let i = 0; i < maxPolls; i++) {
      await new Promise((resolve) => setTimeout(resolve, pollInterval));

      const result: PublishStatus = await getPublishStatus(id, sessionId);

      const currentState = getGroupState(group.id);
      setGroupState(group.id, {
        ...currentState,
        jobId: id,
        status:
          result.status === "published"
            ? "published"
            : result.status === "error"
              ? "error"
              : "publishing",
        phase: result.phase,
        progress: result.progress,
        instagramPostId: result.instagramPostId ?? null,
        error: result.error ?? null,
      });

      if (result.status === "published" || result.status === "error") {
        break;
      }
    }
  } catch (err) {
    setGroupState(group.id, {
      ...getGroupState(group.id),
      status: "error",
      error: err instanceof Error ? err.message : "Publish failed",
    });
  }
}

// --- Sub-components ---

function GroupPublishCard({ group }: { group: PostGroup }) {
  const state = getGroupState(group.id);
  const isIdle = state.status === "idle";
  const isPublishing = state.status === "publishing";
  const isPublished = state.status === "published";
  const isError = state.status === "error";

  // Media preview
  const previewItems = group.keys
    .slice(0, 8)
    .map((key) => groupableMedia.value.find((m) => m.key === key))
    .filter((m): m is GroupableMediaItem => m !== undefined);

  // Count media types
  const allItems = group.keys
    .map((key) => groupableMedia.value.find((m) => m.key === key))
    .filter((m): m is GroupableMediaItem => m !== undefined);
  const photoCount = allItems.filter((m) => m.type === "Photo").length;
  const videoCount = allItems.filter((m) => m.type === "Video").length;

  const hasCaption = state.caption.length > 0;

  return (
    <div
      class="card"
      style={{
        marginBottom: "0.75rem",
        border: isPublished
          ? "1px solid var(--color-success)"
          : isError
            ? "1px solid var(--color-danger)"
            : "1px solid var(--color-border)",
      }}
    >
      {/* Group header with preview */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: "1rem",
          marginBottom: "0.75rem",
        }}
      >
        <div style={{ flex: 1 }}>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "0.5rem",
              marginBottom: "0.25rem",
            }}
          >
            <h3
              style={{
                margin: 0,
                fontSize: "1rem",
                color: group.label
                  ? "var(--color-text)"
                  : "var(--color-text-secondary)",
              }}
            >
              {group.label || "Untitled group"}
            </h3>
            {isPublished && (
              <span
                style={{
                  fontSize: "0.625rem",
                  padding: "0.125rem 0.375rem",
                  borderRadius: "3px",
                  background: "rgba(108, 200, 108, 0.15)",
                  color: "var(--color-success)",
                  fontWeight: 600,
                }}
              >
                Published
              </span>
            )}
          </div>
          <div
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
            }}
          >
            {group.keys.length} item{group.keys.length !== 1 ? "s" : ""}
            {photoCount > 0 && (
              <>
                {" — "}
                {photoCount} photo{photoCount !== 1 ? "s" : ""}
              </>
            )}
            {videoCount > 0 && (
              <>
                {" — "}
                {videoCount} video{videoCount !== 1 ? "s" : ""}
              </>
            )}
          </div>
        </div>

        {/* Mini preview thumbnails */}
        <div style={{ display: "flex", gap: "2px", flexShrink: 0 }}>
          {previewItems.map((item) => (
            <div
              key={item.key}
              style={{
                width: "2.5rem",
                height: "2.5rem",
                borderRadius: "3px",
                overflow: "hidden",
                background: "var(--color-surface-hover)",
                position: "relative",
              }}
            >
              <img
                src={thumbnailUrl(item.thumbnailKey)}
                alt=""
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
                    bottom: "1px",
                    right: "1px",
                    fontSize: "0.5rem",
                    background: "rgba(0,0,0,0.6)",
                    color: "#fff",
                    padding: "1px 2px",
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
                width: "2.5rem",
                height: "2.5rem",
                borderRadius: "3px",
                background: "var(--color-surface-hover)",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                fontSize: "0.5625rem",
                color: "var(--color-text-secondary)",
              }}
            >
              +{group.keys.length - 8}
            </div>
          )}
        </div>
      </div>

      {/* Caption preview */}
      {hasCaption && (
        <div
          style={{
            padding: "0.5rem 0.75rem",
            background: "var(--color-bg)",
            borderRadius: "var(--radius)",
            border: "1px solid var(--color-border)",
            fontSize: "0.8125rem",
            lineHeight: "1.5",
            whiteSpace: "pre-wrap",
            marginBottom: "0.75rem",
            maxHeight: "4.5rem",
            overflow: "hidden",
            position: "relative",
          }}
        >
          {state.caption.slice(0, 200)}
          {state.caption.length > 200 && (
            <span style={{ color: "var(--color-text-secondary)" }}>...</span>
          )}
        </div>
      )}

      {!hasCaption && isIdle && (
        <div
          style={{
            padding: "0.5rem 0.75rem",
            background: "rgba(255, 200, 50, 0.06)",
            borderRadius: "var(--radius)",
            border: "1px solid rgba(255, 200, 50, 0.2)",
            fontSize: "0.75rem",
            color: "var(--color-text-secondary)",
            marginBottom: "0.75rem",
          }}
        >
          No caption set. Generate captions in the Description step first, or publish without a caption.
        </div>
      )}

      {/* Hashtag preview */}
      {state.hashtags.length > 0 && (
        <div
          style={{
            display: "flex",
            flexWrap: "wrap",
            gap: "0.25rem",
            marginBottom: "0.75rem",
          }}
        >
          {state.hashtags.slice(0, 10).map((tag, i) => (
            <span
              key={`${tag}-${i}`}
              style={{
                fontSize: "0.6875rem",
                padding: "0.125rem 0.375rem",
                background: "rgba(108, 140, 255, 0.1)",
                color: "var(--color-primary)",
                borderRadius: "3px",
              }}
            >
              #{tag}
            </span>
          ))}
          {state.hashtags.length > 10 && (
            <span
              style={{
                fontSize: "0.6875rem",
                color: "var(--color-text-secondary)",
                padding: "0.125rem 0.25rem",
              }}
            >
              +{state.hashtags.length - 10} more
            </span>
          )}
        </div>
      )}

      {/* Publishing progress — DDR-056 elapsed timer */}
      {isPublishing && (
        <div
          style={{
            padding: "0.75rem",
            background: "rgba(108, 140, 255, 0.06)",
            borderRadius: "var(--radius)",
            marginBottom: "0.5rem",
          }}
        >
          <div
            style={{
              display: "flex",
              justifyContent: "space-between",
              alignItems: "center",
              marginBottom: "0.5rem",
            }}
          >
            <span style={{ fontSize: "0.8125rem", fontWeight: 500 }}>
              {phaseLabel(state.phase)}
            </span>
            <div style={{ display: "flex", alignItems: "center", gap: "0.75rem" }}>
              <ElapsedTimer />
              <span
                style={{
                  fontSize: "0.75rem",
                  color: "var(--color-text-secondary)",
                }}
              >
                {state.progress.completed}/{state.progress.total}
              </span>
            </div>
          </div>
          <div
            style={{
              height: "4px",
              background: "var(--color-border)",
              borderRadius: "2px",
              overflow: "hidden",
            }}
          >
            <div
              style={{
                height: "100%",
                width:
                  state.progress.total > 0
                    ? `${(state.progress.completed / state.progress.total) * 100}%`
                    : "0%",
                background: "var(--color-primary)",
                borderRadius: "2px",
                transition: "width 0.3s ease",
              }}
            />
          </div>
          <div
            style={{
              marginTop: "0.5rem",
              width: "1.25rem",
              height: "1.25rem",
              border: "2px solid var(--color-border)",
              borderTop: "2px solid var(--color-primary)",
              borderRadius: "50%",
              animation: "spin 1s linear infinite",
              margin: "0.5rem auto 0",
            }}
          />
          <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
        </div>
      )}

      {/* Published success */}
      {isPublished && (
        <div
          style={{
            padding: "0.75rem",
            background: "rgba(108, 200, 108, 0.06)",
            borderRadius: "var(--radius)",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          <span
            style={{
              fontSize: "0.8125rem",
              color: "var(--color-success)",
              fontWeight: 500,
            }}
          >
            Successfully published to Instagram
          </span>
          {state.instagramPostId && (
            <a
              href={`https://www.instagram.com/p/${state.instagramPostId}/`}
              target="_blank"
              rel="noopener noreferrer"
              style={{
                fontSize: "0.8125rem",
                color: "var(--color-primary)",
                textDecoration: "none",
                fontWeight: 500,
              }}
            >
              View on Instagram
            </a>
          )}
        </div>
      )}

      {/* Error state */}
      {isError && (
        <div
          style={{
            padding: "0.75rem",
            background: "rgba(255, 107, 107, 0.06)",
            borderRadius: "var(--radius)",
            marginBottom: "0.5rem",
          }}
        >
          <div
            style={{
              fontSize: "0.8125rem",
              color: "var(--color-danger)",
              marginBottom: "0.5rem",
            }}
          >
            {state.error}
          </div>
          <button
            class="outline"
            onClick={() => handlePublish(group)}
            style={{ fontSize: "0.75rem" }}
          >
            Retry
          </button>
        </div>
      )}

      {/* Publish button */}
      {isIdle && (
        <div style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", gap: "0.75rem" }}>
          {instagramConfigured.value === false && (
            <span
              style={{
                fontSize: "0.75rem",
                color: "var(--color-text-secondary)",
              }}
            >
              Instagram not connected
            </span>
          )}
          <button
            class="primary"
            onClick={() => handlePublish(group)}
            disabled={!instagramConfigured.value}
            style={{
              fontSize: "0.8125rem",
              opacity: instagramConfigured.value ? 1 : 0.4,
              cursor: instagramConfigured.value ? "pointer" : "not-allowed",
            }}
          >
            Publish to Instagram
          </button>
        </div>
      )}
    </div>
  );
}

// --- Main Component ---

export function PublishView() {
  const groups = postGroups.value;

  // Check if Instagram is configured on mount
  useEffect(() => {
    checkInstagramStatus();
  }, []);

  return (
    <div>
      {/* Instagram not configured banner */}
      {instagramConfigured.value === false && (
        <div
          class="card"
          style={{
            marginBottom: "1rem",
            padding: "0.75rem 1rem",
            background: "rgba(255, 200, 50, 0.08)",
            border: "1px solid rgba(255, 200, 50, 0.25)",
            display: "flex",
            alignItems: "center",
            gap: "0.75rem",
          }}
        >
          <span style={{ fontSize: "1.125rem" }}>&#9888;</span>
          <div>
            <div style={{ fontSize: "0.875rem", fontWeight: 500, marginBottom: "0.125rem" }}>
              Instagram not connected
            </div>
            <div style={{ fontSize: "0.75rem", color: "var(--color-text-secondary)" }}>
              Publishing is disabled. Provide a long-lived access token and user ID
              via SSM Parameter Store to enable Instagram publishing.
            </div>
          </div>
        </div>
      )}

      {/* Header info */}
      <div
        class="card"
        style={{
          marginBottom: "1rem",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          flexWrap: "wrap",
          gap: "0.5rem",
        }}
      >
        <div>
          <span style={{ fontSize: "0.875rem" }}>
            <strong>{totalGroups.value}</strong> post group
            {totalGroups.value !== 1 ? "s" : ""}
            {publishedGroups.value > 0 && (
              <>
                {" — "}
                <span style={{ color: "var(--color-success)" }}>
                  {publishedGroups.value} published
                </span>
              </>
            )}
          </span>
        </div>
        <div
          style={{
            fontSize: "0.75rem",
            color: "var(--color-text-secondary)",
          }}
        >
          Each group is published as an Instagram carousel (or single post for 1 item).
        </div>
      </div>

      {/* Group cards */}
      {groups.map((group) => (
        <GroupPublishCard key={group.id} group={group} />
      ))}

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
        <div style={{ display: "flex", gap: "0.5rem" }}>
          <button class="outline" onClick={() => navigateBack()}>
            Back
          </button>
          <button
            class="outline"
            onClick={() => navigateToStep("description")}
            style={{ fontSize: "0.8125rem" }}
          >
            Generate Captions
          </button>
        </div>
        <span style={{ fontSize: "0.875rem" }}>
          {publishedGroups.value === 0 ? (
            <span style={{ color: "var(--color-text-secondary)" }}>
              Ready to publish your posts
            </span>
          ) : (
            <>
              <strong style={{ color: "var(--color-success)" }}>
                {publishedGroups.value} of {totalGroups.value}
              </strong>
              <span style={{ color: "var(--color-text-secondary)" }}>
                {" "}
                group{publishedGroups.value !== 1 ? "s" : ""} published
              </span>
            </>
          )}
        </span>
      </div>
    </div>
  );
}
