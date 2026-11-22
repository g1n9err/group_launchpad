import { PasswordHelp } from "../components/PasswordHelp";
import { HomeLogo } from "../components/HomeLogo";
import { useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Card } from "../components/ui";
import { useSettings } from "../i18n";
import { api } from "../lib/api";
import { storeSession } from "../lib/auth";
import { navigate } from "../lib/router";

export function LoginPage({ passwordReset = false }: { passwordReset?: boolean }) {
  const { t } = useSettings();
  const queryClient = useQueryClient();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(false);
    setSubmitting(true);
    try {
      const { data, response } = await api.POST("/api/auth/login", {
        body: { email: email.trim(), password },
      });
      if (!response.ok || !data) {
        setError(true);
        return;
      }
      queryClient.clear();
      storeSession(data);
      setPassword("");
      if (data.user.role === "lecturer" || data.user.role === "ta") navigate("groups");
      else if (data.user.role === "student") navigate("account");
      else navigate("overview");
    } catch {
      setError(true);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="login-page">
      <div className="login-wrap">
        <HomeLogo size={50} wordmark className="login-brand" />
        <Card className="login-card">
          <div className="login-card-heading">
            <p className="login-eyebrow">{t("login.coordinator")}</p>
            <h1>{t("login.title")}</h1>
            <p>{t("login.description")}</p>
          </div>
          {passwordReset && <p role="status" className="mt-3 text-sm text-text">{t("recovery.saved")}</p>}
          <form onSubmit={onSubmit} className="login-form">
            <label htmlFor="login-email">{t("login.email")}</label>
            <input
              id="login-email"
              type="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              autoComplete="username"
              autoFocus
              required
              className="login-input"
            />
            <label htmlFor="login-password">{t("auth.password")}</label>
            <input
              id="login-password"
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoComplete="current-password"
              required
              aria-invalid={error}
              className={error ? "login-input login-input-error" : "login-input"}
            />
            {error && <p role="alert" className="login-error">{t("auth.invalid")}</p>}
            <button type="submit" className="app-action login-submit" disabled={submitting}>
              {t(submitting ? "login.working" : "login.submit")}
            </button>
          </form>
          <PasswordHelp />
          <p className="mt-5 text-center text-sm text-muted">{t("signup.needAccount")} <a href="#/signup" className="font-semibold text-primary hover:underline">{t("signup.submit")}</a></p>
          <a href="#/" className="login-back">{t("login.back")}</a>
        </Card>
      </div>
    </main>
  );
}
