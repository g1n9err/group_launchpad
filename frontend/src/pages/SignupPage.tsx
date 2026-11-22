import { useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Card } from "../components/ui";
import { HomeLogo } from "../components/HomeLogo";
import { useSettings } from "../i18n";
import { apiUrl } from "../lib/api";
import type { components } from "../lib/schema";
import { storeSession } from "../lib/auth";
import { navigate } from "../lib/router";

type AuthSession = components["schemas"]["AuthSession"];

export function SignupPage() {
  const { t } = useSettings();
  const queryClient = useQueryClient();
  const [fullName, setFullName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    if (password !== confirmation) {
      setError(t("signup.passwordMismatch"));
      return;
    }
    setSubmitting(true);
    try {
      const response = await fetch(apiUrl("/api/auth/register"), {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        credentials: "include",
        cache: "no-store",
        body: JSON.stringify({ fullName: fullName.trim(), email: email.trim(), password }),
      });
      if (!response.ok) {
        setError(response.status === 409 ? t("signup.emailExists") : response.status === 422 ? t("signup.invalid") : t("signup.failed"));
        return;
      }
      const session = await response.json() as AuthSession;
      queryClient.clear();
      storeSession(session);
      setPassword("");
      setConfirmation("");
      navigate("overview");
    } catch {
      setError(t("signup.failed"));
    } finally {
      setSubmitting(false);
    }
  }

  return <main className="login-page">
    <div className="login-wrap">
      <HomeLogo size={50} wordmark className="login-brand" />
      <Card className="login-card">
        <div className="login-card-heading">
          <p className="login-eyebrow">{t("signup.eyebrow")}</p>
          <h1>{t("signup.title")}</h1>
          <p>{t("signup.description")}</p>
        </div>
        <form onSubmit={submit} className="login-form">
          <label htmlFor="signup-name">{t("signup.fullName")}</label>
          <input id="signup-name" className="login-input" value={fullName} onChange={(event) => setFullName(event.target.value)} autoComplete="name" maxLength={160} required />
          <label htmlFor="signup-email">{t("login.email")}</label>
          <input id="signup-email" className="login-input" type="email" value={email} onChange={(event) => setEmail(event.target.value)} autoComplete="email" required />
          <label htmlFor="signup-password">{t("auth.password")}</label>
          <input id="signup-password" className="login-input" type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="new-password" minLength={12} maxLength={72} required />
          <label htmlFor="signup-confirmation">{t("signup.confirmPassword")}</label>
          <input id="signup-confirmation" className="login-input" type="password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} autoComplete="new-password" minLength={12} maxLength={72} required />
          <p className="text-xs text-muted">{t("signup.passwordHint")}</p>
          {error && <p role="alert" className="login-error">{error}</p>}
          <button type="submit" className="app-action login-submit" disabled={submitting}>
            {t(submitting ? "signup.working" : "signup.submit")}
          </button>
        </form>
        <p className="mt-5 text-center text-sm text-muted">{t("signup.haveAccount")} <a href="#/login" className="font-semibold text-primary hover:underline">{t("login.submit")}</a></p>
      </Card>
    </div>
  </main>;
}
