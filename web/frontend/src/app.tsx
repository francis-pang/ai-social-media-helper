import { signal, computed } from "@preact/signals";
import { isCloudMode, invalidateSession } from "./api/client";
import {
  isAuthenticated,
  isAuthRequired,
  authLoading,
  signOut,
} from "./auth/cognito";
import { DownloadView, resetDownloadState } from "./components/DownloadView";
import { FileBrowser } from "./components/FileBrowser";
import {
  EnhancementView,
  resetEnhancementState,
} from "./components/EnhancementView";
import { LoginForm } from "./components/LoginForm";
import { MediaUploader } from "./components/MediaUploader";
import {
  PostGrouper,
  resetPostGrouperState,
} from "./components/PostGrouper";
import { SelectedFiles } from "./components/SelectedFiles";
import {
  SelectionView,
  resetSelectionState,
} from "./components/SelectionView";
import { StepNavigator } from "./components/StepNavigator";
import {
  DescriptionEditor,
  resetDescriptionState,
} from "./components/DescriptionEditor";
import { TriageView } from "./components/TriageView";

/** Application steps across both workflows. */
export type Step =
  // Triage flow (local mode)
  | "browse"
  | "confirm-files"
  | "processing"
  | "results"
  // Selection flow (cloud mode — DDR-029)
  | "upload"
  | "selecting"
  | "review-selection"
  | "enhancing"
  | "review-enhanced"
  | "group-posts"
  | "publish"
  | "description";

export const currentStep = signal<Step>(isCloudMode ? "upload" : "browse");
export const selectedPaths = signal<string[]>([]);
export const triageJobId = signal<string | null>(null);

/** Upload session ID — groups uploaded files in S3 under a common prefix. */
export const uploadSessionId = signal<string | null>(null);

/** Trip/event context for AI selection (e.g., "3-day trip to Tokyo, Oct 2025"). */
export const tripContext = signal<string>("");

/** Step history stack for back-navigation in the selection flow. */
export const stepHistory = signal<Step[]>([]);

// --- Step Navigation (DDR-037) ---

/**
 * Cloud-mode step order for invalidation logic.
 * Maps navigator index to the invalidation step name used by the backend.
 */
const CLOUD_STEP_ORDER: { steps: Step[]; invalidationKey: string }[] = [
  { steps: ["upload"], invalidationKey: "upload" },
  { steps: ["selecting", "review-selection"], invalidationKey: "selection" },
  { steps: ["enhancing", "review-enhanced"], invalidationKey: "enhancement" },
  { steps: ["group-posts"], invalidationKey: "grouping" },
  { steps: ["publish"], invalidationKey: "download" },
  { steps: ["description"], invalidationKey: "description" },
];

/** Get the navigator index (0-based) for a Step value. */
function stepToNavIndex(step: Step): number {
  return CLOUD_STEP_ORDER.findIndex((entry) =>
    (entry.steps as Step[]).includes(step),
  );
}

/** Navigate to a new step, pushing the current step onto the history stack. */
export function navigateToStep(step: Step) {
  stepHistory.value = [...stepHistory.value, currentStep.value];
  currentStep.value = step;
}

/** Navigate back to the previous step (pops from history stack). */
export function navigateBack() {
  const history = stepHistory.value;
  if (history.length === 0) return;
  const prev = history[history.length - 1]!;
  stepHistory.value = history.slice(0, -1);
  currentStep.value = prev;
}

/**
 * Invalidate all downstream state from the given step onward (DDR-037).
 *
 * Called when a user goes back and re-triggers processing at an earlier step.
 * Resets frontend signals for all steps after `fromStep` and fires the
 * backend invalidation endpoint to clear in-memory jobs and S3 artifacts.
 *
 * @param fromStep - The invalidation key (e.g., "selection", "enhancement").
 *                   All steps AFTER this one are invalidated.
 */
export async function invalidateDownstream(
  fromStep: "selection" | "enhancement" | "grouping" | "download" | "description",
) {
  const sessionId = uploadSessionId.value;
  if (!sessionId) return;

  // Fire backend invalidation (best-effort — don't block on it)
  invalidateSession({ sessionId, fromStep }).catch((err) => {
    console.warn("Backend invalidation failed (non-blocking):", err);
  });

  // Reset frontend component signals for each invalidated step.
  // The cascade order is: selection -> enhancement -> grouping -> download -> description.
  const resetMap: Record<string, () => void> = {
    selection: resetSelectionState,
    enhancement: resetEnhancementState,
    grouping: resetPostGrouperState,
    download: resetDownloadState,
    description: resetDescriptionState,
  };

  const fromIndex = CLOUD_STEP_ORDER.findIndex(
    (e) => e.invalidationKey === fromStep,
  );
  if (fromIndex >= 0) {
    // Reset signals for each step from fromIndex onward
    for (const entry of CLOUD_STEP_ORDER.slice(fromIndex)) {
      const resetFn = resetMap[entry.invalidationKey];
      if (resetFn) resetFn();
    }

    // Trim step history: remove entries for invalidated steps
    const stepsToRemove = new Set<Step>(
      CLOUD_STEP_ORDER.slice(fromIndex).flatMap((e) => e.steps),
    );
    stepHistory.value = stepHistory.value.filter((s) => !stepsToRemove.has(s));
  }
}

/** Application title — differs by mode. */
const appTitle = isCloudMode ? "Media Selection" : "Media Triage";

const stepTitle = computed(() => {
  switch (currentStep.value) {
    // Triage flow
    case "browse":
      return "Select Media";
    case "confirm-files":
      return "Confirm Selection";
    case "processing":
      return "Processing...";
    case "results":
      return "Triage Results";
    // Selection flow
    case "upload":
      return "Upload Media";
    case "selecting":
      return "AI Selection";
    case "review-selection":
      return "Review Selection";
    case "enhancing":
      return "Enhancement";
    case "review-enhanced":
      return "Review Enhanced";
    case "group-posts":
      return "Group into Posts";
    case "publish":
      return "Publish or Download";
    case "description":
      return "Post Description";
  }
});

/** Whether the current step is part of the cloud selection flow. */
const isCloudStep = computed(() => stepToNavIndex(currentStep.value) >= 0);

export function App() {
  // Show loading indicator while checking existing session
  if (authLoading.value) {
    return (
      <div style={{ textAlign: "center", padding: "4rem 0" }}>
        <p style={{ color: "var(--color-text-secondary)" }}>Loading...</p>
      </div>
    );
  }

  // Show login form if auth is required and user is not authenticated (DDR-028)
  if (isAuthRequired() && !isAuthenticated.value) {
    return (
      <div>
        <header style={{ marginBottom: "2rem" }}>
          <h1>{appTitle}</h1>
        </header>
        <LoginForm />
      </div>
    );
  }

  return (
    <div>
      <header
        style={{
          marginBottom: isCloudStep.value ? "1rem" : "2rem",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
        }}
      >
        <div>
          <h1>{appTitle}</h1>
          <p style={{ color: "var(--color-text-secondary)" }}>
            {stepTitle.value}
          </p>
        </div>
        {isAuthRequired() && (
          <button
            class="outline"
            onClick={() => signOut()}
            style={{ fontSize: "0.8125rem" }}
          >
            Sign Out
          </button>
        )}
      </header>

      {/* Step navigator — cloud mode only (DDR-037) */}
      {isCloudMode && isCloudStep.value && <StepNavigator />}

      {/* Triage flow — local mode */}
      {currentStep.value === "browse" && !isCloudMode && <FileBrowser />}
      {currentStep.value === "confirm-files" && <SelectedFiles />}
      {(currentStep.value === "processing" ||
        currentStep.value === "results") && <TriageView />}

      {/* Selection flow — cloud mode (DDR-029, DDR-030, DDR-031) */}
      {currentStep.value === "upload" && isCloudMode && <MediaUploader />}
      {(currentStep.value === "selecting" ||
        currentStep.value === "review-selection") &&
        isCloudMode && <SelectionView />}
      {(currentStep.value === "enhancing" ||
        currentStep.value === "review-enhanced") &&
        isCloudMode && <EnhancementView />}

      {/* Post grouping (DDR-033) */}
      {currentStep.value === "group-posts" && isCloudMode && <PostGrouper />}

      {/* Download (DDR-034) */}
      {currentStep.value === "publish" && isCloudMode && <DownloadView />}

      {/* Description (DDR-036) */}
      {currentStep.value === "description" && isCloudMode && (
        <DescriptionEditor />
      )}
    </div>
  );
}
