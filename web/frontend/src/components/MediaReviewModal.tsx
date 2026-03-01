import { useState, useEffect, useRef, useCallback } from "preact/hooks";

interface MediaReviewModalProps {
  items: Array<{
    id: string;
    url: string;
    filename: string;
    type: "image" | "video";
    reason?: string;
    reasoning?: string;
    confidence?: number;
    timestamp?: string;
    metadata?: Record<string, string | number>;
  }>;
  initialIndex: number;
  onClose: () => void;
  onKeep: (id: string) => void;
  onDelete: (id: string) => void;
  onReportFalsePositive?: (id: string) => void;
}

export function MediaReviewModal({
  items,
  initialIndex,
  onClose,
  onKeep,
  onDelete,
  onReportFalsePositive,
}: MediaReviewModalProps) {
  const [currentIndex, setCurrentIndex] = useState(initialIndex);
  const [exposure, setExposure] = useState(1.0);
  const [aiVisionOn, setAiVisionOn] = useState(false);
  const [showReasoning, setShowReasoning] = useState(true);
  const [showMetadata, setShowMetadata] = useState(false);
  const queueRef = useRef<HTMLDivElement>(null);

  const item = items[currentIndex];

  const navigate = useCallback(
    (index: number) => {
      if (index >= 0 && index < items.length) {
        setCurrentIndex(index);
      }
    },
    [items.length],
  );

  useEffect(() => {
    setExposure(1.0);
    setAiVisionOn(false);
  }, [currentIndex]);

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") {
        onClose();
      } else if (e.key === "ArrowLeft") {
        setCurrentIndex((i) => Math.max(0, i - 1));
      } else if (e.key === "ArrowRight") {
        setCurrentIndex((i) => Math.min(items.length - 1, i + 1));
      }
    }
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [items.length, onClose]);

  useEffect(() => {
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = "";
    };
  }, []);

  useEffect(() => {
    if (queueRef.current) {
      const active = queueRef.current.children[currentIndex] as HTMLElement | undefined;
      active?.scrollIntoView({ behavior: "smooth", block: "nearest", inline: "center" });
    }
  }, [currentIndex]);

  if (!item) return null;

  const filterStyle = aiVisionOn ? `brightness(${exposure})` : undefined;
  const confidencePercent =
    item.confidence != null ? Math.round(item.confidence * 100) : null;

  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        zIndex: 9999,
        background: "#1a1a2e",
        display: "flex",
        flexDirection: "column",
      }}
    >
      {/* ── Top info bar ── */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          padding: "0.75rem 1.5rem",
          borderBottom: "1px solid rgba(255, 255, 255, 0.1)",
          flexShrink: 0,
        }}
      >
        <span
          style={{
            color: "#fff",
            fontFamily: "var(--font-mono)",
            fontSize: "0.875rem",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {item.filename}
        </span>

        <span
          style={{
            marginLeft: "0.75rem",
            background: "var(--color-danger)",
            color: "#fff",
            fontSize: "0.7rem",
            fontWeight: 600,
            padding: "0.2rem 0.5rem",
            borderRadius: "4px",
            textTransform: "uppercase",
            letterSpacing: "0.05em",
            flexShrink: 0,
          }}
        >
          Flagged
        </span>

        {item.timestamp && (
          <span
            style={{
              marginLeft: "0.75rem",
              color: "rgba(255, 255, 255, 0.5)",
              fontSize: "0.8rem",
              flexShrink: 0,
            }}
          >
            {item.timestamp}
          </span>
        )}

        <div style={{ flex: 1 }} />

        <button
          onClick={onClose}
          style={{
            background: "none",
            border: "none",
            color: "#fff",
            fontSize: "1.5rem",
            cursor: "pointer",
            padding: "0.25rem 0.5rem",
            lineHeight: 1,
            flexShrink: 0,
          }}
        >
          ✕
        </button>
      </div>

      {/* ── Media preview ── */}
      <div
        style={{
          flex: 1,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          overflow: "hidden",
          position: "relative",
        }}
      >
        {/* Left nav arrow */}
        {currentIndex > 0 && (
          <button
            onClick={() => navigate(currentIndex - 1)}
            style={{
              position: "absolute",
              left: "1rem",
              top: "50%",
              transform: "translateY(-50%)",
              width: "3rem",
              height: "3rem",
              borderRadius: "50%",
              background: "rgba(255, 255, 255, 0.15)",
              border: "none",
              color: "#fff",
              fontSize: "1.25rem",
              cursor: "pointer",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              zIndex: 2,
            }}
          >
            ◀
          </button>
        )}

        {item.type === "video" ? (
          <video
            key={item.id}
            src={item.url}
            controls
            playsInline
            style={{
              maxWidth: "100%",
              maxHeight: "100%",
              objectFit: "contain",
              filter: filterStyle,
            }}
          />
        ) : (
          <img
            key={item.id}
            src={item.url}
            alt={item.filename}
            style={{
              maxWidth: "100%",
              maxHeight: "100%",
              objectFit: "contain",
              filter: filterStyle,
            }}
          />
        )}

        {/* Right nav arrow */}
        {currentIndex < items.length - 1 && (
          <button
            onClick={() => navigate(currentIndex + 1)}
            style={{
              position: "absolute",
              right: "1rem",
              top: "50%",
              transform: "translateY(-50%)",
              width: "3rem",
              height: "3rem",
              borderRadius: "50%",
              background: "rgba(255, 255, 255, 0.15)",
              border: "none",
              color: "#fff",
              fontSize: "1.25rem",
              cursor: "pointer",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              zIndex: 2,
            }}
          >
            ▶
          </button>
        )}

        {/* ── AI Reasoning floating card ── */}
        {(item.reason || item.reasoning) && (
          <div
            style={{
              position: "absolute",
              bottom: "1rem",
              right: "1rem",
              zIndex: 3,
              display: "flex",
              flexDirection: "column",
              alignItems: "flex-end",
              gap: "0.4rem",
            }}
          >
            <button
              onClick={() => setShowReasoning((v) => !v)}
              style={{
                background: showReasoning
                  ? "var(--color-primary)"
                  : "rgba(255, 255, 255, 0.2)",
                color: "#fff",
                border: "none",
                borderRadius: "12px",
                padding: "0.3rem 0.75rem",
                fontSize: "0.75rem",
                fontWeight: 600,
                cursor: "pointer",
              }}
            >
              AI
            </button>

            {showReasoning && (
              <div
                style={{
                  background: "rgba(0, 0, 0, 0.75)",
                  backdropFilter: "blur(8px)",
                  borderRadius: "var(--radius)",
                  padding: "1rem",
                  maxWidth: "320px",
                }}
              >
                {item.reason && (
                  <div
                    style={{
                      color: "#fff",
                      fontWeight: 700,
                      fontSize: "0.9rem",
                      marginBottom: "0.4rem",
                    }}
                  >
                    {item.reason}
                  </div>
                )}

                {item.reasoning && (
                  <div
                    style={{
                      color: "rgba(255, 255, 255, 0.8)",
                      fontSize: "0.8rem",
                      lineHeight: 1.4,
                      marginBottom: confidencePercent != null ? "0.6rem" : 0,
                    }}
                  >
                    {item.reasoning}
                  </div>
                )}

                {confidencePercent != null && (
                  <div>
                    <div
                      style={{
                        display: "flex",
                        justifyContent: "space-between",
                        fontSize: "0.7rem",
                        color: "rgba(255, 255, 255, 0.6)",
                        marginBottom: "0.25rem",
                      }}
                    >
                      <span>Confidence</span>
                      <span>{confidencePercent}%</span>
                    </div>
                    <div
                      style={{
                        height: "4px",
                        borderRadius: "2px",
                        background: "rgba(255, 255, 255, 0.15)",
                        overflow: "hidden",
                      }}
                    >
                      <div
                        style={{
                          width: `${confidencePercent}%`,
                          height: "100%",
                          borderRadius: "2px",
                          background:
                            confidencePercent > 70
                              ? "var(--color-danger)"
                              : confidencePercent > 40
                                ? "var(--color-warning)"
                                : "var(--color-success)",
                        }}
                      />
                    </div>
                  </div>
                )}

                {item.metadata && Object.keys(item.metadata).length > 0 && (
                  <div>
                    <button
                      onClick={() => setShowMetadata((v) => !v)}
                      style={{
                        background: "none",
                        border: "none",
                        color: "rgba(255, 255, 255, 0.5)",
                        fontSize: "0.7rem",
                        cursor: "pointer",
                        padding: "0.4rem 0 0",
                        textDecoration: "underline",
                      }}
                    >
                      {showMetadata ? "Hide details" : "Show details"}
                    </button>
                    {showMetadata && (
                      <div
                        style={{
                          display: "grid",
                          gridTemplateColumns: "auto 1fr",
                          gap: "0.2rem 0.75rem",
                          marginTop: "0.5rem",
                          fontSize: "0.7rem",
                        }}
                      >
                        {Object.entries(item.metadata).map(([k, v]) => (
                          <div key={k} style={{ display: "contents" }}>
                            <span style={{ color: "rgba(255, 255, 255, 0.5)" }}>
                              {k}
                            </span>
                            <span style={{ color: "rgba(255, 255, 255, 0.8)" }}>
                              {String(v)}
                            </span>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
        )}
      </div>

      {/* ── Controls bar ── */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          padding: "0.75rem 1.5rem",
          background: "rgba(0, 0, 0, 0.3)",
          flexShrink: 0,
        }}
      >
        {/* Left: AI Vision toggle + exposure slider */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "1rem",
            flex: 1,
          }}
        >
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "0.5rem",
              color: "rgba(255, 255, 255, 0.8)",
              fontSize: "0.8rem",
              cursor: "pointer",
            }}
            onClick={() => setAiVisionOn((v) => !v)}
          >
            <div
              style={{
                width: "36px",
                height: "20px",
                borderRadius: "10px",
                background: aiVisionOn
                  ? "var(--color-primary)"
                  : "rgba(255, 255, 255, 0.2)",
                position: "relative",
                transition: "background 0.2s",
                flexShrink: 0,
              }}
            >
              <div
                style={{
                  position: "absolute",
                  top: "2px",
                  left: aiVisionOn ? "18px" : "2px",
                  width: "16px",
                  height: "16px",
                  borderRadius: "50%",
                  background: "#fff",
                  transition: "left 0.2s",
                }}
              />
            </div>
            AI Vision
          </div>

          {aiVisionOn && (
            <label
              style={{
                display: "flex",
                alignItems: "center",
                gap: "0.5rem",
                color: "rgba(255, 255, 255, 0.7)",
                fontSize: "0.75rem",
              }}
            >
              Exposure
              <input
                type="range"
                min="0.5"
                max="3.0"
                step="0.1"
                value={exposure}
                onInput={(e) =>
                  setExposure(
                    parseFloat((e.target as HTMLInputElement).value),
                  )
                }
                style={{ width: "120px", accentColor: "var(--color-primary)" }}
              />
              <span
                style={{
                  fontFamily: "var(--font-mono)",
                  minWidth: "2.5rem",
                }}
              >
                {exposure.toFixed(1)}
              </span>
            </label>
          )}
        </div>

        {/* Center: Keep / Delete */}
        <div style={{ display: "flex", gap: "0.75rem" }}>
          <button
            onClick={() => onKeep(item.id)}
            style={{
              background: "transparent",
              border: "2px solid rgba(255, 255, 255, 0.7)",
              color: "#fff",
              padding: "0.6rem 1.5rem",
              borderRadius: "var(--radius)",
              fontSize: "0.9rem",
              fontWeight: 600,
              cursor: "pointer",
            }}
          >
            Keep
          </button>
          <button
            onClick={() => onDelete(item.id)}
            style={{
              background: "var(--color-danger)",
              border: "none",
              color: "#fff",
              padding: "0.6rem 1.5rem",
              borderRadius: "var(--radius)",
              fontSize: "0.9rem",
              fontWeight: 600,
              cursor: "pointer",
            }}
          >
            Delete
          </button>
        </div>

        {/* Right: Report false positive */}
        <div style={{ flex: 1, display: "flex", justifyContent: "flex-end" }}>
          {onReportFalsePositive && (
            <button
              onClick={() => onReportFalsePositive(item.id)}
              style={{
                background: "none",
                border: "none",
                color: "rgba(255, 255, 255, 0.5)",
                fontSize: "0.75rem",
                textDecoration: "underline",
                cursor: "pointer",
              }}
            >
              Report False Positive
            </button>
          )}
        </div>
      </div>

      {/* ── Thumbnail queue strip ── */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          padding: "0.5rem 1rem",
          background: "rgba(0, 0, 0, 0.4)",
          flexShrink: 0,
          gap: "0.75rem",
        }}
      >
        <span
          style={{
            color: "#fff",
            fontSize: "0.7rem",
            fontWeight: 600,
            textTransform: "uppercase",
            letterSpacing: "0.05em",
            whiteSpace: "nowrap",
            flexShrink: 0,
          }}
        >
          Queue ({items.length})
        </span>

        <div
          ref={queueRef}
          style={{
            display: "flex",
            gap: "0.4rem",
            overflowX: "auto",
            flex: 1,
            paddingBlock: "0.25rem",
          }}
        >
          {items.map((thumb, i) => (
            <img
              key={thumb.id}
              src={thumb.url}
              alt={thumb.filename}
              onClick={() => navigate(i)}
              style={{
                width: "64px",
                height: "64px",
                objectFit: "cover",
                borderRadius: "4px",
                cursor: "pointer",
                border:
                  i === currentIndex
                    ? "2px solid var(--color-primary)"
                    : "2px solid transparent",
                opacity: i === currentIndex ? 1 : 0.6,
                flexShrink: 0,
              }}
            />
          ))}
        </div>

        <button
          onClick={onClose}
          style={{
            background: "none",
            border: "none",
            color: "#fff",
            fontSize: "0.75rem",
            cursor: "pointer",
            whiteSpace: "nowrap",
            flexShrink: 0,
          }}
        >
          Back to Grid
        </button>
      </div>
    </div>
  );
}
