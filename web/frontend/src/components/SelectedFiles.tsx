import { signal } from "@preact/signals";
import { currentStep, selectedPaths, triageJobId } from "../app";
import { startTriage } from "../api/client";

const loading = signal(false);
const error = signal<string | null>(null);

async function handleStartTriage() {
  loading.value = true;
  error.value = null;
  try {
    const res = await startTriage({ paths: selectedPaths.value });
    triageJobId.value = res.id;
    currentStep.value = "processing";
  } catch (e) {
    error.value = e instanceof Error ? e.message : "Failed to start triage";
  } finally {
    loading.value = false;
  }
}

function goBack() {
  currentStep.value = "browse";
}

export function SelectedFiles() {
  const paths = selectedPaths.value;

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
        The following {paths.length} path(s) will be sent for AI triage
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
        {paths.map((p) => (
          <div
            key={p}
            style={{
              padding: "0.375rem 0",
              fontSize: "0.8125rem",
              fontFamily: "var(--font-mono)",
              borderBottom: "1px solid var(--color-border)",
            }}
          >
            {p}
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
