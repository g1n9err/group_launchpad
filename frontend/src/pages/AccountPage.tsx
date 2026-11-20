import { useEffect, useState } from "react";
import { Card, PageTitle, StatusBadge } from "../components/ui";
import { useSettings } from "../i18n";
import { fireLock, isUnlocked, onAuthChange } from "../lib/auth";
import { navigate } from "../lib/router";

export function AccountPage() {
  const { t } = useSettings();
  const [unlocked, setUnlocked] = useState(isUnlocked());

  useEffect(() => onAuthChange(() => setUnlocked(isUnlocked())), []);

  function lockSession() {
    fireLock();
    navigate("/");
  }

  return (
    <div className="account-page">
      <PageTitle title={t("account.title")} sub={t("account.passwordNote")} />
      <Card className="account-card p-6 sm:p-8">
        <div className="flex flex-wrap items-center gap-4 border-b border-border pb-6">
          <span className="account-avatar" aria-hidden="true">C</span>
          <div className="min-w-0 flex-1">
            <h2 className="text-xl font-bold text-text">{t("account.roleValue")}</h2>
            <p className="mt-1 text-sm text-muted">{t("account.role")}</p>
          </div>
          <StatusBadge status={unlocked ? "connected" : "disconnected"} t={t} />
        </div>
        <dl className="account-details">
          <div><dt>{t("account.username")}</dt><dd>coordinator</dd></div>
          <div><dt>{t("account.access")}</dt><dd>{t(unlocked ? "account.signedIn" : "account.locked")}</dd></div>
        </dl>
        {unlocked ? (
          <button type="button" onClick={lockSession} className="app-header-control app-header-danger">{t("account.lock")}</button>
        ) : (
          <a href="#/login" className="app-action">{t("account.signIn")}</a>
        )}
      </Card>
    </div>
  );
}
