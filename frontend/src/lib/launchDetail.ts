import type { Lang, useSettings } from "../i18n";

type Translate = ReturnType<typeof useSettings>["t"];

export function formatLaunchDetail(platform: string, detail: string, lang: Lang, t: Translate): string {
  if (lang === "en") return detail;

  const codes = [...new Set(detail.match(/\bHTTP\s+[1-5]\d{2}\b|@[A-Z][A-Za-z0-9]+|\b[A-Z]+(?:_[A-Z]+)+\b|\b(?:invalid_grant|insufficient_scope|permission_denied|user_not_found)\b/g))];
  const diagnostic = codes.length ? ` (${codes.join(", ")})` : "";

  if (platform === "classroom") {
    const old = detail.match(/^course created, (?:(\d+) enrollment(?: and | )?)?(?:(\d+) teacher assignment )?failures$/);
    const current = detail.match(/^course exists, (\d+) student and (\d+) teacher access failures$/);
    const counts = current ?? old;
    if (counts) {
      return t("launch.detail.classroomAccess", { students: counts[1] ?? "0", teachers: counts[2] ?? "0" });
    }
  }

  if (platform === "drive" && detail.startsWith("folders created, permissions partial:")) {
    const count = detail.match(/\b(\d+) Drive permission grants failed\b/)?.[1];
    const email = detail.match(/drive: grant (?:writer|reader) to ([^:\s]+):/)?.[1];
    const summary = count ? t("launch.detail.driveAccessCount", { count }) : t("launch.detail.driveAccess");
    return (email ? `${summary} ${t("launch.detail.driveRecipient", { email })}` : summary) + diagnostic;
  }

  if (platform === "slack") {
    const legacy = detail.match(/^(\d+) channel, (\d+) invite, (\d+) welcome failures$/);
    const failures = detail.match(/(\d+) channel and (\d+) welcome failures$/);
    const invites = detail.match(/^(\d+) unsuccessful channel invitations for (\d+) addresses\. (\d+) Slack accounts not found\./);
    if (legacy || failures) {
      const summary = t("launch.detail.slackFailures", {
        channels: legacy?.[1] ?? failures?.[1] ?? "0",
        invites: legacy?.[2] ?? invites?.[1] ?? "0",
        welcomes: legacy?.[3] ?? failures?.[2] ?? "0",
      });
      return invites ? `${summary} ${t("launch.detail.slackAccounts", { addresses: invites[2], missing: invites[3] })}` : summary;
    }
    if (detail === "all Slack channels failed") return t("launch.detail.slackNoChannels");
  }

  if (detail === "not configured") return t("launch.step.skipped");
  return t("launch.detail.failed") + diagnostic;
}
