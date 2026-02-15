import { thumbnailUrl } from "../../api/client";
import { BundleCard } from "./BundleCard";
import type {
  PostGroup,
  GroupableMediaItem,
  DownloadBundle,
} from "../../types/api";

export interface GroupDownloadState {
  jobId: string | null;
  status: "idle" | "processing" | "complete" | "error";
  bundles: DownloadBundle[];
  error: string | null;
}

interface GroupCardProps {
  group: PostGroup;
  state: GroupDownloadState;
  isExpanded: boolean;
  groupableMedia: GroupableMediaItem[];
  onToggleExpand: () => void;
  onDownload: () => void;
}

export function GroupCard({
  group,
  state,
  isExpanded,
  groupableMedia,
  onToggleExpand,
  onDownload,
}: GroupCardProps) {
  const isIdle = state.status === "idle";
  const isProcessing = state.status === "processing";
  const isComplete = state.status === "complete";
  const isError = state.status === "error";

  // Get media items for preview
  const previewItems = group.keys
    .slice(0, 6)
    .map((key) => groupableMedia.find((m) => m.key === key))
    .filter((m): m is GroupableMediaItem => m !== undefined);

  // Count photos and videos
  const allItems = group.keys
    .map((key) => groupableMedia.find((m) => m.key === key))
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
        onClick={onToggleExpand}
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
                  fontSize: "0.75rem",
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
                  fontSize: "0.75rem",
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
                fontSize: "0.75rem",
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
                onDownload();
              }}
              style={{ fontSize: "0.875rem" }}
            >
              Prepare Download
            </button>
          )}
          {isError && (
            <button
              class="outline"
              onClick={(e) => {
                e.stopPropagation();
                onDownload();
              }}
              style={{ fontSize: "0.875rem" }}
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
