import {
  postGroups,
  selectedGroupId,
  getNextGroupId,
} from "./state";

/** Maximum items per Instagram carousel post. */
export const MAX_ITEMS_PER_GROUP = 20;

export function createGroup(initialKey?: string): string {
  const id = getNextGroupId();
  const keys = initialKey ? [initialKey] : [];
  postGroups.value = [
    ...postGroups.value,
    { id, label: "", keys },
  ];
  selectedGroupId.value = id;
  return id;
}

export function deleteGroup(groupId: string) {
  postGroups.value = postGroups.value.filter((g) => g.id !== groupId);
  if (selectedGroupId.value === groupId) {
    selectedGroupId.value = postGroups.value.length > 0
      ? postGroups.value[0]!.id
      : null;
  }
}

export function updateGroupLabel(groupId: string, label: string) {
  postGroups.value = postGroups.value.map((g) =>
    g.id === groupId ? { ...g, label } : g,
  );
}

export function addToGroup(groupId: string, key: string) {
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

export function removeFromGroup(groupId: string, key: string) {
  postGroups.value = postGroups.value.map((g) =>
    g.id === groupId ? { ...g, keys: g.keys.filter((k) => k !== key) } : g,
  );
}

export function removeFromAllGroups(key: string) {
  postGroups.value = postGroups.value.map((g) =>
    g.keys.includes(key) ? { ...g, keys: g.keys.filter((k) => k !== key) } : g,
  );
}
