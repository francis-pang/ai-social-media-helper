import { signal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import {
  currentStep,
  navigateBack,
  navigateToStep,
  invalidateDownstream,
  uploadSessionId,
  tripContext,
  setStep,
} from "../app";
import { ProcessingIndicator } from "./ProcessingIndicator";
import {
  startSelection,
  getSelectionResults,
  isVideoFile,
} from "../api/client";
import { enhancementKeys } from "./EnhancementView";
import { openMediaPlayer } from "./MediaPlayer";
import type {
  SelectionItem,
  ExcludedItem,
  SelectionSceneGroup,
  SelectionResults,
} from "../types/api";

// --- State ---

const selectionJobId = signal<string | null>(null);
const results = signal<SelectionResults | null>(null);
const error = signal<string | null>(null);

/** User overrides: items moved from excluded to selected (by key). */
const addedToSelection = signal<Set<string>>(new Set());
/** User overrides: items moved from selected to excluded (by key). */
const removedFromSelection = signal<Set<string>>(new Set());

/** Whether the excluded section is expanded. */
const excludedExpanded = signal(false);

/** Whether the scene groups section is expanded. */
const scenesExpanded = signal(false);

/**
 * Reset all selection state to initial values (DDR-037).
 * Called by the invalidation cascade when a previous step changes.
 */
export function resetSelectionState() {
  selectionJobId.value = null;
  results.value = null;
  error.value = null;
  addedToSelection.value = new Set();
  removedFromSelection.value = new Set();
  excludedExpanded.value = false;
  scenesExpanded.value = false;
}

// --- Polling ---

function startSelectionJob() {
  const sessionId = uploadSessionId.value;
  if (!sessionId) {
    error.value = "No upload session found. Please upload media first.";
    return;
  }

  // Invalidate all downstream state (enhancement, grouping, download, description)
  // when re-running selection (DDR-037).
  invalidateDownstream("enhancement");

  error.value = null;
  startSelection({
    sessionId,
    tripContext: tripContext.value,
  })
    .then((res) => {
      selectionJobId.value = res.id;
      pollResults(res.id, sessionId);
    })
    .catch((e) => {
      error.value =
        e instanceof Error ? e.message : "Failed to start selection";
    });
}

function pollResults(id: string, sessionId: string) {
  const interval = setInterval(async () => {
    try {
      const res = await getSelectionResults(id, sessionId);
      results.value = res;
      if (res.status === "complete" || res.status === "error") {
        clearInterval(interval);
        if (res.status === "complete") {
          setStep("review-selection");
        }
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : "Failed to poll results";
      clearInterval(interval);
    }
  }, 3000);
}

// --- Override helpers ---

function toggleOverride(item: SelectionItem | ExcludedItem, isCurrentlySelected: boolean) {
  if (isCurrentlySelected) {
    // Move from selected to excluded
    const next = new Set(removedFromSelection.value);
    if (next.has(item.key)) {
      next.delete(item.key); // Undo override
    } else {
      next.add(item.key);
    }
    removedFromSelection.value = next;

    // Also remove from addedToSelection if it was there
    const added = new Set(addedToSelection.value);
    added.delete(item.key);
    addedToSelection.value = added;
  } else {
    // Move from excluded to selected
    const next = new Set(addedToSelection.value);
    if (next.has(item.key)) {
      next.delete(item.key); // Undo override
    } else {
      next.add(item.key);
    }
    addedToSelection.value = next;

    // Also remove from removedFromSelection if it was there
    const removed = new Set(removedFromSelection.value);
    removed.delete(item.key);
    removedFromSelection.value = removed;
  }
}

/** Get the effective selected items (AI selection + user overrides). */
function getEffectiveSelected(): SelectionItem[] {
  if (!results.value?.selected) return [];
  const removed = removedFromSelection.value;
  const added = addedToSelection.value;

  // Start with AI-selected items, minus user removals
  const selected = (results.value.selected || []).filter(
    (item) => !removed.has(item.key),
  );

  // Add user-promoted items (from excluded list)
  if (results.value.excluded) {
    for (const exc of results.value.excluded) {
      if (added.has(exc.key)) {
        selected.push({
          rank: selected.length + 1,
          media: exc.media,
          filename: exc.filename,
          key: exc.key,
          type: "Photo", // We don't have type info on excluded items in the same shape
          scene: "",
          justification: "Added by user override",
          thumbnailUrl: exc.thumbnailUrl,
        });
      }
    }
  }

  return selected;
}

/** Get the effective excluded items (AI exclusions + user overrides). */
function getEffectiveExcluded(): ExcludedItem[] {
  if (!results.value?.excluded) return [];
  const added = addedToSelection.value;
  const removed = removedFromSelection.value;

  // Start with AI-excluded items, minus user promotions
  const excluded = (results.value.excluded || []).filter(
    (item) => !added.has(item.key),
  );

  // Add user-demoted items (from selected list)
  if (results.value.selected) {
    for (const sel of results.value.selected) {
      if (removed.has(sel.key)) {
        excluded.push({
          media: sel.media,
          filename: sel.filename,
          key: sel.key,
          reason: "Removed by user override",
          category: "redundant-scene",
          thumbnailUrl: sel.thumbnailUrl,
        });
      }
    }
  }

  return excluded;
}

// --- Navigation ---

function handleConfirmSelection() {
  // Collect effective selected keys and pass to enhancement (DDR-031)
  const selected = getEffectiveSelected();
  enhancementKeys.value = selected.map((item) => item.key);
  navigateToStep("enhancing");
}

function handleBack() {
  // Reset state and go back
  results.value = null;
  selectionJobId.value = null;
  addedToSelection.value = new Set();
  removedFromSelection.value = new Set();
  excludedExpanded.value = false;
  scenesExpanded.value = false;
  error.value = null;
  navigateBack();
}

// --- Components ---

function SelectedCard({
  item,
  onToggle,
}: {
  item: SelectionItem;
  onToggle: () => void;
}) {
  const isOverride = addedToSelection.value.has(item.key);
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
            fontSize: "0.6875rem",
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
            fontSize: "0.5625rem",
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
              fontSize: "0.6875rem",
              color: "var(--color-primary)",
              marginBottom: "0.25rem",
            }}
          >
            {item.scene}
          </div>
        )}
        <div
          style={{
            fontSize: "0.625rem",
            color: "var(--color-text-secondary)",
            lineHeight: 1.4,
          }}
        >
          {item.justification}
        </div>
        {item.comparisonNote && (
          <div
            style={{
              fontSize: "0.625rem",
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
            fontSize: "0.6875rem",
            width: "100%",
          }}
        >
          {isOverride ? "Undo Add" : "Remove"}
        </button>
      </div>
    </div>
  );
}

function ExcludedCard({
  item,
  onToggle,
}: {
  item: ExcludedItem;
  onToggle: () => void;
}) {
  const isOverride = removedFromSelection.value.has(item.key);
  return (
    <div
      style={{
        background: isOverride
          ? "rgba(255, 107, 107, 0.08)"
          : "var(--color-bg)",
        borderRadius: "var(--radius)",
        overflow: "hidden",
        border: isOverride
          ? "2px solid var(--color-danger)"
          : "2px solid transparent",
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
          src={item.thumbnailUrl}
          alt={item.filename}
          loading="lazy"
          onClick={() =>
            openMediaPlayer(
              item.key,
              isVideoFile(item.filename) ? "Video" : "Photo",
              item.filename,
            )
          }
          style={{
            width: "100%",
            height: "100%",
            objectFit: "cover",
            cursor: "zoom-in",
            opacity: 0.7,
          }}
          onError={(e) => {
            (e.target as HTMLImageElement).style.display = "none";
          }}
        />
        {/* Category badge */}
        <span
          style={{
            position: "absolute",
            top: "0.375rem",
            right: "0.375rem",
            fontSize: "0.5625rem",
            padding: "0.125rem 0.375rem",
            borderRadius: "4px",
            background: "rgba(255, 107, 107, 0.85)",
            color: "#fff",
            fontWeight: 600,
          }}
        >
          {item.category.replace("-", " ")}
        </span>
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
            marginBottom: "0.25rem",
          }}
        >
          {item.filename}
        </div>
        <div
          style={{
            fontSize: "0.625rem",
            color: "var(--color-text-secondary)",
            lineHeight: 1.4,
          }}
        >
          {item.reason}
        </div>
        {item.duplicateOf && (
          <div
            style={{
              fontSize: "0.625rem",
              color: "var(--color-text-secondary)",
              fontStyle: "italic",
              marginTop: "0.125rem",
            }}
          >
            Duplicate of: {item.duplicateOf}
          </div>
        )}
        <button
          class="outline"
          onClick={onToggle}
          style={{
            marginTop: "0.5rem",
            padding: "0.125rem 0.5rem",
            fontSize: "0.6875rem",
            width: "100%",
          }}
        >
          {isOverride ? "Undo Remove" : "Add to Selection"}
        </button>
      </div>
    </div>
  );
}

function SceneGroupCard({ group }: { group: SelectionSceneGroup }) {
  return (
    <div
      style={{
        background: "var(--color-bg)",
        borderRadius: "var(--radius)",
        padding: "0.75rem",
        marginBottom: "0.75rem",
      }}
    >
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          marginBottom: "0.5rem",
        }}
      >
        <strong style={{ fontSize: "0.875rem" }}>{group.name}</strong>
        <span
          style={{
            fontSize: "0.6875rem",
            color: "var(--color-text-secondary)",
          }}
        >
          {group.items.filter((i) => i.selected).length} /{" "}
          {group.items.length} selected
        </span>
      </div>
      {(group.gps || group.timeRange) && (
        <div
          style={{
            fontSize: "0.6875rem",
            color: "var(--color-text-secondary)",
            marginBottom: "0.5rem",
          }}
        >
          {group.gps && <span>{group.gps}</span>}
          {group.gps && group.timeRange && <span> &middot; </span>}
          {group.timeRange && <span>{group.timeRange}</span>}
        </div>
      )}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fill, minmax(80px, 1fr))",
          gap: "0.375rem",
        }}
      >
        {group.items.map((item) => (
          <div
            key={item.key}
            style={{
              position: "relative",
              aspectRatio: "1",
              borderRadius: "4px",
              overflow: "hidden",
              border: item.selected
                ? "2px solid var(--color-success)"
                : "2px solid var(--color-border)",
              opacity: item.selected ? 1 : 0.5,
            }}
            title={`${item.filename}: ${item.description}`}
          >
            <img
              src={item.thumbnailUrl}
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
          </div>
        ))}
      </div>
    </div>
  );
}

// --- Main Component ---

export function SelectionView() {
  // Start the selection job when the component mounts in "selecting" step
  useEffect(() => {
    if (currentStep.value === "selecting" && !selectionJobId.value) {
      startSelectionJob();
    }
  }, []);

  // --- Processing State (Step 2) — DDR-056 ProcessingIndicator ---
  if (
    currentStep.value === "selecting" ||
    (results.value &&
      (results.value.status === "pending" ||
        results.value.status === "processing"))
  ) {
    return (
      <ProcessingIndicator
        title="AI Selection in Progress"
        description="Scene detection, deduplication, and content evaluation. This may take 1-3 minutes depending on the number of files."
        status={results.value?.status ?? "pending"}
        jobId={selectionJobId.value ?? undefined}
        sessionId={uploadSessionId.value ?? undefined}
        pollIntervalMs={3000}
        onCancel={handleBack}
      >
        {error.value && (
          <div
            style={{
              color: "var(--color-danger)",
              marginTop: "1rem",
              fontSize: "0.875rem",
            }}
          >
            {error.value}
          </div>
        )}
      </ProcessingIndicator>
    );
  }

  // --- Error State ---
  if (results.value?.status === "error") {
    return (
      <div class="card">
        <p style={{ color: "var(--color-danger)" }}>
          Selection failed: {results.value.error}
        </p>
        <button
          class="outline"
          onClick={handleBack}
          style={{ marginTop: "1rem" }}
        >
          Back to Upload
        </button>
      </div>
    );
  }

  // --- Review State (Step 3) ---
  const selected = getEffectiveSelected();
  const excluded = getEffectiveExcluded();
  const sceneGroups = results.value?.sceneGroups || [];
  const hasOverrides =
    addedToSelection.value.size > 0 || removedFromSelection.value.size > 0;

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

      {/* Summary bar */}
      <div
        class="card"
        style={{
          marginBottom: "1.5rem",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
        }}
      >
        <div>
          <span style={{ fontSize: "0.875rem" }}>
            <strong style={{ color: "var(--color-success)" }}>
              {selected.length} selected
            </strong>
            {" / "}
            <span style={{ color: "var(--color-text-secondary)" }}>
              {excluded.length} excluded
            </span>
            {" / "}
            <span style={{ color: "var(--color-text-secondary)" }}>
              {sceneGroups.length} scenes
            </span>
          </span>
          {hasOverrides && (
            <span
              style={{
                fontSize: "0.75rem",
                color: "var(--color-primary)",
                marginLeft: "0.75rem",
              }}
            >
              (modified)
            </span>
          )}
        </div>
        <div>
          <span
            style={{
              fontSize: "0.75rem",
              color: "var(--color-text-secondary)",
            }}
          >
            Click thumbnails to view full-size. Use buttons to override AI
            selections.
          </span>
        </div>
      </div>

      {/* Selected section */}
      <div class="card" style={{ marginBottom: "1.5rem" }}>
        <h2 style={{ color: "var(--color-success)", marginBottom: "1rem" }}>
          Selected ({selected.length})
        </h2>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill, minmax(180px, 1fr))",
            gap: "0.75rem",
          }}
        >
          {selected.map((item) => (
            <SelectedCard
              key={item.key}
              item={item}
              onToggle={() => toggleOverride(item, true)}
            />
          ))}
        </div>
        {selected.length === 0 && (
          <p
            style={{
              color: "var(--color-text-secondary)",
              textAlign: "center",
              padding: "1.5rem",
            }}
          >
            No items selected. Add items from the excluded section below.
          </p>
        )}
      </div>

      {/* Excluded section (collapsible) */}
      <div class="card" style={{ marginBottom: "1.5rem" }}>
        <div
          onClick={() => {
            excludedExpanded.value = !excludedExpanded.value;
          }}
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            cursor: "pointer",
            userSelect: "none",
          }}
        >
          <h2
            style={{
              color: "var(--color-text-secondary)",
              marginBottom: 0,
            }}
          >
            Excluded ({excluded.length})
          </h2>
          <span
            style={{
              fontSize: "0.875rem",
              color: "var(--color-text-secondary)",
            }}
          >
            {excludedExpanded.value ? "Collapse" : "Expand"}
          </span>
        </div>

        {excludedExpanded.value && (
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fill, minmax(180px, 1fr))",
              gap: "0.75rem",
              marginTop: "1rem",
            }}
          >
            {excluded.map((item) => (
              <ExcludedCard
                key={item.key}
                item={item}
                onToggle={() => toggleOverride(item, false)}
              />
            ))}
          </div>
        )}
      </div>

      {/* Scene groups (collapsible) */}
      {sceneGroups.length > 0 && (
        <div class="card" style={{ marginBottom: "1.5rem" }}>
          <div
            onClick={() => {
              scenesExpanded.value = !scenesExpanded.value;
            }}
            style={{
              display: "flex",
              justifyContent: "space-between",
              alignItems: "center",
              cursor: "pointer",
              userSelect: "none",
            }}
          >
            <h2
              style={{
                color: "var(--color-text-secondary)",
                marginBottom: 0,
              }}
            >
              Scene Groups ({sceneGroups.length})
            </h2>
            <span
              style={{
                fontSize: "0.875rem",
                color: "var(--color-text-secondary)",
              }}
            >
              {scenesExpanded.value ? "Collapse" : "Expand"}
            </span>
          </div>

          {scenesExpanded.value && (
            <div style={{ marginTop: "1rem" }}>
              {sceneGroups.map((group) => (
                <SceneGroupCard key={group.name} group={group} />
              ))}
            </div>
          )}
        </div>
      )}

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
          {selected.length} item(s) selected for enhancement
          {hasOverrides && (
            <span style={{ color: "var(--color-primary)", marginLeft: "0.5rem" }}>
              (includes overrides)
            </span>
          )}
        </span>
        <div style={{ display: "flex", gap: "0.75rem" }}>
          <button class="outline" onClick={handleBack}>
            Back to Upload
          </button>
          <button
            class="primary"
            onClick={handleConfirmSelection}
            disabled={selected.length === 0}
          >
            Confirm Selection ({selected.length})
          </button>
        </div>
      </div>
    </div>
  );
}
