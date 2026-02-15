import { thumbnailUrl } from "../../api/client";
import type { EnhancementItem } from "../../types/api";

export function getPhaseLabel(phase: string): string {
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

export function getPhaseColor(phase: string): string {
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

export function EnhancementCard({
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
            fontSize: "0.75rem",
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
              fontSize: "0.75rem",
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
              fontSize: "0.75rem",
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
              fontSize: "0.75rem",
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
