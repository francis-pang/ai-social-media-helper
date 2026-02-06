import { signal, computed } from "@preact/signals";
import { FileBrowser } from "./components/FileBrowser";
import { SelectedFiles } from "./components/SelectedFiles";
import { TriageView } from "./components/TriageView";

/** Application step in the triage workflow. */
type Step = "browse" | "confirm-files" | "processing" | "results";

export const currentStep = signal<Step>("browse");
export const selectedPaths = signal<string[]>([]);
export const triageJobId = signal<string | null>(null);

const stepTitle = computed(() => {
  switch (currentStep.value) {
    case "browse":
      return "Select Media";
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

      {currentStep.value === "browse" && <FileBrowser />}
      {currentStep.value === "confirm-files" && <SelectedFiles />}
      {(currentStep.value === "processing" ||
        currentStep.value === "results") && <TriageView />}
    </div>
  );
}
