import { signal } from "@preact/signals";
import { selectedPaths, triageJobId, uploadSessionId, setStep } from "../app";
import { startTriage, isCloudMode } from "../api/client";

const loading = signal(false);
const error = signal<string | null>(null);

async function handleStartTriage() {
  loading.value = true;
  error.value = null;
  try {
    const req = isCloudMode
      ? { sessionId: uploadSessionId.value! }
      : { paths: selectedPaths.value };

    const res = await startTriage(req);
    triageJobId.value = res.id;
    setStep("processing");
  } catch (e) {
    error.value = e instanceof Error ? e.message : "Failed to start triage";
  } finally {
    loading.value = false;
  }
}

function goBack() {
  setStep("browse");
}

/** Extract just the filename from a path or S3 key. */
function basename(pathOrKey: string): string {
  const parts = pathOrKey.split("/");
  return parts[parts.length - 1] || pathOrKey;
}

export function SelectedFiles() {
  const items = selectedPaths.value;

  return (
    <div class="card">
      <h2>Files to triage</h2>
      <p
        style={{
          color: "var(--color-text-secondary)",
          fontSize: "0.875rem",
          marginBottom: "1rem",
        }}
      >
        The following {items.length} file(s) will be sent for AI triage
        analysis. Media files will be evaluated and categorized as keep or
        discard.
      </p>

      <div
        style={{
          background: "var(--color-bg)",
          borderRadius: "var(--radius)",
          padding: "0.75rem",
          maxHeight: "400px",
          overflowY: "auto",
          marginBottom: "1.5rem",
        }}
      >
        {items.map((p) => (
          <div
            key={p}
            style={{
              padding: "0.375rem 0",
              fontSize: "0.875rem",
              fontFamily: "var(--font-mono)",
              borderBottom: "1px solid var(--color-border)",
            }}
          >
            {isCloudMode ? basename(p) : p}
          </div>
        ))}
      </div>

      {error.value && (
        <div
          style={{
            color: "var(--color-danger)",
            marginBottom: "1rem",
            fontSize: "0.875rem",
          }}
        >
          {error.value}
        </div>
      )}

      <div style={{ display: "flex", gap: "0.75rem", justifyContent: "flex-end" }}>
        <button class="outline" onClick={goBack} disabled={loading.value}>
          Back
        </button>
        <button
          class="primary"
          onClick={handleStartTriage}
          disabled={loading.value}
        >
          {loading.value ? "Starting..." : "Start Triage"}
        </button>
      </div>
    </div>
  );
}
