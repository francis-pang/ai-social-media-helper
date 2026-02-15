import { openMediaPlayer } from "./MediaPlayer";
import type { SelectionItem } from "../types/api";

export function SelectedCard({
  item,
  isOverride,
  onToggle,
}: {
  item: SelectionItem;
  isOverride: boolean;
  onToggle: () => void;
}) {
  return (
    <div
      style={{
        background: isOverride
          ? "rgba(108, 140, 255, 0.08)"
          : "var(--color-bg)",
        borderRadius: "var(--radius)",
        overflow: "hidden",
        border: isOverride
          ? "2px solid var(--color-primary)"
          : "2px solid transparent",
      }}
    >
      {/* Thumbnail */}
      <div
        style={{
          width: "100%",
          aspectRatio: "1",
          background: "var(--color-surface-hover)",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          position: "relative",
        }}
      >
        <img
          src={item.thumbnailUrl}
          alt={item.filename}
          loading="lazy"
          onClick={() => openMediaPlayer(item.key, item.type, item.filename)}
          style={{
            width: "100%",
            height: "100%",
            objectFit: "cover",
            cursor: "zoom-in",
          }}
          onError={(e) => {
            (e.target as HTMLImageElement).style.display = "none";
          }}
        />
        {/* Rank badge */}
        <span
          style={{
            position: "absolute",
            top: "0.375rem",
            left: "0.375rem",
            background: "var(--color-primary)",
            color: "#fff",
            fontSize: "0.75rem",
            fontWeight: 700,
            width: "1.5rem",
            height: "1.5rem",
            borderRadius: "50%",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
          }}
        >
          {item.rank}
        </span>
        {/* Type badge */}
        <span
          style={{
            position: "absolute",
            top: "0.375rem",
            right: "0.375rem",
            fontSize: "0.75rem",
            padding: "0.125rem 0.375rem",
            borderRadius: "4px",
            background:
              item.type === "Video"
                ? "rgba(108, 140, 255, 0.85)"
                : "rgba(81, 207, 102, 0.85)",
            color: "#fff",
            fontWeight: 600,
            textTransform: "uppercase",
          }}
        >
          {item.type}
        </span>
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
            marginBottom: "0.25rem",
          }}
        >
          {item.filename}
        </div>
        {item.scene && (
          <div
            style={{
              fontSize: "0.75rem",
              color: "var(--color-primary)",
              marginBottom: "0.25rem",
            }}
          >
            {item.scene}
          </div>
        )}
        <div
          style={{
            fontSize: "0.75rem",
            color: "var(--color-text-secondary)",
            lineHeight: 1.4,
          }}
        >
          {item.justification}
        </div>
        {item.comparisonNote && (
          <div
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              fontStyle: "italic",
              marginTop: "0.25rem",
            }}
          >
            {item.comparisonNote}
          </div>
        )}
        {/* Remove from selection button */}
        <button
          class="outline"
          onClick={onToggle}
          style={{
            marginTop: "0.5rem",
            padding: "0.125rem 0.5rem",
            fontSize: "0.75rem",
            width: "100%",
          }}
        >
          {isOverride ? "Undo Add" : "Remove"}
        </button>
      </div>
    </div>
  );
}
