# DDR-057: Desktop 1440p UI Optimization for 1-Meter Viewing Distance

**Date**: 2026-02-11  
**Status**: Accepted  
**Iteration**: 1

## Context

The web UI was designed with default 16px root font sizing, which assumes a standard ~60cm viewing distance on consumer monitors. In practice, the application is used on a 27–32" desktop monitor at 2560x1440 resolution (~109 PPI) from a comfortable distance of approximately 1 meter. At this distance, the default text sizes fall below the minimum comfortable visual angle (20–22 arcminutes per ISO 9241-303), causing eye strain during sustained use. Similarly, click targets designed for close-up interaction are too small at arm's length, violating Fitts's Law principles.

Key ergonomic observations:

- At 109 PPI, each pixel is ~0.233mm. The standard comfortable viewing distance of ~60cm requires a **1.67x linear scale** to maintain the same angular size at 1 meter.
- Comfortable character heights for sustained reading require **25–28px** at 109 PPI (5.8–6.4mm physical height).
- A **24px base font** (5.6mm) sits at the lower bound of this comfort range while keeping the UI compact enough to remain functional.
- The **absolute minimum text size** is 18px (0.75rem at 24px base ≈ 4.2mm). Nothing smaller.
- All interactive click targets must meet the **44px minimum** effective height (Fitts's Law threshold).

Additionally, the existing UI used hardcoded pixel values for grid track sizes and scattered inline `fontSize` declarations across 16 components, making systematic size adjustments fragile and inconsistent.

## Decision

Scale the entire web UI for comfortable use at 1 meter on a 1440p desktop monitor through the following coordinated changes:

### 1. Global CSS Foundation (`style.css`)

- **Root font**: Increase `html { font-size }` from `16px` to `24px`, giving a 1.5× scale factor that cascades through all rem-based sizes.
- **Layout width**: Increase `#app { max-width }` from `1200px` to `2000px` (~78% of 2560px).
- **Border radii**: Scale `--radius` from `8px` to `12px` and `--radius-lg` from `12px` to `18px`.
- **Button sizing**: Increase padding to `0.625rem 1.5rem`, font-size to `1rem`, add `min-height: 2.75rem` (66px, well above 44px Fitts threshold), and add `transform: scale(1.02)` on hover for better visibility.
- **Headings**: Scale `h1` to `1.75rem` (42px) and `h2` to `1.25rem` (30px).
- **Cards**: Increase padding from `1.5rem` to `2rem`.
- **Code**: Increase font-size from `0.8125rem` to `0.875rem`.
- **CSS grid variables**: Centralize grid track minimums as CSS custom properties (`--grid-card-lg`, `--grid-card-md`, `--grid-card-sm`, `--grid-thumb-sm`) so component grids reference variables instead of hardcoded pixel values.
- **Typography utility classes**: Add `.text-xs` (0.75rem), `.text-sm` (0.875rem), `.text-base` (1rem), `.text-lg` (1.125rem), `.text-xl` (1.25rem) for centralized font sizing.
- **Text block constraint**: Add `.text-block { max-width: 65ch }` for prose readability (prevents eye-tracking fatigue on wide monitors).

### 2. Component Updates (16 files)

- Replace all inline `fontSize` values below `0.75rem` (e.g., `0.5rem`, `0.5625rem`, `0.625rem`, `0.6875rem`) with at least `0.75rem`.
- Replace `0.8125rem` occurrences with `0.875rem` for consistency with the `.text-sm` tier.
- Swap hardcoded pixel values in CSS grid `minmax()` calls with the new CSS custom properties.
- Use utility classes (`text-sm`, `text-xs`) where inline `fontSize` can be replaced with `className`.
- Ensure form inputs (LoginForm) have `min-height: 2.75rem` and `font-size: 1rem`.
- Ensure the MediaPlayer close button meets the 44px click target.

### 3. No Viewport Meta Changes

Desktop browsers ignore `<meta name="viewport">` width directives. The existing `width=device-width, initial-scale=1.0` is harmless and standard. All scaling is handled purely via CSS.

## Rationale

1. **Single highest-leverage change**: Increasing the root font-size from 16px to 24px cascades through every rem-based measurement in the application, achieving global scaling with one line of CSS.
2. **Ergonomic standards compliance**: The 24px base meets ISO 9241-303 comfortable viewing angles for sustained reading at 1 meter.
3. **Centralized grid sizing**: CSS custom properties for grid tracks eliminate scattered magic numbers and enable future layout tuning from a single location.
4. **Fitts's Law compliance**: All interactive elements (buttons, checkboxes, clickable thumbnails) meet or exceed the 44px effective height threshold.
5. **Minimal code churn**: Global CSS changes handle ~80% of the scaling; component changes are primarily replacing sub-minimum font sizes and swapping hardcoded grid values for variables.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Browser zoom (Ctrl + +) | Inconsistent across browsers; does not fix grid layouts or click targets; user must manually set each session |
| CSS `zoom` property | Not a web standard; inconsistent rendering across browsers; does not affect media queries |
| `transform: scale(1.5)` on root | Scales the entire layout including viewport interactions; breaks scroll, focus, and pointer calculations |
| 1.25× scale factor (20px base) | Insufficient for 1-meter distance; falls below ISO 9241-303 minimum at the edges |
| Per-component responsive breakpoints | Massive code churn for 16+ components; does not address the fundamental root font sizing issue |

## Consequences

**Positive:**
- All text is comfortably readable at 1 meter (minimum 18px / 4.2mm physical)
- All click targets exceed the 44px Fitts's Law threshold
- Grid layouts use centralized CSS variables, enabling easy future adjustment
- Typography utility classes reduce inline style proliferation going forward
- Text blocks are width-constrained to 65 characters for optimal readability

**Trade-offs:**
- The UI is optimized for a specific use case (1440p desktop at 1m) and may feel oversized at closer distances or on smaller screens
- The `max-width: 2000px` on `#app` uses more horizontal space, which may look sparse on very wide or lower-resolution monitors
- Existing screenshots and visual regression baselines will need updating

## Font Size Reference (at 24px root)

| CSS Value | Effective px | Physical (109 PPI) | Usage | Utility Class |
|-----------|-------------|---------------------|-------|---------------|
| `0.75rem` | 18px | 4.2mm | Absolute minimum — badges, tertiary labels | `.text-xs` |
| `0.875rem` | 21px | 4.9mm | Secondary text, metadata | `.text-sm` |
| `1rem` | 24px | 5.6mm | Body text | `.text-base` |
| `1.125rem` | 27px | 6.3mm | Emphasis | `.text-lg` |
| `1.25rem` | 30px | 7.0mm | Card/section titles | `.text-xl` |
| `1.75rem` | 42px | 9.8mm | Page title (h1) | — |

## Related Documents

- [DDR-022: Web UI with Preact SPA and Go JSON API](./DDR-022-web-ui-preact-spa.md)
- [DDR-037: Step Navigation UI and Downstream State Invalidation](./DDR-037-step-navigation-and-state-invalidation.md)
- [UI Guidelines: Desktop 1440p Typography and Layout](../ui-guidelines.md)
