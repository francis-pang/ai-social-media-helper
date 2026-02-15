import { thumbnailUrl } from "../../api/client";
import type { GroupableMediaItem } from "../../types/api";

interface MediaThumbnailProps {
  item: GroupableMediaItem;
  isInGroup: boolean;
  showAssignHint?: boolean;
  isDragging: boolean;
  onDragStart: () => void;
  onDragEnd: () => void;
  onClick: () => void;
}

/** A draggable media thumbnail. */
export function MediaThumbnail({
  item,
  isInGroup,
  showAssignHint,
  isDragging,
  onDragStart,
  onDragEnd,
  onClick,
}: MediaThumbnailProps) {
  return (
    <div
      draggable
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onClick={onClick}
      title={
        isInGroup
          ? `${item.filename} — click to remove from group`
          : showAssignHint
            ? `${item.filename} — click to add to selected group`
            : item.filename
      }
      style={{
        position: "relative",
        borderRadius: "var(--radius)",
        overflow: "hidden",
        cursor: "grab",
        opacity: isDragging ? 0.4 : 1,
        transition: "opacity 0.15s, transform 0.15s",
        border: "2px solid transparent",
        background: "var(--color-bg)",
      }}
    >
      <div
        style={{
          width: "100%",
          aspectRatio: "1",
          background: "var(--color-surface-hover)",
        }}
      >
        <img
          src={thumbnailUrl(item.thumbnailKey)}
          alt={item.filename}
          loading="lazy"
          style={{
            width: "100%",
            height: "100%",
            objectFit: "cover",
            pointerEvents: "none",
          }}
          onError={(e) => {
            (e.target as HTMLImageElement).style.display = "none";
          }}
        />
      </div>

      {/* Type badge */}
      {item.type === "Video" && (
        <span
          style={{
            position: "absolute",
            top: "0.25rem",
            left: "0.25rem",
            fontSize: "0.75rem",
            padding: "0.0625rem 0.25rem",
            borderRadius: "3px",
            background: "rgba(108, 140, 255, 0.85)",
            color: "#fff",
            fontWeight: 600,
          }}
        >
          Video
        </span>
      )}

      {/* Remove indicator when in group */}
      {isInGroup && (
        <div
          style={{
            position: "absolute",
            top: "0.25rem",
            right: "0.25rem",
            width: "1rem",
            height: "1rem",
            borderRadius: "50%",
            background: "rgba(255, 107, 107, 0.85)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            fontSize: "0.75rem",
            color: "#fff",
            fontWeight: 700,
            lineHeight: 1,
          }}
        >
          ×
        </div>
      )}

      {/* Filename */}
      <div
        style={{
          padding: "0.25rem 0.375rem",
          fontSize: "0.75rem",
          fontFamily: "var(--font-mono)",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
          color: "var(--color-text-secondary)",
        }}
      >
        {item.filename}
      </div>
    </div>
  );
}
