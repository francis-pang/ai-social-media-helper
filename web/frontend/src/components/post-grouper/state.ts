import { signal } from "@preact/signals";
import type { PostGroup, GroupableMediaItem } from "../../types/api";

/** All post groups. Exported so downstream steps can consume them. */
export const postGroups = signal<PostGroup[]>([]);

/** All media items available for grouping (populated by EnhancementView on proceed). */
export const groupableMedia = signal<GroupableMediaItem[]>([]);

/** Currently selected/expanded group. */
export const selectedGroupId = signal<string | null>(null);

/** Drag state: which item is being dragged, and from where. */
export const dragItem = signal<{
  key: string;
  sourceGroupId: string | null; // null = ungrouped pool
} | null>(null);

/** Which drop target is currently highlighted. */
export const dragOverTarget = signal<string | null>(null);

/** Counter for generating unique group IDs. */
let groupIdCounter = 0;

/** Get next unique group ID (increments internal counter). */
export function getNextGroupId(): string {
  groupIdCounter++;
  return `group-${groupIdCounter}-${Date.now()}`;
}

/**
 * Reset all post grouping state to initial values (DDR-037).
 * Called by the invalidation cascade when a previous step changes.
 */
export function resetPostGrouperState() {
  postGroups.value = [];
  groupableMedia.value = [];
  selectedGroupId.value = null;
  dragItem.value = null;
  dragOverTarget.value = null;
  groupIdCounter = 0;
}
