import { signal, computed } from "@preact/signals";
import { navigateBack, navigateToStep } from "../app";
import { thumbnailUrl } from "../api/client";
import type { PostGroup, GroupableMediaItem } from "../types/api";

// --- Constants ---

/** Maximum items per Instagram carousel post. */
const MAX_ITEMS_PER_GROUP = 20;

// --- Exported State ---

/** All post groups. Exported so downstream steps can consume them. */
export const postGroups = signal<PostGroup[]>([]);

/** All media items available for grouping (populated by EnhancementView on proceed). */
export const groupableMedia = signal<GroupableMediaItem[]>([]);

// --- Internal State ---

/** Currently selected/expanded group. */
const selectedGroupId = signal<string | null>(null);

/** Drag state: which item is being dragged, and from where. */
const dragItem = signal<{
  key: string;
  sourceGroupId: string | null; // null = ungrouped pool
} | null>(null);

/** Which drop target is currently highlighted. */
const dragOverTarget = signal<string | null>(null);

/** Counter for generating unique group IDs. */
let groupIdCounter = 0;

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

// --- Group Operations ---

function createGroup(initialKey?: string): string {
  groupIdCounter++;
  const id = `group-${groupIdCounter}-${Date.now()}`;
  const keys = initialKey ? [initialKey] : [];
  postGroups.value = [
    ...postGroups.value,
    { id, label: "", keys },
  ];
  selectedGroupId.value = id;
  return id;
}

function deleteGroup(groupId: string) {
  postGroups.value = postGroups.value.filter((g) => g.id !== groupId);
  if (selectedGroupId.value === groupId) {
    selectedGroupId.value = postGroups.value.length > 0
      ? postGroups.value[0]!.id
      : null;
  }
}

function updateGroupLabel(groupId: string, label: string) {
  postGroups.value = postGroups.value.map((g) =>
    g.id === groupId ? { ...g, label } : g,
  );
}

function addToGroup(groupId: string, key: string) {
  const group = postGroups.value.find((g) => g.id === groupId);
  if (!group) return;
  if (group.keys.length >= MAX_ITEMS_PER_GROUP) return;
  if (group.keys.includes(key)) return;

  // Remove from any other group first
  removeFromAllGroups(key);

  postGroups.value = postGroups.value.map((g) =>
    g.id === groupId ? { ...g, keys: [...g.keys, key] } : g,
  );
}

function removeFromGroup(groupId: string, key: string) {
  postGroups.value = postGroups.value.map((g) =>
    g.id === groupId ? { ...g, keys: g.keys.filter((k) => k !== key) } : g,
  );
}

function removeFromAllGroups(key: string) {
  postGroups.value = postGroups.value.map((g) =>
    g.keys.includes(key) ? { ...g, keys: g.keys.filter((k) => k !== key) } : g,
  );
}

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

// --- Sub-components ---

/** A draggable media thumbnail. */
function MediaThumbnail({
  item,
  isInGroup,
  groupId,
  showAssignHint,
}: {
  item: GroupableMediaItem;
  isInGroup: boolean;
  groupId: string | null;
  showAssignHint?: boolean;
}) {
  const isDragging =
    dragItem.value?.key === item.key;

  return (
    <div
      draggable
      onDragStart={() => handleDragStart(item.key, groupId)}
      onDragEnd={handleDragEnd}
      onClick={() => handleThumbnailClick(item.key, isInGroup)}
      title={
        isInGroup
          ? `${item.filename} — click to remove from group`
          : showAssignHint
            ? `${item.filename} — click to add to selected group`
            : item.filename
      }
      style={{
        position: "relative",
        borderRadius: "var(--radius)",
        overflow: "hidden",
        cursor: "grab",
        opacity: isDragging ? 0.4 : 1,
        transition: "opacity 0.15s, transform 0.15s",
        border: "2px solid transparent",
        background: "var(--color-bg)",
      }}
    >
      <div
        style={{
          width: "100%",
          aspectRatio: "1",
          background: "var(--color-surface-hover)",
        }}
      >
        <img
          src={thumbnailUrl(item.thumbnailKey)}
          alt={item.filename}
          loading="lazy"
          style={{
            width: "100%",
            height: "100%",
            objectFit: "cover",
            pointerEvents: "none",
          }}
          onError={(e) => {
            (e.target as HTMLImageElement).style.display = "none";
          }}
        />
      </div>

      {/* Type badge */}
      {item.type === "Video" && (
        <span
          style={{
            position: "absolute",
            top: "0.25rem",
            left: "0.25rem",
            fontSize: "0.5rem",
            padding: "0.0625rem 0.25rem",
            borderRadius: "3px",
            background: "rgba(108, 140, 255, 0.85)",
            color: "#fff",
            fontWeight: 600,
          }}
        >
          Video
        </span>
      )}

      {/* Remove indicator when in group */}
      {isInGroup && (
        <div
          style={{
            position: "absolute",
            top: "0.25rem",
            right: "0.25rem",
            width: "1rem",
            height: "1rem",
            borderRadius: "50%",
            background: "rgba(255, 107, 107, 0.85)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            fontSize: "0.625rem",
            color: "#fff",
            fontWeight: 700,
            lineHeight: 1,
          }}
        >
          ×
        </div>
      )}

      {/* Filename */}
      <div
        style={{
          padding: "0.25rem 0.375rem",
          fontSize: "0.5625rem",
          fontFamily: "var(--font-mono)",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
          color: "var(--color-text-secondary)",
        }}
      >
        {item.filename}
      </div>
    </div>
  );
}

/** A compact group icon in the group strip. */
function GroupIcon({
  group,
  isSelected,
  onSelect,
  onDelete,
}: {
  group: PostGroup;
  isSelected: boolean;
  onSelect: () => void;
  onDelete: () => void;
}) {
  const isOver = dragOverTarget.value === group.id;
  const isFull = group.keys.length >= MAX_ITEMS_PER_GROUP;
  // Get first 4 items for the mosaic preview
  const previewItems = group.keys
    .slice(0, 4)
    .map((key) => groupableMedia.value.find((m) => m.key === key))
    .filter((m): m is GroupableMediaItem => m !== undefined);

  return (
    <div
      onClick={onSelect}
      onDragOver={handleDragOver}
      onDragEnter={() => {
        if (!isFull) dragOverTarget.value = group.id;
      }}
      onDragLeave={() => {
        if (dragOverTarget.value === group.id) dragOverTarget.value = null;
      }}
      onDrop={(e) => handleDropOnGroup(e, group.id)}
      style={{
        minWidth: "8.5rem",
        maxWidth: "10rem",
        padding: "0.5rem",
        background: isSelected
          ? "rgba(108, 140, 255, 0.12)"
          : "var(--color-bg)",
        border: isOver
          ? "2px dashed var(--color-primary)"
          : isSelected
            ? "2px solid var(--color-primary)"
            : "2px solid var(--color-border)",
        borderRadius: "var(--radius)",
        cursor: "pointer",
        transition: "border-color 0.15s, background 0.15s",
        flexShrink: 0,
        position: "relative",
      }}
    >
      {/* Delete button */}
      <button
        onClick={(e) => {
          e.stopPropagation();
          onDelete();
        }}
        style={{
          position: "absolute",
          top: "0.25rem",
          right: "0.25rem",
          width: "1.125rem",
          height: "1.125rem",
          borderRadius: "50%",
          background: "var(--color-surface-hover)",
          border: "none",
          color: "var(--color-text-secondary)",
          fontSize: "0.625rem",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          padding: 0,
          cursor: "pointer",
          lineHeight: 1,
        }}
        title="Delete group"
      >
        ×
      </button>

      {/* Mini mosaic preview */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "1fr 1fr",
          gap: "2px",
          width: "3rem",
          height: "3rem",
          margin: "0 auto 0.375rem",
          borderRadius: "4px",
          overflow: "hidden",
          background: "var(--color-surface-hover)",
        }}
      >
        {[0, 1, 2, 3].map((i) => {
          const item = previewItems[i];
          return (
            <div
              key={i}
              style={{
                background: "var(--color-surface-hover)",
                overflow: "hidden",
              }}
            >
              {item && (
                <img
                  src={thumbnailUrl(item.thumbnailKey)}
                  alt=""
                  style={{
                    width: "100%",
                    height: "100%",
                    objectFit: "cover",
                    display: "block",
                  }}
                  onError={(e) => {
                    (e.target as HTMLImageElement).style.display = "none";
                  }}
                />
              )}
            </div>
          );
        })}
      </div>

      {/* Group name (truncated) */}
      <div
        style={{
          fontSize: "0.6875rem",
          fontWeight: 600,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
          textAlign: "center",
          marginBottom: "0.125rem",
          color: group.label ? "var(--color-text)" : "var(--color-text-secondary)",
        }}
        title={group.label || "Untitled group"}
      >
        {group.label || "Untitled"}
      </div>

      {/* Item count */}
      <div
        style={{
          fontSize: "0.625rem",
          textAlign: "center",
          color: isFull
            ? "var(--color-danger)"
            : "var(--color-text-secondary)",
        }}
      >
        {group.keys.length}/{MAX_ITEMS_PER_GROUP}
      </div>
    </div>
  );
}

/** The "+ New Group" drop target / button. */
function NewGroupButton() {
  const isOver = dragOverTarget.value === "__new__";

  return (
    <div
      onClick={() => createGroup()}
      onDragOver={handleDragOver}
      onDragEnter={() => {
        dragOverTarget.value = "__new__";
      }}
      onDragLeave={() => {
        if (dragOverTarget.value === "__new__") dragOverTarget.value = null;
      }}
      onDrop={handleDropOnNewGroup}
      style={{
        minWidth: "8.5rem",
        maxWidth: "10rem",
        padding: "0.5rem",
        background: isOver
          ? "rgba(108, 140, 255, 0.08)"
          : "var(--color-bg)",
        border: isOver
          ? "2px dashed var(--color-primary)"
          : "2px dashed var(--color-border)",
        borderRadius: "var(--radius)",
        cursor: "pointer",
        transition: "border-color 0.15s, background 0.15s",
        flexShrink: 0,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        minHeight: "6.5rem",
      }}
    >
      <div
        style={{
          fontSize: "1.5rem",
          color: isOver
            ? "var(--color-primary)"
            : "var(--color-text-secondary)",
          lineHeight: 1,
          marginBottom: "0.375rem",
        }}
      >
        +
      </div>
      <div
        style={{
          fontSize: "0.6875rem",
          color: isOver
            ? "var(--color-primary)"
            : "var(--color-text-secondary)",
          fontWeight: 500,
        }}
      >
        New Group
      </div>
    </div>
  );
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
                fontSize: "0.8125rem",
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
                fontSize: "0.6875rem",
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
              gridTemplateColumns: "repeat(auto-fill, minmax(100px, 1fr))",
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
                groupId={null}
                showAssignHint={hasSelectedGroup}
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
              fontSize: "0.8125rem",
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
            />
          ))}
          <NewGroupButton />
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
                  fontSize: "0.8125rem",
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
                fontSize: "0.8125rem",
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
                gridTemplateColumns: "repeat(auto-fill, minmax(100px, 1fr))",
                gap: "0.5rem",
              }}
            >
              {currentGroupItems.map((item) => (
                <MediaThumbnail
                  key={item.key}
                  item={item}
                  isInGroup={true}
                  groupId={currentGroup.id}
                />
              ))}
            </div>
          )}

          {currentGroupItems.length > 0 && (
            <div
              style={{
                marginTop: "0.5rem",
                fontSize: "0.6875rem",
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
          {nonEmptyGroups.length === 0 ? (
            <span style={{ color: "var(--color-text-secondary)" }}>
              Create a group and add media to proceed
            </span>
          ) : (
            <>
              <strong style={{ color: "var(--color-success)" }}>
                {nonEmptyGroups.length} post
                {nonEmptyGroups.length !== 1 ? "s" : ""}
              </strong>
              <span style={{ color: "var(--color-text-secondary)" }}>
                {" "}ready ({totalGrouped} items)
              </span>
              {totalUngrouped > 0 && (
                <span
                  style={{
                    color: "var(--color-text-secondary)",
                    fontSize: "0.75rem",
                    marginLeft: "0.5rem",
                  }}
                >
                  ({totalUngrouped} ungrouped — will not be published)
                </span>
              )}
            </>
          )}
        </span>
        <div style={{ display: "flex", gap: "0.75rem" }}>
          <button class="outline" onClick={handleBack}>
            Back to Enhancement
          </button>
          <button
            class="primary"
            onClick={handleProceed}
            disabled={nonEmptyGroups.length === 0}
          >
            Continue ({nonEmptyGroups.length} post
            {nonEmptyGroups.length !== 1 ? "s" : ""})
          </button>
        </div>
      </div>
    </div>
  );
}
