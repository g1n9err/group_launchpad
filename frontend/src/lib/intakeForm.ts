import type { components } from "./schema";

export type IntakeQuestion = components["schemas"]["IntakeFormQuestion"];
export type EditableQuestion = {
  id: number;
  title: string;
  type: IntakeQuestion["type"];
  required: boolean;
  optionsText: string;
};

let nextQuestionId = 0;

export function editableQuestion(title = ""): EditableQuestion {
  return { id: nextQuestionId++, title, type: "text", required: true, optionsText: "" };
}

export function toIntakeQuestions(questions: EditableQuestion[]): IntakeQuestion[] {
  return questions.map(({ title, type, required, optionsText }) => ({
    title: title.trim(), type, required,
    ...(type === "single_choice" ? { options: optionsText.split("\n").map((option) => option.trim()).filter(Boolean) } : {}),
  }));
}

export const intakeInputClass = "w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-text outline-none focus:ring-2 focus:ring-primary/40";
export const intakeButtonClass = "rounded-md border border-border px-3 py-2 text-sm font-medium text-text hover:bg-surface-muted disabled:opacity-50";
export const intakeActionClass = "rounded-md bg-primary px-4 py-2 text-sm font-semibold text-on-primary hover:bg-primary-hover disabled:opacity-50";
