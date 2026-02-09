import { signal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { getFullMediaUrl, openFullImage } from "../api/client";

// --- State (DDR-038) ---

/** Whether the media player overlay is visible. */
const isOpen = signal(false);

/** S3 key (or local path) of the media to display. */
const mediaKey = signal("");

/** Media type determines whether to render <img> or <video>. */
const mediaType = signal<"Photo" | "Video">("Photo");

/** Display filename shown in the overlay header. */
const mediaFilename = signal("");

/** Resolved full-resolution URL for the media. */
const resolvedUrl = signal<string | null>(null);

/** Whether the URL is still loading. */
const loading = signal(false);

// --- Public API ---

/**
 * Open the media player overlay with the given media.
 *
 * @param key - S3 key or local filesystem path of the media file.
 * @param type - Whether the media is a photo or video.
 * @param filename - Display filename for the overlay header.
 */
export function openMediaPlayer(
  key: string,
  type: "Photo" | "Video",
  filename: string,
) {
  mediaKey.value = key;
  mediaType.value = type;
  mediaFilename.value = filename;
  resolvedUrl.value = null;
  loading.value = true;
  isOpen.value = true;

  // Resolve the full-resolution URL asynchronously
  getFullMediaUrl(key)
    .then((url) => {
      resolvedUrl.value = url;
    })
    .catch(() => {
      resolvedUrl.value = null;
    })
    .finally(() => {
      loading.value = false;
    });
}

/** Close the media player overlay and reset state. */
export function closeMediaPlayer() {
  isOpen.value = false;
  mediaKey.value = "";
  mediaFilename.value = "";
  resolvedUrl.value = null;
  loading.value = false;
}

// --- Component ---

export function MediaPlayer() {
  const backdropRef = useRef<HTMLDivElement>(null);

  // Keyboard handler: Escape to close
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape" && isOpen.value) {
        closeMediaPlayer();
      }
    }
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, []);

  // Prevent body scroll when overlay is open
  useEffect(() => {
    if (isOpen.value) {
      document.body.style.overflow = "hidden";
    } else {
      document.body.style.overflow = "";
    }
    return () => {
      document.body.style.overflow = "";
    };
  });

  if (!isOpen.value) return null;

  const url = resolvedUrl.value;
  const isVideo = mediaType.value === "Video";

  return (
    <div
      ref={backdropRef}
      onClick={(e) => {
        // Close only when clicking directly on the backdrop, not on child elements
        if (e.target === backdropRef.current) {
          closeMediaPlayer();
        }
      }}
      style={{
        position: "fixed",
        inset: 0,
        zIndex: 9999,
        background: "rgba(0, 0, 0, 0.85)",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
      }}
    >
      {/* Header bar */}
      <div
        style={{
          position: "absolute",
          top: 0,
          left: 0,
          right: 0,
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          padding: "0.75rem 1.5rem",
          background: "rgba(0, 0, 0, 0.6)",
          backdropFilter: "blur(8px)",
          zIndex: 1,
        }}
      >
        {/* Filename */}
        <div
          style={{
            fontSize: "0.875rem",
            fontFamily: "var(--font-mono)",
            color: "#e4e6f0",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            marginRight: "1rem",
          }}
        >
          {mediaFilename.value}
        </div>

        {/* Action buttons */}
        <div style={{ display: "flex", gap: "0.5rem", flexShrink: 0 }}>
          <button
            class="outline"
            onClick={(e) => {
              e.stopPropagation();
              openFullImage(mediaKey.value);
            }}
            style={{
              fontSize: "0.75rem",
              padding: "0.25rem 0.75rem",
              color: "#e4e6f0",
              borderColor: "rgba(255, 255, 255, 0.2)",
            }}
          >
            Open in New Tab
          </button>
          <button
            class="outline"
            onClick={(e) => {
              e.stopPropagation();
              closeMediaPlayer();
            }}
            style={{
              fontSize: "0.875rem",
              padding: "0.25rem 0.625rem",
              color: "#e4e6f0",
              borderColor: "rgba(255, 255, 255, 0.2)",
              fontWeight: 700,
              lineHeight: 1,
            }}
          >
            ✕
          </button>
        </div>
      </div>

      {/* Media content */}
      <div
        style={{
          maxWidth: "90vw",
          maxHeight: "80vh",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
        }}
      >
        {/* Loading state */}
        {loading.value && (
          <div style={{ color: "#e4e6f0", fontSize: "1rem" }}>Loading...</div>
        )}

        {/* Error state */}
        {!loading.value && !url && (
          <div
            style={{
              color: "var(--color-danger)",
              fontSize: "0.875rem",
              textAlign: "center",
            }}
          >
            <div style={{ marginBottom: "0.5rem" }}>Failed to load media.</div>
            <button
              class="outline"
              onClick={(e) => {
                e.stopPropagation();
                openFullImage(mediaKey.value);
              }}
              style={{
                fontSize: "0.75rem",
                color: "#e4e6f0",
                borderColor: "rgba(255, 255, 255, 0.2)",
              }}
            >
              Try opening in new tab
            </button>
          </div>
        )}

        {/* Photo viewer */}
        {!loading.value && url && !isVideo && (
          <img
            src={url}
            alt={mediaFilename.value}
            style={{
              maxWidth: "90vw",
              maxHeight: "80vh",
              objectFit: "contain",
              borderRadius: "var(--radius)",
            }}
          />
        )}

        {/* Video player */}
        {!loading.value && url && isVideo && (
          <video
            src={url}
            controls
            autoPlay
            style={{
              maxWidth: "90vw",
              maxHeight: "80vh",
              borderRadius: "var(--radius)",
            }}
          >
            Your browser does not support the video tag.
          </video>
        )}
      </div>

      {/* Keyboard hint */}
      <div
        style={{
          position: "absolute",
          bottom: "1rem",
          fontSize: "0.6875rem",
          color: "rgba(255, 255, 255, 0.4)",
        }}
      >
        Press Esc or click outside to close
      </div>
    </div>
  );
}
