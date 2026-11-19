import { groupOnboardingOptions, useAllGroups, useGoogleStatus, useSlackStatus } from "../lib/queries";
import { useSettings } from "../i18n";
import { useQueries } from "@tanstack/react-query";
import { Card, PageTitle, StatCard, StatusBadge } from "../components/ui";

import { AuditNotifications } from "../components/AuditNotifications";
import { DashboardAnnouncements } from "../components/DashboardAnnouncements";

export function OverviewPage() {
  const { t, formatNumber } = useSettings();
  const groups = useAllGroups();
  const google = useGoogleStatus();
  const slack = useSlackStatus();
  const onboarding = useQueries({
    queries: (groups.data ?? []).map((group) => groupOnboardingOptions(group.id)),
  });
  const onboardingFailed = groups.isError || onboarding.some((report) => report.isError);
  const onboardingPending = groups.isPending || onboarding.some((report) => report.isPending);
  const totalStudents = onboarding.reduce((total, report) => total + (report.data?.summary.expectedCount ?? 0), 0);
  const joinedStudents = onboarding.reduce((total, report) => total + (report.data?.summary.joinedCount ?? 0), 0);
  const activeGroups = groups.data?.filter((group) => group.status === "launched" || group.status === "launching");
  const recentGroups = groups.data?.filter((group) => group.status !== "archived").sort((a, b) => {
    const failed = (status: string) => status === "failed" || status === "partially_failed";
    return Number(failed(b.status)) - Number(failed(a.status)) || Date.parse(b.createdAt) - Date.parse(a.createdAt);
  }).slice(0, 5);
  const percent = totalStudents > 0 ? Math.round((joinedStudents / totalStudents) * 1000) / 10 : 0;
  const rosterReady = !onboardingPending && !onboardingFailed;

  return (
    <div className="space-y-6">
      <PageTitle title={t("overview.title")} sub={t("app.tagline")} />

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label={t("overview.totalGroups")} value={groups.data ? formatNumber(groups.data.length) : "—"} />
        <StatCard label={t("overview.totalStudents")} value={rosterReady ? formatNumber(totalStudents) : "—"} />
        <StatCard label={t("overview.activeGroups")} value={activeGroups ? formatNumber(activeGroups.length) : "—"} />
        <StatCard
          label={t("overview.onboardingProgress")}
          value={rosterReady ? `${formatNumber(percent)}%` : "—"}
          sub={rosterReady ? t("overview.joinedOf", { joined: joinedStudents, total: totalStudents }) : t(onboardingFailed ? "onboarding.error" : "onboarding.loading")}
        >
          {rosterReady && <div className="mt-3 h-2 w-full overflow-hidden rounded-full bg-surface-muted">
            <div className="h-full rounded-full bg-success-text transition-all" style={{ width: `${Math.min(percent, 100)}%` }} />
          </div>}
        </StatCard>
      </div>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <Card className="p-5">
          <h3 className="text-sm font-semibold text-text">{t("overview.integrationStatus")}</h3>
          <div className="mt-3 space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-sm text-muted">Slack</span>
              {slack.isPending ? (
                <span className="text-xs text-muted">{t("integrations.checking")}</span>
              ) : slack.isError ? (
                <StatusBadge status="failed" t={t} />
              ) : (
                <StatusBadge status={slack.data.status} t={t} />
              )}
            </div>
            <div className="flex items-center justify-between">
              <span className="text-sm text-muted">Google</span>
              {google.isPending ? (
                <span className="text-xs text-muted">{t("integrations.checking")}</span>
              ) : google.isError ? (
                <StatusBadge status="failed" t={t} />
              ) : (
                <StatusBadge status={google.data.status} t={t} />
              )}
            </div>
          </div>
        </Card>

        <Card className="p-5">
          <h2 className="text-sm font-semibold text-text">{t("overview.launchStatus")}</h2>
          {groups.isPending && <p role="status" className="mt-3 text-sm text-muted">{t("list.loading")}</p>}
          {groups.isError && <p role="alert" className="mt-3 text-sm text-danger-text">{t("list.error")}</p>}
          {recentGroups?.length === 0 && <p className="mt-3 text-sm text-muted">{t("list.empty")}</p>}
          <ul className="mt-2 divide-y divide-border">
            {recentGroups?.map((group) => (
              <li key={group.id} className="flex flex-wrap items-center justify-between gap-2 py-2 text-sm">
                <a className="font-medium text-primary underline" href={`#/group-details/${group.id}`}>{group.name}</a>
                <StatusBadge status={group.status} t={t} />
              </li>
            ))}
          </ul>
        </Card>
      </div>

      <AuditNotifications>
        {(authorization, onUnauthorized, reload) => groups.data && (
          <DashboardAnnouncements groups={groups.data} authorization={authorization}
            onUnauthorized={onUnauthorized} reload={reload} />
        )}
      </AuditNotifications>
    </div>
  );
}
