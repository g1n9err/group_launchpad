// Groups page: list + create form + details — preserves ALL existing
// functionality (student ingestion, onboarding panel, statuses).

import { useState, type FormEvent } from "react";
import { useCreateGroup, useGroupOnboarding, useGroups } from "../lib/queries";
import { useSettings } from "../i18n";
import { navigate, useRoute } from "../lib/router";
import { Card, OnboardingBadge, PageTitle, StatusBadge } from "../components/ui";
import { IconBack } from "../components/icons";
import { GroupLaunch } from "../components/GroupLaunch";

function CreateGroupForm() {
  const { t } = useSettings();
  const [name, setName] = useState("");
  const [courseName, setCourseName] = useState("");
  const [startDate, setStartDate] = useState("");
  const [studentEmails, setStudentEmails] = useState("");
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
    const emails = studentEmails.split(/[\s,]+/).map((s) => s.trim()).filter((s) => s.length > 0);
    try {
      await create.mutateAsync({ name, courseName, startDate, studentEmails: emails });
      setName(""); setCourseName(""); setStartDate(""); setStudentEmails("");
    } catch (err) {
      setApiError(err as { code?: string; fieldErrors?: Record<string, string>; message: string });
    }
  }

  return (
    <Card className="p-6">
      <h2 className="text-base font-semibold text-text">{t("form.title")}</h2>
      <p className="mt-1 text-sm text-muted">{t("form.subtitle")}</p>

      <form onSubmit={onSubmit}>
        <div className="mt-4 grid gap-4 md:grid-cols-3">
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
  const { data, isPending, isError } = useGroups();

  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between px-6 py-4">
        <h2 className="text-base font-semibold text-text">{t("list.title")}</h2>
        {data && <span className="text-sm text-muted">{t("list.total", { count: data.page.total })}</span>}
      </div>

      {isPending && <p className="px-6 pb-6 text-sm text-muted">{t("list.loading")}</p>}
      {isError && <p className="px-6 pb-6 text-sm text-danger-text">{t("list.error")}</p>}
      {data && data.data.length === 0 && <p className="px-6 pb-6 text-sm text-muted">{t("list.empty")}</p>}

      {data && data.data.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-t border-border bg-surface-muted/50 text-xs uppercase tracking-wide text-muted">
                <th className="px-4 py-2 font-semibold">{t("list.colGroup")}</th>
                <th className="px-4 py-2 font-semibold">{t("list.colStarts")}</th>
                <th className="px-4 py-2 font-semibold">{t("list.colCourse")}</th>
                <th className="px-4 py-2 font-semibold">{t("list.colStatus")}</th>
                <th className="px-4 py-2 font-semibold">{t("list.colCreated")}</th>
              </tr>
            </thead>
            <tbody>
              {data.data.map((group) => (
                <tr
                  key={group.id}
                  onClick={() => navigate(`/groups/${group.id}`)}
                  className="cursor-pointer border-t border-border transition-colors hover:bg-surface-muted/60"
                >
                  <td className="px-4 py-3 font-medium text-text">{group.name}</td>
                  <td className="px-4 py-3 text-muted">{group.courseName}</td>
                  <td className="px-4 py-3 text-muted">{group.startDate}</td>
                  <td className="px-4 py-3"><StatusBadge status={group.status} t={t} /></td>
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
  const { data, isPending, isError, refetch } = useGroupOnboarding(groupId);
  const groups = useGroups();
  const group = groups.data?.data.find((g) => g.id === groupId);

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

      <GroupLaunch key={groupId} groupId={groupId} />

      <Card className="p-6">
        <div className="flex items-center justify-between gap-2">
          <h2 className="text-base font-semibold text-text">{t("onboarding.progressTitle")}</h2>
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

        {data && (
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
                <table className="w-full text-left text-sm">
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
      </Card>
    </div>
  );
}

export function GroupsPage() {
  const { t } = useSettings();
  const route = useRoute();

  if (route.page === "group-details" && route.id) {
    return <GroupDetails groupId={route.id} />;
  }

  return (
    <div className="space-y-6">
      <PageTitle title={t("nav.groups")} sub={t("form.subtitle")} />
      <CreateGroupForm />
      <GroupsList />
    </div>
  );
}
