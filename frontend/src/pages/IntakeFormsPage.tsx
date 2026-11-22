import { useCallback, useEffect, useState } from "react";
import { Card, Chip, PageTitle } from "../components/ui";
import { AppSelect } from "../components/AppSelect";
import { IntakeQuestions } from "../components/IntakeQuestions";
import { editableQuestion, intakeActionClass, intakeButtonClass, intakeInputClass, toIntakeQuestions, type EditableQuestion } from "../lib/intakeForm";
import { useSettings } from "../i18n";
import type { TranslationKey } from "../i18n/en";
import { api, type Group } from "../lib/api";
import { useIntakeGroups } from "../lib/queries";
import type { components } from "../lib/schema";
import { fireLock, getAuthHeader, useAuthUser } from "../lib/auth";
import { useWorkspaceScope } from "../lib/workspaceScope";

type FormState = components["schemas"]["IntakeFormState"];
type Summary = components["schemas"]["IntakeResponseSummary"];
type Action = "draft" | "publish" | "distribute" | "import" | "refresh";

export function IntakeFormsPage() {
  const { t } = useSettings();
  const user = useAuthUser();
  const authorization = user ? getAuthHeader().Authorization ?? "" : "";
  const [groupId, setGroupId] = useState("");
  const [panel, setPanel] = useState<"create" | "history">("create");
  const groups = useIntakeGroups(Boolean(authorization));
  const workspaceScope = useWorkspaceScope();
  const visibleGroups = workspaceScope.filterGroups(groups.data);
  const onUnauthorized = useCallback(() => fireLock(), []);
  useEffect(() => {
    if (visibleGroups && groupId && !visibleGroups.some((group) => group.id === groupId)) setGroupId(visibleGroups[0]?.id ?? "");
  }, [visibleGroups, groupId]);

  return (
    <div className="space-y-5">
      <PageTitle title={t("nav.intakeForms")} />
      <Card className="p-5">
        {authorization ? (
          <div className="flex flex-wrap items-end gap-3">
            <label className="w-72 max-w-full text-sm font-medium text-text">
              {t("intake.group")}
              <AppSelect className={`mt-1 ${intakeInputClass}`} ariaLabel={t("intake.group")} value={groupId}
                placeholder={t("intake.choose")} options={(visibleGroups ?? []).map((group) => ({ value: group.id, label: group.name }))}
                onChange={setGroupId} />
            </label>
            {groups.isError && <p role="alert" className="text-sm text-danger-text">{t("intake.loadFailed")}</p>}
          </div>
        ) : <a href="#/login" className="app-action">{t("auth.signIn")}</a>}
      </Card>
      {authorization && <>
        <div className="flex flex-wrap gap-2" role="tablist" aria-label={t("nav.intakeForms")}>
          {(["create", "history"] as const).map((key) => (
            <button key={key} type="button" role="tab" aria-selected={panel === key}
              className={`min-h-10 rounded-lg border px-4 text-sm font-semibold transition ${panel === key ? "border-primary bg-primary text-on-primary" : "border-border bg-surface text-text hover:border-primary"}`}
              onClick={() => setPanel(key)}>{t(key === "create" ? "intake.createSection" : "intake.history")}</button>
          ))}
        </div>
        {panel === "create" && (groupId
          ? <IntakeWorkflow key={groupId} groupId={groupId} authorization={authorization} onUnauthorized={onUnauthorized} />
          : <Card className="p-5"><p className="text-sm text-muted">{t("intake.chooseGroupFirst")}</p></Card>)}
        {panel === "history" && <IntakeHistory groups={visibleGroups} authorization={authorization} onUnauthorized={onUnauthorized} />}
      </>}
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
  const [summary, setSummary] = useState<Summary>();
  const [busy, setBusy] = useState<Action>();
  const [emailMessage, setEmailMessage] = useState("");
  const [confirm, setConfirm] = useState<"publish" | "distribute">();
  const [error, setError] = useState<TranslationKey>();
  const [notice, setNotice] = useState<TranslationKey>();
  const [extraError, setExtraError] = useState<TranslationKey>();
  const [reload, setReload] = useState(0);
  const published = Boolean(form?.responderUrl);

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
        } else if (result.data) {
          setForm(result.data);
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
    if (!form?.questions || published) return; // sent form: add-only, start empty
    setExtraQuestions(form.questions.slice(2).map((question) => ({
      id: nextEditableId(),
      title: question.title,
      type: question.type,
      required: question.required,
      optionsText: (question.options ?? []).join("\n"),
    })));
  }, [form?.questions, published]);

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
            ? await api.POST("/api/groups/{groupId}/forms/distribute", { ...options, body: { message: emailMessage.trim() || undefined } })
            : await api.POST("/api/groups/{groupId}/forms/responses/import", options);

      if (result.response.status === 401) { onUnauthorized(); return; }
      if (!result.data) {
        setError(result.response.status === 422 ? "intake.invalidQuestions"
          : result.response.status === 409 ? "intake.conflict" : "intake.actionFailed");
        return;
      }
      if ("saved" in result.data) setSummary(result.data);
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
    // ՈՒՂԱՐԿՎԱԾ ֆորմա՝ առկա հարցերին ենք ավելացնում նորերը (append-only)․
    // սևագիր՝ ուղարկում ենք ամբողջական նոր ցանկը (ազատ խմբագրում)։
    const questions = published
      ? [...(form.questions ?? []), ...customs]
      : [{ title: "Full name", type: "text", required: true } as const,
         { title: "Email address", type: "text", required: true } as const, ...customs];
    try {
      const result = await api.POST("/api/groups/{groupId}/forms/provision", {
        ...options, body: { questions },
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
            {/* Question editor: drafts are freely editable; sent forms accept new questions (append-only). */}
            {ready && (
              <form className="space-y-3 border-t border-border pt-4" onSubmit={saveQuestions}>
                <div>
                  <h3 className="text-sm font-semibold text-text">{t(published ? "intake.addSentQuestions" : "intake.editQuestions")}</h3>
                  <p className="mt-1 text-xs text-muted">{t(published ? "intake.addSentQuestionsHint" : "intake.editQuestionsHint")}</p>
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
            {confirm === "distribute" && (
              <label className="block text-sm text-text">
                {t("intake.emailMessage")}
                <textarea className={`mt-1 ${intakeInputClass}`} rows={5} maxLength={4000}
                  value={emailMessage} onChange={(event) => setEmailMessage(event.target.value)}
                  aria-describedby="intake-email-link" />
                <span id="intake-email-link" className="mt-1 block text-muted">{t("intake.emailLink")}</span>
              </label>
            )}
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
    </>
  );
}

type GroupDeliverySummary = { group: Group; sent: number; failed: number; unavailable: boolean };

function IntakeHistory({ groups, authorization, onUnauthorized }: {
  groups?: Group[]; authorization: string; onUnauthorized: () => void;
}) {
  const { t } = useSettings();
  const [filter, setFilter] = useState<"sent" | "failed">("sent");
  const [searchOpen, setSearchOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [rows, setRows] = useState<GroupDeliverySummary[]>();
  const [loading, setLoading] = useState(false);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    if (!groups) return;
    const groupList = groups;
    const controller = new AbortController();
    async function load() {
      setLoading(true);
      const summaries: GroupDeliverySummary[] = [];
      for (let offset = 0; offset < groupList.length; offset += 5) {
        if (controller.signal.aborted) return;
        const results = await Promise.allSettled(groupList.slice(offset, offset + 5).map(async (group): Promise<GroupDeliverySummary> => {
          const request = { headers: { Authorization: authorization }, params: { path: { groupId: group.id } }, signal: controller.signal };
          const formResult = await api.GET("/api/groups/{groupId}/forms", request);
          if (formResult.response.status === 401) { onUnauthorized(); throw new Error("Unauthorized"); }
          if (formResult.response.status === 404) return { group, sent: 0, failed: 0, unavailable: false };
          if (!formResult.data) throw formResult.error ?? new Error("Could not load intake form");
          if (!formResult.data.responderUrl) return { group, sent: 0, failed: 0, unavailable: false };
          const deliveryResult = await api.GET("/api/groups/{groupId}/forms/deliveries", request);
          if (deliveryResult.response.status === 401) { onUnauthorized(); throw new Error("Unauthorized"); }
          if (!deliveryResult.data) throw deliveryResult.error ?? new Error("Could not load form deliveries");
          return {
            group,
            sent: deliveryResult.data.deliveries.filter(({ status }) => status === "sent").length,
            failed: deliveryResult.data.deliveries.filter(({ status }) => status === "failed" || status === "unknown").length,
            unavailable: false,
          };
        }));
        results.forEach((result, index) => summaries.push(result.status === "fulfilled"
          ? result.value
          : { group: groupList[offset + index], sent: 0, failed: 0, unavailable: true }));
      }
      if (!controller.signal.aborted) {
        setRows(summaries);
        setLoading(false);
      }
    }
    setRows(undefined);
    void load();
    return () => controller.abort();
  }, [groups, authorization, onUnauthorized, reload]);

  const query = search.trim().toLocaleLowerCase();
  const nameMatches = rows?.filter((row) => row.group.name.toLocaleLowerCase().includes(query)) ?? [];
  const exactNameMatches = query ? nameMatches.filter((row) => row.group.name.trim().toLocaleLowerCase() === query) : [];
  const filteredNames = exactNameMatches.length ? exactNameMatches : nameMatches;
  const visible = filteredNames.filter((row) =>
    (row.unavailable || (filter === "sent" ? row.sent > 0 : row.failed > 0)));

  return (
    <Card className="space-y-4 p-5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="font-semibold text-text">{t("intake.deliveries")}</h2>
        <button type="button" className={intakeButtonClass} disabled={loading}
          onClick={() => setReload((value) => value + 1)}>{t("audit.refresh")}</button>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex gap-2 border-b border-border" role="tablist" aria-label={t("intake.deliveries")}>
          {(["sent", "failed"] as const).map((key) => (
            <button key={key} type="button" role="tab" aria-selected={filter === key}
              className={`min-h-10 border-b-2 px-3 text-sm font-semibold transition ${filter === key ? "border-primary text-primary" : "border-transparent text-muted hover:text-text"}`}
              onClick={() => setFilter(key)}>{t(`intake.deliveryFilter.${key}`)}</button>
          ))}
        </div>
        <button type="button" className={intakeButtonClass} aria-expanded={searchOpen}
          onClick={() => setSearchOpen((open) => !open)}>{t("intake.searchGroups")}</button>
      </div>
      {searchOpen && <label className="block max-w-sm text-sm font-medium text-text">
        {t("audit.groupSearch")}
        <input type="search" className={`mt-1 ${intakeInputClass}`} value={search} onChange={(event) => setSearch(event.target.value)} />
      </label>}
      {(!groups || loading) && <p role="status" className="text-sm text-muted">{t("intake.loading")}</p>}
      {groups && !loading && visible?.length === 0 && <p className="text-sm text-muted">
        {query && nameMatches.length === 0
          ? t("intake.noGroupsMatch") : t(`intake.deliveryFilter.empty.${filter}`)}
      </p>}
      {!!visible?.length && <ul className="divide-y divide-border">
        {visible.map(({ group, sent, failed, unavailable }) => (
          <li key={group.id} className="flex flex-wrap items-center justify-between gap-3 py-3 text-sm">
            <span className="font-medium text-text">{group.name}</span>
            {unavailable ? <Chip tone="warn">{t("intake.deliveryUnavailable")}</Chip> : (
              <Chip tone={filter === "sent" ? "success" : "warn"}>
                {t(`intake.deliveryCount.${filter}`, { count: filter === "sent" ? sent : failed })}
              </Chip>
            )}
            <span className="w-full text-xs text-muted">
              {t("intake.deliveryCount.sent", { count: sent })} · {t("intake.deliveryCount.failed", { count: failed })}
            </span>
          </li>
        ))}
      </ul>}
    </Card>
  );
}

let extraQuestionId = 0;
function nextEditableId(): number {
  return ++extraQuestionId;
}
