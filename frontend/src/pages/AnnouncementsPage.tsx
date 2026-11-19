// Announcements page: UI scaffolding (PRD §4.8). No scheduling/sending yet —
// honest placeholders, nothing simulated.

import { useSettings } from "../i18n";
import { Card, Chip, PageTitle } from "../components/ui";

export function AnnouncementsPage() {
  const { t } = useSettings();
  const sections = [
    { titleKey: "announcements.create", descKey: "announcements.createDesc" },
    { titleKey: "announcements.schedule", descKey: "announcements.scheduleDesc" },
    { titleKey: "announcements.history", descKey: "announcements.historyDesc" },
  ] as const;
  return (
    <div className="space-y-6">
      <PageTitle title={t("announcements.title")} />
      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        {sections.map((s) => (
          <Card key={s.titleKey} className="border-dashed p-5">
            <Chip tone="info">{t("common.comingSoon")}</Chip>
            <h3 className="mt-3 text-sm font-semibold text-text">{t(s.titleKey)}</h3>
            <p className="mt-1 text-xs text-muted">{t(s.descKey)}</p>
          </Card>
        ))}
      </div>
    </div>
  );
}
