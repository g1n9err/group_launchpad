import { useState } from "react";
import { Card, PageTitle, StatusBadge } from "../components/ui";
import { useSettings } from "../i18n";
import { api } from "../lib/api";
import { fireLock, useAuthUser } from "../lib/auth";
import { navigate } from "../lib/router";

export function AccountPage() {
  const { t } = useSettings();
  const user = useAuthUser();
  const [loggingOut, setLoggingOut] = useState(false);

  async function logout() {
    setLoggingOut(true);
    try {
      if (user) await api.POST("/api/auth/logout");
    } catch {
      // Always clear client-side access, even if the API cannot be reached.
    } finally {
      fireLock();
      setLoggingOut(false);
      navigate("/");
    }
  }

  const roleKey = user ? `auth.role.${user.role}` as Parameters<typeof t>[0] : "auth.role.coordinator";
  return (
    <div className="account-page">
      <PageTitle title={t("account.title")} sub={t("account.passwordNote")} />
      <Card className="account-card p-6 sm:p-8">
        <div className="flex flex-wrap items-center gap-4 border-b border-border pb-6">
          <span className="account-avatar" aria-hidden="true">{(user?.email?.[0] ?? "G").toLocaleUpperCase()}</span>
          <div className="min-w-0 flex-1">
            <h2 className="text-xl font-bold text-text">{user?.email ?? t("account.roleValue")}</h2>
            <p className="mt-1 text-sm text-muted">{t(roleKey)}</p>
          </div>
          <StatusBadge status={user ? "connected" : "disconnected"} t={t} />
        </div>
        <dl className="account-details">
          <div><dt>{t("account.username")}</dt><dd>{user?.email ?? "—"}</dd></div>
          <div><dt>{t("account.role")}</dt><dd>{user ? t(roleKey) : t("account.locked")}</dd></div>
          <div><dt>{t("account.access")}</dt><dd>{t(user ? "account.signedIn" : "account.locked")}</dd></div>
        </dl>
        {user ? (
          <button type="button" disabled={loggingOut} onClick={() => void logout()} className="app-header-control app-header-danger">
            {t(loggingOut ? "login.working" : "account.lock")}
          </button>
        ) : (
          <a href="#/login" className="app-action">{t("account.signIn")}</a>
        )}
      </Card>
    </div>
  );
}
