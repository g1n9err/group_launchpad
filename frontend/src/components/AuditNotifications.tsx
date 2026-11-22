import { auditProviders, auditStatusLabels } from "../lib/auditDetail";
import { useCallback, useEffect, useState, type ReactNode } from "react";
import { useSettings } from "../i18n";
import type { TranslationKey } from "../i18n/en";
import { api } from "../lib/api";
import { intakeInputClass } from "../lib/intakeForm";
import { fireLock, getAuthHeader, isUnlocked, onAuthChange } from "../lib/auth";
import type { components } from "../lib/schema";
import { Card, Chip } from "./ui";
import { useWorkspaceScope } from "../lib/workspaceScope";

type Feed = components["schemas"]["AuditNotifications"];

const emailLabels: Record<components["schemas"]["AuditNotification"]["emailStatus"], TranslationKey> = {
  disabled: "audit.emailDisabled", pending: "audit.emailPending", sending: "audit.emailPending",
  sent: "audit.emailSent", failed: "audit.emailFailed", unknown: "audit.emailUnknown",
};

export function AuditNotifications({ children, loadNotifications = true, showFeed = true }: {
  children?: (authorization: string, onUnauthorized: () => void, reload: number) => ReactNode;
  loadNotifications?: boolean;
  showFeed?: boolean;
} = {}) {
  const { t, lang } = useSettings();
  const [authorized, setAuthorized] = useState(isUnlocked());
  const [feed, setFeed] = useState<Feed>();
  const [error, setError] = useState<TranslationKey>();
  const [loading, setLoading] = useState(false);
  const [reload, setReload] = useState(0);
  const [groupSearch, setGroupSearch] = useState("");
  const [groupName, setGroupName] = useState("");
  const workspaceScope = useWorkspaceScope();
  const workspaceFilter = workspaceScope.selectedWorkspaceId;

  const authorization = authorized ? getAuthHeader().Authorization ?? "" : "";

  useEffect(() => onAuthChange(() => setAuthorized(isUnlocked())), []);
  useEffect(() => {
    const timer = window.setTimeout(() => setGroupName(groupSearch.trim()), 250);
    return () => window.clearTimeout(timer);
  }, [groupSearch]);

  const rejectAuthorization = useCallback(() => {
    fireLock();
    setFeed(undefined);
    setLoading(false);
    setError("audit.invalidPassword");
  }, []);

  useEffect(() => {
    if (!authorization || !loadNotifications) return;
    const controller = new AbortController();
    let loading = false;
    async function refresh() {
      if (loading) return;
      loading = true;
      setLoading(true);
      try {
        const { data, response } = await api.GET("/api/audit-notifications", {
          headers: { Authorization: authorization },
          params: { query: { groupName: groupName || undefined } },
          signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        if (response.status === 401) {
          rejectAuthorization();
        } else if (!data) {
          setError("audit.loadFailed");
        } else {
          setFeed(data);
          setError(undefined);
        }
      } catch {
        if (!controller.signal.aborted) setError("audit.loadFailed");
      } finally {
        loading = false;
        if (!controller.signal.aborted) setLoading(false);
      }
    }
    void refresh();
    const timer = window.setInterval(() => void refresh(), 60_000);
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [authorization, groupName, reload, rejectAuthorization, loadNotifications]);

  const label = (status: string) => auditStatusLabels[status] ? t(auditStatusLabels[status]) : status;
  const dateFormat = new Intl.DateTimeFormat({ hy: "hy-AM", en: "en-US", ru: "ru-RU" }[lang], { dateStyle: "medium", timeStyle: "short" });
  const time = (value: string) => dateFormat.format(new Date(value));

  const notifications = (
    <Card className="space-y-4 p-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-base font-semibold text-text">{t("audit.title")}</h2>
        {authorization && (
          <div className="flex gap-2">
            <button type="button" onClick={() => setReload((n) => n + 1)} disabled={loading}
              className="rounded-md border border-border px-3 py-2 text-sm text-text hover:bg-surface-muted disabled:opacity-50">
              {t("audit.refresh")}
            </button>
            <button type="button" onClick={() => { fireLock(); setFeed(undefined); setError(undefined); setLoading(false); }}
              className="rounded-md border border-border px-3 py-2 text-sm text-text hover:bg-surface-muted">
              {t("audit.lock")}
            </button>
          </div>
        )}
      </div>

      {!authorization && <a href="#/login" className="app-action">{t("auth.signIn")}</a>}
      {authorization && (
        <div className="flex flex-wrap gap-4">
          <label className="block w-80 max-w-full text-sm font-medium text-text">
            {t("audit.groupSearch")}
            <input type="search" className={`mt-1 ${intakeInputClass}`}
              value={groupSearch} onChange={(event) => setGroupSearch(event.target.value)} />
          </label>
          <label className="block w-80 max-w-full text-sm font-medium text-text">
            {t("audit.workspaceFilter")}
            <select className={`mt-1 ${intakeInputClass}`} value={workspaceFilter} onChange={(event) => workspaceScope.setSelectedWorkspaceId(event.target.value)}>
              <option value="">{t("workspace.all")}</option>
              <option value="__default__">{t("workspace.default")}</option>
              {workspaceScope.workspaces.map((workspace) => <option key={workspace.id} value={workspace.id}>{workspace.teamName}</option>)}
            </select>
          </label>
        </div>
      )}

      {error && <p className="text-sm text-danger-text" role="alert">{t(error)}</p>}
      {authorization && loading && !feed && <p className="text-sm text-muted" role="status">{t("audit.loading")}</p>}

      {feed && (
        (() => {
          const openIssues = feed.openIssues.filter((item) => !workspaceFilter || ((workspaceScope.assignmentByGroup.get(item.groupId)?.workspaceId ?? "__default__") === workspaceFilter));
          const notifications = feed.notifications.filter((item) => !workspaceFilter || ((workspaceScope.assignmentByGroup.get(item.groupId)?.workspaceId ?? "__default__") === workspaceFilter));
          return (
        <div className="grid gap-6 lg:grid-cols-2">
          <section className="space-y-3">
            <h3 className="text-sm font-semibold text-text">{t("audit.open")}</h3>
            {openIssues.length === 0 && <p className="text-sm text-muted">{t("audit.noOpen")}</p>}
            {openIssues.map((group) => (
              <div className="rounded-lg border border-border p-3" key={group.groupId}>
                <p className="text-sm font-medium text-text">{group.groupName}</p>
                <p className="text-xs text-muted">{t("audit.workspaceFilter")}: {workspaceScope.assignmentByGroup.get(group.groupId)?.workspaceName ?? t("groups.defaultSlackWorkspace")}</p>
                <p className="text-xs text-muted">{time(group.checkedAt)}</p>
                <ul className="mt-2 space-y-1 text-sm">
                  {group.issues.map((issue) => (
                    <li className="flex flex-wrap items-center justify-between gap-2" key={`${issue.provider}/${issue.key}`}>
                      <span>{auditProviders[issue.provider] ?? issue.provider} · {issue.key}</span>
                      <Chip tone="warn">{label(issue.status)}</Chip>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </section>
          <section className="space-y-3">
            <h3 className="text-sm font-semibold text-text">{t("audit.recent")}</h3>
            {notifications.length === 0 && <p className="text-sm text-muted">{t("audit.noChanges")}</p>}
            {notifications.map((notification) => (
              <div className="rounded-lg border border-border p-3" key={notification.id}>
                <div className="flex flex-wrap justify-between gap-2">
                  <p className="text-sm font-medium text-text">{notification.groupName}</p>
                  <Chip tone={notification.emailStatus === "failed" ? "danger" : "muted"}>{t(emailLabels[notification.emailStatus])}</Chip>
                </div>
                <p className="text-xs text-muted">{t("audit.workspaceFilter")}: {workspaceScope.assignmentByGroup.get(notification.groupId)?.workspaceName ?? t("groups.defaultSlackWorkspace")}</p>
                <p className="text-xs text-muted">{time(notification.checkedAt)}</p>
                <ul className="mt-2 space-y-1 text-sm">
                  {notification.changes.map((change) => (
                    <li className="flex flex-wrap items-center justify-between gap-2" key={`${change.provider}/${change.key}`}>
                      <span>{auditProviders[change.provider] ?? change.provider} · {change.key}</span>
                      <Chip tone={change.after === "ok" ? "success" : "warn"}>
                        {change.before && change.before !== "ok" && change.after !== "ok" ? `${label(change.before)} → ${label(change.after)}` : label(change.after)}
                      </Chip>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </section>
        </div>
          );
        })()
      )}
    </Card>
  );

  return (
    <>
      {!showFeed && !authorization && <a href="#/login" className="app-action">{t("auth.signIn")}</a>}
      {showFeed && notifications}
      {authorization && feed && children?.(authorization, rejectAuthorization, reload)}
    </>
  );
}
