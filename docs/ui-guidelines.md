# UI Guidelines: Desktop 1440p Typography and Layout

**Established**: 2026-02-11 (DDR-057)  
**Target Environment**: 27–32" monitor, 2560×1440 resolution (~109 PPI), ~1 meter viewing distance

---

## Overview

This document defines the visual design standards for the web frontend. All rules are optimized for comfortable use on a 1440p desktop monitor at approximately 1 meter viewing distance, following ISO 9241-303 ergonomic standards.

The design system is centralized in [`style.css`](../web/frontend/src/style.css) and enforced through CSS custom properties, utility classes, and the conventions described here.

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

Colors are defined as CSS custom properties in `:root`:

| Variable | Value | Usage |
|----------|-------|-------|
| `--color-bg` | `#0f1117` | Page background |
| `--color-surface` | `#1a1d27` | Card/panel backgrounds |
| `--color-surface-hover` | `#242836` | Hover state backgrounds |
| `--color-border` | `#2e3348` | Borders, dividers |
| `--color-text` | `#e4e6f0` | Primary text |
| `--color-text-secondary` | `#8b8fa8` | Secondary/muted text |
| `--color-primary` | `#6c8cff` | Primary actions, links, current step |
| `--color-primary-hover` | `#5a7aee` | Primary button hover |
| `--color-danger` | `#ff6b6b` | Destructive actions, errors |
| `--color-danger-hover` | `#ee5a5a` | Danger button hover |
| `--color-success` | `#51cf66` | Success states, completed steps |

**Always use CSS variables** for colors — never hardcode hex values in components, except for overlays with explicit alpha channels (e.g., `rgba(108, 140, 255, 0.15)`).

---

## Component Conventions

### Inline Styles vs. CSS Classes

The codebase uses a mix of inline styles (for layout-specific, one-off styling) and CSS classes (for shared, reusable patterns). Follow these guidelines:

- **CSS classes** for: font sizes (utility classes), card styling (`.card`), button variants (`.primary`, `.outline`, `.danger`), text constraints (`.text-block`).
- **Inline styles** for: grid layouts with dynamic values, conditional styling, component-specific spacing, positional styling (`position: absolute`, `top`, `left`, etc.).

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

### Sticky Action Bars

Step-level action bars (containing Back / Continue buttons) should use:

```jsx
style={{
  position: "sticky",
  bottom: "1rem",
  padding: "1rem 1.5rem",
  background: "var(--color-surface)",
  borderRadius: "var(--radius-lg)",
  border: "1px solid var(--color-border)",
}}
```

This keeps navigation controls visible as the user scrolls through content.

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
- [ ] Colors reference CSS custom properties
- [ ] Prose text uses `.text-block` for line-length constraint
- [ ] File names use `--font-mono` font family
- [ ] Thumbnails use `aspect-ratio: 1`, `object-fit: cover`, and `loading="lazy"`
- [ ] Error states are at least `0.875rem` and use `--color-danger`

---

## Related Documents

- [DDR-057: Desktop 1440p UI Optimization](./design-decisions/DDR-057-desktop-1440p-ui-optimization.md) — The design decision record establishing these guidelines
- [DDR-022: Web UI with Preact SPA](./design-decisions/DDR-022-web-ui-preact-spa.md) — Web UI architecture
- [`style.css`](../web/frontend/src/style.css) — The source of truth for all CSS custom properties and global styles
