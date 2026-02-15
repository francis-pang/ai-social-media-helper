import { thumbnailUrl } from "../../api/client";
import type { PostGroup, GroupableMediaItem } from "../../types/api";
import { dragOverTarget, groupableMedia } from "./state";
import { MAX_ITEMS_PER_GROUP } from "./useGroupOperations";

interface GroupIconProps {
  group: PostGroup;
  isSelected: boolean;
  onSelect: () => void;
  onDelete: () => void;
  onDragOver: (e: DragEvent) => void;
  onDragEnter: () => void;
  onDragLeave: () => void;
  onDrop: (e: DragEvent) => void;
}

/** A compact group icon in the group strip. */
export function GroupIcon({
  group,
  isSelected,
  onSelect,
  onDelete,
  onDragOver,
  onDragEnter,
  onDragLeave,
  onDrop,
}: GroupIconProps) {
  const isOver = dragOverTarget.value === group.id;
  const isFull = group.keys.length >= MAX_ITEMS_PER_GROUP;
  // Get first 4 items for the mosaic preview
  const previewItems = group.keys
    .slice(0, 4)
    .map((key) => groupableMedia.value.find((m) => m.key === key))
    .filter((m): m is GroupableMediaItem => m !== undefined);

  return (
    <div
      onClick={onSelect}
      onDragOver={onDragOver}
      onDragEnter={onDragEnter}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
      style={{
        minWidth: "8.5rem",
        maxWidth: "10rem",
        padding: "0.5rem",
        background: isSelected
          ? "rgba(108, 140, 255, 0.12)"
          : "var(--color-bg)",
        border: isOver
          ? "2px dashed var(--color-primary)"
          : isSelected
            ? "2px solid var(--color-primary)"
            : "2px solid var(--color-border)",
        borderRadius: "var(--radius)",
        cursor: "pointer",
        transition: "border-color 0.15s, background 0.15s",
        flexShrink: 0,
        position: "relative",
      }}
    >
      {/* Delete button */}
      <button
        onClick={(e) => {
          e.stopPropagation();
          onDelete();
        }}
        style={{
          position: "absolute",
          top: "0.25rem",
          right: "0.25rem",
          width: "1.125rem",
          height: "1.125rem",
          borderRadius: "50%",
          background: "var(--color-surface-hover)",
          border: "none",
          color: "var(--color-text-secondary)",
          fontSize: "0.75rem",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          padding: 0,
          cursor: "pointer",
          lineHeight: 1,
        }}
        title="Delete group"
      >
        ×
      </button>

      {/* Mini mosaic preview */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "1fr 1fr",
          gap: "2px",
          width: "3rem",
          height: "3rem",
          margin: "0 auto 0.375rem",
          borderRadius: "4px",
          overflow: "hidden",
          background: "var(--color-surface-hover)",
        }}
      >
        {[0, 1, 2, 3].map((i) => {
          const item = previewItems[i];
          return (
            <div
              key={i}
              style={{
                background: "var(--color-surface-hover)",
                overflow: "hidden",
              }}
            >
              {item && (
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
              )}
            </div>
          );
        })}
      </div>

      {/* Group name (truncated) */}
      <div
        style={{
          fontSize: "0.75rem",
          fontWeight: 600,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
          textAlign: "center",
          marginBottom: "0.125rem",
          color: group.label ? "var(--color-text)" : "var(--color-text-secondary)",
        }}
        title={group.label || "Untitled group"}
      >
        {group.label || "Untitled"}
      </div>

      {/* Item count */}
      <div
        style={{
          fontSize: "0.75rem",
          textAlign: "center",
          color: isFull
            ? "var(--color-danger)"
            : "var(--color-text-secondary)",
        }}
      >
        {group.keys.length}/{MAX_ITEMS_PER_GROUP}
      </div>
    </div>
  );
}

interface NewGroupButtonProps {
  onCreateGroup: () => void;
  onDragOver: (e: DragEvent) => void;
  onDragEnter: () => void;
  onDragLeave: () => void;
  onDrop: (e: DragEvent) => void;
}

/** The "+ New Group" drop target / button. */
export function NewGroupButton({
  onCreateGroup,
  onDragOver,
  onDragEnter,
  onDragLeave,
  onDrop,
}: NewGroupButtonProps) {
  const isOver = dragOverTarget.value === "__new__";

  return (
    <div
      onClick={onCreateGroup}
      onDragOver={onDragOver}
      onDragEnter={onDragEnter}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
      style={{
        minWidth: "8.5rem",
        maxWidth: "10rem",
        padding: "0.5rem",
        background: isOver
          ? "rgba(108, 140, 255, 0.08)"
          : "var(--color-bg)",
        border: isOver
          ? "2px dashed var(--color-primary)"
          : "2px dashed var(--color-border)",
        borderRadius: "var(--radius)",
        cursor: "pointer",
        transition: "border-color 0.15s, background 0.15s",
        flexShrink: 0,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        minHeight: "6.5rem",
      }}
    >
      <div
        style={{
          fontSize: "1.5rem",
          color: isOver
            ? "var(--color-primary)"
            : "var(--color-text-secondary)",
          lineHeight: 1,
          marginBottom: "0.375rem",
        }}
      >
        +
      </div>
      <div
        style={{
          fontSize: "0.75rem",
          color: isOver
            ? "var(--color-primary)"
            : "var(--color-text-secondary)",
          fontWeight: 500,
        }}
      >
        New Group
      </div>
    </div>
  );
}
