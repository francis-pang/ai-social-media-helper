import { signal, computed } from "@preact/signals";
import { isCloudMode } from "./api/client";
import { FileBrowser } from "./components/FileBrowser";
import { FileUploader } from "./components/FileUploader";
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
  return (
    <div>
      <header style={{ marginBottom: "2rem" }}>
        <h1>Media Triage</h1>
        <p style={{ color: "var(--color-text-secondary)" }}>
          {stepTitle.value}
        </p>
      </header>

      {currentStep.value === "browse" &&
        (isCloudMode ? <FileUploader /> : <FileBrowser />)}
      {currentStep.value === "confirm-files" && <SelectedFiles />}
      {(currentStep.value === "processing" ||
        currentStep.value === "results") && <TriageView />}
    </div>
  );
}
