import { TranslateDraft } from "./TranslateDraft";
import { useSettings } from "../i18n";
import { AppSelect } from "./AppSelect";
import { editableQuestion, intakeButtonClass, intakeInputClass, type EditableQuestion, type IntakeQuestion } from "../lib/intakeForm";

export function IntakeQuestions({ questions, setQuestions, disabled }: {
  questions: EditableQuestion[];
  setQuestions: (items: EditableQuestion[]) => void;
  disabled: boolean;
}) {
  const { t } = useSettings();
  const update = (id: number, values: Partial<EditableQuestion>) =>
    setQuestions(questions.map((question) => question.id === id ? { ...question, ...values } : question));
  const move = (from: number, to: number) => {
    const next = [...questions];
    const [question] = next.splice(from, 1);
    next.splice(to, 0, question);
    setQuestions(next);
  };

  return (
    <fieldset disabled={disabled} className="space-y-4">
      <legend className="mb-3 text-sm font-semibold text-text">{t("intake.questions")}</legend>
      {questions.map((question, index) => (
        <div key={question.id} className="space-y-3 rounded-lg border border-border bg-surface px-4 py-3">
          <div className="grid gap-3 md:grid-cols-[1fr_12rem]">
            <label className="text-sm font-medium text-text">
              {t("intake.question")}
              <input className={`mt-1 ${intakeInputClass}`} required maxLength={300} value={question.title}
                onChange={(event) => update(question.id, { title: event.target.value })} />
            </label>
            <label className="text-sm font-medium text-text">
              {t("intake.type")}
              <AppSelect className={`mt-1 ${intakeInputClass}`} ariaLabel={t("intake.type")} value={question.type}
                options={[
                  { value: "text", label: t("intake.text") },
                  { value: "paragraph", label: t("intake.paragraph") },
                  { value: "single_choice", label: t("intake.single_choice") },
                ]}
                onChange={(value) => update(question.id, { type: value as IntakeQuestion["type"] })} />
            </label>
          </div>
          <TranslateDraft value={question.title} maxLength={300}
            onApply={(title) => update(question.id, { title })} />
          {question.type === "single_choice" && (
            <div>
              <label className="block text-sm font-medium text-text">
                {t("intake.options")}
                <textarea className={`mt-1 ${intakeInputClass}`} rows={3} required value={question.optionsText}
                  onChange={(event) => update(question.id, { optionsText: event.target.value })} />
              </label>
              <TranslateDraft value={question.optionsText}
                onApply={(optionsText) => update(question.id, { optionsText })} />
            </div>
          )}
          <div className="flex flex-wrap items-center justify-between gap-2">
            <label className="flex items-center gap-2 text-sm text-text">
              <input type="checkbox" checked={question.required} onChange={(event) => update(question.id, { required: event.target.checked })} />
              {t("intake.required")}
            </label>
            <div className="flex flex-wrap gap-2">
              <button className={intakeButtonClass} type="button" disabled={index === 0} onClick={() => move(index, index - 1)}>{t("intake.moveUp")}</button>
              <button className={intakeButtonClass} type="button" disabled={index === questions.length - 1} onClick={() => move(index, index + 1)}>{t("intake.moveDown")}</button>
              <button className={intakeButtonClass} type="button" disabled={questions.length < 2}
                onClick={() => setQuestions(questions.filter((item) => item.id !== question.id))}>{t("intake.remove")}</button>
            </div>
          </div>
        </div>
      ))}
      <button className={intakeButtonClass} type="button" disabled={questions.length >= 50}
        onClick={() => setQuestions([...questions, editableQuestion()])}>{t("intake.add")}</button>
    </fieldset>
  );
}
