import { computed } from "@preact/signals";
import { currentStep, navigateToStep, stepHistory } from "../app";
import type { JSX } from "preact";

/**
 * StepNavigator — horizontal step indicator for the cloud selection flow (DDR-037).
 *
 * Design:
 * - Compact numbered pills with short labels
 * - Arrow connectors between steps (inspired by reference screenshots)
 * - Minimalist: no icons, no descriptions — just step number + 1-2 word label
 * - Completed steps are clickable for back-navigation
 * - Current step is highlighted; future steps are dimmed
 */

/** The 6 user-facing steps in the cloud selection flow. */
const STEPS = [
  { id: 1, label: "Upload", steps: ["upload"] },
  { id: 2, label: "Select", steps: ["selecting", "review-selection"] },
  { id: 3, label: "Enhance", steps: ["enhancing", "review-enhanced"] },
  { id: 4, label: "Group", steps: ["group-posts"] },
  { id: 5, label: "Download", steps: ["publish"] },
  { id: 6, label: "Caption", steps: ["description"] },
] as const;

/** Map a Step value to its navigator index (0-based). */
function stepToIndex(step: string): number {
  return STEPS.findIndex((s) => (s.steps as readonly string[]).includes(step));
}

/** The current navigator index. */
const currentIndex = computed(() => stepToIndex(currentStep.value));

/**
 * The highest step index the user has reached.
 * Derived from the step history stack + current step.
 */
const highestReached = computed(() => {
  const indices = [
    ...stepHistory.value.map((s) => stepToIndex(s)),
    currentIndex.value,
  ].filter((i) => i >= 0);
  return Math.max(...indices, 0);
});

// --- Styles ---

const NAV_CONTAINER: JSX.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "0",
  marginBottom: "1.5rem",
  padding: "0.75rem 1rem",
  background: "var(--color-surface)",
  borderRadius: "var(--radius-lg)",
  border: "1px solid var(--color-border)",
  overflowX: "auto",
  flexWrap: "wrap",
  justifyContent: "center",
};

function stepPillStyle(
  state: "completed" | "current" | "future",
): JSX.CSSProperties {
  const base: JSX.CSSProperties = {
    display: "flex",
    alignItems: "center",
    gap: "0.375rem",
    padding: "0.5rem 1rem",
    borderRadius: "999px",
    fontSize: "0.875rem",
    fontWeight: "600",
    letterSpacing: "0.02em",
    whiteSpace: "nowrap",
    transition: "all 0.2s ease",
    border: "2px solid transparent",
  };

  switch (state) {
    case "completed":
      return {
        ...base,
        background: "rgba(81, 207, 102, 0.12)",
        color: "#51cf66",
        border: "2px solid rgba(81, 207, 102, 0.25)",
        cursor: "pointer",
      };
    case "current":
      return {
        ...base,
        background: "rgba(108, 140, 255, 0.15)",
        color: "#6c8cff",
        border: "2px solid rgba(108, 140, 255, 0.4)",
        cursor: "default",
      };
    case "future":
      return {
        ...base,
        background: "transparent",
        color: "var(--color-text-secondary)",
        opacity: "0.5",
        cursor: "default",
      };
  }
}

function numberBadgeStyle(
  state: "completed" | "current" | "future",
): JSX.CSSProperties {
  const base: JSX.CSSProperties = {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    width: "1.75rem",
    height: "1.75rem",
    borderRadius: "50%",
    fontSize: "0.75rem",
    fontWeight: "700",
    lineHeight: "1",
    flexShrink: "0",
  };

  switch (state) {
    case "completed":
      return {
        ...base,
        background: "#51cf66",
        color: "#0f1117",
      };
    case "current":
      return {
        ...base,
        background: "#6c8cff",
        color: "#ffffff",
      };
    case "future":
      return {
        ...base,
        background: "var(--color-border)",
        color: "var(--color-text-secondary)",
      };
  }
}

function arrowStyle(completed: boolean): JSX.CSSProperties {
  return {
    display: "flex",
    alignItems: "center",
    padding: "0 0.25rem",
    color: completed ? "rgba(81, 207, 102, 0.5)" : "var(--color-border)",
    fontSize: "1.125rem",
    flexShrink: "0",
    userSelect: "none",
  };
}

// --- Component ---

export function StepNavigator() {
  const ci = currentIndex.value;
  const hr = highestReached.value;

  return (
    <nav style={NAV_CONTAINER} aria-label="Workflow steps">
      {STEPS.map((step, idx) => {
        const state: "completed" | "current" | "future" =
          idx < ci
            ? "completed"
            : idx === ci
              ? "current"
              : idx <= hr
                ? "completed"
                : "future";

        const isClickable = state === "completed";

        function handleClick() {
          if (!isClickable) return;
          // Navigate to the first step value for this navigator entry.
          // Use navigateToStep so the history stack is updated.
          const targetStep = step.steps[0];
          navigateToStep(targetStep as Parameters<typeof navigateToStep>[0]);
        }

        return (
          <>
            <div
              key={step.id}
              style={stepPillStyle(state)}
              onClick={handleClick}
              role={isClickable ? "button" : undefined}
              tabIndex={isClickable ? 0 : undefined}
              aria-current={state === "current" ? "step" : undefined}
              onKeyDown={
                isClickable
                  ? (e: KeyboardEvent) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        handleClick();
                      }
                    }
                  : undefined
              }
              title={
                isClickable
                  ? `Go back to ${step.label}`
                  : state === "current"
                    ? `Currently on ${step.label}`
                    : step.label
              }
            >
              <span style={numberBadgeStyle(state)}>
                {state === "completed" && idx < ci ? "✓" : step.id}
              </span>
              <span>{step.label}</span>
            </div>

            {/* Arrow connector (not after the last step) */}
            {idx < STEPS.length - 1 && (
              <span style={arrowStyle(idx < ci)} aria-hidden="true">
                →
              </span>
            )}
          </>
        );
      })}
    </nav>
  );
}
