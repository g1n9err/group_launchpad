// Integrations page: REAL status for Slack + Google (existing endpoints);
// Classroom/Drive/Forms are honest not-implemented placeholders.

import { googleConnectUrl, useGoogleStatus, useSlackStatus } from "../lib/queries";
import { useSettings } from "../i18n";
import { Card, Chip, PageTitle, StatusBadge } from "../components/ui";

function IntegrationCard({
  name,
  desc,
  status,
  statusT,
  pending,
  error,
  notConfigured,
  action,
}: {
  name: string;
  desc: string;
  status?: string;
  statusT: ReturnType<typeof useSettings>["t"];
  pending?: boolean;
  error?: boolean;
  notConfigured?: string;
  action?: React.ReactNode;
}) {
  const { t } = useSettings();
  return (
    <Card className="p-5">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h3 className="text-sm font-semibold text-text">{name}</h3>
          <p className="mt-1 text-xs text-muted">{desc}</p>
        </div>
        {pending ? (
          <span className="text-xs text-muted">{t("integrations.checking")}</span>
        ) : status !== undefined ? (
          <StatusBadge status={status} t={statusT} />
        ) : null}
      </div>
      {error && <p className="mt-3 text-xs text-muted">{notConfigured}</p>}
      {action && <div className="mt-4">{action}</div>}
    </Card>
  );
}

export function IntegrationsPage() {
  const { t } = useSettings();
  const google = useGoogleStatus();
  const slack = useSlackStatus();

  return (
    <div className="space-y-6">
      <PageTitle title={t("integrations.title")} />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
        <IntegrationCard
          name={t("integrations.slack")}
          desc={slack.data?.teamName ? t("integrations.workspace", { name: slack.data.teamName }) : t("integrations.slackDesc")}
          status={slack.data?.status}
          statusT={t}
          pending={slack.isPending}
          error={slack.isError}
          notConfigured={t("integrations.notConfigured")}
        />

        <IntegrationCard
          name={t("integrations.google")}
          desc={google.data?.accountEmail ?? t("integrations.googleDesc")}
          status={google.data?.status}
          statusT={t}
          pending={google.isPending}
          error={google.isError}
          notConfigured={t("integrations.notConfigured")}
          action={
            google.data?.status !== "connected" && !google.isError && !google.isPending ? (
              <a
                href={googleConnectUrl()}
                className="inline-block rounded-md bg-primary px-3 py-1.5 text-xs font-semibold text-on-primary transition-colors hover:bg-primary-hover"
              >
                {t("integrations.connectGoogle")}
              </a>
            ) : undefined
          }
        />

        <Card className="border-dashed p-5">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h3 className="text-sm font-semibold text-text">{t("integrations.classroom")}</h3>
              <p className="mt-1 text-xs text-muted">{t("integrations.googleDesc")}</p>
            </div>
            <Chip tone="muted">{t("integrations.notImplemented")}</Chip>
          </div>
        </Card>

        <Card className="border-dashed p-5">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h3 className="text-sm font-semibold text-text">{t("integrations.drive")}</h3>
            </div>
            <Chip tone="muted">{t("integrations.notImplemented")}</Chip>
          </div>
        </Card>

        <Card className="border-dashed p-5">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h3 className="text-sm font-semibold text-text">{t("integrations.forms")}</h3>
            </div>
            <Chip tone="muted">{t("integrations.notImplemented")}</Chip>
          </div>
        </Card>
      </div>
    </div>
  );
}
