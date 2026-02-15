/**
 * URL-based routing for the SPA (DDR-056).
 *
 * Uses history.pushState / popstate to keep the browser URL in sync with the
 * current application step.  The Go server and CloudFront already serve
 * index.html for unknown paths, so no server-side routing changes are needed.
 */
import type { Step, Workflow } from "./app";
import {
  currentStep,
  activeWorkflow,
  uploadSessionId,
  stepHistory,
} from "./app";
import { isCloudMode } from "./api/client";

// ---------------------------------------------------------------------------
// Path mappings
// ---------------------------------------------------------------------------

const STEP_TO_PATH: Partial<Record<Step, string>> = {
  landing: "/",
  "triage-upload": "/triage/upload",
  processing: "/triage/processing",
  results: "/triage/results",
  upload: "/select/upload",
  selecting: "/select/ai",
  "review-selection": "/select/review",
  enhancing: "/select/enhance",
  "review-enhanced": "/select/enhanced",
  "group-posts": "/select/groups",
  publish: "/select/download",
  description: "/select/description",
  "instagram-publish": "/select/publish",
};

/** Reverse map: path → step. */
const PATH_TO_STEP: Record<string, Step> = {};
for (const [step, path] of Object.entries(STEP_TO_PATH)) {
  PATH_TO_STEP[path!] = step as Step;
}

/** Steps that carry the session ID in the query string. */
const SESSION_STEPS = new Set<Step>([
  "triage-upload",
  "processing",
  "results",
  "selecting",
  "review-selection",
  "enhancing",
  "review-enhanced",
  "group-posts",
  "publish",
  "description",
  "instagram-publish",
]);

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Derive the active workflow from a step value. */
function workflowForStep(step: Step): Workflow {
  if (step === "landing") return null;
  if (
    step === "triage-upload" ||
    step === "processing" ||
    step === "results" ||
    step === "browse" ||
    step === "confirm-files"
  ) {
    return "triage";
  }
  return "selection";
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/**
 * Push the browser URL to match the given step and optional session ID.
 * No-ops in local mode and for local-only steps (browse, confirm-files).
 */
export function syncUrlToStep(step: Step, sessionId?: string | null) {
  if (!isCloudMode) return;

  const path = STEP_TO_PATH[step];
  if (path === undefined) return; // local-only step — no URL update

  let url = path;
  if (sessionId && SESSION_STEPS.has(step)) {
    url += `?session=${encodeURIComponent(sessionId)}`;
  }

  // Avoid pushing a duplicate entry
  if (window.location.pathname + window.location.search !== url) {
    history.pushState({ step }, "", url);
  }
}

/**
 * Parse the current URL into application state.
 * Returns null if the path doesn't map to a known step.
 */
export function parseUrlToState(): {
  step: Step;
  workflow: Workflow;
  sessionId: string | null;
} | null {
  const path = window.location.pathname;
  const params = new URLSearchParams(window.location.search);

  const step = PATH_TO_STEP[path];
  if (!step) return null;

  return {
    step,
    workflow: workflowForStep(step),
    sessionId: params.get("session"),
  };
}

/**
 * Initialise the router.
 *
 * - Parses the current URL and restores state (step, workflow, session ID).
 * - Sets up a `popstate` listener for browser back/forward navigation.
 *
 * Call once before the initial render.
 */
export function initRouter() {
  if (!isCloudMode) return;

  // Restore state from URL on initial load
  const state = parseUrlToState();
  if (state && state.step !== "landing") {
    currentStep.value = state.step;
    activeWorkflow.value = state.workflow;
    if (state.sessionId) {
      uploadSessionId.value = state.sessionId;
    }
  }

  // Browser back/forward
  window.addEventListener("popstate", () => {
    const parsed = parseUrlToState();
    if (parsed) {
      currentStep.value = parsed.step;
      activeWorkflow.value = parsed.workflow;
      if (parsed.sessionId) {
        uploadSessionId.value = parsed.sessionId;
      }
      // Clear step history — browser navigation replaces in-app history
      stepHistory.value = [];
    }
  });
}
