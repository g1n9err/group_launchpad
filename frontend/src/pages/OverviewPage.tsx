import { groupOnboardingOptions, useAllGroups, useGoogleStatus, useSlackStatus } from "../lib/queries";
import { useSettings } from "../i18n";
import { useQueries } from "@tanstack/react-query";
import { useState } from "react";
import { Card, PageTitle, StatCard, StatusBadge } from "../components/ui";
import { AuditNotifications } from "../components/AuditNotifications";
import { DashboardAnnouncements } from "../components/DashboardAnnouncements";
import { AuthGate } from "../components/AuthGate";
import { IconAutomation, IconBack, IconGroups, IconOverview, IconStudents } from "../components/icons";
import { useWorkspaceScope } from "../lib/workspaceScope";

const launchStages = [
  { key: "attention", color: "bg-warn-text", label: "overview.needsAttention" },
  { key: "launched", color: "bg-success-text", label: "status.launched" },
  { key: "launching", color: "bg-primary", label: "status.launching" },
  { key: "draft", color: "bg-muted-text", label: "status.draft" },
] as const;

type LaunchStage = (typeof launchStages)[number]["key"];
type OverviewSection = "launch" | "audit" | "announcements";
type AnnouncementFilter = "upcoming" | "published" | "failed";

function launchStage(status: LaunchStage | "failed" | "partially_failed" | "archived"): LaunchStage | undefined {
  if (status === "failed" || status === "partially_failed") return "attention";
  return status === "archived" ? undefined : status;
}

export function OverviewPage() {
  return (
    <AuthGate>
      <OverviewContent />
    </AuthGate>
  );
}

function OverviewContent() {
  const { t, formatNumber } = useSettings();
  const groups = useAllGroups();
  const workspaceScope = useWorkspaceScope();
  const visibleGroups = workspaceScope.filterGroups(groups.data);
  const google = useGoogleStatus();
  const slack = useSlackStatus();
  const [stageFilter, setStageFilter] = useState<LaunchStage>();
  const [section, setSection] = useState<OverviewSection>("launch");
  const [announcementFilter, setAnnouncementFilter] = useState<AnnouncementFilter>("upcoming");
  const onboarding = useQueries({
    queries: (visibleGroups ?? []).map((group) => groupOnboardingOptions(group.id)),
  });
  const onboardingFailed = groups.isError || onboarding.some((report) => report.isError);
  const onboardingPending = groups.isPending || onboarding.some((report) => report.isPending);
  const totalStudents = onboarding.reduce((total, report) => total + (report.data?.summary.expectedCount ?? 0), 0);
  const joinedStudents = onboarding.reduce((total, report) => total + (report.data?.summary.joinedCount ?? 0), 0);
  const activeGroups = visibleGroups?.filter((group) => group.status !== "draft" && group.status !== "archived");
  const stages = launchStages.map((stage) => ({
    ...stage, count: visibleGroups?.filter((group) => launchStage(group.status) === stage.key).length ?? 0,
  }));
  const displayStages = stages.filter(({ count }) => count > 0);
  const filter = displayStages.some(({ key }) => key === stageFilter) ? stageFilter : undefined;
  const attentionCount = stages[0].count;
  const recentGroups = visibleGroups?.filter((group) => {
    const stage = launchStage(group.status);
    return stage && (!filter || stage === filter);
  }).sort((a, b) => {
    return Number(launchStage(b.status) === "attention") - Number(launchStage(a.status) === "attention") || Date.parse(b.createdAt) - Date.parse(a.createdAt);
  }).slice(0, 5);
  const percent = totalStudents > 0 ? Math.round((joinedStudents / totalStudents) * 1000) / 10 : 0;
  const rosterReady = !onboardingPending && !onboardingFailed;

  return (
    <div className="cyber-dashboard space-y-5">
      <div className="dashboard-hero mb-5">
        <div className="dashboard-hero-copy">
          <PageTitle title={t("overview.title")} sub={t("overview.summary")} />
        </div>
        <a href="#/groups/new" className="app-action">{t("form.title")} <IconBack className="h-4 w-4 rotate-180" /></a>
      </div>

      <Card className="flex flex-wrap items-center justify-between gap-4 p-4">
        <div><h2 className="text-sm font-semibold text-text">{t("workspace.selector")}</h2>
          <p className="mt-1 text-xs text-muted">{t("workspace.scopeHint")}</p></div>
        <div className="flex flex-wrap items-center gap-3">
          {workspaceScope.workspacesFailed ? (
            <a href="#/integrations" className="rounded-full border border-border bg-warn-bg px-3 py-1 text-xs font-semibold text-warn-text">
              {t("workspace.loadFailed")}
            </a>
          ) : (
            <span className="rounded-full border border-border bg-success-bg px-3 py-1 text-xs font-semibold text-success-text" role="status">
              {t("integrations.connectedSlackWorkspaces", { count: workspaceScope.workspacesPending ? "…" : workspaceScope.workspaces.length })}
            </span>
          )}
          <select aria-label={t("workspace.selector")} className="min-h-10 min-w-64 rounded-md border border-border bg-surface px-3 text-sm text-text"
            value={workspaceScope.selectedWorkspaceId} disabled={workspaceScope.workspacesPending || workspaceScope.assignmentsPending}
            onChange={(event) => workspaceScope.setSelectedWorkspaceId(event.target.value)}>
            <option value="">{t("workspace.all")}</option>
            <option value="__default__">{t("workspace.default")}</option>
            {workspaceScope.workspaces.map((workspace) => <option key={workspace.id} value={workspace.id}>{workspace.teamName}</option>)}
          </select>
        </div>
      </Card>

      <div className="flex flex-wrap gap-2" role="tablist" aria-label={t("overview.sections")}>
        {(["launch", "audit", "announcements"] as const).map((key) => (
          <button key={key} type="button" role="tab" aria-selected={section === key}
            className={`min-h-10 rounded-lg border px-4 text-sm font-semibold transition ${section === key ? "border-primary bg-primary text-on-primary" : "border-border bg-surface text-text hover:border-primary"}`}
            onClick={() => setSection(key)}>{t(`overview.section.${key}`)}</button>
        ))}
      </div>

      {section === "launch" && <>
      <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <Card className="p-5">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2 className="font-bold text-text">{t("overview.launchStatus")}</h2>
            <a href="#/groups" className="text-sm font-semibold text-primary hover:underline">{t("nav.groups")}</a>
          </div>
          {attentionCount > 0 && (
            <p className="mt-3 border-s-2 border-warn-text bg-warn-bg px-3 py-2 text-sm font-semibold text-warn-text">
              {t("overview.attentionCount", { count: formatNumber(attentionCount) })}
            </p>
          )}
          {!!displayStages.length && (
            <div className="dashboard-stage-chart mt-4 border border-border p-3">
              <div aria-hidden="true" className="flex h-3 gap-0.5">
                {displayStages.map(({ key, color, count }) => (
                  <span key={key} className={color} style={{ flex: count }} />
                ))}
              </div>
              <div className="mt-3 flex flex-wrap gap-2" role="group" aria-label={t("overview.launchStatus")}>
                {displayStages.map(({ key, color, label, count }) => (
                  <button key={key} type="button" aria-pressed={filter === key}
                    className="dashboard-stage-filter inline-flex min-h-9 items-center gap-2 border border-border bg-surface px-2 text-xs text-text hover:border-primary"
                    onClick={() => setStageFilter(filter === key ? undefined : key)}>
                    <span aria-hidden="true" className={`size-2 shrink-0 ${color}`} />
                    {t(label)} <strong>{formatNumber(count)}</strong>
                  </button>
                ))}
              </div>
            </div>
          )}
          {groups.isPending && <p role="status" className="mt-3 text-sm text-muted">{t("list.loading")}</p>}
          {groups.isError && <p role="alert" className="mt-3 text-sm text-danger-text">{t("list.error")}</p>}
          {recentGroups?.length === 0 && <p className="mt-3 text-sm text-muted">{t("list.empty")}</p>}
          <ul className="mt-2 divide-y divide-border">
            {recentGroups?.map((group) => (
              <li key={group.id} className="flex flex-wrap items-center justify-between gap-2 py-3 text-sm">
                <div className="min-w-0">
                  <a className="break-words font-semibold text-text hover:text-primary hover:underline" href={`#/group-details/${group.id}`}>{group.name}</a>
                  <p className="break-words text-xs text-muted">{group.courseName}</p>
                </div>
                <StatusBadge status={group.status} t={t} />
              </li>
            ))}
          </ul>
        </Card>

        <Card className="p-5">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2 className="font-bold text-text">{t("overview.integrationStatus")}</h2>
            <a href="#/integrations" className="text-sm font-semibold text-primary hover:underline">{t("nav.integrations")}</a>
          </div>
          <div className="mt-2 divide-y divide-border">
            <div className="flex items-center justify-between gap-2 py-3">
              <span className="flex items-center gap-2 text-sm font-medium text-text">
                <span aria-hidden="true" className="dashboard-provider-icon bg-soft text-info-text">S</span>Slack
              </span>
              {slack.isPending ? (
                <span className="text-xs text-muted">{t("integrations.checking")}</span>
              ) : slack.isError ? (
                <StatusBadge status="failed" t={t} />
              ) : (
                <StatusBadge status={slack.data.status} t={t} />
              )}
            </div>
            <div className="flex items-center justify-between gap-2 py-3">
              <span className="flex items-center gap-2 text-sm font-medium text-text">
                <span aria-hidden="true" className="dashboard-provider-icon bg-soft text-info-text">G</span>Google
              </span>
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
      </div>

      <div className="dashboard-metrics grid grid-cols-2 gap-px xl:grid-cols-4">
        <StatCard icon={<IconGroups />} label={t("overview.totalGroups")} value={visibleGroups ? formatNumber(visibleGroups.length) : "—"} />
        <StatCard icon={<IconStudents />} label={t("overview.totalStudents")} value={rosterReady ? formatNumber(totalStudents) : "—"} />
        <StatCard icon={<IconAutomation />} label={t("overview.activeGroups")} value={activeGroups ? formatNumber(activeGroups.length) : "—"} />
        <StatCard
          icon={<IconOverview />}
          label={t("overview.onboardingProgress")}
          value={rosterReady ? `${formatNumber(percent)}%` : "—"}
          sub={rosterReady ? t("overview.joinedOf", { joined: joinedStudents, total: totalStudents }) : t(onboardingFailed ? "onboarding.error" : "onboarding.loading")}
          visual={rosterReady && totalStudents > 0 && <span aria-hidden="true" className="dashboard-progress-ring"
            style={{ background: `conic-gradient(var(--primary) 0 ${Math.min(percent, 100)}%, var(--border) 0)` }} />}
        />
      </div>
      </>}

      {section === "audit" && <AuditNotifications />}
      {section === "announcements" && <>
        <div className="flex flex-wrap gap-2 border-b border-border pb-2" role="tablist" aria-label={t("announcements.filter")}>
          {(["upcoming", "published", "failed"] as const).map((key) => (
            <button key={key} type="button" role="tab" aria-selected={announcementFilter === key}
              className={`min-h-10 border-b-2 px-4 text-sm font-semibold transition ${announcementFilter === key ? "border-primary text-primary" : "border-transparent text-muted hover:border-border hover:text-text"}`}
              onClick={() => setAnnouncementFilter(key)}>{t(`announcements.filter.${key}`)}</button>
          ))}
        </div>
        <AuditNotifications key="announcements" loadNotifications={false} showFeed={false}>
          {(authorization, onUnauthorized, reload) => visibleGroups && (
            <DashboardAnnouncements groups={visibleGroups} authorization={authorization}
              onUnauthorized={onUnauthorized} reload={reload} filter={announcementFilter} />
          )}
        </AuditNotifications>
      </>}
    </div>
  );
}
