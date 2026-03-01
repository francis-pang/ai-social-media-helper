/**
 * Shared sticky action bar used at the bottom of every view.
 *
 * Provides a consistent layout: left slot (status text) and right slot (buttons),
 * with sticky positioning and the standard surface/border styling.
 */
import type { ComponentChildren } from "preact";

interface ActionBarProps {
  left: ComponentChildren;
  right: ComponentChildren;
}

export function ActionBar({ left, right }: ActionBarProps) {
  return (
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
      {left}
      {right}
    </div>
  );
}
