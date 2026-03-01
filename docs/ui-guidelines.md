# UI Guidelines: Desktop 1440p Typography and Layout

**Established**: 2026-02-11 (DDR-057)  
**Target Environment**: 27–32" monitor, 2560×1440 resolution (~109 PPI), ~1 meter viewing distance

---

## Overview

This document defines the visual design standards for the web frontend. All rules are optimized for comfortable use on a 1440p desktop monitor at approximately 1 meter viewing distance, following ISO 9241-303 ergonomic standards.

The design system uses a **light theme with purple accents** (`--color-primary: #7c3aed`). It is centralized in [`style.css`](../web/frontend/src/style.css) and enforced through CSS custom properties, utility classes, and the conventions described here.

---

## Typography

### Root Font Size

The root font size is set to **24px** (`html { font-size: 24px }`), providing a 1.5× scale factor over the browser default of 16px. All `rem` values throughout the application resolve relative to this base.

**Do not change the root font size** without revisiting all downstream sizing.

### Font Size Scale

All text in the application must use one of the following sizes. These are the **only** permitted text sizes.

| Tier | CSS Value | Effective px | Physical Size | Usage | Utility Class |
|------|-----------|-------------|---------------|-------|---------------|
| **Minimum** | `0.75rem` | 18px | 4.2mm | Badges, tertiary labels, timestamps | `.text-xs` |
| **Secondary** | `0.875rem` | 21px | 4.9mm | Metadata, file sizes, secondary descriptions | `.text-sm` |
| **Body** | `1rem` | 24px | 5.6mm | Default body text, paragraphs, form inputs | `.text-base` |
| **Emphasis** | `1.125rem` | 27px | 6.3mm | Emphasized inline text, step arrows | `.text-lg` |
| **Title** | `1.25rem` | 30px | 7.0mm | Card titles, section headings | `.text-xl` |
| **Page Title** | `1.75rem` | 42px | 9.8mm | Page-level `h1` headings | — |
| **Section Title** | `1.25rem` | 30px | 7.0mm | `h2` headings | — |

### Minimum Text Size Rule

**No text may be smaller than `0.75rem` (18px / 4.2mm).** This is the absolute minimum for legibility at 1 meter. Font sizes such as `0.5rem`, `0.5625rem`, `0.625rem`, and `0.6875rem` are prohibited.

### When to Use Each Tier

- **`.text-xs` (0.75rem)**: Badge labels inside thumbnails, version indicators, character counts, progress fractions (e.g., "3/20"), keyboard shortcut hints.
- **`.text-sm` (0.875rem)**: File names in lists, upload progress text, secondary descriptions, action button text in compact contexts (e.g., "Remove", "Retry"), form labels.
- **`.text-base` (1rem)**: Default for all body paragraphs, form input text, primary button labels, descriptions, error messages.
- **`.text-lg` (1.125rem)**: Arrow connectors between steps, emphasized inline labels, elapsed time displays.
- **`.text-xl` (1.25rem)**: Workflow card titles (LandingPage), section headings inside cards.

### Typography Best Practices

1. **Prefer utility classes over inline styles** for font sizing. Use `className="text-sm"` instead of `style={{ fontSize: "0.875rem" }}` when possible. Inline styles are acceptable for one-off styling in JSX, but utility classes centralize the design system.

2. **Constrain text block width** to 65 characters using the `.text-block` utility class. This prevents eye-tracking fatigue on wide monitors. Apply to all prose paragraphs, descriptions, and multi-sentence text.

3. **Monospace text** (file names, code, keys) uses the `--font-mono` CSS variable (`SF Mono`, `Fira Code`, `Fira Mono`). Code elements use `0.875rem` via the global `code` rule.

---

## Layout

### App Container

```css
#app {
  max-width: 2000px;  /* ~78% of 2560px */
  margin: 0 auto;
  padding: 2rem 1.5rem;
}
```

The container uses approximately 78% of a 2560px viewport width, centered with auto margins.

### Grid System

Grid track minimums are defined as CSS custom properties in `:root`. **Always use these variables** instead of hardcoded pixel values in `grid-template-columns`.

| Variable | Value | Effective px | Usage |
|----------|-------|-------------|-------|
| `--grid-card-lg` | `21rem` | ~504px | Landing page workflow cards |
| `--grid-card-md` | `13rem` | ~312px | Selection/triage thumbnail grids |
| `--grid-card-sm` | `10rem` | ~240px | Enhancement/grouper thumbnail grids |
| `--grid-thumb-sm` | `7rem` | ~168px | Scene group small thumbnails |

#### Usage Pattern

```css
/* Correct — uses CSS variable */
grid-template-columns: repeat(auto-fill, minmax(var(--grid-card-md), 1fr));

/* Incorrect — hardcoded pixels */
grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
```

### Sidebar Layout

The `.layout-sidebar` class provides a 2-column grid with a fixed-width sidebar.

```css
.layout-sidebar {
  display: grid;
  grid-template-columns: 1fr 320px;
  gap: 1.5rem;
}
```

Use for screens with a main content area and a side panel (e.g., triage review with metadata sidebar, detail views with actions panel).

Sidebar sections use `.sidebar-panel` — a white card with border and shadow. `h3` children inside a sidebar panel are auto-styled as small-caps section headers.

```html
<div class="layout-sidebar">
  <main><!-- primary content --></main>
  <aside>
    <div class="sidebar-panel">
      <h3>Details</h3>
      <!-- section content -->
    </div>
  </aside>
</div>
```

### Masonry Grid

The `.masonry-grid` class uses CSS columns for variable-height card layouts (3 columns). Use for triage result screens where cards have different content heights.

```css
.masonry-grid {
  columns: 3;
}
```

### Drop Zone

The `.drop-zone` class provides a dashed-border container for file upload areas. It includes hover and active states (purple border + light purple background).

```html
<div class="drop-zone">
  <span class="drop-zone__icon">📁</span>
  <span class="drop-zone__title">Drop files here</span>
  <span class="drop-zone__subtitle">or click to browse</span>
</div>
```

All three child elements (`__icon`, `__title`, `__subtitle`) should be present for consistent layout.

### Border Radii

| Variable | Value | Usage |
|----------|-------|-------|
| `--radius` | `12px` | Buttons, inputs, small cards, thumbnails |
| `--radius-lg` | `18px` | Card containers, the step navigator bar |

### Card Padding

All `.card` elements use `2rem` padding. Do not override this with smaller values.

---

## Interactive Elements

### Click Target Sizes (Fitts's Law)

All interactive elements must have an **effective click area of at least 44px** in height. This includes:

- Buttons (enforced by `min-height: 2.75rem` / 66px)
- Step navigator pills (enforced by pill padding)
- Checkboxes (at least `1.25rem` × `1.25rem`)
- Clickable thumbnail cards (naturally larger than 44px)
- Media player overlay controls

### Buttons

Global button styles are defined in `style.css`:

```css
button {
  padding: 0.625rem 1.5rem;
  font-size: 1rem;
  min-height: 2.75rem;  /* 66px — well above 44px threshold */
}
```

Button hover states include `transform: scale(1.02)` for visibility at distance, plus higher-contrast border color for `.outline` buttons.

**Compact buttons** (e.g., "Remove" in file lists) may use smaller padding via inline styles but must still meet the 44px height threshold through their container or the global `min-height`.

### Form Inputs

All text inputs must have:
- `font-size: 1rem` (24px) — matches body text
- `min-height: 2.75rem` (66px) — matches button height for visual consistency
- Adequate padding (`0.5rem 0.75rem` minimum)

---

## Color System

Colors are defined as CSS custom properties in `:root`. The design uses a light background with purple primary accents.

| Variable | Value | Usage |
|----------|-------|-------|
| `--color-bg` | `#f8f9fc` | Page background |
| `--color-surface` | `#ffffff` | Card/panel backgrounds |
| `--color-surface-hover` | `#f3f4f6` | Hover state backgrounds |
| `--color-surface-alt` | `#f1f3f9` | Alternate surface (e.g., sidebar sections, code blocks) |
| `--color-border` | `#e2e5ef` | Borders, dividers |
| `--color-text` | `#1a1d2e` | Primary text |
| `--color-text-secondary` | `#6b7280` | Secondary/muted text |
| `--color-primary` | `#7c3aed` | Primary actions, links, active states |
| `--color-primary-hover` | `#6d28d9` | Primary button hover |
| `--color-primary-light` | `rgba(124, 58, 237, 0.08)` | Tinted backgrounds for selected/active items |
| `--color-danger` | `#ef4444` | Destructive actions, errors |
| `--color-danger-hover` | `#dc2626` | Danger button hover |
| `--color-success` | `#22c55e` | Success states, completed steps |
| `--color-warning` | `#f59e0b` | Warning states, caution indicators |
| `--color-shadow` | `rgba(0, 0, 0, 0.06)` | Box shadows |

### Color Rules

- **Always use CSS variables** for colors — never hardcode hex values in components.
- **`rgba()` convention for tints**: Use `rgba(r, g, b, 0.08)` for tinted backgrounds (e.g., `--color-primary-light` for purple-tinted selection highlights). This keeps tints visually consistent with their base color while staying readable on white surfaces.
- **Exception — MediaReviewModal**: The full-screen review modal uses a charcoal backdrop (`#1a1a2e`). This is an intentional dark exception for media review where a dark background improves image evaluation. See Component Conventions below.

---

## Component Conventions

### Inline Styles vs. CSS Classes

The codebase uses a mix of inline styles (for layout-specific, one-off styling) and CSS classes (for shared, reusable patterns). Follow these guidelines:

- **CSS classes** for: font sizes (utility classes), card styling (`.card`), button variants (`.primary`, `.outline`, `.danger`), text constraints (`.text-block`), layout patterns (`.layout-sidebar`, `.masonry-grid`), component patterns (`.drop-zone`, `.log-console`, `.step-pipeline`).
- **Inline styles** for: grid layouts with dynamic values, conditional styling, component-specific spacing, positional styling (`position: absolute`, `top`, `left`, etc.).

### Drop Zone

Use `.drop-zone` for any file/media upload area. Required child elements:

| Element | Class | Content |
|---------|-------|---------|
| Icon | `.drop-zone__icon` | Upload icon or emoji |
| Title | `.drop-zone__title` | Primary call-to-action text |
| Subtitle | `.drop-zone__subtitle` | Secondary hint (e.g., "or click to browse") |

The drop zone shows a dashed border by default, transitioning to a solid purple border with `--color-primary-light` background on hover/drag-active.

### Streaming Log Console

Use `.log-console` for scrollable monospace output areas (e.g., processing logs, server output).

```html
<div class="log-console">
  <div class="log-entry log-entry--info">Processing file...</div>
  <div class="log-entry log-entry--success">✓ Upload complete</div>
  <div class="log-entry log-entry--warn">⚠ Low resolution detected</div>
  <div class="log-entry log-entry--error">✗ Failed to process</div>
  <div class="log-entry log-entry--debug">debug: raw response...</div>
</div>
```

| Modifier | Color Purpose |
|----------|--------------|
| `.log-entry--info` | Default text color |
| `.log-entry--success` | `--color-success` |
| `.log-entry--warn` | `--color-warning` |
| `.log-entry--error` | `--color-danger` |
| `.log-entry--debug` | `--color-text-secondary` |

### Step Pipeline Indicator

Use `.step-pipeline` for horizontal multi-step progress indicators with connecting lines.

```html
<div class="step-pipeline">
  <div class="step-pipeline__step step-pipeline__step--done">
    <span class="step-pipeline__icon">✓</span>
    <span class="step-pipeline__label">Upload</span>
  </div>
  <div class="step-pipeline__connector"></div>
  <div class="step-pipeline__step step-pipeline__step--active">
    <span class="step-pipeline__icon">2</span>
    <span class="step-pipeline__label">Triage</span>
  </div>
  <div class="step-pipeline__connector"></div>
  <div class="step-pipeline__step step-pipeline__step--pending">
    <span class="step-pipeline__icon">3</span>
    <span class="step-pipeline__label">Export</span>
  </div>
</div>
```

| Modifier | Appearance |
|----------|------------|
| `--done` | `--color-success` icon, completed state |
| `--active` | `--color-primary` icon, current step |
| `--pending` | `--color-text-secondary` icon, dimmed |

### Reason Filter Pills

Use `.reason-filters` as a flex container for rows of `.reason-pill` buttons. Pills toggle between default and `--active` states to filter content.

```html
<div class="reason-filters">
  <button class="reason-pill reason-pill--active">Blurry</button>
  <button class="reason-pill">Duplicate</button>
  <button class="reason-pill">Overexposed</button>
</div>
```

Active pills use `--color-primary` with a `--color-primary-light` background.

### Sidebar Panels

Use `.sidebar-panel` inside a `.layout-sidebar` aside column. Each panel is a white card with border and shadow. `h3` elements inside are auto-styled as small-caps section headers.

```html
<div class="sidebar-panel">
  <h3>AI Analysis</h3>
  <p>Confidence: 92%</p>
</div>
```

### Sticky Action Bar

Use `.sticky-action-bar` for step-level navigation bars (Back / Continue buttons) that remain visible during scroll.

```html
<div class="sticky-action-bar">
  <button class="outline">Back</button>
  <button class="primary">Continue</button>
</div>
```

The bar uses `position: sticky`, `bottom: 1rem`, and a box shadow via `--color-shadow`.

### Progress Top Bar

Use `.progress-top-bar` with `.progress-top-bar__fill` for a thin fixed-position progress indicator at the top of the viewport.

```html
<div class="progress-top-bar">
  <div class="progress-top-bar__fill" style="width: 45%"></div>
</div>
```

### MediaReviewModal

The MediaReviewModal uses a full-screen charcoal backdrop (`#1a1a2e`) — an intentional dark exception to the light theme. Dark backgrounds are appropriate when the primary task is evaluating visual media (photos, video frames), as they reduce glare and improve perceived contrast of the content being reviewed.

Features:
- AI Vision brightness toggle and exposure slider for image evaluation
- Floating AI reasoning card overlaying the media
- Thumbnail queue strip along the bottom edge
- Keyboard navigation (arrow keys, Escape to close)

**When to use dark vs. light backgrounds**: Use the light theme (default) for all workflow, form, and data screens. Use a dark backdrop only for full-screen media review where accurate color/exposure evaluation is required.

### Badges and Status Indicators

Thumbnail overlay badges (type, phase, rank, score) should:
- Use `0.75rem` font size (minimum tier)
- Have at least `0.125rem 0.375rem` padding
- Use a `4px` border radius (tighter than `--radius` for compact elements)
- Use semi-transparent backgrounds with appropriate contrast against the thumbnail

### Media Thumbnails

Thumbnail containers should:
- Use `aspect-ratio: 1` for consistent square sizing
- Use `object-fit: cover` for images
- Include `loading="lazy"` for off-screen images
- Provide `onError` handlers to gracefully hide broken images

---

## Responsive Considerations

### Current Scope

The UI is optimized for a single target: **2560×1440 desktop at ~1 meter**. There are no mobile or tablet breakpoints.

### Safety Nets

Even without breakpoints, the following safety nets are retained for window-snapped layouts (e.g., half-screen, quarter-screen on multi-monitor setups):

- `flex-wrap: wrap` on flex containers that might overflow
- `overflow-x: auto` on the step navigator and horizontal scroll areas
- `auto-fill` (not `auto-fit`) in grid templates, so grids degrade gracefully at narrow widths

### Future Scaling

If additional viewing distances or resolutions need support:
1. Adjust `html { font-size }` — all rem-based values cascade automatically
2. Adjust `--grid-card-*` variables for grid track sizing
3. Adjust `#app { max-width }` for viewport utilization
4. No component changes should be needed

---

## Checklist for New Components

When adding a new component, verify:

- [ ] All text uses a permitted font size tier (minimum `0.75rem`)
- [ ] Buttons and interactive elements meet the 44px click target
- [ ] Grids use `--grid-card-*` variables, not hardcoded pixel values
- [ ] Colors reference CSS custom properties (no hardcoded hex except `rgba()` tints)
- [ ] Prose text uses `.text-block` for line-length constraint
- [ ] File names use `--font-mono` font family
- [ ] Thumbnails use `aspect-ratio: 1`, `object-fit: cover`, and `loading="lazy"`
- [ ] Error states are at least `0.875rem` and use `--color-danger`
- [ ] Upload areas use `.drop-zone` with all three child elements
- [ ] Log output uses `.log-console` with appropriate `--info`/`--success`/`--warn`/`--error` modifiers
- [ ] Multi-step flows use `.step-pipeline` for progress indication
- [ ] Sidebar layouts use `.layout-sidebar` + `.sidebar-panel`
- [ ] Action bars use `.sticky-action-bar` instead of inline sticky styles

---

## Related Documents

- [DDR-057: Desktop 1440p UI Optimization](./design-decisions/DDR-057-desktop-1440p-ui-optimization.md) — The design decision record establishing these guidelines
- [DDR-022: Web UI with Preact SPA](./design-decisions/DDR-022-web-ui-preact-spa.md) — Web UI architecture
- [`style.css`](../web/frontend/src/style.css) — The source of truth for all CSS custom properties and global styles
