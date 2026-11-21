import { useCallback, useEffect, useState } from "react";
import { Card, Chip, PageTitle } from "../components/ui";
import { IntakeQuestions } from "../components/IntakeQuestions";
import { editableQuestion, intakeActionClass, intakeButtonClass, intakeInputClass, toIntakeQuestions, type EditableQuestion } from "../lib/intakeForm";
import { useSettings } from "../i18n";
import type { TranslationKey } from "../i18n/en";
import { api } from "../lib/api";
import { useIntakeGroups } from "../lib/queries";
import type { components } from "../lib/schema";
import { fireLock, getAuthHeader, useAuthUser } from "../lib/auth";

type FormState = components["schemas"]["IntakeFormState"];
type Delivery = components["schemas"]["IntakeDelivery"];
type Summary = components["schemas"]["IntakeResponseSummary"];
type Action = "draft" | "publish" | "distribute" | "import" | "refresh";

export function IntakeFormsPage() {
  const { t } = useSettings();
  const user = useAuthUser();
  const authorization = user ? getAuthHeader().Authorization ?? "" : "";
  const [groupId, setGroupId] = useState("");
  const groups = useIntakeGroups(Boolean(authorization));
  const onUnauthorized = useCallback(() => fireLock(), []);

  return (
    <div className="space-y-5">
      <PageTitle title={t("nav.intakeForms")} />
      <Card className="p-5">
        {authorization ? (
          <div className="flex flex-wrap items-end gap-3">
            <label className="w-72 max-w-full text-sm font-medium text-text">
              {t("intake.group")}
              <select className={`mt-1 ${intakeInputClass}`} value={groupId} onChange={(event) => setGroupId(event.target.value)}>
                <option value="">{t("intake.choose")}</option>
                {groups.data?.map((group) => (
                  <option key={group.id} value={group.id}>{group.name}</option>
                ))}
              </select>
            </label>
            {groups.isError && <p role="alert" className="text-sm text-danger-text">{t("intake.loadFailed")}</p>}
          </div>
        ) : <a href="#/login" className="app-action">{t("auth.signIn")}</a>}
      </Card>
      {authorization && groupId && <IntakeWorkflow key={groupId} groupId={groupId} authorization={authorization} onUnauthorized={onUnauthorized} />}
    </div>
  );
}

function IntakeWorkflow({ groupId, authorization, onUnauthorized }: {
  groupId: string; authorization: string; onUnauthorized: () => void;
}) {
  const { t } = useSettings();
  const [questions, setQuestions] = useState(() => [editableQuestion(t("intake.fullName"))]);
  const [extraQuestions, setExtraQuestions] = useState<EditableQuestion[]>([]);
  const [form, setForm] = useState<FormState | null>();
  const [deliveries, setDeliveries] = useState<Delivery[]>([]);
  const [summary, setSummary] = useState<Summary>();
  const [busy, setBusy] = useState<Action>();
  const [confirm, setConfirm] = useState<"publish" | "distribute">();
  const [error, setError] = useState<TranslationKey>();
  const [notice, setNotice] = useState<TranslationKey>();
  const [extraError, setExtraError] = useState<TranslationKey>();
  const [reload, setReload] = useState(0);

  const options = { headers: { Authorization: authorization }, params: { path: { groupId } } };

  useEffect(() => {
    const controller = new AbortController();
    const request = { headers: { Authorization: authorization }, params: { path: { groupId } }, signal: controller.signal };
    async function load() {
      try {
        const result = await api.GET("/api/groups/{groupId}/forms", request);
        if (controller.signal.aborted) return;
        if (result.response.status === 401) { onUnauthorized(); return; }
        if (result.response.status === 404) {
          setForm(null);
          setDeliveries([]);
        } else if (result.data) {
          setForm(result.data);
          if (result.data.responderUrl) {
            const status = await api.GET("/api/groups/{groupId}/forms/deliveries", request);
            if (controller.signal.aborted) return;
            if (status.response.status === 401) { onUnauthorized(); return; }
            if (!status.data) { setError("intake.loadFailed"); return; }
            setDeliveries(status.data.deliveries);
          }
        } else {
          setError("intake.loadFailed");
        }
      } catch {
        if (!controller.signal.aborted) setError("intake.loadFailed");
      }
    }
    void load();
    return () => controller.abort();
  }, [groupId, authorization, reload, onUnauthorized]);

  // ── Draft question editor ────────────────────────────────────────────────
  // Load the form's current custom questions (everything after the mandatory
  // Full name / Email address pair) into the editor whenever a draft form
  // loads, so it can be freely edited: change, remove, add.
  useEffect(() => {
    if (!form?.questions) return;
    setExtraQuestions(form.questions.slice(2).map((question) => ({
      id: nextEditableId(),
      title: question.title,
      type: question.type,
      required: question.required,
      optionsText: (question.options ?? []).join("\n"),
    })));
  }, [form?.questions]);

  async function act(action: Action) {
    if (busy) return;
    setBusy(action);
    setConfirm(undefined);
    setError(undefined);
    setNotice(undefined);
    try {
      if (action === "refresh") {
        setReload((version) => version + 1);
        return;
      }
      const result = action === "draft"
        ? await api.POST("/api/groups/{groupId}/forms/provision", { ...options, body: { questions: form?.questions ?? toIntakeQuestions(questions) } })
        : action === "publish"
          ? await api.POST("/api/groups/{groupId}/forms/publish", options)
          : action === "distribute"
            ? await api.POST("/api/groups/{groupId}/forms/distribute", options)
            : await api.POST("/api/groups/{groupId}/forms/responses/import", options);

      if (result.response.status === 401) { onUnauthorized(); return; }
      if (!result.data) {
        setError(result.response.status === 422 ? "intake.invalidQuestions"
          : result.response.status === 409 ? "intake.conflict" : "intake.actionFailed");
        return;
      }
      if ("saved" in result.data) setSummary(result.data);
      if ("deliveries" in result.data) setDeliveries(result.data.deliveries);
      setNotice(action === "draft" ? "intake.drafted" : action === "publish" ? "intake.published"
        : action === "distribute" ? "intake.queued" : "intake.imported");
      setReload((version) => version + 1);
    } catch {
      setError("intake.actionFailed");
    } finally {
      setBusy(undefined);
    }
  }

  // Edit the questions of an existing DRAFT form: the request carries the
  // FULL intended custom set (the server pins the mandatory Full name /
  // Email address pair and rebuilds the draft). Published forms are locked.
  async function saveQuestions(event: React.FormEvent) {
    event.preventDefault();
    if (busy || !form?.questions) return;
    setExtraError(undefined);
    setNotice(undefined);
    const customs = toIntakeQuestions(extraQuestions).filter((question) => question.title);
    setBusy("draft");
    try {
      const result = await api.POST("/api/groups/{groupId}/forms/provision", {
        ...options, body: { questions: [
          { title: "Full name", type: "text", required: true },
          { title: "Email address", type: "text", required: true },
          ...customs,
        ] },
      });
      if (result.response.status === 401) { onUnauthorized(); return; }
      if (!result.data) {
        setExtraError(result.response.status === 422 ? "intake.invalidQuestions"
          : result.response.status === 409 ? "intake.conflict" : "intake.actionFailed");
        return;
      }
      setNotice("intake.questionsUpdated");
      setReload((version) => version + 1);
    } catch {
      setExtraError("intake.actionFailed");
    } finally {
      setBusy(undefined);
    }
  }

  const ready = form?.status === "created" || form?.status === "distributed";
  const published = Boolean(form?.responderUrl);
  const stateUnknown = form === undefined;
  const questionTypeLabels = { text: "intake.text", paragraph: "intake.paragraph", single_choice: "intake.single_choice" } as const;

  return (
    <>
      <Card className="space-y-4 p-5">
        {stateUnknown && (error
          ? <button type="button" className={intakeButtonClass} onClick={() => void act("refresh")}>{t("audit.refresh")}</button>
          : <p role="status" className="text-sm text-muted">{t("intake.loading")}</p>)}
        {form === null && (
          <form className="space-y-4" onSubmit={(event) => { event.preventDefault(); void act("draft"); }}>
            <p className="text-sm text-muted">{t("intake.verifiedHint")}</p>
            <IntakeQuestions questions={questions} setQuestions={setQuestions} disabled={Boolean(busy)} />
            <button type="submit" className={intakeActionClass} disabled={Boolean(busy)}>{t("intake.create")}</button>
          </form>
        )}
        {form && (
          <>
            <div className="flex flex-wrap items-center gap-3">
              <Chip tone={published ? "success" : "info"}>{t(published ? "intake.live" : ready ? "intake.draft" : "intake.pending")}</Chip>
              {/* Links render only for https URLs — any other scheme (e.g. a
                  compromised API response) is shown as plain text, never
                  navigated. */}
              {form.editUrl?.startsWith("https://") && <a className="text-sm font-medium text-primary underline" href={form.editUrl} target="_blank" rel="noreferrer">{t("intake.preview")}</a>}
              {form.responderUrl?.startsWith("https://") && <a className="text-sm font-medium text-primary underline" href={form.responderUrl} target="_blank" rel="noreferrer">{t("intake.responder")}</a>}
            </div>
            <ol className="list-decimal space-y-2 pl-5 text-sm">
              {form.questions.map((question, index) => (
                <li key={`${index}/${question.title}`}>
                  <span className="font-medium text-text">{question.title}</span>{" "}
                  <span className="text-muted">· {t(questionTypeLabels[question.type])}{question.required && ` · ${t("intake.required")}`}</span>
                  {!!question.options?.length && <p className="text-muted">{question.options.join(" · ")}</p>}
                </li>
              ))}
            </ol>
            {/* Full question editor for a draft (not yet published) form. */}
            {ready && !published && (
              <form className="space-y-3 border-t border-border pt-4" onSubmit={saveQuestions}>
                <div>
                  <h3 className="text-sm font-semibold text-text">{t("intake.editQuestions")}</h3>
                  <p className="mt-1 text-xs text-muted">{t("intake.editQuestionsHint")}</p>
                </div>
                <IntakeQuestions questions={extraQuestions} setQuestions={setExtraQuestions} disabled={Boolean(busy)} />
                <div className="flex flex-wrap items-center gap-3">
                  <button type="submit" className={intakeActionClass} disabled={Boolean(busy)}>{t("intake.editQuestionsAction")}</button>
                  {extraError && <p role="alert" className="text-sm text-danger-text">{t(extraError)}</p>}
                </div>
              </form>
            )}
            <div className="flex flex-wrap gap-2">
              {!ready && form.status === "pending" && (
                <button type="button" className={intakeActionClass} disabled={Boolean(busy)} onClick={() => void act("draft")}>{t("intake.resumeDraft")}</button>
              )}
              {ready && (!published || !form.verifiedEmail) && (
                <button type="button" className={intakeActionClass} disabled={Boolean(busy)} onClick={() => setConfirm("publish")}>{t("intake.publish")}</button>
              )}
              {ready && published && form.verifiedEmail && (
                <>
                  <button type="button" className={intakeActionClass} disabled={Boolean(busy)} onClick={() => setConfirm("distribute")}>{t("intake.send")}</button>
                  <button type="button" className={intakeButtonClass} disabled={Boolean(busy)} onClick={() => void act("import")}>{t("intake.import")}</button>
                </>
              )}
              <button type="button" className={intakeButtonClass} disabled={Boolean(busy)} onClick={() => void act("refresh")}>{t("audit.refresh")}</button>
            </div>
          </>
        )}
        {confirm && (
          <div className="space-y-3 rounded-lg border border-border bg-surface-muted p-4">
            <p className="text-sm text-text">{t(confirm === "publish" ? "intake.confirmPublish" : "intake.confirmSend")}</p>
            <div className="flex flex-wrap gap-2">
              <button type="button" className={intakeActionClass} onClick={() => void act(confirm)}>{t(confirm === "publish" ? "intake.confirmPublishAction" : "intake.confirmSendAction")}</button>
              <button type="button" className={intakeButtonClass} onClick={() => setConfirm(undefined)}>{t("intake.cancel")}</button>
            </div>
          </div>
        )}
        {busy && <p role="status" className="text-sm text-muted">{t("intake.working")}</p>}
        {error && <p role="alert" className="text-sm text-danger-text">{t(error)}</p>}
        {notice && <p role="status" className="text-sm text-success-text">{t(notice)}</p>}
        {summary && <p role="status" className="text-sm text-text">{t("intake.importSummary", summary)}</p>}
      </Card>

      {!!deliveries.length && (
        <Card className="space-y-3 p-5">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-sm font-semibold text-text">{t("intake.deliveries")}</h2>
            <button type="button" className={intakeButtonClass} onClick={() => void act("refresh")}>{t("audit.refresh")}</button>
          </div>
          <ul className="divide-y divide-border">
            {deliveries.map((item) => (
              <li className="flex flex-wrap items-center justify-between gap-2 py-2 text-sm" key={item.id}>
                <span className="break-all text-text">{item.recipient}</span>
                <Chip tone={item.status === "sent" ? "success" : item.status === "failed" || item.status === "unknown" ? "warn" : "info"}>
                  {t(emailStatusLabels[item.status])}
                </Chip>
                {item.error && <p className="w-full text-xs text-muted">{item.error}</p>}
              </li>
            ))}
          </ul>
        </Card>
      )}
    </>
  );
}

let extraQuestionId = 0;
function nextEditableId(): number {
  return ++extraQuestionId;
}

const emailStatusLabels = {
  pending: "audit.emailPending", sending: "audit.emailPending", sent: "audit.emailSent",
  failed: "audit.emailFailed", unknown: "audit.emailUnknown",
} as const;
