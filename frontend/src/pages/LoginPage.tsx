import { useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Card } from "../components/ui";
import { useSettings } from "../i18n";
import { basicAuthValue, unlock } from "../lib/auth";
import { navigate } from "../lib/router";

export function LoginPage() {
  const { t } = useSettings();
  const queryClient = useQueryClient();
  const [password, setPassword] = useState("");
  const [error, setError] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(false);
    setSubmitting(true);
    const candidate = basicAuthValue("coordinator", password);

    try {
      const response = await fetch(
        `${import.meta.env.VITE_API_URL ?? ""}/api/groups/00000000-0000-0000-0000-000000000000/onboarding`,
        { headers: { Authorization: candidate } },
      );
      if (response.status !== 404) {
        setError(true);
        return;
      }
      queryClient.clear();
      unlock("coordinator", password);
      navigate("overview");
    } catch {
      setError(true);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="login-page">
      <div className="login-wrap">
        <a href="#/" aria-label="Group Launchpad" className="login-brand">
          <img src="/favicon.png" width="50" height="50" alt="" />
          <span className="app-wordmark">Group <strong>Launchpad</strong></span>
        </a>
        <Card className="login-card">
          <div className="login-card-heading">
            <p className="login-eyebrow">{t("login.coordinator")}</p>
            <h1>{t("login.title")}</h1>
            <p>{t("login.description")}</p>
          </div>
          <form onSubmit={onSubmit} className="login-form">
            <label htmlFor="coordinator-password">{t("auth.password")}</label>
            <input
              id="coordinator-password"
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoComplete="current-password"
              autoFocus
              required
              aria-invalid={error}
              className={error ? "login-input login-input-error" : "login-input"}
            />
            {error && <p role="alert" className="login-error">{t("auth.invalid")}</p>}
            <button type="submit" className="app-action login-submit" disabled={submitting}>
              {t(submitting ? "login.working" : "login.submit")}
            </button>
          </form>
          <a href="#/" className="login-back">{t("login.back")}</a>
        </Card>
      </div>
    </main>
  );
}
