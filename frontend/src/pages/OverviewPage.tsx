// Overview page: real data only (groups, students, onboarding progress,
// integration statuses); activity and announcements are honest placeholders.

import { useGroups, useGoogleStatus, useGroupOnboarding, useSlackStatus } from "../lib/queries";
import { useSettings } from "../i18n";
import { useQueryClient } from "@tanstack/react-query";
import { Card, ComingSoon, PageTitle, StatCard, StatusBadge } from "../components/ui";

export function OverviewPage() {
  const { t, formatNumber } = useSettings();
  const groups = useGroups();
  const google = useGoogleStatus();
  const slack = useSlackStatus();
  const queryClient = useQueryClient();

  // Aggregate per-group onboarding reports (existing endpoints only).
  const onboardingReports = (groups.data?.data ?? []).map((g) => ({
    id: g.id,
    report: queryClient.getQueryData(["onboarding", g.id]) as
      | { summary: { expectedCount: number; joinedCount: number } }
      | undefined,
  }));

  const totalStudents = onboardingReports.reduce((acc, r) => acc + (r.report?.summary.expectedCount ?? 0), 0);
  const joinedStudents = onboardingReports.reduce((acc, r) => acc + (r.report?.summary.joinedCount ?? 0), 0);
  const activeGroups = (groups.data?.data ?? []).filter(
    (g) => g.status === "launched" || g.status === "launching",
  ).length;
  const percent = totalStudents > 0 ? Math.round((joinedStudents / totalStudents) * 1000) / 10 : 0;

  return (
    <div className="space-y-6">
      <PageTitle title={t("overview.title")} sub={t("app.tagline")} />

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label={t("overview.totalGroups")} value={formatNumber(groups.data?.page.total ?? 0)} />
        <StatCard label={t("overview.totalStudents")} value={formatNumber(totalStudents)} />
        <StatCard label={t("overview.activeGroups")} value={formatNumber(activeGroups)} />
        <StatCard
          label={t("overview.onboardingProgress")}
          value={`${formatNumber(percent)}%`}
          sub={t("overview.joinedOf", { joined: joinedStudents, total: totalStudents })}
        >
          <div className="mt-3 h-2 w-full overflow-hidden rounded-full bg-surface-muted">
            <div className="h-full rounded-full bg-success-text transition-all" style={{ width: `${Math.min(percent, 100)}%` }} />
          </div>
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

        <ComingSoon title={t("overview.recentActivity")} />
      </div>

      <ComingSoon title={t("overview.upcomingAnnouncements")} />
    </div>
  );
}

// Re-export to keep the onboarding query warm on this page.
export { useGroupOnboarding };
