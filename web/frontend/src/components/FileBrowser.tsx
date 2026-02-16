import { signal } from "@preact/signals";
import { pick } from "../api/client";
import { currentStep, selectedPaths } from "../app";

const pickedPaths = signal<string[]>([]);
const loading = signal(false);
const error = signal<string | null>(null);

async function openPicker(mode: "files" | "directory") {
  loading.value = true;
  error.value = null;
  try {
    const res = await pick({ mode });
    if (res.canceled) {
      return;
    }
    // Append to existing selection (allow multiple pick operations)
    const combined = new Set([...pickedPaths.value, ...res.paths]);
    pickedPaths.value = Array.from(combined);
  } catch (e) {
    error.value = e instanceof Error ? e.message : "Failed to open file picker";
  } finally {
    loading.value = false;
  }
}

function removePath(path: string) {
  pickedPaths.value = pickedPaths.value.filter((p) => p !== path);
}

function clearAll() {
  pickedPaths.value = [];
}

/** Reset the file browser state. Called when returning to browse from triage. */
export function resetFileBrowserState() {
  pickedPaths.value = [];
  loading.value = false;
  error.value = null;
}

function proceedWithSelection() {
  selectedPaths.value = pickedPaths.value;
  currentStep.value = "confirm-files";
}

/** Extract just the filename from an absolute path. */
function basename(path: string): string {
  const parts = path.split("/");
  return parts[parts.length - 1] || path;
}

export function FileBrowser() {
  const count = pickedPaths.value.length;

  return (
    <div class="card">
      <p
        style={{
          color: "var(--color-text-secondary)",
          fontSize: "0.875rem",
          marginBottom: "1.5rem",
        }}
      >
        Choose media files or a folder using your system file picker.
      </p>

      <div
        style={{
          display: "flex",
          gap: "0.75rem",
          marginBottom: "1.5rem",
          flexWrap: "wrap",
        }}
      >
        <button
          class="primary"
          onClick={() => openPicker("files")}
          disabled={loading.value}
        >
          {loading.value ? "Opening..." : "Pick Files"}
        </button>
        <button
          class="outline"
          onClick={() => openPicker("directory")}
          disabled={loading.value}
        >
          {loading.value ? "Opening..." : "Pick Folder"}
        </button>
        {count > 0 && (
          <button
            class="outline"
            onClick={clearAll}
            disabled={loading.value}
            style={{ marginLeft: "auto" }}
          >
            Clear all
          </button>
        )}
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

      {count > 0 && (
        <div
          style={{
            background: "var(--color-bg)",
            borderRadius: "var(--radius)",
            padding: "0.5rem",
            maxHeight: "400px",
            overflowY: "auto",
            marginBottom: "1.5rem",
          }}
        >
          {pickedPaths.value.map((p) => (
            <div
              key={p}
              style={{
                display: "flex",
                alignItems: "center",
                gap: "0.75rem",
                padding: "0.375rem 0.5rem",
                borderBottom: "1px solid var(--color-border)",
              }}
            >
              <span
                style={{
                  flex: 1,
                  fontSize: "0.875rem",
                  fontFamily: "var(--font-mono)",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
                title={p}
              >
                {basename(p)}
              </span>
              <span
                style={{
                  fontSize: "0.75rem",
                  color: "var(--color-text-secondary)",
                  flexShrink: 0,
                  maxWidth: "50%",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                  direction: "rtl",
                  textAlign: "left",
                }}
                title={p}
              >
                {p}
              </span>
              <button
                class="outline"
                onClick={() => removePath(p)}
                style={{
                  padding: "0.125rem 0.5rem",
                  fontSize: "0.75rem",
                  flexShrink: 0,
                }}
              >
                Remove
              </button>
            </div>
          ))}
        </div>
      )}

      {count === 0 && (
        <p
          style={{
            color: "var(--color-text-secondary)",
            padding: "2rem 1rem",
            textAlign: "center",
            fontSize: "0.875rem",
          }}
        >
          No files selected yet. Use the buttons above to open the file picker.
        </p>
      )}

      {count > 0 && (
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
          }}
        >
          <span
            style={{
              fontSize: "0.875rem",
              color: "var(--color-text-secondary)",
            }}
          >
            {count} item(s) selected
          </span>
          <button class="primary" onClick={proceedWithSelection}>
            Continue
          </button>
        </div>
      )}
    </div>
  );
}
