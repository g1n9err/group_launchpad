import { useState, type FormEvent } from "react";
import { HomeLogo } from "../components/HomeLogo";
import { Card } from "../components/ui";
import { useSettings } from "../i18n";
import { apiUrl } from "../lib/api";
import { lock } from "../lib/auth";

export function PasswordRecoveryPage({ token, reset }: { token?: string; reset: boolean }) {
  const { t, lang } = useSettings();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [state, setState] = useState<"pending" | "sent" | "invalid" | "unavailable" | "mismatch" | "length">();

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (state === "pending") return;
    if (reset && password !== confirm) {
      setState("mismatch");
      return;
    }
    const bytes = new TextEncoder().encode(password).length;
    if (reset && (bytes < 12 || bytes > 72)) {
      setState("length");
      return;
    }
    setState("pending");
    try {
      const response = await fetch(apiUrl(reset ? "/api/auth/password-reset" : "/api/auth/password-reset/request"), {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(reset ? { token, password } : { email: email.trim(), language: lang }),
      });
      if (reset && response.status === 204) {
        lock();
        setPassword("");
        setConfirm("");
        window.location.replace("#/login/reset-complete");
      } else if (!reset && response.status === 202) {
        setState("sent");
      } else {
        setState(reset && response.status === 400 ? "invalid" : "unavailable");
      }
    } catch {
      setState("unavailable");
    }
  }

  const invalid = reset && (!token || state === "invalid");
  return (
    <main className="login-page">
      <div className="login-wrap">
        <HomeLogo size={50} wordmark className="login-brand" />
        <Card className="login-card">
          <div className="login-card-heading">
            <h1>{t(reset ? "recovery.resetTitle" : "login.forgotPassword")}</h1>
          </div>
          {invalid ? (
            <p role="alert" className="login-error">{t("recovery.invalid")}</p>
          ) : state === "sent" ? (
            <p role="status">{t("recovery.sent")}</p>
          ) : (
            <form className="login-form" onSubmit={submit}>
              {reset ? <>
                <label htmlFor="reset-password">{t("auth.password")}</label>
                <input id="reset-password" className="login-input" type="password" autoComplete="new-password"
                  required value={password} onChange={(event) => setPassword(event.target.value)} />
                <label htmlFor="reset-confirm">{t("signup.confirmPassword")}</label>
                <input id="reset-confirm" className="login-input" type="password" autoComplete="new-password"
                  required value={confirm} onChange={(event) => setConfirm(event.target.value)} />
              </> : <>
                <label htmlFor="reset-email">{t("login.email")}</label>
                <input id="reset-email" className="login-input" type="email" autoComplete="email"
                  required value={email} onChange={(event) => setEmail(event.target.value)} />
              </>}
              {state === "unavailable" && <p role="alert" className="login-error">{t("recovery.unavailable")}</p>}
              {state === "mismatch" && <p role="alert" className="login-error">{t("signup.passwordMismatch")}</p>}
              {state === "length" && <p role="alert" className="login-error">{t("recovery.length")}</p>}
              <button className="app-action login-submit" type="submit" disabled={state === "pending"}>
                {t(state === "pending" ? "intake.working" : reset ? "recovery.save" : "recovery.send")}
              </button>
            </form>
          )}
          {reset && <a href="#/forgot-password" className="login-back">{t("recovery.send")}</a>}
          <a href="#/login" className="login-back">{t("login.submit")}</a>
        </Card>
      </div>
    </main>
  );
}
