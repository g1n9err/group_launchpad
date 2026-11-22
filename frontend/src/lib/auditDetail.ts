import type { useSettings } from "../i18n";
import type { TranslationKey } from "../i18n/en";

export const auditStatusLabels: Record<string, TranslationKey> = {
  ok: "audit.resolved",
  unlinked: "audit.unlinked",
  missing_or_inaccessible: "audit.missing",
  unavailable: "audit.unavailable",
  archived: "audit.archived",
  provisioned: "audit.provisioned",
  inactive: "audit.inactive",
  trashed: "audit.trashed",
  misconfigured: "audit.misconfigured",
};

export const auditProviders: Record<string, string> = {
  slack: "Slack", google_classroom: "Classroom", google_drive: "Drive", google_forms: "Forms",
};

type Translate = ReturnType<typeof useSettings>["t"];

export function auditNotificationLines(detail: string | undefined, t: Translate): string[] {
  if (!detail) return [];
  let changes: unknown;
  try {
    changes = JSON.parse(detail);
  } catch {
    return [];
  }
  if (!Array.isArray(changes)) return [];

  return changes.flatMap((change: unknown) => {
    if (!change || typeof change !== "object" ||
        !("provider" in change) || typeof change.provider !== "string" ||
        !("kind" in change) || typeof change.kind !== "string" ||
        !("key" in change) || typeof change.key !== "string" ||
        !("after" in change) || typeof change.after !== "string") return [];

    const provider = Object.hasOwn(auditProviders, change.provider) ? auditProviders[change.provider] : undefined;
    const status = Object.hasOwn(auditStatusLabels, change.after) ? auditStatusLabels[change.after] : undefined;
    if (!provider || !status) return [];
    let resource = change.key;
    if (change.kind === "membership") {
      if (change.key === "lecturer") resource = t("notifications.auditLecturer");
      if (change.key === "teaching_staff") resource = t("notifications.auditTeachingStaff");
      if (change.key === "ta") resource = t("notifications.auditTA");
    } else if (change.provider === "google_forms" && change.key === "intake") {
      resource = t("notifications.auditIntake");
    }
    return [`${provider} · ${resource} — ${t(status)}`];
  });
}
