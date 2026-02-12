/**
 * LandingPage — workflow chooser for cloud mode (DDR-042).
 *
 * Shows two cards: Media Triage and Media Selection.
 * The user picks which workflow to enter, then proceeds to upload.
 */
import { activeWorkflow, navigateToStep } from "../app";
import type { Workflow, Step } from "../app";

interface WorkflowOption {
  id: Workflow & string;
  title: string;
  description: string;
  details: string[];
  startStep: Step;
  accentColor: string;
  accentBg: string;
}

const WORKFLOWS: WorkflowOption[] = [
  {
    id: "triage",
    title: "Media Triage",
    description: "Identify and delete unsaveable media",
    details: [
      "AI detects blurry, dark, and accidental photos/videos",
      "Review flagged items with thumbnail previews",
      "Multi-select deletion with confirmation",
    ],
    startStep: "triage-upload",
    accentColor: "var(--color-danger)",
    accentBg: "rgba(255, 107, 107, 0.1)",
  },
  {
    id: "selection",
    title: "Media Selection",
    description: "Full AI pipeline for Instagram posts",
    details: [
      "AI selects the best photos and videos",
      "Enhance, group into posts, generate captions",
      "Download bundles or publish to Instagram",
    ],
    startStep: "upload",
    accentColor: "var(--color-primary)",
    accentBg: "rgba(108, 140, 255, 0.1)",
  },
];

function selectWorkflow(option: WorkflowOption) {
  activeWorkflow.value = option.id;
  navigateToStep(option.startStep);
}

export function LandingPage() {
  return (
    <div>
      <p
        style={{
          color: "var(--color-text-secondary)",
          fontSize: "0.9375rem",
          marginBottom: "2rem",
          lineHeight: "1.6",
        }}
      >
        Choose a tool to get started. Upload your media files, and AI will
        handle the rest.
      </p>

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fit, minmax(320px, 1fr))",
          gap: "1.5rem",
        }}
      >
        {WORKFLOWS.map((option) => (
          <div
            key={option.id}
            onClick={() => selectWorkflow(option)}
            role="button"
            tabIndex={0}
            onKeyDown={(e: KeyboardEvent) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                selectWorkflow(option);
              }
            }}
            style={{
              background: "var(--color-surface)",
              border: "1px solid var(--color-border)",
              borderRadius: "var(--radius-lg)",
              padding: "2rem",
              cursor: "pointer",
              transition: "all 0.2s ease",
              position: "relative",
              overflow: "hidden",
            }}
            onMouseEnter={(e) => {
              const el = e.currentTarget as HTMLElement;
              el.style.borderColor = option.accentColor;
              el.style.transform = "translateY(-2px)";
              el.style.boxShadow = `0 4px 12px ${option.accentBg}`;
            }}
            onMouseLeave={(e) => {
              const el = e.currentTarget as HTMLElement;
              el.style.borderColor = "var(--color-border)";
              el.style.transform = "none";
              el.style.boxShadow = "none";
            }}
          >
            {/* Accent bar at top */}
            <div
              style={{
                position: "absolute",
                top: 0,
                left: 0,
                right: 0,
                height: "3px",
                background: option.accentColor,
                opacity: 0.6,
              }}
            />

            <h2
              style={{
                fontSize: "1.25rem",
                fontWeight: 600,
                marginBottom: "0.5rem",
                color: option.accentColor,
              }}
            >
              {option.title}
            </h2>

            <p
              style={{
                color: "var(--color-text-secondary)",
                fontSize: "0.9375rem",
                marginBottom: "1.25rem",
              }}
            >
              {option.description}
            </p>

            <ul
              style={{
                listStyle: "none",
                padding: 0,
                margin: 0,
              }}
            >
              {option.details.map((detail, i) => (
                <li
                  key={i}
                  style={{
                    fontSize: "0.8125rem",
                    color: "var(--color-text-secondary)",
                    padding: "0.25rem 0",
                    paddingLeft: "1rem",
                    position: "relative",
                  }}
                >
                  <span
                    style={{
                      position: "absolute",
                      left: 0,
                      color: option.accentColor,
                      opacity: 0.7,
                    }}
                  >
                    -
                  </span>
                  {detail}
                </li>
              ))}
            </ul>

            <div
              style={{
                marginTop: "1.5rem",
                display: "flex",
                justifyContent: "flex-end",
              }}
            >
              <span
                style={{
                  fontSize: "0.8125rem",
                  fontWeight: 500,
                  color: option.accentColor,
                  display: "flex",
                  alignItems: "center",
                  gap: "0.25rem",
                }}
              >
                Get started
                <span aria-hidden="true">→</span>
              </span>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
