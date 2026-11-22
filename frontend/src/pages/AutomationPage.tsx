import { useEffect, useRef, useState } from "react";
import { useSettings } from "../i18n";
import { AppSelect } from "../components/AppSelect";
import { AuditNotifications } from "../components/AuditNotifications";
import { Card, Chip, PageTitle } from "../components/ui";
import { api } from "../lib/api";
import { useIntakeGroups } from "../lib/queries";
import { intakeActionClass, intakeInputClass } from "../lib/intakeForm";
import type { components } from "../lib/schema";
import { useWorkspaceScope } from "../lib/workspaceScope";

export function AutomationPage() {
  const { t } = useSettings();
  return (
    <div className="space-y-6">
      <PageTitle title={t("automation.title")} sub={t("automation.summary")} />
      <AuditNotifications>
        {(authorization, onUnauthorized, _, refreshFeed) => (
          <GroupAudits authorization={authorization} onUnauthorized={onUnauthorized} refreshFeed={refreshFeed} />
        )}
      </AuditNotifications>
    </div>
  );
}

function GroupAudits({ authorization, onUnauthorized, refreshFeed }: { authorization: string; onUnauthorized: () => void; refreshFeed: () => void }) {
  const { t } = useSettings();
  const groups = useIntakeGroups(true);
  const workspaceScope = useWorkspaceScope();
  const visibleGroups = workspaceScope.filterGroups(groups.data);
  const [groupId, setGroupId] = useState("");
  useEffect(() => {
    if (visibleGroups?.length && !visibleGroups.some((group) => group.id === groupId)) setGroupId(visibleGroups[0].id);
    else if (visibleGroups?.length === 0 && groupId) setGroupId("");
  }, [visibleGroups, groupId]);

  return (
    <Card className="space-y-4 p-5">
      <h2 className="text-base font-bold">{t("automation.groupAudit")}</h2>
      <label className="block max-w-sm text-sm font-medium">
        {t("intake.group")}
        <AppSelect className={`mt-1 ${intakeInputClass}`} ariaLabel={t("intake.group")} value={groupId}
          placeholder={t("intake.choose")} options={(visibleGroups ?? []).map((group) => ({ value: group.id, label: group.name }))}
          onChange={setGroupId} />
      </label>
      {groups.isPending && <p role="status" className="text-sm text-muted">{t("list.loading")}</p>}
      {groups.isError && <p role="alert" className="text-sm text-danger-text">{t("list.error")}</p>}
      {visibleGroups?.length === 0 && <p className="text-sm text-muted">{t("list.empty")}</p>}
      {groupId && (
        <AuditHistory key={groupId} {...{ authorization, groupId, onUnauthorized, refreshFeed }} />
      )}
    </Card>
  );
}

function AuditHistory({ authorization, groupId, onUnauthorized, refreshFeed }: {
  authorization: string; groupId: string; onUnauthorized: () => void; refreshFeed: () => void;
}) {
  const { t, formatDate, lang } = useSettings();
  const [history, setHistory] = useState<components["schemas"]["GroupAudit"][]>();
  const [reload, setReload] = useState(0);
  const [loadingFailed, setLoadingFailed] = useState(false);
  const [queueState, setQueueState] = useState<"pending" | "queued" | "finished" | "delayed" | "error">();
  const previousAudit = useRef<{ id?: string }>(undefined);
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
        if (data) {
          setHistory(data);
          const latestManual = data.find((item) => item.type === "manual");
          if (previousAudit.current && latestManual && latestManual.id !== previousAudit.current.id) {
            previousAudit.current = undefined;
            setQueueState("finished");
            refreshFeed();
          }
        }
        setLoadingFailed(!data);
      } catch {
        if (!controller.signal.aborted) setLoadingFailed(true);
      }
    }
    void load();
    return () => controller.abort();
  }, [authorization, groupId, onUnauthorized, refreshFeed, reload]);

  useEffect(() => {
    if (queueState !== "queued") return;
    const interval = window.setInterval(() => setReload((value) => value + 1), 3000);
    const timeout = window.setTimeout(() => setQueueState("delayed"), 190_000);
    return () => {
      window.clearInterval(interval);
      window.clearTimeout(timeout);
    };
  }, [queueState]);

  async function queueAudit() {
    if (!history || submission.current) return;
    const lastManual = history.find((item) => item.type === "manual");
    const controller = new AbortController();
    submission.current = controller;
    setQueueState("pending");
    try {
      const { response } = await api.POST("/api/groups/{groupId}/audits", {
        params: { path: { groupId } }, headers: { Authorization: authorization }, signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      if (response.status === 401) { onUnauthorized(); return; }
      if (response.status === 202) {
        previousAudit.current = { id: lastManual?.id };
        setQueueState("queued");
        setReload((value) => value + 1);
      } else {
        setQueueState("error");
      }
    } catch {
      if (!controller.signal.aborted) setQueueState("error");
    } finally {
      submission.current = null;
    }
  }

  return (
    <div className="space-y-3 border-t border-border pt-4">
      <div className="flex flex-wrap items-center gap-3">
        <button type="button" className={intakeActionClass} disabled={!history || queueState === "pending" || queueState === "queued"}
          onClick={() => void queueAudit()}>{t("automation.runAudit")}</button>
        <button type="button" className="min-h-10 px-2 text-sm font-semibold text-primary hover:underline"
          onClick={() => setReload((value) => value + 1)}>{t("automation.refreshHistory")}</button>
      </div>
      {queueState === "queued" && <p role="status" className="text-sm text-muted">{t("automation.auditQueued")}</p>}
      {queueState === "finished" && <p role="status" className="text-sm text-muted">{t("automation.auditFinished")}</p>}
      {queueState === "delayed" && <p role="status" className="text-sm text-muted">{t("automation.auditDelayed")}</p>}
      {queueState === "error" && <p role="alert" className="text-sm text-danger-text">{t("automation.auditQueueFailed")}</p>}
      {loadingFailed && <p role="alert" className="text-sm text-danger-text">{t("automation.auditHistoryFailed")}</p>}
      {!history && !loadingFailed && <p role="status" className="text-sm text-muted">{t("audit.loading")}</p>}
      {history?.length === 0 && !loadingFailed && <p className="text-sm text-muted">{t("automation.noAudits")}</p>}
      {!!history?.length && (
        <ul className="divide-y divide-border">
          {history.map((item) => (
            <li key={item.id} className="flex flex-wrap items-center justify-between gap-2 py-2 text-sm">
              <span>{t(item.type === "daily_sync" ? "automation.dailySync" : "automation.manualAudit")} · {formatDate(item.checkedAt)} · {new Date(item.checkedAt).toLocaleTimeString(lang, { hour: "2-digit", minute: "2-digit" })}</span>
              <Chip tone={item.issues.length ? "warn" : "success"}>{t("automation.auditIssues", { count: item.issues.length })}</Chip>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
