import { useState, type FormEvent } from "react";
import { useSettings } from "../i18n";
import { api } from "../lib/api";
import { intakeActionClass, intakeButtonClass, intakeInputClass } from "../lib/intakeForm";
import type { components } from "../lib/schema";

type RosterResult = components["schemas"]["GroupRosterUpdateResult"];
type Staff = components["schemas"]["GroupOnboardingStaff"][];
const split = (value: string) => value.split(/[\n,;]+/).map((item) => item.trim()).filter(Boolean);

export function GroupRosterEditor({ groupId, mode, staff, students, onSaved }: {
  groupId: string; mode: "students" | "staff"; staff: Staff; students: string[]; onSaved: () => void;
}) {
  const { t } = useSettings();
  const [editing, setEditing] = useState(false);
  const [studentEmails, setStudentEmails] = useState("");
  const [lecturerEmails, setLecturerEmails] = useState("");
  const [taEmails, setTaEmails] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<RosterResult>();

  function open() {
    setStudentEmails(students.join("\n"));
    setLecturerEmails([...new Set(staff.filter(({ role }) => role === "lecturer").map(({ email }) => email))].join("\n"));
    setTaEmails([...new Set(staff.filter(({ role }) => role === "ta" || role === "teaching_staff").map(({ email }) => email))].join("\n"));
    setError(""); setResult(undefined); setEditing(true);
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (saving) return;
    setSaving(true); setError("");
    const body = mode === "students"
      ? { studentEmails: split(studentEmails) }
      : { lecturerEmails: split(lecturerEmails), taEmails: split(taEmails) };
    try {
      const response = await api.POST("/api/groups/{groupId}/roster", { params: { path: { groupId } }, body });
      if (!response.data) { setError(response.error?.error?.message ?? t("roster.saveFailed")); return; }
      setResult(response.data); setEditing(false); onSaved();
    } catch { setError(t("roster.saveFailed")); }
    finally { setSaving(false); }
  }

  const failedInvites = result?.slack?.invites.filter(({ status }) => status === "failed") ?? [];
  const missingAccounts = new Set(failedInvites.filter(({ error }) => error === "user_not_found").map(({ email }) => email));
  const otherFailures = failedInvites.filter(({ email }) => !missingAccounts.has(email));
  const missingChannels = result?.slack?.channels.filter(({ status }) => status === "failed") ?? [];
  const accessRows = [
    ["launch.drive", result?.driveAccess], ["launch.classroom", result?.classroomAccess],
    ["roster.classroomEmail", result?.classroomEmail],
    ["roster.driveRemoval", result?.driveRemoval], ["roster.classroomRemoval", result?.classroomRemoval], ["roster.slackRemoval", result?.slackRemoval],
  ] as const;
  const hasIssues = result?.slackError || missingChannels.length > 0 || failedInvites.length > 0 ||
    accessRows.some(([, status]) => status && status !== "ready" && status !== "not_provisioned");

  return (
    <div className="mt-4 space-y-3">
      {!editing ? <button type="button" className={intakeButtonClass} onClick={open}>
        {t(mode === "students" ? "roster.editStudents" : "roster.editStaff")}
      </button> : (
        <form className="max-w-xl space-y-3 rounded-md border border-border p-4" onSubmit={(event) => void save(event)}>
          {mode === "students" ? (
            <label className="block text-sm font-medium text-text">{t("roster.studentEmails")}
              <textarea className={`mt-1 ${intakeInputClass}`} rows={6} value={studentEmails} onChange={(event) => setStudentEmails(event.target.value)} placeholder={t("form.studentsPlaceholder")} />
            </label>
          ) : <>
            <label className="block text-sm font-medium text-text">{t("roster.lecturerEmails")}
              <textarea className={`mt-1 ${intakeInputClass}`} rows={4} value={lecturerEmails} onChange={(event) => setLecturerEmails(event.target.value)} placeholder={t("form.staffEmailsPlaceholder")} />
            </label>
            <label className="block text-sm font-medium text-text">{t("roster.taEmails")}
              <textarea className={`mt-1 ${intakeInputClass}`} rows={4} value={taEmails} onChange={(event) => setTaEmails(event.target.value)} placeholder={t("form.staffEmailsPlaceholder")} />
            </label>
          </>}
          <p className="text-xs text-muted">{t("roster.fullListHint")}</p>
          {error && <p role="alert" className="text-sm text-danger-text">{error}</p>}
          <div className="flex flex-wrap gap-2">
            <button type="submit" className={intakeActionClass} disabled={saving}>{t(saving ? "roster.saving" : "roster.saveAndInvite")}</button>
            <button type="button" className={intakeButtonClass} disabled={saving} onClick={() => setEditing(false)}>{t("intake.cancel")}</button>
          </div>
        </form>
      )}
      {result && <div role="status" className="space-y-1 text-sm text-text">
        <p>{t(hasIssues ? "roster.savedWithIssues" : "roster.saved")}</p>
        {result.slackError && <p>{t("roster.slackUnavailable")}</p>}
        {missingAccounts.size > 0 && <p>{t("roster.missingAccounts", { emails: [...missingAccounts].join(", ") })}</p>}
        {otherFailures.length > 0 && <p>{t("roster.inviteFailures", { count: otherFailures.length })}</p>}
        {missingChannels.length > 0 && <p>{t("roster.missingChannels", { channels: missingChannels.map(({ name }) => name).join(", ") })}</p>}
        {accessRows.map(([label, status]) => status && <p key={label}>{t(label)} · {t(label === "roster.classroomEmail" && status === "ready" ? "roster.emailQueued" : `roster.access.${status}`)}</p>)}
      </div>}
    </div>
  );
}
