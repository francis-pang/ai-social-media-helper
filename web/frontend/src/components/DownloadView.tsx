import { signal, computed } from "@preact/signals";
import { navigateBack, uploadSessionId } from "../app";
import { startDownload, getDownloadResults, thumbnailUrl } from "../api/client";
import { postGroups } from "./PostGrouper";
import type {
  PostGroup,
  DownloadBundle,
  GroupableMediaItem,
} from "../types/api";
import { groupableMedia } from "./PostGrouper";

// --- State ---

/** Track download job state per group. */
interface GroupDownloadState {
  jobId: string | null;
  status: "idle" | "processing" | "complete" | "error";
  bundles: DownloadBundle[];
  error: string | null;
}

const downloadStates = signal<Record<string, GroupDownloadState>>({});

/** Which group is currently expanded. */
const expandedGroupId = signal<string | null>(null);

// --- Helpers ---

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`;
}

function getGroupState(groupId: string): GroupDownloadState {
  return (
    downloadStates.value[groupId] ?? {
      jobId: null,
      status: "idle",
      bundles: [],
      error: null,
    }
  );
}

function setGroupState(groupId: string, state: GroupDownloadState) {
  downloadStates.value = {
    ...downloadStates.value,
    [groupId]: state,
  };
}

// --- Actions ---

async function handleDownload(group: PostGroup) {
  const sessionId = uploadSessionId.value;
  if (!sessionId) return;

  setGroupState(group.id, {
    jobId: null,
    status: "processing",
    bundles: [],
    error: null,
  });

  try {
    // Start the download job
    const { id } = await startDownload({
      sessionId,
      keys: group.keys,
      groupLabel: group.label || "media",
    });

    setGroupState(group.id, {
      jobId: id,
      status: "processing",
      bundles: [],
      error: null,
    });

    // Poll for results
    const pollInterval = 2000; // 2 seconds
    const maxPolls = 150; // 5 minutes max

    for (let i = 0; i < maxPolls; i++) {
      await new Promise((resolve) => setTimeout(resolve, pollInterval));

      const results = await getDownloadResults(id, sessionId);

      setGroupState(group.id, {
        jobId: id,
        status:
          results.status === "complete" || results.status === "error"
            ? results.status
            : "processing",
        bundles: results.bundles ?? [],
        error: results.error ?? null,
      });

      if (results.status === "complete" || results.status === "error") {
        break;
      }
    }
  } catch (err) {
    setGroupState(group.id, {
      jobId: null,
      status: "error",
      bundles: [],
      error: err instanceof Error ? err.message : "Download failed",
    });
  }
}

// --- Summary Stats ---

const totalGroups = computed(() => postGroups.value.length);
const completedGroups = computed(
  () =>
    Object.values(downloadStates.value).filter((s) => s.status === "complete")
      .length,
);

// --- Sub-components ---

function BundleCard({ bundle }: { bundle: DownloadBundle }) {
  const isComplete = bundle.status === "complete";
  const isError = bundle.status === "error";
  const isProcessing =
    bundle.status === "processing" || bundle.status === "pending";

  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        padding: "0.75rem 1rem",
        background: "var(--color-bg)",
        borderRadius: "var(--radius)",
        border: isError
          ? "1px solid var(--color-danger)"
          : "1px solid var(--color-border)",
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
          {/* Type icon */}
          <span
            style={{
              fontSize: "0.625rem",
              padding: "0.125rem 0.375rem",
              borderRadius: "3px",
              background:
                bundle.type === "images"
                  ? "rgba(108, 200, 108, 0.15)"
                  : "rgba(108, 140, 255, 0.15)",
              color:
                bundle.type === "images"
                  ? "var(--color-success)"
                  : "var(--color-primary)",
              fontWeight: 600,
              textTransform: "uppercase",
            }}
          >
            {bundle.type === "images" ? "Photos" : "Videos"}
          </span>

          <span
            style={{
              fontSize: "0.8125rem",
              fontWeight: 500,
              fontFamily: "var(--font-mono)",
            }}
          >
            {bundle.name}
          </span>
        </div>

        <div
          style={{
            fontSize: "0.75rem",
            color: "var(--color-text-secondary)",
          }}
        >
          {bundle.fileCount} file{bundle.fileCount !== 1 ? "s" : ""}
          {" — "}
          {formatBytes(bundle.totalSize)}
          {isComplete && bundle.zipSize > 0 && (
            <> (ZIP: {formatBytes(bundle.zipSize)})</>
          )}
        </div>

        {isError && bundle.error && (
          <div
            style={{
              fontSize: "0.75rem",
              color: "var(--color-danger)",
              marginTop: "0.25rem",
            }}
          >
            {bundle.error}
          </div>
        )}
      </div>

      <div style={{ marginLeft: "1rem" }}>
        {isProcessing && (
          <span
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              fontStyle: "italic",
            }}
          >
            Creating ZIP...
          </span>
        )}

        {isComplete && bundle.downloadUrl && (
          <a
            href={bundle.downloadUrl}
            download={bundle.name}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "0.375rem",
              padding: "0.375rem 0.75rem",
              fontSize: "0.8125rem",
              fontWeight: 500,
              background: "var(--color-primary)",
              color: "#fff",
              borderRadius: "var(--radius)",
              textDecoration: "none",
              cursor: "pointer",
              transition: "opacity 0.15s",
            }}
          >
            Download
          </a>
        )}

        {isError && (
          <span
            style={{
              fontSize: "0.75rem",
              color: "var(--color-danger)",
              fontWeight: 500,
            }}
          >
            Failed
          </span>
        )}
      </div>
    </div>
  );
}

function GroupCard({ group }: { group: PostGroup }) {
  const state = getGroupState(group.id);
  const isExpanded = expandedGroupId.value === group.id;
  const isIdle = state.status === "idle";
  const isProcessing = state.status === "processing";
  const isComplete = state.status === "complete";
  const isError = state.status === "error";

  // Get media items for preview
  const previewItems = group.keys
    .slice(0, 6)
    .map((key) => groupableMedia.value.find((m) => m.key === key))
    .filter((m): m is GroupableMediaItem => m !== undefined);

  // Count photos and videos
  const allItems = group.keys
    .map((key) => groupableMedia.value.find((m) => m.key === key))
    .filter((m): m is GroupableMediaItem => m !== undefined);
  const photoCount = allItems.filter((m) => m.type === "Photo").length;
  const videoCount = allItems.filter((m) => m.type === "Video").length;

  // Count completed bundles
  const completedBundles = state.bundles.filter(
    (b) => b.status === "complete",
  ).length;
  const totalBundles = state.bundles.length;

  return (
    <div
      class="card"
      style={{
        marginBottom: "0.75rem",
        border: isComplete
          ? "1px solid var(--color-success)"
          : isError
            ? "1px solid var(--color-danger)"
            : "1px solid var(--color-border)",
      }}
    >
      {/* Group header */}
      <div
        onClick={() => {
          expandedGroupId.value = isExpanded ? null : group.id;
        }}
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          cursor: "pointer",
          gap: "1rem",
        }}
      >
        <div style={{ flex: 1 }}>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "0.75rem",
              marginBottom: "0.375rem",
            }}
          >
            {/* Expand/collapse indicator */}
            <span
              style={{
                fontSize: "0.75rem",
                color: "var(--color-text-secondary)",
                transition: "transform 0.15s",
                transform: isExpanded ? "rotate(90deg)" : "rotate(0deg)",
              }}
            >
              &#9654;
            </span>

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

            {/* Status badge */}
            {isComplete && (
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
                Ready
              </span>
            )}
            {isProcessing && (
              <span
                style={{
                  fontSize: "0.625rem",
                  padding: "0.125rem 0.375rem",
                  borderRadius: "3px",
                  background: "rgba(108, 140, 255, 0.15)",
                  color: "var(--color-primary)",
                  fontWeight: 600,
                }}
              >
                Creating ZIPs... {completedBundles}/{totalBundles}
              </span>
            )}
          </div>

          <div
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              marginLeft: "1.5rem",
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
        <div
          style={{
            display: "flex",
            gap: "2px",
            flexShrink: 0,
          }}
        >
          {previewItems.map((item) => (
            <div
              key={item.key}
              style={{
                width: "2rem",
                height: "2rem",
                borderRadius: "3px",
                overflow: "hidden",
                background: "var(--color-surface-hover)",
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
            </div>
          ))}
          {group.keys.length > 6 && (
            <div
              style={{
                width: "2rem",
                height: "2rem",
                borderRadius: "3px",
                background: "var(--color-surface-hover)",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                fontSize: "0.5625rem",
                color: "var(--color-text-secondary)",
              }}
            >
              +{group.keys.length - 6}
            </div>
          )}
        </div>

        {/* Download button */}
        <div style={{ flexShrink: 0 }}>
          {isIdle && (
            <button
              class="primary"
              onClick={(e) => {
                e.stopPropagation();
                handleDownload(group);
              }}
              style={{ fontSize: "0.8125rem" }}
            >
              Prepare Download
            </button>
          )}
          {isError && (
            <button
              class="outline"
              onClick={(e) => {
                e.stopPropagation();
                handleDownload(group);
              }}
              style={{ fontSize: "0.8125rem" }}
            >
              Retry
            </button>
          )}
        </div>
      </div>

      {/* Expanded: bundle list */}
      {isExpanded && (isComplete || isProcessing || isError) && (
        <div style={{ marginTop: "1rem" }}>
          {state.bundles.length === 0 && isProcessing && (
            <div
              style={{
                textAlign: "center",
                padding: "1.5rem",
                color: "var(--color-text-secondary)",
                fontSize: "0.875rem",
              }}
            >
              Calculating bundles...
            </div>
          )}

          {state.bundles.length > 0 && (
            <div
              style={{
                display: "flex",
                flexDirection: "column",
                gap: "0.5rem",
              }}
            >
              {state.bundles.map((bundle, i) => (
                <BundleCard key={`${group.id}-${i}`} bundle={bundle} />
              ))}
            </div>
          )}

          {isComplete && (
            <div
              style={{
                marginTop: "0.75rem",
                padding: "0.5rem 0.75rem",
                background: "rgba(108, 200, 108, 0.06)",
                borderRadius: "var(--radius)",
                fontSize: "0.75rem",
                color: "var(--color-success)",
              }}
            >
              All bundles ready. Download links expire in 1 hour.
            </div>
          )}

          {isError && state.error && (
            <div
              style={{
                marginTop: "0.75rem",
                padding: "0.5rem 0.75rem",
                background: "rgba(255, 107, 107, 0.06)",
                borderRadius: "var(--radius)",
                fontSize: "0.75rem",
                color: "var(--color-danger)",
              }}
            >
              {state.error}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// --- Main Component ---

export function DownloadView() {
  const groups = postGroups.value;

  return (
    <div>
      {/* Info bar */}
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
            {completedGroups.value > 0 && (
              <>
                {" — "}
                <span style={{ color: "var(--color-success)" }}>
                  {completedGroups.value} ready for download
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
          Photos are bundled into one ZIP. Videos are split into bundles
          under 375 MB each for fast downloads.
        </div>
      </div>

      {/* Group list */}
      {groups.map((group) => (
        <GroupCard key={group.id} group={group} />
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
        <span style={{ fontSize: "0.875rem" }}>
          {completedGroups.value === 0 ? (
            <span style={{ color: "var(--color-text-secondary)" }}>
              Click "Prepare Download" on a group to create ZIP bundles
            </span>
          ) : (
            <>
              <strong style={{ color: "var(--color-success)" }}>
                {completedGroups.value} of {totalGroups.value}
              </strong>
              <span style={{ color: "var(--color-text-secondary)" }}>
                {" "}
                group{completedGroups.value !== 1 ? "s" : ""} ready
              </span>
            </>
          )}
        </span>
        <button class="outline" onClick={() => navigateBack()}>
          Back to Grouping
        </button>
      </div>
    </div>
  );
}
