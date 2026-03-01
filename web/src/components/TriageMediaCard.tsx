import { openMediaPlayer } from "./MediaPlayer";
import { isCloudMode, isVideoFile, thumbnailUrl } from "../api/client";
import type { TriageItem } from "../types/api";

/** Get the identifier for a triage item (key in cloud mode, path in local). */
export function itemId(item: TriageItem): string {
  return isCloudMode ? (item.key ?? item.path) : item.path;
}

/** Get the thumbnail source for a triage item (400px). */
export function itemThumb(item: TriageItem): string {
  if (item.thumbnailUrl) {
    return item.thumbnailUrl;
  }
  if (isCloudMode && item.key) {
    return thumbnailUrl(item.key);
  }
  return thumbnailUrl(item.path);
}

export function MediaCard({
  item,
  selectable,
  isSelected,
  onToggle,
  onReview,
}: {
  item: TriageItem;
  selectable: boolean;
  isSelected: boolean;
  onToggle?: () => void;
  onReview?: () => void;
}) {
  return (
    <div
      onClick={selectable ? onToggle : undefined}
      style={{
        background: isSelected
          ? "rgba(239, 68, 68, 0.08)"
          : "var(--color-surface)",
        border: isSelected
          ? "2px solid var(--color-danger)"
          : selectable
            ? "2px solid transparent"
            : "1px solid var(--color-border)",
        borderRadius: "var(--radius)",
        overflow: "hidden",
        cursor: selectable ? "pointer" : "default",
      }}
    >
      <div
        onClick={(e) => {
          e.stopPropagation();
          if (onReview) {
            onReview();
            return;
          }
          const mediaKey = (isCloudMode && item.processedKey) || itemId(item);
          openMediaPlayer(
            mediaKey,
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
        {/* Fallback placeholder: hidden until onError triggers */}
        <div
          style={{
            display: "none",
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
              accentColor: "var(--color-danger)",
            }}
          />
        )}
        {selectable && (
          <span
            style={{
              position: "absolute",
              bottom: "0.5rem",
              left: "0.5rem",
              background: "rgba(239, 68, 68, 0.9)",
              color: "white",
              borderRadius: "999px",
              padding: "0.15rem 0.5rem",
              fontSize: "0.7rem",
              pointerEvents: "none",
              maxWidth: "70%",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {item.reason}
          </span>
        )}
      </div>
      <div style={{ padding: "0.75rem" }}>
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
        {selectable && (
          <>
            <div
              style={{
                fontSize: "1rem",
                color: "var(--color-text)",
                marginTop: "0.5rem",
                lineHeight: 1.4,
              }}
            >
              {item.reason}
            </div>
            {onReview && (
              <div
                onClick={(e) => {
                  e.stopPropagation();
                  onReview();
                }}
                style={{
                  color: "var(--color-primary)",
                  cursor: "pointer",
                  fontSize: "0.75rem",
                  marginTop: "0.375rem",
                }}
              >
                Review
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
