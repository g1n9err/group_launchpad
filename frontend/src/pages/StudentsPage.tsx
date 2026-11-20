// Students page: real roster data assembled from existing per-group
// onboarding endpoints (no backend changes); client-side search/filter.
// Roster data is coordinator-only — AuthGate blocks anonymous access.

import { useMemo, useState } from "react";
import { groupOnboardingOptions, useAllGroups } from "../lib/queries";
import { AuthGate } from "../components/AuthGate";
import { useSettings } from "../i18n";
import { useQueries } from "@tanstack/react-query";
import { Card, OnboardingBadge, PageTitle } from "../components/ui";

interface StudentRow {
  userId: string;
  fullName: string;
  email: string;
  groupName: string;
  onboardingStatus: string;
}

function useAllStudents(): { rows: StudentRow[]; loading: boolean; loadFailed: boolean } {
  const groups = useAllGroups();
  const ids = groups.data?.map((g) => ({ id: g.id, name: g.name })) ?? [];
  const reports = useQueries({
    queries: ids.map((group) => groupOnboardingOptions(group.id)),
  });
  const loading = groups.isPending || reports.some((r) => r.isPending);
  const rows: StudentRow[] = [];
  ids.forEach((g, i) => {
    const data = reports[i]?.data;
    if (!data) return;
    for (const s of data.students) {
      rows.push({
        userId: s.userId,
        fullName: s.fullName,
        email: s.email,
        groupName: g.name,
        onboardingStatus: s.onboardingStatus,
      });
    }
  });
  return { rows, loading, loadFailed: groups.isError || reports.some((report) => report.isError) };
}

function StudentsContent() {
  const { t } = useSettings();
  const { rows, loading, loadFailed } = useAllStudents();
  const [query, setQuery] = useState("");

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return rows;
    return rows.filter(
      (r) =>
        r.fullName.toLowerCase().includes(q) ||
        r.email.toLowerCase().includes(q) ||
        r.groupName.toLowerCase().includes(q),
    );
  }, [rows, query]);

  return (
    <Card className="overflow-hidden">
      <div className="border-b border-border px-5 py-3">
        <input
          type="search"
          aria-label={t("students.searchPlaceholder")}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("students.searchPlaceholder")}
          className="w-full max-w-md rounded-md border border-border bg-surface px-3 py-2 text-sm text-text outline-none focus:ring-2 focus:ring-primary/40"
        />
      </div>

      {loading && <p className="px-5 py-6 text-sm text-muted">{t("list.loading")}</p>}

      {loadFailed && <p role="alert" className="px-5 py-3 text-sm text-danger-text">{t("students.loadError")}</p>}

      {!loading && !loadFailed && filtered.length === 0 && (
        <p className="px-5 py-6 text-sm text-muted">
          {rows.length === 0 ? t("students.none") : t("students.noResults")}
        </p>
      )}

      {!loading && filtered.length > 0 && (
        <div className="overflow-x-auto">
          <table className="app-table w-full text-left text-sm">
            <thead>
              <tr className="border-b border-border bg-surface-muted/50 text-xs uppercase tracking-wide text-muted">
                <th className="px-4 py-2 font-semibold">{t("students.colName")}</th>
                <th className="px-4 py-2 font-semibold">{t("students.colEmail")}</th>
                <th className="px-4 py-2 font-semibold">{t("students.colGroup")}</th>
                <th className="px-4 py-2 font-semibold">{t("students.colOnboarding")}</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((r) => (
                <tr key={`${r.userId}-${r.groupName}`} className="border-b border-border last:border-0">
                  <td className="px-4 py-2.5 font-medium text-text">{r.fullName}</td>
                  <td className="px-4 py-2.5 text-muted">{r.email}</td>
                  <td className="px-4 py-2.5 text-muted">{r.groupName}</td>
                  <td className="px-4 py-2.5"><OnboardingBadge status={r.onboardingStatus} t={t} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

export function StudentsPage() {
  const { t } = useSettings();

  return (
    <div className="space-y-6">
      <PageTitle title={t("students.title")} />
      <AuthGate>
        <StudentsContent />
      </AuthGate>
    </div>
  );
}
