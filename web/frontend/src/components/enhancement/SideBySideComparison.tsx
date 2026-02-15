import { thumbnailUrl } from "../../api/client";
import { openMediaPlayer } from "../MediaPlayer";
import { getPhaseLabel, getPhaseColor } from "./EnhancementCard";
import type { EnhancementItem } from "../../types/api";

interface SideBySideComparisonProps {
  item: EnhancementItem;
  feedbackText: string;
  onFeedbackInput: (text: string) => void;
  feedbackLoading: boolean;
  onSubmitFeedback: () => void;
}

export function SideBySideComparison({
  item,
  feedbackText,
  onFeedbackInput,
  feedbackLoading,
  onSubmitFeedback,
}: SideBySideComparisonProps) {
  const originalThumb = thumbnailUrl(item.originalThumbKey || item.key);
  const enhancedThumb = item.enhancedThumbKey
    ? thumbnailUrl(item.enhancedThumbKey)
    : null;

  return (
    <div class="card" style={{ marginBottom: "1.5rem" }}>
      <h3
        style={{
          marginBottom: "1rem",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
        }}
      >
        <span>{item.filename}</span>
        <span
          style={{
            fontSize: "0.75rem",
            color: getPhaseColor(item.phase),
            fontWeight: 600,
          }}
        >
          {getPhaseLabel(item.phase)}
        </span>
      </h3>

      {/* Side-by-side images */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: enhancedThumb ? "1fr 1fr" : "1fr",
          gap: "1rem",
          marginBottom: "1rem",
        }}
      >
        {/* Original */}
        <div>
          <div
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              marginBottom: "0.375rem",
              textAlign: "center",
            }}
          >
            Original
          </div>
          <div
            style={{
              background: "var(--color-surface-hover)",
              borderRadius: "var(--radius)",
              overflow: "hidden",
              aspectRatio: "4/3",
            }}
          >
            <img
              src={originalThumb}
              alt={`Original: ${item.filename}`}
              onClick={() => openMediaPlayer(item.originalKey, "Photo", item.filename)}
              style={{
                width: "100%",
                height: "100%",
                objectFit: "contain",
                cursor: "zoom-in",
              }}
              onError={(e) => {
                (e.target as HTMLImageElement).style.display = "none";
              }}
            />
          </div>
        </div>

        {/* Enhanced */}
        {enhancedThumb && (
          <div>
            <div
              style={{
                fontSize: "0.75rem",
                color: "var(--color-text-secondary)",
                marginBottom: "0.375rem",
                textAlign: "center",
              }}
            >
              Enhanced
              {item.analysis && (
                <span
                  style={{
                    marginLeft: "0.375rem",
                    color:
                      item.analysis.professionalScore >= 8.5
                        ? "var(--color-success)"
                        : "var(--color-warning, #ffc107)",
                  }}
                >
                  (Score: {item.analysis.professionalScore.toFixed(1)})
                </span>
              )}
            </div>
            <div
              style={{
                background: "var(--color-surface-hover)",
                borderRadius: "var(--radius)",
                overflow: "hidden",
                aspectRatio: "4/3",
              }}
            >
              <img
                src={enhancedThumb}
                alt={`Enhanced: ${item.filename}`}
                onClick={() => openMediaPlayer(item.enhancedKey, "Photo", `${item.filename} (enhanced)`)}
                style={{
                  width: "100%",
                  height: "100%",
                  objectFit: "contain",
                  cursor: "zoom-in",
                }}
                onError={(e) => {
                  (e.target as HTMLImageElement).style.display = "none";
                }}
              />
            </div>
          </div>
        )}
      </div>

      {/* Enhancement details */}
      {item.phase1Text && (
        <div style={{ marginBottom: "0.75rem" }}>
          <div
            style={{
              fontSize: "0.75rem",
              fontWeight: 600,
              color: "var(--color-text-secondary)",
              marginBottom: "0.25rem",
            }}
          >
            Changes Applied:
          </div>
          <div
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              lineHeight: 1.5,
              background: "var(--color-bg)",
              padding: "0.5rem 0.75rem",
              borderRadius: "var(--radius)",
            }}
          >
            {item.phase1Text}
          </div>
        </div>
      )}

      {/* Analysis details */}
      {item.analysis && !item.analysis.noFurtherEditsNeeded && (
        <div style={{ marginBottom: "0.75rem" }}>
          <div
            style={{
              fontSize: "0.75rem",
              fontWeight: 600,
              color: "var(--color-text-secondary)",
              marginBottom: "0.25rem",
            }}
          >
            Analysis:
          </div>
          <div
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
              lineHeight: 1.5,
              background: "var(--color-bg)",
              padding: "0.5rem 0.75rem",
              borderRadius: "var(--radius)",
            }}
          >
            <div>{item.analysis.overallAssessment}</div>
            {item.analysis.remainingImprovements.length > 0 && (
              <ul
                style={{
                  margin: "0.375rem 0 0 0",
                  paddingLeft: "1.25rem",
                }}
              >
                {item.analysis.remainingImprovements.map((imp, i) => (
                  <li key={i} style={{ marginBottom: "0.25rem" }}>
                    <span
                      style={{
                        fontWeight: 600,
                        color:
                          imp.impact === "high"
                            ? "var(--color-danger)"
                            : "var(--color-text-secondary)",
                      }}
                    >
                      [{imp.impact}]
                    </span>{" "}
                    {imp.description}
                    {imp.imagenSuitable && (
                      <span
                        style={{
                          fontSize: "0.75rem",
                          color: "var(--color-primary)",
                          marginLeft: "0.375rem",
                        }}
                      >
                        (surgical edit)
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}

      {/* Feedback history */}
      {item.feedbackHistory && item.feedbackHistory.length > 0 && (
        <div style={{ marginBottom: "0.75rem" }}>
          <div
            style={{
              fontSize: "0.75rem",
              fontWeight: 600,
              color: "var(--color-text-secondary)",
              marginBottom: "0.25rem",
            }}
          >
            Feedback History:
          </div>
          {item.feedbackHistory.map((fb, i) => (
            <div
              key={i}
              style={{
                fontSize: "0.75rem",
                background: "var(--color-bg)",
                padding: "0.375rem 0.75rem",
                borderRadius: "var(--radius)",
                marginBottom: "0.375rem",
                borderLeft: `3px solid ${fb.success ? "var(--color-success)" : "var(--color-danger)"}`,
              }}
            >
              <div style={{ fontWeight: 600 }}>You: {fb.userFeedback}</div>
              <div style={{ color: "var(--color-text-secondary)" }}>
                AI ({fb.method}): {fb.modelResponse}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Feedback input */}
      {(item.phase === "complete" || item.phase === "feedback") && (
        <div
          style={{
            display: "flex",
            gap: "0.5rem",
            alignItems: "flex-start",
          }}
        >
          <textarea
            value={feedbackText}
            onInput={(e) => {
              onFeedbackInput((e.target as HTMLTextAreaElement).value);
            }}
            placeholder='Give feedback, e.g., "make the sky more blue", "remove the trash can on the right"...'
            style={{
              flex: 1,
              minHeight: "2.5rem",
              resize: "vertical",
              fontSize: "0.875rem",
            }}
            disabled={feedbackLoading}
          />
          <button
            class="primary"
            onClick={onSubmitFeedback}
            disabled={!feedbackText.trim() || feedbackLoading}
            style={{ whiteSpace: "nowrap" }}
          >
            {feedbackLoading ? "Processing..." : "Apply Feedback"}
          </button>
        </div>
      )}
    </div>
  );
}
