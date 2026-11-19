import { useSettings } from "../i18n";
import { AuditNotifications } from "../components/AuditNotifications";
import { Card, Chip, PageTitle } from "../components/ui";

const FEATURES = [
  { titleKey: "automation.onboardingChecks", descKey: "automation.onboardingChecksDesc" },
  { titleKey: "automation.retries", descKey: "automation.retriesDesc" },
  { titleKey: "automation.jobs", descKey: "automation.jobsDesc" },
] as const;

export function AutomationPage() {
  const { t } = useSettings();
  return (
    <div className="space-y-6">
      <PageTitle title={t("automation.title")} />
      <AuditNotifications />
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        {FEATURES.map((f) => (
          <Card key={f.titleKey} className="border-dashed p-5">
            <div className="flex items-center gap-2">
              <Chip tone="info">{t("common.comingSoon")}</Chip>
            </div>
            <h3 className="mt-3 text-sm font-semibold text-text">{t(f.titleKey)}</h3>
            <p className="mt-1 text-xs text-muted">{t(f.descKey)}</p>
          </Card>
        ))}
      </div>
    </div>
  );
}
