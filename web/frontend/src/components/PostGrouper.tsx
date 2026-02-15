import { computed } from "@preact/signals";
import { navigateBack, navigateToStep } from "../app";
import type { GroupableMediaItem } from "../types/api";
import { ActionBar } from "./shared/ActionBar";
import { MediaThumbnail } from "./post-grouper/MediaThumbnail";
import { GroupIcon, NewGroupButton } from "./post-grouper/GroupIcon";
import {
  postGroups,
  groupableMedia,
  selectedGroupId,
  dragItem,
  dragOverTarget,
} from "./post-grouper/state";
export { postGroups, groupableMedia, resetPostGrouperState } from "./post-grouper/state";
import {
  createGroup,
  deleteGroup,
  updateGroupLabel,
  addToGroup,
  removeFromGroup,
  removeFromAllGroups,
  MAX_ITEMS_PER_GROUP,
} from "./post-grouper/useGroupOperations";

// --- Computed ---

/** Set of keys currently assigned to any group. */
const assignedKeys = computed(() => {
  const keys = new Set<string>();
  for (const group of postGroups.value) {
    for (const key of group.keys) {
      keys.add(key);
    }
  }
  return keys;
});

/** Media items not assigned to any group. */
const ungroupedMedia = computed(() =>
  groupableMedia.value.filter((item) => !assignedKeys.value.has(item.key)),
);

/** Currently selected group object. */
const selectedGroup = computed(() =>
  postGroups.value.find((g) => g.id === selectedGroupId.value) ?? null,
);

/** Media items in the currently selected group. */
const selectedGroupMedia = computed(() => {
  const group = selectedGroup.value;
  if (!group) return [];
  return group.keys
    .map((key) => groupableMedia.value.find((m) => m.key === key))
    .filter((m): m is GroupableMediaItem => m !== undefined);
});

// --- Drag and Drop Handlers ---

function handleDragStart(key: string, sourceGroupId: string | null) {
  dragItem.value = { key, sourceGroupId };
}

function handleDragEnd() {
  dragItem.value = null;
  dragOverTarget.value = null;
}

function handleDragOver(e: DragEvent) {
  e.preventDefault();
  if (e.dataTransfer) {
    e.dataTransfer.dropEffect = "move";
  }
}

function handleDropOnGroup(e: DragEvent, groupId: string) {
  e.preventDefault();
  const item = dragItem.value;
  if (!item) return;
  addToGroup(groupId, item.key);
  dragItem.value = null;
  dragOverTarget.value = null;
}

function handleDropOnNewGroup(e: DragEvent) {
  e.preventDefault();
  const item = dragItem.value;
  if (!item) return;
  // Remove from current group
  removeFromAllGroups(item.key);
  createGroup(item.key);
  dragItem.value = null;
  dragOverTarget.value = null;
}

function handleDropOnUngrouped(e: DragEvent) {
  e.preventDefault();
  const item = dragItem.value;
  if (!item) return;
  removeFromAllGroups(item.key);
  dragItem.value = null;
  dragOverTarget.value = null;
}

// --- Click-to-assign ---

function handleThumbnailClick(key: string, isInGroup: boolean) {
  if (isInGroup) {
    // Click item in group detail → remove from group (back to ungrouped)
    const group = selectedGroup.value;
    if (group) {
      removeFromGroup(group.id, key);
    }
  } else {
    // Click item in ungrouped pool → add to selected group
    if (selectedGroupId.value) {
      addToGroup(selectedGroupId.value, key);
    }
  }
}

// --- Navigation ---

function handleProceed() {
  // Filter out empty groups
  const nonEmpty = postGroups.value.filter((g) => g.keys.length > 0);
  if (nonEmpty.length === 0) return;
  postGroups.value = nonEmpty;
  navigateToStep("publish");
}

function handleBack() {
  navigateBack();
}

// --- Main Component ---

export function PostGrouper() {
  const groups = postGroups.value;
  const ungrouped = ungroupedMedia.value;
  const currentGroup = selectedGroup.value;
  const currentGroupItems = selectedGroupMedia.value;
  const hasSelectedGroup = !!selectedGroupId.value;

  // Summary counts
  const totalMedia = groupableMedia.value.length;
  const totalGrouped = assignedKeys.value.size;
  const totalUngrouped = totalMedia - totalGrouped;
  const nonEmptyGroups = groups.filter((g) => g.keys.length > 0);

  return (
    <div>
      {/* Summary bar */}
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
            <strong>{totalMedia}</strong> media items
            {totalGrouped > 0 && (
              <>
                {" — "}
                <span style={{ color: "var(--color-success)" }}>
                  {totalGrouped} grouped
                </span>
              </>
            )}
            {totalUngrouped > 0 && (
              <>
                {" — "}
                <span style={{ color: "var(--color-text-secondary)" }}>
                  {totalUngrouped} remaining
                </span>
              </>
            )}
          </span>
        </div>
        <div style={{ fontSize: "0.75rem", color: "var(--color-text-secondary)" }}>
          {hasSelectedGroup
            ? "Click thumbnails to add/remove from selected group, or drag to any group."
            : "Select or create a group below, then click thumbnails to add them."}
        </div>
      </div>

      {/* Ungrouped media pool */}
      <div
        class="card"
        style={{ marginBottom: "1rem" }}
        onDragOver={handleDragOver}
        onDragEnter={() => {
          dragOverTarget.value = "__ungrouped__";
        }}
        onDragLeave={(e) => {
          // Only clear if actually leaving the container (not entering a child)
          const relatedTarget = (e as DragEvent).relatedTarget as HTMLElement | null;
          if (relatedTarget && (e.currentTarget as HTMLElement).contains(relatedTarget)) return;
          if (dragOverTarget.value === "__ungrouped__") dragOverTarget.value = null;
        }}
        onDrop={handleDropOnUngrouped}
      >
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            marginBottom: "0.75rem",
          }}
        >
          <h2 style={{ marginBottom: 0 }}>
            Ungrouped Media
            <span
              style={{
                fontSize: "0.875rem",
                fontWeight: 400,
                color: "var(--color-text-secondary)",
                marginLeft: "0.5rem",
              }}
            >
              ({ungrouped.length})
            </span>
          </h2>
          {ungrouped.length > 0 && hasSelectedGroup && (
            <span
              style={{
                fontSize: "0.75rem",
                color: "var(--color-primary)",
                fontStyle: "italic",
              }}
            >
              Click to add to &ldquo;{currentGroup?.label || "Untitled"}&rdquo;
            </span>
          )}
        </div>

        {ungrouped.length === 0 ? (
          <div
            style={{
              textAlign: "center",
              padding: "2rem",
              color: "var(--color-text-secondary)",
              fontSize: "0.875rem",
              borderRadius: "var(--radius)",
              background:
                dragOverTarget.value === "__ungrouped__"
                  ? "rgba(108, 140, 255, 0.06)"
                  : "transparent",
              border:
                dragOverTarget.value === "__ungrouped__"
                  ? "2px dashed var(--color-primary)"
                  : "2px dashed transparent",
              transition: "background 0.15s, border-color 0.15s",
            }}
          >
            All media has been grouped! Drag items here to ungroup them.
          </div>
        ) : (
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fill, minmax(var(--grid-card-sm), 1fr))",
              gap: "0.5rem",
              borderRadius: "var(--radius)",
              padding:
                dragOverTarget.value === "__ungrouped__"
                  ? "0.5rem"
                  : "0",
              background:
                dragOverTarget.value === "__ungrouped__"
                  ? "rgba(108, 140, 255, 0.06)"
                  : "transparent",
              border:
                dragOverTarget.value === "__ungrouped__"
                  ? "2px dashed var(--color-primary)"
                  : "2px dashed transparent",
              transition: "background 0.15s, border-color 0.15s, padding 0.15s",
            }}
          >
            {ungrouped.map((item) => (
              <MediaThumbnail
                key={item.key}
                item={item}
                isInGroup={false}
                showAssignHint={hasSelectedGroup}
                isDragging={dragItem.value?.key === item.key}
                onDragStart={() => handleDragStart(item.key, null)}
                onDragEnd={handleDragEnd}
                onClick={() => handleThumbnailClick(item.key, false)}
              />
            ))}
          </div>
        )}
      </div>

      {/* Post Groups strip */}
      <div class="card" style={{ marginBottom: "1rem" }}>
        <h2 style={{ marginBottom: "0.75rem" }}>
          Post Groups
          <span
            style={{
              fontSize: "0.875rem",
              fontWeight: 400,
              color: "var(--color-text-secondary)",
              marginLeft: "0.5rem",
            }}
          >
            ({groups.length})
          </span>
        </h2>

        {/* Horizontal scrollable strip of group icons */}
        <div
          style={{
            display: "flex",
            gap: "0.75rem",
            overflowX: "auto",
            paddingBottom: "0.5rem",
          }}
        >
          {groups.map((group) => (
            <GroupIcon
              key={group.id}
              group={group}
              isSelected={group.id === selectedGroupId.value}
              onSelect={() => {
                selectedGroupId.value =
                  selectedGroupId.value === group.id ? null : group.id;
              }}
              onDelete={() => deleteGroup(group.id)}
              onDragOver={handleDragOver}
              onDragEnter={() => {
                if (group.keys.length < MAX_ITEMS_PER_GROUP) dragOverTarget.value = group.id;
              }}
              onDragLeave={() => {
                if (dragOverTarget.value === group.id) dragOverTarget.value = null;
              }}
              onDrop={(e) => handleDropOnGroup(e, group.id)}
            />
          ))}
          <NewGroupButton
            onCreateGroup={() => createGroup()}
            onDragOver={handleDragOver}
            onDragEnter={() => { dragOverTarget.value = "__new__"; }}
            onDragLeave={() => {
              if (dragOverTarget.value === "__new__") dragOverTarget.value = null;
            }}
            onDrop={handleDropOnNewGroup}
          />
        </div>
      </div>

      {/* Selected group detail panel */}
      {currentGroup && (
        <div class="card" style={{ marginBottom: "1rem" }}>
          <div
            style={{
              display: "flex",
              justifyContent: "space-between",
              alignItems: "flex-start",
              marginBottom: "0.75rem",
              gap: "1rem",
            }}
          >
            <div style={{ flex: 1 }}>
              <div
                style={{
                  fontSize: "0.75rem",
                  color: "var(--color-text-secondary)",
                  marginBottom: "0.25rem",
                }}
              >
                Group label — describe this post (used for AI caption generation)
              </div>
              <textarea
                value={currentGroup.label}
                onInput={(e) =>
                  updateGroupLabel(
                    currentGroup.id,
                    (e.target as HTMLTextAreaElement).value,
                  )
                }
                placeholder="e.g., First morning in Shibuya — the energy of the crossing, coffee at Blue Bottle, found a great vintage shop on Cat Street..."
                style={{
                  width: "100%",
                  minHeight: "3rem",
                  resize: "vertical",
                  fontSize: "0.875rem",
                  background: "var(--color-bg)",
                  border: "1px solid var(--color-border)",
                  borderRadius: "var(--radius)",
                  padding: "0.5rem 0.75rem",
                  color: "var(--color-text)",
                  lineHeight: 1.5,
                }}
              />
            </div>
            <div
              style={{
                fontSize: "0.875rem",
                color:
                  currentGroup.keys.length >= MAX_ITEMS_PER_GROUP
                    ? "var(--color-danger)"
                    : "var(--color-text-secondary)",
                fontWeight: 600,
                whiteSpace: "nowrap",
                paddingTop: "1.25rem",
              }}
            >
              {currentGroup.keys.length}/{MAX_ITEMS_PER_GROUP}
            </div>
          </div>

          {currentGroupItems.length === 0 ? (
            <div
              style={{
                textAlign: "center",
                padding: "2rem",
                color: "var(--color-text-secondary)",
                fontSize: "0.875rem",
                borderRadius: "var(--radius)",
                border: "2px dashed var(--color-border)",
              }}
            >
              No items in this group yet. Click thumbnails above or drag media here.
            </div>
          ) : (
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "repeat(auto-fill, minmax(var(--grid-card-sm), 1fr))",
                gap: "0.5rem",
              }}
            >
              {currentGroupItems.map((item) => (
                <MediaThumbnail
                  key={item.key}
                  item={item}
                  isInGroup={true}
                  isDragging={dragItem.value?.key === item.key}
                  onDragStart={() => handleDragStart(item.key, currentGroup.id)}
                  onDragEnd={handleDragEnd}
                  onClick={() => handleThumbnailClick(item.key, true)}
                />
              ))}
            </div>
          )}

          {currentGroupItems.length > 0 && (
            <div
              style={{
                marginTop: "0.5rem",
                fontSize: "0.75rem",
                color: "var(--color-text-secondary)",
                fontStyle: "italic",
              }}
            >
              Click a thumbnail to remove it from this group.
            </div>
          )}
        </div>
      )}

      {/* Action bar */}
      <ActionBar
        left={
          <span style={{ fontSize: "0.875rem" }}>
            {nonEmptyGroups.length === 0 ? (
              <span style={{ color: "var(--color-text-secondary)" }}>
                Create a group and add media to proceed
              </span>
            ) : (
              <>
                <strong style={{ color: "var(--color-success)" }}>
                  {nonEmptyGroups.length} post{nonEmptyGroups.length !== 1 ? "s" : ""}
                </strong>
                <span style={{ color: "var(--color-text-secondary)" }}>
                  {" "}ready ({totalGrouped} items)
                </span>
                {totalUngrouped > 0 && (
                  <span style={{ color: "var(--color-text-secondary)", fontSize: "0.75rem", marginLeft: "0.5rem" }}>
                    ({totalUngrouped} ungrouped — will not be published)
                  </span>
                )}
              </>
            )}
          </span>
        }
        right={
          <div style={{ display: "flex", gap: "0.75rem" }}>
            <button class="outline" onClick={handleBack}>Back to Enhancement</button>
            <button class="primary" onClick={handleProceed} disabled={nonEmptyGroups.length === 0}>
              Continue ({nonEmptyGroups.length} post{nonEmptyGroups.length !== 1 ? "s" : ""})
            </button>
          </div>
        }
      />
    </div>
  );
}
