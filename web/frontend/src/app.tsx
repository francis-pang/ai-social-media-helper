import { signal, computed } from "@preact/signals";
import { isCloudMode } from "./api/client";
import {
  isAuthenticated,
  isAuthRequired,
  authLoading,
  signOut,
} from "./auth/cognito";
import { FileBrowser } from "./components/FileBrowser";
import { FileUploader } from "./components/FileUploader";
import { LoginForm } from "./components/LoginForm";
import { SelectedFiles } from "./components/SelectedFiles";
import { TriageView } from "./components/TriageView";

/** Application step in the triage workflow. */
type Step = "browse" | "confirm-files" | "processing" | "results";

export const currentStep = signal<Step>("browse");
export const selectedPaths = signal<string[]>([]);
export const triageJobId = signal<string | null>(null);

/** Upload session ID (Phase 2 cloud mode — groups uploaded files in S3). */
export const uploadSessionId = signal<string | null>(null);

const stepTitle = computed(() => {
  switch (currentStep.value) {
    case "browse":
      return isCloudMode ? "Upload Media" : "Select Media";
    case "confirm-files":
      return "Confirm Selection";
    case "processing":
      return "Processing...";
    case "results":
      return "Triage Results";
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
          <h1>Media Triage</h1>
        </header>
        <LoginForm />
      </div>
    );
  }

  return (
    <div>
      <header style={{ marginBottom: "2rem", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <div>
          <h1>Media Triage</h1>
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

      {currentStep.value === "browse" &&
        (isCloudMode ? <FileUploader /> : <FileBrowser />)}
      {currentStep.value === "confirm-files" && <SelectedFiles />}
      {(currentStep.value === "processing" ||
        currentStep.value === "results") && <TriageView />}
    </div>
  );
}
