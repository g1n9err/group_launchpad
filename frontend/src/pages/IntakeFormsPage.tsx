import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Card, Chip, PageTitle } from "../components/ui";
import { IntakeQuestions } from "../components/IntakeQuestions";
import { editableQuestion, intakeActionClass, intakeButtonClass, intakeInputClass, toIntakeQuestions } from "../lib/intakeForm";
import { useSettings } from "../i18n";
import type { TranslationKey } from "../i18n/en";
import { api } from "../lib/api";
import { useIntakeGroups } from "../lib/queries";
import type { components } from "../lib/schema";

type FormState = components["schemas"]["IntakeFormState"];
type Delivery = components["schemas"]["IntakeDelivery"];
type Summary = components["schemas"]["IntakeResponseSummary"];
type Action = "draft" | "publish" | "distribute" | "import" | "refresh";

export function IntakeFormsPage() {
  const { t } = useSettings();
  const [password, setPassword] = useState("");
  const [authorization, setAuthorization] = useState("");
  const [groupId, setGroupId] = useState("");
  const [invalid, setInvalid] = useState(false);
  const groups = useIntakeGroups(Boolean(authorization));

  function unlock(event: FormEvent) {
    event.preventDefault();
    const bytes = new TextEncoder().encode("coordinator:" + password);
    setAuthorization("Basic " + btoa(String.fromCharCode(...bytes)));
    setPassword("");
    setInvalid(false);
  }

  const lock = useCallback((rejected = false) => {
    setAuthorization("");
    setGroupId("");
    setInvalid(rejected);
  }, []);
  const onUnauthorized = useCallback(() => lock(true), [lock]);

  return (
    <div className="space-y-5">
      <PageTitle title={t("nav.intakeForms")} />
      <Card className="p-5">
        {!authorization ? (
          <form className="flex flex-wrap items-end gap-3" onSubmit={unlock}>
            <label className="text-sm font-medium text-text">
              {t("audit.password")}
              <input type="password" autoComplete="current-password" required maxLength={256}
                className={`mt-1 block w-64 max-w-full ${intakeInputClass}`}
                value={password} onChange={(event) => setPassword(event.target.value)} />
            </label>
            <button type="submit" className={intakeActionClass}>{t("intake.open")}</button>
            {invalid && <p role="alert" className="text-sm text-danger-text">{t("audit.invalidPassword")}</p>}
          </form>
        ) : (
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
            <button type="button" className={intakeButtonClass} onClick={() => lock()}>{t("audit.lock")}</button>
            {groups.isError && <p role="alert" className="text-sm text-danger-text">{t("intake.loadFailed")}</p>}
          </div>
        )}
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
  const [form, setForm] = useState<FormState | null>();
  const [deliveries, setDeliveries] = useState<Delivery[]>([]);
  const [summary, setSummary] = useState<Summary>();
  const [busy, setBusy] = useState<Action>();
  const [confirm, setConfirm] = useState<"publish" | "distribute">();
  const [error, setError] = useState<TranslationKey>();
  const [notice, setNotice] = useState<TranslationKey>();
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
              {form.editUrl && <a className="text-sm font-medium text-primary underline" href={form.editUrl} target="_blank" rel="noreferrer">{t("intake.preview")}</a>}
              {form.responderUrl && <a className="text-sm font-medium text-primary underline" href={form.responderUrl} target="_blank" rel="noreferrer">{t("intake.responder")}</a>}
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

const emailStatusLabels = {
  pending: "audit.emailPending", sending: "audit.emailPending", sent: "audit.emailSent",
  failed: "audit.emailFailed", unknown: "audit.emailUnknown",
} as const;
