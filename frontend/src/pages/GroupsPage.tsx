// Groups page: list + create form + details — preserves ALL existing
// functionality (student ingestion, onboarding panel, statuses).

import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCreateGroup, useGroupOnboarding, useAllGroups } from "../lib/queries";
import { AuthGate } from "../components/AuthGate";
import { useSettings } from "../i18n";
import { api } from "../lib/api";
import { useAuthUser } from "../lib/auth";
import { navigate, useRoute } from "../lib/router";
import { Card, OnboardingBadge, PageTitle, StatusBadge } from "../components/ui";
import { IconBack } from "../components/icons";
import { GroupLaunch } from "../components/GroupLaunch";
import { GroupRosterEditor } from "../components/GroupRosterEditor";
import { GroupAnnouncementsPanel } from "../components/GroupAnnouncementsPanel";
import { apiUrl } from "../lib/api";
import { getAuthHeader } from "../lib/auth";
import { useWorkspaceScope } from "../lib/workspaceScope";

function GroupSlackWorkspace({ groupId }: { groupId: string }) {
  const { t } = useSettings();
  const queryClient = useQueryClient();
  const assignment = useQuery({
    queryKey: ["group-slack-workspace", groupId],
    queryFn: async () => {
      const response = await fetch(apiUrl(`/api/groups/${groupId}/slack/workspace`), { headers: getAuthHeader(), cache: "no-store" });
      if (!response.ok) throw new Error(String(response.status));
      return (await response.json()) as { workspaceId: string };
    },
  });
  const workspaces = useQuery({
    queryKey: ["slack-workspaces"],
    queryFn: async () => {
      const response = await fetch(apiUrl("/api/integrations/slack/workspaces"), { headers: getAuthHeader(), cache: "no-store" });
      if (!response.ok) throw new Error(String(response.status));
      return (await response.json()) as { data: { id: string; teamName: string }[] };
    },
  });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  async function choose(workspaceId: string) {
    setSaving(true); setError("");
    try {
      const response = await fetch(apiUrl(`/api/groups/${groupId}/slack/workspace`), {
        method: "PUT", headers: { ...getAuthHeader(), "Content-Type": "application/json" },
        body: JSON.stringify({ workspaceId }),
      });
      if (!response.ok) throw new Error("save");
      await queryClient.invalidateQueries({ queryKey: ["group-slack-workspace", groupId] });
      await queryClient.invalidateQueries({ queryKey: ["group-slack-workspaces"] });
    } catch { setError(t("groups.slackWorkspaceSaveFailed")); }
    finally { setSaving(false); }
  }
  if (workspaces.data?.data.length === 0) return null;
  return <Card className="p-5">
    <h2 className="text-sm font-semibold text-text">{t("groups.slackWorkspace")}</h2>
    <p className="mt-1 text-xs text-muted">{t("groups.slackWorkspaceHint")}</p>
    <select aria-label={t("groups.slackWorkspace")} disabled={saving || workspaces.isPending || assignment.isPending}
      className="mt-3 min-h-10 w-full max-w-md rounded-md border border-border bg-surface px-3 text-sm text-text"
      value={assignment.data?.workspaceId ?? ""} onChange={(e) => void choose(e.target.value)}>
      <option value="">{t("groups.defaultSlackWorkspace")}</option>
      {(workspaces.data?.data ?? []).map((w) => <option key={w.id} value={w.id}>{w.teamName}</option>)}
    </select>
    {error && <p className="mt-2 text-xs text-danger-text">{error}</p>}
  </Card>;
}

function CreateGroupForm() {
  const { t } = useSettings();
  const [name, setName] = useState("");
  const [courseName, setCourseName] = useState("");
  const [startDate, setStartDate] = useState("");
  const [studentEmails, setStudentEmails] = useState("");
  const [staffEmails, setStaffEmails] = useState("");
  const [lecturerEmails, setLecturerEmails] = useState("");
  const [apiError, setApiError] = useState<{ code?: string; fieldErrors?: Record<string, string>; message: string } | null>(null);
  const create = useCreateGroup();

  const fieldError = (field: string) => apiError?.fieldErrors?.[field];
  const localizedMessage = () => {
    if (!apiError) return "";
    const key = `error.${apiError.code ?? "default"}` as Parameters<typeof t>[0];
    const known = t(key);
    return known !== key ? known : apiError.message;
  };

  const inputClass = (field: string) =>
    `w-full rounded-md border bg-surface px-3 py-2 text-sm text-text outline-none transition-colors focus:ring-2 ${
      fieldError(field) ? "border-danger-ring focus:ring-danger-ring/50" : "border-border focus:ring-primary/40"
    }`;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setApiError(null);
    const emails = (value: string) => value.split(/[\s,]+/).map((s) => s.trim()).filter((s) => s.length > 0);
    try {
      const group = await create.mutateAsync({ name, courseName, startDate,
        studentEmails: emails(studentEmails), staffEmails: emails(staffEmails), lecturerEmails: emails(lecturerEmails) });
      if (group) navigate(`/group-details/${group.id}`);
    } catch (err) {
      setApiError(err as { code?: string; fieldErrors?: Record<string, string>; message: string });
    }
  }

  return (
    <Card className="p-6">

      <form onSubmit={onSubmit}>
        <div className="mt-4 grid gap-4 md:grid-cols-2">
          <div>
            <label htmlFor="name" className="mb-1 block text-sm font-medium text-text">{t("form.name")}</label>
            <input id="name" className={inputClass("name")} value={name} onChange={(e) => setName(e.target.value)} placeholder={t("form.namePlaceholder")} />
            {fieldError("name") && <p className="mt-1 text-xs text-danger-text">{fieldError("name")}</p>}
          </div>
          <div>
            <label htmlFor="courseName" className="mb-1 block text-sm font-medium text-text">{t("form.course")}</label>
            <input id="courseName" className={inputClass("courseName")} value={courseName} onChange={(e) => setCourseName(e.target.value)} placeholder={t("form.coursePlaceholder")} />
            {fieldError("courseName") && <p className="mt-1 text-xs text-danger-text">{fieldError("courseName")}</p>}
          </div>
          <div>
            <label htmlFor="startDate" className="mb-1 block text-sm font-medium text-text">{t("form.startDate")}</label>
            <input id="startDate" type="date" className={inputClass("startDate")} value={startDate} onChange={(e) => setStartDate(e.target.value)} />
            {fieldError("startDate") && <p className="mt-1 text-xs text-danger-text">{fieldError("startDate")}</p>}
          </div>
        </div>

        <div className="mt-4 grid gap-4 md:grid-cols-2">
          <div>
            <label htmlFor="staffEmails" className="mb-1 block text-sm font-medium text-text">{t("form.staffEmails")}</label>
            <textarea id="staffEmails" rows={3} required className={inputClass("staffEmails")} value={staffEmails} onChange={(e) => setStaffEmails(e.target.value)} placeholder={t("form.staffEmailsPlaceholder")} />
            {fieldError("staffEmails") && <p className="mt-1 text-xs text-danger-text">{fieldError("staffEmails")}</p>}
          </div>
          <div>
            <label htmlFor="lecturerEmails" className="mb-1 block text-sm font-medium text-text">{t("form.lecturerEmails")}</label>
            <textarea id="lecturerEmails" rows={3} required className={inputClass("lecturerEmails")} value={lecturerEmails} onChange={(e) => setLecturerEmails(e.target.value)} placeholder={t("form.staffEmailsPlaceholder")} />
            {fieldError("lecturerEmails") && <p className="mt-1 text-xs text-danger-text">{fieldError("lecturerEmails")}</p>}
          </div>
        </div>

        <div className="mt-4">
          <label htmlFor="studentEmails" className="mb-1 block text-sm font-medium text-text">{t("form.students")}</label>
          <textarea id="studentEmails" rows={4} className={inputClass("studentEmails")} value={studentEmails} onChange={(e) => setStudentEmails(e.target.value)} placeholder={t("form.studentsPlaceholder")} />
          <p className="mt-1 text-xs text-muted">{t("form.studentsHint")}</p>
          {fieldError("studentEmails") && <p className="mt-1 text-xs text-danger-text">{fieldError("studentEmails")}</p>}
        </div>

        {apiError && !apiError.fieldErrors && (
          <p className="mt-3 rounded-md bg-danger-bg px-3 py-2 text-sm text-danger-text">{localizedMessage()}</p>
        )}

        <button
          type="submit"
          disabled={create.isPending}
          className="mt-4 rounded-md bg-primary px-4 py-2 text-sm font-semibold text-on-primary transition-colors hover:bg-primary-hover disabled:opacity-50"
        >
          {create.isPending ? t("form.submitting") : t("form.submit")}
        </button>
      </form>
    </Card>
  );
}

function GroupsList() {
  const { t, formatDate } = useSettings();
  const { data, isPending, isError } = useAllGroups();
  const workspaceScope = useWorkspaceScope();
  const visibleGroups = workspaceScope.filterGroups(data);

  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between px-6 py-4">
        <h2 className="text-base font-semibold text-text">{t("list.title")}</h2>
        {visibleGroups && <span className="text-sm text-muted">{t("list.total", { count: visibleGroups.length })}</span>}
      </div>

      {isPending && <p className="px-6 pb-6 text-sm text-muted">{t("list.loading")}</p>}
      {isError && <p className="px-6 pb-6 text-sm text-danger-text">{t("list.error")}</p>}
      {visibleGroups && visibleGroups.length === 0 && <p className="px-6 pb-6 text-sm text-muted">{t("list.empty")}</p>}

      {visibleGroups && visibleGroups.length > 0 && (
        <div className="overflow-x-auto">
          <table className="app-table w-full text-left text-sm">
            <thead>
              <tr className="border-t border-border bg-surface-muted/50 text-xs uppercase tracking-wide text-muted">
                <th className="px-4 py-2 font-semibold">{t("list.colGroup")}</th>
                <th className="px-4 py-2 font-semibold">{t("list.colCourse")}</th>
                <th className="px-4 py-2 font-semibold">{t("list.colStarts")}</th>
                <th className="px-4 py-2 font-semibold">{t("list.colStatus")}</th>
                <th className="px-4 py-2 font-semibold">{t("list.colSlackWorkspace")}</th>
                <th className="px-4 py-2 font-semibold">{t("list.colCreated")}</th>
              </tr>
            </thead>
            <tbody>
              {visibleGroups.map((group) => (
                <tr
                  key={group.id}
                  className="border-t border-border transition-colors hover:bg-surface-muted/60"
                >
                  <td className="px-4 py-3 font-semibold text-text">
                    <a className="text-primary hover:underline" href={`#/group-details/${group.id}`}>{group.name}</a>
                  </td>
                  <td className="px-4 py-3 text-muted">{group.courseName}</td>
                  <td className="px-4 py-3 text-muted">{group.startDate}</td>
                  <td className="px-4 py-3"><StatusBadge status={group.status} t={t} /></td>
                  <td className="px-4 py-3 text-muted">{workspaceScope.assignmentByGroup.get(group.id)?.workspaceName ?? t("groups.defaultSlackWorkspace")}</td>
                  <td className="px-4 py-3 text-muted">{formatDate(group.createdAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

function GroupDetails({ groupId }: { groupId: string }) {
  const { t } = useSettings();
  const [rosterView, setRosterView] = useState<"students" | "staff">("students");
  const [groupView, setGroupView] = useState<"launch" | "members" | "announcements">("launch");
  const user = useAuthUser();
  const isStaff = user?.role === "admin" || user?.role === "coordinator";
  const isGroupReader = user?.role === "lecturer" || user?.role === "ta";
  const { data, isPending, isError, refetch } = useGroupOnboarding(groupId, isStaff);
  const groups = useAllGroups();
  const group = groups.data?.find((g) => g.id === groupId);
  const resources = useQuery({
    queryKey: ["group-resources", groupId],
    enabled: isGroupReader,
    queryFn: async () => {
      const { data, error } = await api.GET("/api/groups/{groupId}/resources", { params: { path: { groupId } } });
      if (!data) throw error ?? new Error("Could not load group resources");
      return data;
    },
  });

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-2">
        <button
          onClick={() => navigate("/groups")}
          className="flex items-center gap-1 rounded-md border border-border bg-surface px-3 py-1.5 text-xs font-semibold text-text transition-colors hover:bg-surface-muted"
        >
          <IconBack className="h-4 w-4" />
          {t("groups.backToList")}
        </button>
      </div>

      {group && (
        <Card className="p-6">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h1 className="text-xl font-bold text-text">{group.name}</h1>
              <p className="mt-1 text-sm text-muted">{t("onboarding.subtitle", { course: group.courseName })}</p>
            </div>
            <StatusBadge status={group.status} t={t} />
          </div>
        </Card>
      )}

      {isStaff && (
        <div className="flex flex-wrap gap-2" role="tablist" aria-label={t("groups.details")}>
          {(["launch", "members", "announcements"] as const).map((view) => (
            <button key={view} type="button" role="tab" aria-selected={groupView === view}
              className={`min-h-10 rounded-lg border px-4 text-sm font-semibold transition ${groupView === view ? "border-primary bg-primary text-on-primary" : "border-border bg-surface text-text hover:border-primary"}`}
              onClick={() => setGroupView(view)}>
              {t(view === "launch" ? "launch.title" : view === "members" ? "onboarding.membersTitle" : "announcements.title")}
            </button>
          ))}
        </div>
      )}
      {isStaff && groupView === "launch" && <GroupLaunch key={groupId} groupId={groupId} />}
      {isStaff && group && <GroupSlackWorkspace key={groupId} groupId={groupId} />}
      {group && (isGroupReader || (isStaff && groupView === "announcements")) && <GroupAnnouncementsPanel key={groupId} groupId={groupId} />}

      {isGroupReader && (
        <Card className="p-6">
          <h2 className="text-base font-semibold text-text">{t("groups.resources")}</h2>
          {resources.isPending && <p className="mt-4 text-sm text-muted">{t("list.loading")}</p>}
          {resources.isError && <p role="alert" className="mt-4 text-sm text-danger-text">{t("groups.resourcesError")}</p>}
          {resources.data?.length === 0 && <p className="mt-4 text-sm text-muted">{t("groups.resourcesEmpty")}</p>}
          {resources.data && resources.data.length > 0 && (
            <ul className="mt-4 divide-y divide-border">
              {resources.data.map((resource) => (
                <li key={`${resource.provider}:${resource.kind}:${resource.ref}`} className="flex flex-wrap items-center justify-between gap-3 py-3">
                  <span className="text-sm font-medium text-text">{resource.name}</span>
                  {resource.ref.startsWith("https://") ? (
                    <a className="text-sm font-semibold text-primary hover:underline" href={resource.ref} target="_blank" rel="noreferrer">
                      {resource.provider} · {resource.kind}
                    </a>
                  ) : (
                    <span className="text-sm text-muted">{resource.provider} · {resource.kind}</span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </Card>
      )}

      {isStaff && groupView === "members" && <Card className="p-6">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-3">
            <h2 className="text-base font-semibold text-text">{t("onboarding.membersTitle")}</h2>
            <div className="inline-flex rounded-md border border-border p-0.5" role="group" aria-label={t("onboarding.membersTitle")}>
              {([
                ["students", "nav.students"],
                ["staff", "onboarding.teachingStaff"],
              ] as const).map(([view, label]) => (
                <button key={view} type="button" aria-pressed={rosterView === view} onClick={() => setRosterView(view)}
                  className={`rounded px-3 py-1.5 text-sm font-medium transition-colors ${rosterView === view
                    ? "bg-primary text-on-primary" : "text-text hover:bg-surface-muted"}`}>
                  {t(label)}
                </button>
              ))}
            </div>
          </div>
          <button
            onClick={() => refetch()}
            className="rounded-md border border-border px-3 py-1.5 text-xs font-semibold text-text transition-colors hover:bg-surface-muted"
          >
            {t("onboarding.refresh")}
          </button>
        </div>

        {isPending && <p className="mt-6 text-sm text-muted">{t("onboarding.loading")}</p>}
        {isError && (
          <div className="mt-6">
            <p className="text-sm text-danger-text">{t("onboarding.error")}</p>
            <button
              onClick={() => refetch()}
              className="mt-2 rounded-md bg-primary px-3 py-1.5 text-xs font-semibold text-on-primary transition-colors hover:bg-primary-hover"
            >
              {t("onboarding.retry")}
            </button>
          </div>
        )}

        {data && group?.status !== "archived" && (
          <GroupRosterEditor key={rosterView} groupId={groupId} mode={rosterView}
            staff={data.staff ?? []} students={data.students.map(({ email }) => email)} onSaved={() => void refetch()} />
        )}

        {data && rosterView === "students" && (
          <>
            <div className="mt-4 grid grid-cols-3 gap-3">
              <div className="rounded-lg bg-surface-muted p-3 text-center">
                <div className="text-xl font-bold text-text">{data.summary.expectedCount}</div>
                <div className="text-xs text-muted">{t("onboarding.expected")}</div>
              </div>
              <div className="rounded-lg bg-success-bg p-3 text-center">
                <div className="text-xl font-bold text-success-text">{data.summary.joinedCount}</div>
                <div className="text-xs text-success-text">{t("onboarding.joined")}</div>
              </div>
              <div className="rounded-lg bg-warn-bg p-3 text-center">
                <div className="text-xl font-bold text-warn-text">{data.summary.pendingCount}</div>
                <div className="text-xs text-warn-text">{t("onboarding.pending")}</div>
              </div>
            </div>

            <div className="mt-4">
              <div className="mb-1 flex items-center justify-between text-xs text-muted">
                <span>{t("onboarding.completion")}</span>
                <span className="font-semibold text-text">{data.summary.completionPercentage}%</span>
              </div>
              <div className="h-2.5 w-full overflow-hidden rounded-full bg-surface-muted">
                <div
                  className="h-full rounded-full bg-success-text transition-all"
                  style={{ width: `${Math.min(data.summary.completionPercentage, 100)}%` }}
                />
              </div>
            </div>

            {data.students.length === 0 ? (
              <p className="mt-6 text-sm text-muted">{t("onboarding.noStudents")}</p>
            ) : (
              <div className="mt-6 overflow-x-auto">
                <table className="app-table w-full text-left text-sm">
                  <thead>
                    <tr className="border-t border-border bg-surface-muted/50 text-xs uppercase tracking-wide text-muted">
                      <th className="px-3 py-2 font-semibold">{t("onboarding.colStudent")}</th>
                      <th className="px-3 py-2 font-semibold">{t("onboarding.colEmail")}</th>
                      <th className="px-3 py-2 font-semibold">{t("onboarding.colStatus")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.students.map((s) => (
                      <tr key={s.userId} className="border-t border-border">
                        <td className="px-3 py-2 text-text">{s.fullName}</td>
                        <td className="px-3 py-2 text-muted">{s.email}</td>
                        <td className="px-3 py-2"><OnboardingBadge status={s.onboardingStatus} t={t} /></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </>
        )}
        {data && rosterView === "staff" && (
          <div className="mt-4 grid gap-4 md:grid-cols-2">
            {([
              ["lecturer", "onboarding.lecturer"],
              ["ta", "onboarding.ta"],
            ] as const).map(([role, label]) => {
              const emails = new Set(data.staff?.filter((member) =>
                member.role === role || (role === "ta" && member.role === "teaching_staff"),
              ).map(({ email }) => email));
              return (
                <section key={role} className="rounded-md border border-border p-3 text-sm">
                  <h3 className="font-semibold text-text">{t(label)}</h3>
                  {emails.size > 0 ? (
                    <ul className="mt-2 space-y-1 text-muted">
                      {[...emails].map((email) => <li key={email} className="break-all">{email}</li>)}
                    </ul>
                  ) : <p className="mt-2 text-muted">{t("onboarding.noStaffRole")}</p>}
                </section>
              );
            })}
          </div>
        )}
      </Card>}
    </div>
  );
}

export function GroupsPage() {
  const { t } = useSettings();
  const route = useRoute();
  const user = useAuthUser();
  const canCreate = user?.role === "admin" || user?.role === "coordinator";

  if (route.page === "group-details" && route.id) {
    return (
      <AuthGate roles={["admin", "coordinator", "lecturer", "ta"]}>
        <GroupDetails key={route.id} groupId={route.id} />
      </AuthGate>
    );
  }

  if (route.page === "groups" && route.id === "new") {
    return (
      <div className="max-w-4xl space-y-6">
        <PageTitle title={t("form.title")} sub={t("form.subtitle")}
          actions={<a className="text-sm font-semibold text-primary hover:underline" href="#/groups">{t("groups.backToList")}</a>} />
        <AuthGate roles={["admin", "coordinator"]}>
          <CreateGroupForm />
        </AuthGate>
      </div>
    );
  }
  if (route.page === "groups" && route.id) return (
    <AuthGate roles={["admin", "coordinator", "lecturer", "ta"]}>
      <GroupDetails key={route.id} groupId={route.id} />
    </AuthGate>
  );

  return (
    <div className="space-y-6">
      <PageTitle title={t("nav.groups")} actions={canCreate ? <a className="app-action" href="#/groups/new">{t("form.title")}</a> : undefined} />
      <AuthGate roles={["admin", "coordinator", "lecturer", "ta"]}>
        <GroupsList />
      </AuthGate>
    </div>
  );
}
