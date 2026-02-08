import { signal, computed } from "@preact/signals";
import { isCloudMode } from "./api/client";
import {
  isAuthenticated,
  isAuthRequired,
  authLoading,
  signOut,
} from "./auth/cognito";
import { FileBrowser } from "./components/FileBrowser";
import { LoginForm } from "./components/LoginForm";
import { MediaUploader } from "./components/MediaUploader";
import { SelectedFiles } from "./components/SelectedFiles";
import { TriageView } from "./components/TriageView";

/** Application steps across both workflows. */
type Step =
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
          marginBottom: "2rem",
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

      {/* Triage flow — local mode */}
      {currentStep.value === "browse" && !isCloudMode && <FileBrowser />}
      {currentStep.value === "confirm-files" && <SelectedFiles />}
      {(currentStep.value === "processing" ||
        currentStep.value === "results") && <TriageView />}

      {/* Selection flow — cloud mode (DDR-029) */}
      {currentStep.value === "upload" && isCloudMode && <MediaUploader />}
      {currentStep.value === "selecting" && isCloudMode && (
        <div class="card" style={{ textAlign: "center", padding: "3rem" }}>
          <p style={{ color: "var(--color-text-secondary)" }}>
            AI Selection — coming in Step 2.
          </p>
          <button
            class="outline"
            onClick={navigateBack}
            style={{ marginTop: "1rem" }}
          >
            Back to Upload
          </button>
        </div>
      )}
    </div>
  );
}
