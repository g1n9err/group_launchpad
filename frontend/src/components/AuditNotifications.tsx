import { useCallback, useEffect, useState, type ReactNode } from "react";
import { useSettings } from "../i18n";
import type { TranslationKey } from "../i18n/en";
import { api } from "../lib/api";
import { fireLock, getAuthHeader, isUnlocked, onAuthChange } from "../lib/auth";
import type { components } from "../lib/schema";
import { Card, Chip } from "./ui";

type Feed = components["schemas"]["AuditNotifications"];

const statusLabels: Record<string, TranslationKey> = {
  ok: "audit.resolved",
  unlinked: "audit.unlinked",
  missing_or_inaccessible: "audit.missing",
  unavailable: "audit.unavailable",
  archived: "audit.archived",
  inactive: "audit.inactive",
  trashed: "audit.trashed",
  misconfigured: "audit.misconfigured",
};

const emailLabels: Record<components["schemas"]["AuditNotification"]["emailStatus"], TranslationKey> = {
  disabled: "audit.emailDisabled", pending: "audit.emailPending", sending: "audit.emailPending",
  sent: "audit.emailSent", failed: "audit.emailFailed", unknown: "audit.emailUnknown",
};

const providers: Record<string, string> = {
  slack: "Slack", google_classroom: "Classroom", google_drive: "Drive", google_forms: "Forms",
};

export function AuditNotifications({ children }: {
  children?: (authorization: string, onUnauthorized: () => void, reload: number) => ReactNode;
} = {}) {
  const { t, lang } = useSettings();
  const [authorized, setAuthorized] = useState(isUnlocked());
  const [feed, setFeed] = useState<Feed>();
  const [error, setError] = useState<TranslationKey>();
  const [loading, setLoading] = useState(false);
  const [reload, setReload] = useState(0);

  const authorization = authorized ? getAuthHeader().Authorization ?? "" : "";

  useEffect(() => onAuthChange(() => setAuthorized(isUnlocked())), []);

  const rejectAuthorization = useCallback(() => {
    fireLock();
    setFeed(undefined);
    setLoading(false);
    setError("audit.invalidPassword");
  }, []);

  useEffect(() => {
    if (!authorization) return;
    const controller = new AbortController();
    let loading = false;
    async function refresh() {
      if (loading) return;
      loading = true;
      setLoading(true);
      try {
        const { data, response } = await api.GET("/api/audit-notifications", {
          headers: { Authorization: authorization },
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
  }, [authorization, reload, rejectAuthorization]);

  const label = (status: string) => statusLabels[status] ? t(statusLabels[status]) : status;
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

      {error && <p className="text-sm text-danger-text" role="alert">{t(error)}</p>}
      {authorization && loading && !feed && <p className="text-sm text-muted" role="status">{t("audit.loading")}</p>}

      {feed && (
        <div className="grid gap-6 lg:grid-cols-2">
          <section className="space-y-3">
            <h3 className="text-sm font-semibold text-text">{t("audit.open")}</h3>
            {feed.openIssues.length === 0 && <p className="text-sm text-muted">{t("audit.noOpen")}</p>}
            {feed.openIssues.map((group) => (
              <div className="rounded-lg border border-border p-3" key={group.groupId}>
                <p className="text-sm font-medium text-text">{group.groupName}</p>
                <p className="text-xs text-muted">{time(group.checkedAt)}</p>
                <ul className="mt-2 space-y-1 text-sm">
                  {group.issues.map((issue) => (
                    <li className="flex flex-wrap items-center justify-between gap-2" key={`${issue.provider}/${issue.key}`}>
                      <span>{providers[issue.provider] ?? issue.provider} · {issue.key}</span>
                      <Chip tone="warn">{label(issue.status)}</Chip>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </section>
          <section className="space-y-3">
            <h3 className="text-sm font-semibold text-text">{t("audit.recent")}</h3>
            {feed.notifications.length === 0 && <p className="text-sm text-muted">{t("audit.noChanges")}</p>}
            {feed.notifications.map((notification) => (
              <div className="rounded-lg border border-border p-3" key={notification.id}>
                <div className="flex flex-wrap justify-between gap-2">
                  <p className="text-sm font-medium text-text">{notification.groupName}</p>
                  <Chip tone={notification.emailStatus === "failed" ? "danger" : "muted"}>{t(emailLabels[notification.emailStatus])}</Chip>
                </div>
                <p className="text-xs text-muted">{time(notification.checkedAt)}</p>
                <ul className="mt-2 space-y-1 text-sm">
                  {notification.changes.map((change) => (
                    <li className="flex flex-wrap items-center justify-between gap-2" key={`${change.provider}/${change.key}`}>
                      <span>{providers[change.provider] ?? change.provider} · {change.key}</span>
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
      )}
    </Card>
  );

  return (
    <>
      {notifications}
      {authorization && feed && children?.(authorization, rejectAuthorization, reload)}
    </>
  );
}
