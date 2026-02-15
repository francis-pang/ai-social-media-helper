import { formatBytes } from "../../utils/format";
import type { DownloadBundle } from "../../types/api";

export function BundleCard({ bundle }: { bundle: DownloadBundle }) {
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
              fontSize: "0.75rem",
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
              fontSize: "0.875rem",
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
              fontSize: "0.875rem",
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
