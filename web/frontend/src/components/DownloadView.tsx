import { signal, computed } from "@preact/signals";
import { navigateBack, navigateToStep, uploadSessionId } from "../app";
import { startDownload, getDownloadResults } from "../api/client";
import { ActionBar } from "./shared/ActionBar";
import { GroupCard } from "./download/GroupCard";
import { postGroups, groupableMedia } from "./PostGrouper";
import type { PostGroup, DownloadBundle } from "../types/api";

// --- State ---

/** Track download job state per group. */
interface GroupDownloadState {
  jobId: string | null;
  status: "idle" | "processing" | "complete" | "error";
  bundles: DownloadBundle[];
  error: string | null;
}

const downloadStates = signal<Record<string, GroupDownloadState>>({});

/** Which group is currently expanded. */
const expandedGroupId = signal<string | null>(null);

/**
 * Reset all download state to initial values (DDR-037).
 * Called by the invalidation cascade when a previous step changes.
 */
export function resetDownloadState() {
  downloadStates.value = {};
  expandedGroupId.value = null;
}

// --- Helpers ---

function getGroupState(groupId: string): GroupDownloadState {
  return (
    downloadStates.value[groupId] ?? {
      jobId: null,
      status: "idle",
      bundles: [],
      error: null,
    }
  );
}

function setGroupState(groupId: string, state: GroupDownloadState) {
  downloadStates.value = {
    ...downloadStates.value,
    [groupId]: state,
  };
}

// --- Actions ---

async function handleDownload(group: PostGroup) {
  const sessionId = uploadSessionId.value;
  if (!sessionId) return;

  setGroupState(group.id, {
    jobId: null,
    status: "processing",
    bundles: [],
    error: null,
  });

  try {
    // Start the download job
    const { id } = await startDownload({
      sessionId,
      keys: group.keys,
      groupLabel: group.label || "media",
    });

    setGroupState(group.id, {
      jobId: id,
      status: "processing",
      bundles: [],
      error: null,
    });

    // Poll for results
    const pollInterval = 2000; // 2 seconds
    const maxPolls = 150; // 5 minutes max

    for (let i = 0; i < maxPolls; i++) {
      await new Promise((resolve) => setTimeout(resolve, pollInterval));

      const results = await getDownloadResults(id, sessionId);

      setGroupState(group.id, {
        jobId: id,
        status:
          results.status === "complete" || results.status === "error"
            ? results.status
            : "processing",
        bundles: results.bundles ?? [],
        error: results.error ?? null,
      });

      if (results.status === "complete" || results.status === "error") {
        break;
      }
    }
  } catch (err) {
    setGroupState(group.id, {
      jobId: null,
      status: "error",
      bundles: [],
      error: err instanceof Error ? err.message : "Download failed",
    });
  }
}

// --- Summary Stats ---

const totalGroups = computed(() => postGroups.value.length);
const completedGroups = computed(
  () =>
    Object.values(downloadStates.value).filter((s) => s.status === "complete")
      .length,
);

// --- Main Component ---

export function DownloadView() {
  const groups = postGroups.value;

  return (
    <div>
      {/* Info bar */}
      <div
        class="card"
        style={{
          marginBottom: "1rem",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          flexWrap: "wrap",
          gap: "0.5rem",
        }}
      >
        <div>
          <span style={{ fontSize: "0.875rem" }}>
            <strong>{totalGroups.value}</strong> post group
            {totalGroups.value !== 1 ? "s" : ""}
            {completedGroups.value > 0 && (
              <>
                {" — "}
                <span style={{ color: "var(--color-success)" }}>
                  {completedGroups.value} ready for download
                </span>
              </>
            )}
          </span>
        </div>
        <div
          style={{
            fontSize: "0.75rem",
            color: "var(--color-text-secondary)",
          }}
        >
          Photos are bundled into one ZIP. Videos are split into bundles
          under 375 MB each for fast downloads.
        </div>
      </div>

      {/* Group list */}
      {groups.map((group) => (
        <GroupCard
          key={group.id}
          group={group}
          state={getGroupState(group.id)}
          isExpanded={expandedGroupId.value === group.id}
          groupableMedia={groupableMedia.value}
          onToggleExpand={() => {
            expandedGroupId.value = expandedGroupId.value === group.id ? null : group.id;
          }}
          onDownload={() => handleDownload(group)}
        />
      ))}

      <ActionBar
        left={
          <span style={{ fontSize: "0.875rem" }}>
            {completedGroups.value === 0 ? (
              <span style={{ color: "var(--color-text-secondary)" }}>
                Click "Prepare Download" on a group to create ZIP bundles
              </span>
            ) : (
              <>
                <strong style={{ color: "var(--color-success)" }}>
                  {completedGroups.value} of {totalGroups.value}
                </strong>
                <span style={{ color: "var(--color-text-secondary)" }}>
                  {" "}
                  group{completedGroups.value !== 1 ? "s" : ""} ready
                </span>
              </>
            )}
          </span>
        }
        right={
          <div style={{ display: "flex", gap: "0.5rem" }}>
            <button class="outline" onClick={() => navigateBack()}>
              Back to Grouping
            </button>
            <button
              class="primary"
              onClick={() => navigateToStep("description")}
              style={{ fontSize: "0.875rem" }}
            >
              Generate Captions
            </button>
          </div>
        }
      />
    </div>
  );
}
