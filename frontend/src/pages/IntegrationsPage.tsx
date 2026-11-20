import { googleConnectUrl, useGoogleStatus, useSlackStatus } from "../lib/queries";
import { useSettings } from "../i18n";
import { Card, PageTitle, StatusBadge } from "../components/ui";
import { apiUrl } from "../lib/api";
import { getAuthHeader } from "../lib/auth";

// Google connect/reconnect rides the in-memory Bearer session: fetch the
// consent URL with Accept: application/json (the API then answers JSON
// instead of a 302) and navigate the browser there. No Basic-credentials
// dialog, and the token never lands in a URL.
async function connectGoogle(onFallback: () => void) {
  try {
    const response = await fetch(apiUrl("/api/integrations/google/connect"), {
      headers: { ...getAuthHeader(), Accept: "application/json" },
      cache: "no-store",
    });
    if (!response.ok) throw new Error(String(response.status));
    const body = (await response.json()) as { url?: string };
    if (!body.url) throw new Error("no url");
    window.location.assign(body.url);
  } catch {
    onFallback(); // legacy navigation path (Basic dialog) still works
  }
}

function IntegrationCard({
  name,
  desc,
  status,
  statusT,
  pending,
  error,
  errorMessage,
  action,
}: {
  name: string;
  desc: string;
  status?: string;
  statusT: ReturnType<typeof useSettings>["t"];
  pending?: boolean;
  error?: boolean;
  errorMessage?: string;
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
      {error && <p className="mt-3 text-xs text-muted">{errorMessage}</p>}
      {action && <div className="mt-4">{action}</div>}
    </Card>
  );
}

export function IntegrationsPage() {
  const { t } = useSettings();
  const google = useGoogleStatus();
  const slack = useSlackStatus();
  const loadError = (error: unknown) =>
    (error as { error?: { code?: string } } | null)?.error?.code === "NOT_READY"
      ? t("integrations.notConfigured")
      : t("integrations.loadError");

  return (
    <div className="space-y-6">
      <PageTitle title={t("integrations.title")} />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <IntegrationCard
          name={t("integrations.slack")}
          desc={slack.data?.teamName ? t("integrations.workspace", { name: slack.data.teamName }) : t("integrations.slackDesc")}
          status={slack.data?.status}
          statusT={t}
          pending={slack.isPending}
          error={slack.isError}
          errorMessage={loadError(slack.error)}
          action={
            <button
              type="button"
              disabled={slack.isFetching}
              onClick={() => void slack.refetch()}
              className="inline-block rounded-md border border-border px-3 py-1.5 text-xs font-semibold text-text transition-colors hover:bg-surface-muted disabled:opacity-50"
            >
              {t("integrations.checkSlack")}
            </button>
          }
        />

        <IntegrationCard
          name={t("integrations.google")}
          desc={google.data?.accountEmail ?? t("integrations.googleDesc")}
          status={google.data?.status}
          statusT={t}
          pending={google.isPending}
          error={google.isError}
          errorMessage={loadError(google.error)}
          action={
            !google.isError && !google.isPending ? (
              <button
                type="button"
                onClick={() => void connectGoogle(() => window.location.assign(googleConnectUrl()))}
                className="inline-block rounded-md bg-primary px-3 py-1.5 text-xs font-semibold text-on-primary transition-colors hover:bg-primary-hover"
              >
                {google.data?.status === "connected"
                  ? t("integrations.reconnectGoogle")
                  : t("integrations.connectGoogle")}
              </button>
            ) : undefined
          }
        />

      </div>

      <Card className="p-5">
        <h2 className="text-base font-bold">{t("integrations.googleDesc")}</h2>
        <p className="mt-1 text-sm text-muted">{t("integrations.usesGoogle")}</p>
        <ul className="mt-3 divide-y divide-border">
          {([
            { name: "integrations.classroom", route: "groups", action: "nav.groups" },
            { name: "integrations.drive", route: "groups", action: "nav.groups" },
            { name: "integrations.forms", route: "intake-forms", action: "nav.intakeForms" },
          ] as const).map(({ name, route, action }) => (
            <li key={name} className="flex flex-wrap items-center justify-between gap-2 py-3 text-sm">
              <span className="font-medium">{t(name)}</span>
              <a className="font-semibold text-primary hover:underline" href={`#/${route}`}>{t(action)}</a>
            </li>
          ))}
        </ul>
      </Card>
    </div>
  );
}
