# DDR-033: Post Grouping UI — Drag-and-Drop Media Grouping

**Date**: 2026-02-08  
**Status**: Accepted  
**Iteration**: Step 6 of Media Selection Flow

## Context

After AI selection (Steps 2-3, DDR-030) and enhancement (Steps 4-5, DDR-031/DDR-032), the user has a set of enhanced media ready for publication. Instagram carousel posts support up to 20 items per post. Users typically want to group their media into thematic posts — e.g., "Tokyo Day 1 — Temples", "Tokyo Day 2 — Street Food", "Nightlife & City Views".

The grouping step must support:
1. **Viewing all enhanced media** on a single screen without excessive scrolling
2. **Creating multiple post groups** — each representing one Instagram carousel or download bundle
3. **Moving media freely** between groups — drag from one group to another, or remove and reassign
4. **Labeling groups** with descriptive text — these labels serve dual purpose as organizational aids during grouping AND as context for AI caption generation in Step 8
5. **Creating new groups on the fly** — no predefined number of groups; the user decides as they go
6. **Maximum 20 items per group** — Instagram carousel limit

Key constraint: This is a purely client-side operation. No backend API calls are needed during grouping. The group state is held in Preact signals and only persisted (to DynamoDB via the API Lambda) when the user proceeds to Step 7.

## Decision

### 1. Two-Panel Layout: Media Pool + Post Groups

The screen is divided into two areas:

```
┌────────────────────────────────────────────────────────────┐
│  UNGROUPED MEDIA (scrollable grid of thumbnails)          │
│  ┌─────┐ ┌─────┐ ┌─────┐ ┌─────┐ ┌─────┐ ┌─────┐       │
│  │img01│ │img02│ │img03│ │img04│ │img05│ │img06│       │
│  └─────┘ └─────┘ └─────┘ └─────┘ └─────┘ └─────┘       │
│  ┌─────┐ ┌─────┐ ...                                      │
│  │img07│ │img08│                                           │
│  └─────┘ └─────┘                                           │
├────────────────────────────────────────────────────────────┤
│  POST GROUPS (horizontal strip of compact group icons)     │
│                                                            │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐  │
│  │ Group 1  │  │ Group 2  │  │ Group 3  │  │ + New    │  │
│  │ 🖼 5     │  │ 🖼 3     │  │ 🖼 8     │  │  Group   │  │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘  │
│                                                            │
│  [Selected Group Detail Panel — shows items + label]       │
│  ┌─────┐ ┌─────┐ ┌─────┐ ┌─────┐ ┌─────┐                │
│  │img01│ │img02│ │img03│ │img04│ │img05│                │
│  └─────┘ └─────┘ └─────┘ └─────┘ └─────┘                │
│  Label: "Tokyo Day 1 — Temples and shrines at sunset"     │
└────────────────────────────────────────────────────────────┘
```

**Ungrouped Media Pool (top):** Grid of all enhanced media not yet assigned to any group. Each item is a clickable/draggable thumbnail. Clicking a thumbnail while a group is selected assigns it to that group. Drag-and-drop is also supported.

**Post Groups Strip (bottom):** Compact horizontal strip of group icons. Each icon shows the group name (truncated), item count, and a small preview mosaic. The "+" button creates a new group. Clicking a group icon expands it to show its contents inline.

**Selected Group Detail:** When a group icon is clicked/expanded, its contents appear as a grid of thumbnails below the strip. Items can be dragged out (back to ungrouped) or to other group icons.

### 2. Interaction Model

**Adding media to a group:**
- Click a thumbnail in the ungrouped pool → assigns to the currently selected group
- Drag a thumbnail from the ungrouped pool → drop on a group icon
- Drag a thumbnail from the ungrouped pool → drop on the "+ New Group" icon → creates a new group with that item

**Removing media from a group:**
- Click a thumbnail in the group detail → moves it back to the ungrouped pool
- Drag from the group detail → drop on ungrouped area or another group icon

**Moving media between groups:**
- Drag a thumbnail from one group → drop on another group icon
- Or: remove from current group (click), then add to target group (click)

**Group management:**
- Click "+ New Group" to create an empty group
- Click a group icon to select/expand it and see its contents
- Edit group label via an inline text input (supports long descriptive text)
- Delete a group (returns all its items to ungrouped)
- Max 20 items per group — UI prevents adding more

### 3. Group Label as Caption Context

Each group's label field accepts long descriptive text — not just a short name. This text serves two purposes:

1. **During Step 6:** Helps the user remember what each group is about while organizing
2. **During Step 8:** Passed to Gemini as context for AI caption generation. A label like "First morning in Shibuya — the energy of the crossing, coffee at Blue Bottle, found a great vintage shop on Cat Street" gives Gemini rich context to craft an engaging Instagram caption.

The label field is implemented as a `<textarea>` (not `<input>`) to accommodate multi-line descriptions.

### 4. Client-Side State Management

Post groups are managed entirely in Preact signals:

```typescript
interface PostGroup {
  id: string;
  label: string;
  keys: string[];   // S3 keys of enhanced media in this group
}

// Signals
const postGroups = signal<PostGroup[]>([]);
const selectedGroupId = signal<string | null>(null);
const dragItem = signal<{ key: string; sourceGroupId: string | null } | null>(null);
```

No API calls during grouping — state is persisted to DynamoDB only when the user clicks "Continue" to proceed to Step 7.

### 5. Compact Group Icons

Each group in the strip is rendered as a compact card (~120px wide) showing:
- A small mosaic of the first 4 thumbnails (2×2 grid, ~24px each)
- The group name (first line, truncated with ellipsis)
- Item count badge (e.g., "5/20")
- A colored border when selected

This keeps the group strip compact even with many groups, preventing clutter.

### 6. HTML5 Drag and Drop

The implementation uses the native HTML5 Drag and Drop API:
- `draggable="true"` on media thumbnails
- `onDragStart` stores the item key and source group in `dragItem` signal
- `onDragOver` on drop targets (group icons, ungrouped area, "+ New Group")
- `onDrop` performs the move operation
- Visual feedback: drop targets highlight with `border-color` change on `onDragEnter`/`onDragLeave`

No third-party drag-and-drop library — HTML5 DnD is sufficient for this use case and avoids dependency bloat. The interaction model also supports click-based assignment as an alternative, ensuring usability without drag-and-drop.

## Rationale

### Why two-panel (pool + groups) instead of multi-column groups?

Displaying all groups as separate columns side-by-side wastes horizontal space and becomes unusable with more than 3-4 groups. The pool-at-top + compact-icons-at-bottom approach scales to any number of groups while keeping the media pool visible at all times.

### Why click-to-assign in addition to drag-and-drop?

Drag-and-drop can be cumbersome for assigning many items quickly. Click-to-assign (click thumbnail → goes to selected group) is much faster for bulk operations. Both interaction modes complement each other.

### Why textarea for group labels?

Short input fields encourage short names like "Day 1" which don't help Gemini generate good captions. A textarea signals to the user that descriptive text is welcome and useful, leading to richer context for Step 8.

### Why no backend during grouping?

Grouping is a fast, iterative, UI-heavy operation. Making API calls on every drag-and-drop would add latency and complexity. The final group state is small (just arrays of S3 keys) and can be persisted in a single API call when the user proceeds.

### Why HTML5 Drag and Drop instead of a library?

The drag-and-drop needs are simple: move items between flat lists. Libraries like `dnd-kit` or `react-beautiful-dnd` add 15-30KB gzipped for features we don't need (nested lists, virtualization, keyboard DnD). HTML5 DnD works well for this use case in Chrome on macOS.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Multi-column group layout (Trello-style) | Doesn't scale beyond 3-4 groups; horizontal scrolling is awkward |
| Modal dialogs per group | Loses overview of all groups; tedious for frequent moves between groups |
| Server-side group state with real-time sync | Unnecessary complexity; grouping is a single-user, single-session activity |
| Third-party DnD library (dnd-kit, react-beautiful-dnd) | Adds bundle size; HTML5 DnD is sufficient for Chrome macOS target |
| Separate page per group | Loses the single-screen overview requirement |
| Auto-grouping by AI | Removes user control; AI can't know the user's posting strategy |

## Consequences

**Positive:**
- Single-screen workflow — user sees all media and all groups simultaneously
- Fast interaction — click-to-assign for bulk, drag-and-drop for precision
- Group labels serve as both organization and AI caption context
- No backend latency during grouping
- Compact group icons scale to any number of groups
- Works with both photos and videos
- HTML5 DnD keeps bundle size small

**Trade-offs:**
- HTML5 DnD has no keyboard accessibility (acceptable for Chrome macOS target)
- Group state is lost if the user refreshes the page before proceeding (acceptable for v1; DynamoDB persistence can be added later with auto-save)
- No undo/redo for grouping operations (can be added later with a history stack)
- The "+ New Group" drop target may be missed on first use — the UI should make it visually prominent

## Related Documents

- [DDR-030](./DDR-030-selection-ui.md) — Media Selection UI (Steps 2-3)
- [DDR-031](./DDR-031-multi-step-photo-enhancement.md) — Multi-Step Photo Enhancement Pipeline (Steps 4-5)
- [DDR-032](./DDR-032-multi-step-video-enhancement.md) — Multi-Step Video Enhancement Pipeline (Steps 4-5)
- [Media Selection Feature Plan](../../.cursor/plans/media_selection_feature_update_141c5fac.plan.md) — Full feature plan
