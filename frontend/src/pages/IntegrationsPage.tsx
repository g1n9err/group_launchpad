import { googleConnectUrl, useGoogleStatus, useSlackStatus } from "../lib/queries";
import { useSettings } from "../i18n";
import { Card, PageTitle, StatusBadge } from "../components/ui";
import { apiUrl } from "../lib/api";
import { getAuthHeader } from "../lib/auth";
import { PlatformLogo, type Platform } from "../components/PlatformLogo";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

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

async function connectSlack() {
  const response = await fetch(apiUrl("/api/integrations/slack/connect"), {
    headers: { ...getAuthHeader(), Accept: "application/json" }, cache: "no-store",
  });
  if (!response.ok) throw new Error(String(response.status));
  const body = (await response.json()) as { url?: string };
  if (!body.url) throw new Error("Slack did not return an authorization URL");
  window.location.assign(body.url);
}

function IntegrationCard({
  name,
  mark,
  desc,
  status,
  statusT,
  pending,
  error,
  errorMessage,
  action,
}: {
  name: string;
  mark: Platform;
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
        <div className="flex items-start gap-2.5">
          <PlatformLogo platform={mark} />
          <div>
            <h3 className="text-sm font-semibold text-text">{name}</h3>
            <p className="mt-1 text-xs text-muted">{desc}</p>
          </div>
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
  const queryClient = useQueryClient();
  const [linkingWorkspace, setLinkingWorkspace] = useState("");
  const [workspaceLinkError, setWorkspaceLinkError] = useState(false);
  const [slackConnectError, setSlackConnectError] = useState(false);
  const google = useGoogleStatus();
  const slack = useSlackStatus();
  const workspaces = useQuery({
    queryKey: ["slack-workspaces"],
    queryFn: async () => {
      const response = await fetch(apiUrl("/api/integrations/slack/workspaces"), { headers: getAuthHeader(), cache: "no-store" });
      if (!response.ok) throw new Error(String(response.status));
      return (await response.json()) as { data: { id: string; teamId: string; teamName: string; status: string; googleAccountId?: string; googleAccountEmail?: string }[] };
    },
  });
  const googleAccounts = useQuery({
    queryKey: ["google-workspaces"],
    queryFn: async () => {
      const response = await fetch(apiUrl("/api/integrations/google/workspaces"), { headers: getAuthHeader(), cache: "no-store" });
      if (!response.ok) throw new Error(String(response.status));
      return (await response.json()) as { data: { id: string; accountEmail: string; status: string }[] };
    },
  });
  const loadError = (error: unknown) =>
    (error as { error?: { code?: string } } | null)?.error?.code === "NOT_READY"
      ? t("integrations.notConfigured")
      : t("integrations.loadError");

  async function setWorkspaceGoogleAccount(slackWorkspaceId: string, googleAccountId: string) {
    setLinkingWorkspace(slackWorkspaceId);
    setWorkspaceLinkError(false);
    try {
      const response = await fetch(apiUrl(`/api/integrations/slack/workspaces/${slackWorkspaceId}/google-account`), {
        method: "PUT", headers: { ...getAuthHeader(), "Content-Type": "application/json" },
        body: JSON.stringify({ googleAccountId }),
      });
      if (!response.ok) throw new Error(String(response.status));
      await queryClient.invalidateQueries({ queryKey: ["slack-workspaces"] });
    } catch { setWorkspaceLinkError(true); }
    finally { setLinkingWorkspace(""); }
  }

  return (
    <div className="space-y-6">
      <PageTitle title={t("integrations.title")} />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <IntegrationCard
          name={t("integrations.slack")}
          mark="slack"
          desc={slack.data?.teamName ? t("integrations.workspace", { name: slack.data.teamName }) : t("integrations.slackDesc")}
          status={slack.data?.status}
          statusT={t}
          pending={slack.isPending}
          error={slack.isError}
          errorMessage={loadError(slack.error)}
          action={
            <div className="flex flex-wrap gap-2">
            <button
              type="button"
              disabled={slack.isFetching}
              onClick={() => void slack.refetch()}
              className="inline-block rounded-md border border-border px-3 py-1.5 text-xs font-semibold text-text transition-colors hover:bg-surface-muted disabled:opacity-50"
            >
              {t("integrations.checkSlack")}
            </button>
            <button type="button" onClick={() => { setSlackConnectError(false); void connectSlack().catch(() => setSlackConnectError(true)); }}
              className="inline-block rounded-md bg-primary px-3 py-1.5 text-xs font-semibold text-on-primary transition-colors hover:bg-primary-hover">
              {t("integrations.connectSlack")}
            </button>
            {slackConnectError && <p className="w-full text-xs text-danger-text">{t("integrations.slackConnectFailed")}</p>}
            </div>
          }
        />

        <IntegrationCard
          name={t("integrations.google")}
          mark="classroom"
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
                {t("integrations.connectGoogleAccount")}
              </button>
            ) : undefined
          }
        />

      </div>

      {workspaces.data?.data && workspaces.data.data.length > 0 && (
        <Card className="p-5">
          <h2 className="text-sm font-semibold text-text">{t("integrations.connectedSlackWorkspaces", { count: workspaces.data.data.length })}</h2>
          <ul className="mt-3 divide-y divide-border">
            {workspaces.data.data.map((workspace) => <li key={workspace.id} className="flex flex-wrap items-center justify-between gap-3 py-3 text-sm">
              <div><span>{workspace.teamName} <span className="text-muted">({workspace.teamId})</span></span><StatusBadge status={workspace.status} t={t} /></div>
              <label className="flex flex-wrap items-center gap-2 text-xs text-muted">{t("integrations.formsGoogleAccount")}
                <select className="min-h-9 rounded-md border border-border bg-surface px-2 text-sm text-text" disabled={linkingWorkspace === workspace.id || !googleAccounts.data}
                  value={workspace.googleAccountId ?? ""} onChange={(event) => void setWorkspaceGoogleAccount(workspace.id, event.target.value)}>
                  <option value="">{t("integrations.chooseGoogleAccount")}</option>
                  {(googleAccounts.data?.data ?? []).map((account) => <option key={account.id} value={account.id}>{account.accountEmail}</option>)}
                </select>
              </label>
            </li>)}
          </ul>
          {workspaceLinkError && <p className="mt-3 text-xs text-danger-text">{t("integrations.workspaceLinkFailed")}</p>}
        </Card>
      )}
      {googleAccounts.data?.data && googleAccounts.data.data.length > 0 && (
        <Card className="p-5">
          <h2 className="text-sm font-semibold text-text">{t("integrations.googleAccounts", { count: googleAccounts.data.data.length })}</h2>
          <ul className="mt-3 divide-y divide-border">
            {googleAccounts.data.data.map((account) => <li key={account.id} className="flex items-center justify-between gap-3 py-2 text-sm">
              <span>{account.accountEmail}</span><StatusBadge status={account.status} t={t} />
            </li>)}
          </ul>
        </Card>
      )}

      <Card className="p-5">
        <h2 className="text-base font-bold">{t("integrations.googleDesc")}</h2>
        <p className="mt-1 text-sm text-muted">{t("integrations.usesGoogle")}</p>
        <ul className="mt-3 divide-y divide-border">
          {([
            { name: "integrations.classroom", route: "groups", action: "nav.groups", mark: "classroom" },
            { name: "integrations.drive", route: "groups", action: "nav.groups", mark: "drive" },
            { name: "integrations.forms", route: "intake-forms", action: "nav.intakeForms", mark: "forms" },
          ] as const).map(({ name, route, action, mark }) => (
            <li key={name} className="flex flex-wrap items-center justify-between gap-2 py-3 text-sm">
              <span className="flex items-center gap-2 font-medium"><PlatformLogo platform={mark} />{t(name)}</span>
              <a className="font-semibold text-primary hover:underline" href={`#/${route}`}>{t(action)}</a>
            </li>
          ))}
        </ul>
      </Card>
    </div>
  );
}
