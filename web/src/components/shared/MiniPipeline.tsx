export type MiniPipelineStage = "done" | "active" | "pending";

export interface MiniPipelineStep {
  label: string;
  stage: MiniPipelineStage;
}

export function MiniPipeline({ steps }: { steps: MiniPipelineStep[] }) {
  return (
    <div class="mini-pipeline">
      {steps.flatMap((step, i) => {
        const els = [
          <div class={`mini-pipeline__step mini-pipeline__step--${step.stage}`} key={`s-${i}`}>
            <div class="mini-pipeline__circle">
              {step.stage === "done" ? "\u2713" : ""}
            </div>
            <div class="mini-pipeline__label">{step.label}</div>
          </div>,
        ];
        if (i < steps.length - 1) {
          const connDone = step.stage === "done";
          els.push(
            <div
              class={`mini-pipeline__connector${connDone ? " mini-pipeline__connector--done" : ""}`}
              key={`c-${i}`}
            />,
          );
        }
        return els;
      })}
    </div>
  );
}
