import { signal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { currentStep, triageJobId, selectedPaths, uploadSessionId } from "../app";
import {
  getTriageResults,
  confirmTriage,
  thumbnailUrl,
  openFullImage,
  isCloudMode,
} from "../api/client";
import type { TriageItem, TriageResults } from "../types/api";

const results = signal<TriageResults | null>(null);
const selectedForDeletion = signal<Set<string>>(new Set());
const confirmLoading = signal(false);
const confirmResult = signal<{
  deleted: number;
  errors: string[];
  reclaimedBytes: number;
} | null>(null);
const error = signal<string | null>(null);

/** Get the identifier for a triage item (key in cloud mode, path in local). */
function itemId(item: TriageItem): string {
  return isCloudMode ? (item.key ?? item.path) : item.path;
}

/** Get the thumbnail source for a triage item. */
function itemThumb(item: TriageItem): string {
  if (isCloudMode && item.key) {
    return thumbnailUrl(item.key);
  }
  return thumbnailUrl(item.path);
}

function pollResults(id: string) {
  const interval = setInterval(async () => {
    try {
      const res = await getTriageResults(id);
      results.value = res;
      if (res.status === "complete" || res.status === "error") {
        clearInterval(interval);
        currentStep.value = "results";
        if (res.discard.length > 0) {
          selectedForDeletion.value = new Set(
            res.discard.map((item) => itemId(item)),
          );
        }
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : "Failed to poll results";
      clearInterval(interval);
    }
  }, 2000);

  return () => clearInterval(interval);
}

function toggleDeletion(id: string) {
  const next = new Set(selectedForDeletion.value);
  if (next.has(id)) {
    next.delete(id);
  } else {
    next.add(id);
  }
  selectedForDeletion.value = next;
}

function selectAllDiscard() {
  if (!results.value) return;
  selectedForDeletion.value = new Set(
    results.value.discard.map((item) => itemId(item)),
  );
}

function deselectAll() {
  selectedForDeletion.value = new Set();
}

async function handleConfirmDeletion() {
  if (!triageJobId.value) return;
  confirmLoading.value = true;
  try {
    const ids = Array.from(selectedForDeletion.value);
    const req = isCloudMode
      ? { deleteKeys: ids }
      : { deletePaths: ids };
    const res = await confirmTriage(triageJobId.value, req);
    confirmResult.value = res;
  } catch (e) {
    error.value = e instanceof Error ? e.message : "Failed to confirm deletion";
  } finally {
    confirmLoading.value = false;
  }
}

function startOver() {
  results.value = null;
  selectedForDeletion.value = new Set();
  confirmResult.value = null;
  error.value = null;
  triageJobId.value = null;
  selectedPaths.value = [];
  uploadSessionId.value = null;
  currentStep.value = "browse";
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function MediaCard({
  item,
  selectable,
  isSelected,
  onToggle,
}: {
  item: TriageItem;
  selectable: boolean;
  isSelected: boolean;
  onToggle?: () => void;
}) {
  return (
    <div
      onClick={selectable ? onToggle : undefined}
      style={{
        background: isSelected
          ? "rgba(255, 107, 107, 0.1)"
          : "var(--color-bg)",
        border: isSelected
          ? "2px solid var(--color-danger)"
          : "2px solid transparent",
        borderRadius: "var(--radius)",
        overflow: "hidden",
        cursor: selectable ? "pointer" : "default",
      }}
    >
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
          src={itemThumb(item)}
          alt={item.filename}
          title={item.filename}
          loading="lazy"
          onClick={(e) => {
            e.stopPropagation();
            openFullImage(itemId(item));
          }}
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
        {selectable && (
          <input
            type="checkbox"
            checked={isSelected}
            onClick={(e) => e.stopPropagation()}
            onChange={onToggle}
            style={{
              position: "absolute",
              top: "0.5rem",
              left: "0.5rem",
              width: "1.25rem",
              height: "1.25rem",
            }}
          />
        )}
      </div>
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
        <div
          style={{
            fontSize: "0.6875rem",
            color: "var(--color-text-secondary)",
            marginTop: "0.25rem",
          }}
        >
          {item.reason}
        </div>
      </div>
    </div>
  );
}

export function TriageView() {
  useEffect(() => {
    if (triageJobId.value && !results.value) {
      return pollResults(triageJobId.value);
    }
  }, []);

  // Show deletion confirmation result
  if (confirmResult.value) {
    return (
      <div class="card" style={{ textAlign: "center" }}>
        <div style={{ fontSize: "2rem", marginBottom: "0.5rem" }}>
          {confirmResult.value.errors.length === 0 ? "Done" : "Completed with errors"}
        </div>
        <p>
          Deleted {confirmResult.value.deleted} file(s)
          {!isCloudMode && confirmResult.value.reclaimedBytes > 0 &&
            `, reclaimed ${formatBytes(confirmResult.value.reclaimedBytes)}`
          }.
        </p>
        {confirmResult.value.errors.length > 0 && (
          <div
            style={{
              color: "var(--color-danger)",
              marginTop: "1rem",
              fontSize: "0.875rem",
            }}
          >
            {confirmResult.value.errors.map((err) => (
              <div key={err}>{err}</div>
            ))}
          </div>
        )}
        <button class="primary" onClick={startOver} style={{ marginTop: "1.5rem" }}>
          Start Over
        </button>
      </div>
    );
  }

  // Show processing state
  if (!results.value || results.value.status === "pending" || results.value.status === "processing") {
    return (
      <div class="card" style={{ textAlign: "center", padding: "3rem" }}>
        <p style={{ color: "var(--color-text-secondary)" }}>
          Analyzing media with AI... This may take a minute.
        </p>
      </div>
    );
  }

  // Show error
  if (results.value.status === "error") {
    return (
      <div class="card">
        <p style={{ color: "var(--color-danger)" }}>
          Triage failed: {results.value.error}
        </p>
        <button class="outline" onClick={startOver} style={{ marginTop: "1rem" }}>
          Start Over
        </button>
      </div>
    );
  }

  // Show results
  const { keep, discard } = results.value;

  return (
    <div>
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

      {/* Discard section */}
      <div class="card" style={{ marginBottom: "1.5rem" }}>
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            marginBottom: "1rem",
          }}
        >
          <h2 style={{ color: "var(--color-danger)" }}>
            Discard ({discard.length})
          </h2>
          <div style={{ display: "flex", gap: "0.5rem" }}>
            <button class="outline" onClick={selectAllDiscard}>
              Select all
            </button>
            <button class="outline" onClick={deselectAll}>
              Deselect all
            </button>
          </div>
        </div>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill, minmax(160px, 1fr))",
            gap: "0.75rem",
          }}
        >
          {discard.map((item) => (
            <MediaCard
              key={itemId(item)}
              item={item}
              selectable={true}
              isSelected={selectedForDeletion.value.has(itemId(item))}
              onToggle={() => toggleDeletion(itemId(item))}
            />
          ))}
        </div>
      </div>

      {/* Keep section */}
      <div class="card" style={{ marginBottom: "1.5rem" }}>
        <h2 style={{ color: "var(--color-success)", marginBottom: "1rem" }}>
          Keep ({keep.length})
        </h2>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill, minmax(160px, 1fr))",
            gap: "0.75rem",
          }}
        >
          {keep.map((item) => (
            <MediaCard
              key={itemId(item)}
              item={item}
              selectable={false}
              isSelected={false}
            />
          ))}
        </div>
      </div>

      {/* Action bar */}
      <div
        style={{
          position: "sticky",
          bottom: "1rem",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          padding: "1rem 1.5rem",
          background: "var(--color-surface)",
          borderRadius: "var(--radius-lg)",
          border: "1px solid var(--color-border)",
        }}
      >
        <span style={{ fontSize: "0.875rem" }}>
          {selectedForDeletion.value.size} of {discard.length} marked for
          deletion
        </span>
        <div style={{ display: "flex", gap: "0.75rem" }}>
          <button class="outline" onClick={startOver}>
            Cancel
          </button>
          <button
            class="danger"
            onClick={handleConfirmDeletion}
            disabled={
              selectedForDeletion.value.size === 0 || confirmLoading.value
            }
          >
            {confirmLoading.value
              ? "Deleting..."
              : `Delete ${selectedForDeletion.value.size} file(s)`}
          </button>
        </div>
      </div>
    </div>
  );
}
