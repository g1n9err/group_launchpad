import { useEffect, useRef, useState } from "react";
import { useSettings } from "../i18n";
import { AuditNotifications } from "../components/AuditNotifications";
import { Card, Chip, PageTitle } from "../components/ui";
import { api } from "../lib/api";
import { useIntakeGroups } from "../lib/queries";
import { intakeActionClass, intakeInputClass } from "../lib/intakeForm";
import type { components } from "../lib/schema";

export function AutomationPage() {
  const { t } = useSettings();
  return (
    <div className="space-y-6">
      <PageTitle title={t("automation.title")} sub={t("automation.summary")} />
      <AuditNotifications>
        {(authorization, onUnauthorized) => (
          <GroupAudits authorization={authorization} onUnauthorized={onUnauthorized} />
        )}
      </AuditNotifications>
    </div>
  );
}

function GroupAudits({ authorization, onUnauthorized }: { authorization: string; onUnauthorized: () => void }) {
  const { t } = useSettings();
  const groups = useIntakeGroups(true);
  const [groupId, setGroupId] = useState("");

  return (
    <Card className="space-y-4 p-5">
      <h2 className="text-base font-bold">{t("automation.groupAudit")}</h2>
      <label className="block max-w-sm text-sm font-medium">
        {t("intake.group")}
        <select className={`mt-1 ${intakeInputClass}`} value={groupId} onChange={(event) => setGroupId(event.target.value)}>
          <option value="">{t("intake.choose")}</option>
          {groups.data?.map((group) => <option key={group.id} value={group.id}>{group.name}</option>)}
        </select>
      </label>
      {groups.isPending && <p role="status" className="text-sm text-muted">{t("list.loading")}</p>}
      {groups.isError && <p role="alert" className="text-sm text-danger-text">{t("list.error")}</p>}
      {groups.data?.length === 0 && <p className="text-sm text-muted">{t("list.empty")}</p>}
      {groupId && (
        <AuditHistory key={groupId} {...{ authorization, groupId, onUnauthorized }} />
      )}
    </Card>
  );
}

function AuditHistory({ authorization, groupId, onUnauthorized }: {
  authorization: string; groupId: string; onUnauthorized: () => void;
}) {
  const { t, formatDate } = useSettings();
  const [history, setHistory] = useState<components["schemas"]["GroupAudit"][]>();
  const [reload, setReload] = useState(0);
  const [loadingFailed, setLoadingFailed] = useState(false);
  const [queueState, setQueueState] = useState<"pending" | "queued" | "error">();
  const submission = useRef<AbortController | null>(null);

  useEffect(() => () => submission.current?.abort(), []);
  useEffect(() => {
    const controller = new AbortController();
    async function load() {
      try {
        const { data, response } = await api.GET("/api/groups/{groupId}/audits", {
          params: { path: { groupId } }, headers: { Authorization: authorization }, signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        if (response.status === 401) { onUnauthorized(); return; }
        if (data) setHistory(data);
        setLoadingFailed(!data);
      } catch {
        if (!controller.signal.aborted) setLoadingFailed(true);
      }
    }
    void load();
    return () => controller.abort();
  }, [authorization, groupId, onUnauthorized, reload]);

  async function queueAudit() {
    if (submission.current) return;
    const controller = new AbortController();
    submission.current = controller;
    setQueueState("pending");
    try {
      const { response } = await api.POST("/api/groups/{groupId}/audits", {
        params: { path: { groupId } }, headers: { Authorization: authorization }, signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      if (response.status === 401) { onUnauthorized(); return; }
      setQueueState(response.status === 202 ? "queued" : "error");
    } catch {
      if (!controller.signal.aborted) setQueueState("error");
    } finally {
      submission.current = null;
    }
  }

  return (
    <div className="space-y-3 border-t border-border pt-4">
      <div className="flex flex-wrap items-center gap-3">
        <button type="button" className={intakeActionClass} disabled={queueState === "pending"}
          onClick={() => void queueAudit()}>{t("automation.runAudit")}</button>
        <button type="button" className="min-h-10 px-2 text-sm font-semibold text-primary hover:underline"
          onClick={() => setReload((value) => value + 1)}>{t("automation.refreshHistory")}</button>
      </div>
      {queueState === "queued" && <p role="status" className="text-sm text-muted">{t("automation.auditQueued")}</p>}
      {queueState === "error" && <p role="alert" className="text-sm text-danger-text">{t("automation.auditQueueFailed")}</p>}
      {loadingFailed && <p role="alert" className="text-sm text-danger-text">{t("automation.auditHistoryFailed")}</p>}
      {!history && !loadingFailed && <p role="status" className="text-sm text-muted">{t("audit.loading")}</p>}
      {history?.length === 0 && !loadingFailed && <p className="text-sm text-muted">{t("automation.noAudits")}</p>}
      {!!history?.length && (
        <ul className="divide-y divide-border">
          {history.map((item) => (
            <li key={item.id} className="flex flex-wrap items-center justify-between gap-2 py-2 text-sm">
              <span>{t(item.type === "daily_sync" ? "automation.dailySync" : "automation.manualAudit")} · {formatDate(item.checkedAt)}</span>
              <Chip tone={item.issues.length ? "warn" : "success"}>{t("automation.auditIssues", { count: item.issues.length })}</Chip>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
