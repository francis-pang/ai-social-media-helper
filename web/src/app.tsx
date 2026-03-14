import { signal, computed } from "@preact/signals";
import { isCloudMode, invalidateSession } from "./api/client";

const ECONOMY_MODE_KEY = "economy_mode";

function loadEconomyMode(): boolean {
  try {
    const stored = localStorage.getItem(ECONOMY_MODE_KEY);
    if (stored === null) return true; // default ON
    return stored === "true";
  } catch {
    return true;
  }
}

function saveEconomyMode(value: boolean) {
  try {
    localStorage.setItem(ECONOMY_MODE_KEY, String(value));
  } catch {
    // ignore
  }
}
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
import { LandingPage } from "./components/LandingPage";
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
import {
  PublishView,
  resetPublishState,
} from "./components/PublishView";
import { MediaPlayer } from "./components/MediaPlayer";
import { TriageView } from "./components/TriageView";
import { FBPrepView, resetFBPrepState } from "./components/FBPrepView";
import { FBPrepUploader, resetFBPrepUploaderState } from "./components/FBPrepUploader";
import { FileUploader, resetFileUploaderState } from "./components/FileUploader";
import { syncUrlToStep } from "./router";

/** Application steps across both workflows. */
export type Step =
  // Landing page (cloud mode — DDR-042)
  | "landing"
  // Triage flow (local mode or cloud triage)
  | "browse"
  | "confirm-files"
  | "processing"
  | "results"
  // Cloud triage flow (DDR-042) — upload then triage
  | "triage-upload"
  // Selection flow (cloud mode — DDR-029)
  | "upload"
  | "selecting"
  | "review-selection"
  | "enhancing"
  | "review-enhanced"
  | "group-posts"
  | "publish"
  | "description"
  | "instagram-publish"
  | "fb-prep-upload"
  | "fb-prep";

/**
 * Active workflow — which feature the user chose from the landing page (DDR-042).
 * - "triage": Media triage (identify unsaveable media)
 * - "selection": Media selection pipeline (select, enhance, group, publish)
 * - null: Not yet chosen (on landing page)
 */
export type Workflow = "triage" | "selection" | "fb-prep" | null;
export const activeWorkflow = signal<Workflow>(isCloudMode ? null : "triage");

export const currentStep = signal<Step>(isCloudMode ? "landing" : "browse");
export const selectedPaths = signal<string[]>([]);
export const triageJobId = signal<string | null>(null);

/** Upload session ID — groups uploaded files in S3 under a common prefix. */
export const uploadSessionId = signal<string | null>(null);

/** DDR-074: File handles retained from showOpenFilePicker() for local deletion after triage. */
export const fileHandles = signal<Map<string, FileSystemFileHandle>>(new Map());

/** Trip/event context for AI selection (e.g., "3-day trip to Tokyo, Oct 2025"). */
export const tripContext = signal<string>("");

/** Economy mode: 50% cost savings, ~10 min processing. Persisted in localStorage. */
export const economyMode = signal<boolean>(loadEconomyMode());

/** Set economy mode and persist to localStorage. */
export function setEconomyMode(value: boolean) {
  economyMode.value = value;
  saveEconomyMode(value);
}

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
  { steps: ["instagram-publish"], invalidationKey: "publish" },
];

/** Get the navigator index (0-based) for a Step value. */
function stepToNavIndex(step: Step): number {
  return CLOUD_STEP_ORDER.findIndex((entry) =>
    (entry.steps as Step[]).includes(step),
  );
}

/**
 * Set the current step and sync the browser URL (DDR-056).
 * Use this instead of `currentStep.value = ...` for direct step assignments.
 */
export function setStep(step: Step) {
  currentStep.value = step;
  syncUrlToStep(step, uploadSessionId.value);
}

/** Navigate to a new step, pushing the current step onto the history stack. */
export function navigateToStep(step: Step) {
  stepHistory.value = [...stepHistory.value, currentStep.value];
  currentStep.value = step;
  syncUrlToStep(step, uploadSessionId.value);
}

/** Reset to call when leaving a step via Back (steps that use navigateBack directly). */
const RESET_ON_BACK: Partial<Record<Step, () => void>> = {
  "fb-prep": resetFBPrepState,
  "group-posts": resetPostGrouperState,
  "publish": resetDownloadState,
  "description": resetDescriptionState,
  "instagram-publish": resetPublishState,
};

/** Navigate back to the previous step (pops from history stack). */
export function navigateBack() {
  const history = stepHistory.value;
  if (history.length === 0) return;
  const prev = history.at(-1)!;
  const resetFn = RESET_ON_BACK[currentStep.value];
  if (resetFn) resetFn();
  stepHistory.value = history.slice(0, -1);
  currentStep.value = prev;
  syncUrlToStep(prev, uploadSessionId.value);
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
  fromStep: "selection" | "enhancement" | "grouping" | "download" | "description" | "publish",
) {
  const sessionId = uploadSessionId.value;
  if (!sessionId) return;

  // Fire backend invalidation (best-effort — don't block on it)
  invalidateSession({ sessionId, fromStep }).catch((err) => {
    // eslint-disable-next-line no-console
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
    publish: resetPublishState,
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

/** Application title — dynamic based on active workflow (DDR-042). */
const appTitle = computed(() => {
  if (!isCloudMode) return "Media Triage";
  if (activeWorkflow.value === "triage") return "Media Triage";
  if (activeWorkflow.value === "selection") return "Media Tools";
  if (activeWorkflow.value === "fb-prep") return "Facebook Prep";
  return "Media Tools";
});

const stepTitle = computed(() => {
  switch (currentStep.value) {
    // Landing page
    case "landing":
      return "Choose a Tool";
    // Triage flow
    case "browse":
      return "Select Media";
    case "confirm-files":
      return "Confirm Selection";
    case "triage-upload":
      return "Upload & Process Media";
    case "processing":
      return "AI Analysis";
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
    case "instagram-publish":
      return "Publish to Instagram";
    case "fb-prep-upload":
      return "Upload Media";
    case "fb-prep":
      return "Facebook Prep";
  }
});

/**
 * Navigate back to the landing page, resetting workflow state (DDR-042).
 * Called when user clicks the app title or "Back to Home" button.
 */
export function navigateToLanding() {
  activeWorkflow.value = null;
  currentStep.value = "landing";
  stepHistory.value = [];
  selectedPaths.value = [];
  triageJobId.value = null;
  uploadSessionId.value = null;
  fileHandles.value = new Map();
  tripContext.value = "";
  // Reset all downstream component state
  resetSelectionState();
  resetEnhancementState();
  resetPostGrouperState();
  resetDownloadState();
  resetDescriptionState();
  resetPublishState();
  resetFileUploaderState();
  resetFBPrepUploaderState();
  resetFBPrepState();
  syncUrlToStep("landing");
}

/** Whether the current step is part of the cloud selection flow (shows step navigator). */
const isSelectionStep = computed(
  () => activeWorkflow.value === "selection" && stepToNavIndex(currentStep.value) >= 0,
);

/** Whether we're on the landing page. */
const isOnLanding = computed(() => currentStep.value === "landing");

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
        <nav
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            background: "var(--color-surface)",
            borderBottom: "1px solid var(--color-border)",
            padding: "0.75rem 1.5rem",
            marginBottom: "2rem",
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
            <span
              style={{
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                width: "28px",
                height: "28px",
                borderRadius: "50%",
                background: "var(--color-primary)",
                color: "#fff",
                fontSize: "14px",
                lineHeight: 1,
              }}
            >
              ⬡
            </span>
            <span style={{ fontWeight: 700, color: "var(--color-text)", fontSize: "1rem" }}>
              {appTitle.value}
            </span>
          </div>
          <div />
          <div style={{ display: "flex", gap: "0.75rem", alignItems: "center" }}>
            <label
              class="economy-mode-toggle"
              style={{
                display: "flex",
                alignItems: "center",
                gap: "0.5rem",
                cursor: "pointer",
                userSelect: "none",
              }}
            >
              <span
                style={{
                  display: "flex",
                  flexDirection: "column",
                  alignItems: "flex-end",
                }}
              >
                <span class="text-sm" style={{ fontWeight: 500, color: "var(--color-text)" }}>
                  Economy Mode
                </span>
                <span class="text-xs" style={{ color: "var(--color-text-secondary)" }}>
                  50% cost savings, ~10 min processing
                </span>
              </span>
              <span
                class="toggle-switch"
                role="switch"
                aria-checked={economyMode.value}
                aria-label="Economy Mode"
                onClick={() => setEconomyMode(!economyMode.value)}
                style={{
                  flexShrink: 0,
                  width: "2.5rem",
                  height: "1.375rem",
                  borderRadius: "999px",
                  background: economyMode.value ? "var(--color-primary)" : "var(--color-border)",
                  position: "relative",
                  transition: "background 0.15s",
                }}
              >
                <span
                  style={{
                    position: "absolute",
                    top: "0.125rem",
                    left: economyMode.value ? "1.25rem" : "0.125rem",
                    width: "1.125rem",
                    height: "1.125rem",
                    borderRadius: "50%",
                    background: "white",
                    boxShadow: "0 1px 2px rgba(0,0,0,0.2)",
                    transition: "left 0.15s",
                  }}
                />
              </span>
            </label>
          </div>
        </nav>
        <LoginForm />
      </div>
    );
  }

  return (
    <div>
      <nav
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          background: "var(--color-surface)",
          borderBottom: "1px solid var(--color-border)",
          padding: "0.75rem 1.5rem",
          marginBottom: isSelectionStep.value ? "1rem" : "1.5rem",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
          <span
            style={{
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              width: "28px",
              height: "28px",
              borderRadius: "50%",
              background: "var(--color-primary)",
              color: "#fff",
              fontSize: "14px",
              lineHeight: 1,
            }}
          >
            ⬡
          </span>
          <span
            style={{
              fontWeight: 700,
              color: "var(--color-text)",
              fontSize: "1rem",
              cursor: isCloudMode && !isOnLanding.value ? "pointer" : "default",
            }}
            onClick={isCloudMode && !isOnLanding.value ? navigateToLanding : undefined}
          >
            {appTitle.value}
          </span>
          {activeWorkflow.value === "triage" && (
            <span style={{ color: "var(--color-text-secondary)", fontSize: "0.875rem" }}>
              › {stepTitle.value}
            </span>
          )}
        </div>
        <div class="navbar-progress" />
        <div style={{ display: "flex", gap: "0.75rem", alignItems: "center" }}>
          {/* Economy Mode toggle — 50% cost savings, ~10 min processing */}
          <label
            class="economy-mode-toggle"
            style={{
              display: "flex",
              alignItems: "center",
              gap: "0.5rem",
              cursor: "pointer",
              userSelect: "none",
            }}
          >
            <span
              style={{
                display: "flex",
                flexDirection: "column",
                alignItems: "flex-end",
              }}
            >
              <span class="text-sm" style={{ fontWeight: 500, color: "var(--color-text)" }}>
                Economy Mode
              </span>
              <span class="text-xs" style={{ color: "var(--color-text-secondary)" }}>
                50% cost savings, ~10 min processing
              </span>
            </span>
            <span
              class="toggle-switch"
              role="switch"
              aria-checked={economyMode.value}
              aria-label="Economy Mode"
              onClick={() => setEconomyMode(!economyMode.value)}
              style={{
                flexShrink: 0,
                width: "2.5rem",
                height: "1.375rem",
                borderRadius: "999px",
                background: economyMode.value ? "var(--color-primary)" : "var(--color-border)",
                position: "relative",
                transition: "background 0.15s",
              }}
            >
              <span
                style={{
                  position: "absolute",
                  top: "0.125rem",
                  left: economyMode.value ? "1.25rem" : "0.125rem",
                  width: "1.125rem",
                  height: "1.125rem",
                  borderRadius: "50%",
                  background: "white",
                  boxShadow: "0 1px 2px rgba(0,0,0,0.2)",
                  transition: "left 0.15s",
                }}
              />
            </span>
          </label>
          {isCloudMode && !isOnLanding.value && (
            <button
              class="outline text-sm"
              onClick={navigateToLanding}
              style={{
                color: "var(--color-primary)",
                borderColor: "var(--color-primary)",
              }}
            >
              Home
            </button>
          )}
          {isAuthRequired() && (
            <button
              class="outline text-sm"
              onClick={() => signOut()}
              style={{
                color: "var(--color-primary)",
                borderColor: "var(--color-primary)",
              }}
            >
              Sign Out
            </button>
          )}
        </div>
      </nav>

      {/* Landing page — cloud mode workflow chooser (DDR-042) */}
      {currentStep.value === "landing" && isCloudMode && <LandingPage />}

      {/* Step navigator — selection workflow only (DDR-037) */}
      {isCloudMode && isSelectionStep.value && <StepNavigator />}

      {/* Triage flow — local mode */}
      {currentStep.value === "browse" && !isCloudMode && (
        <div class="layout-sidebar"><FileBrowser /></div>
      )}
      {currentStep.value === "confirm-files" && (
        <div class="layout-sidebar"><SelectedFiles /></div>
      )}

      {/* Triage flow — cloud triage upload (DDR-042) */}
      {currentStep.value === "triage-upload" && isCloudMode && (
        <FileUploader />
      )}

      {/* Triage results — both local and cloud triage */}
      {(currentStep.value === "processing" ||
        currentStep.value === "results") && (
        <div class="layout-sidebar"><TriageView /></div>
      )}

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

      {/* Instagram Publish (DDR-040) */}
      {currentStep.value === "instagram-publish" && isCloudMode && (
        <PublishView />
      )}

      {/* Facebook Prep upload step (DDR-080) */}
      {currentStep.value === "fb-prep-upload" && isCloudMode && <FBPrepUploader />}

      {/* Facebook Prep results (FB Prep workflow) */}
      {currentStep.value === "fb-prep" && isCloudMode && <FBPrepView />}

      {/* Global overlay media player (DDR-038) */}
      <MediaPlayer />

      <footer
        style={{
          display: "flex",
          justifyContent: "space-between",
          padding: "1rem 0",
          borderTop: "1px solid var(--color-border)",
          color: "var(--color-text-secondary)",
          fontSize: "0.75rem",
          marginTop: "2rem",
        }}
      >
        <span>v0.1.0</span>
        <span>
          <span style={{ color: "var(--color-success)" }}>●</span> System Operational
        </span>
      </footer>
    </div>
  );
}
