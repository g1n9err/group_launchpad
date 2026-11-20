// Coordinator unlock/lock UI + credential lifecycle. The password lives in
// memory only (lib/auth.ts). Locking clears all cached protected data.

import { useEffect, useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { fireLock, isUnlocked, onAuthChange, unlock } from "../lib/auth";
import { useSettings } from "../i18n";

export function useCoordinatorLock() {
  const queryClient = useQueryClient();

  useEffect(() => {
    const unsubscribe = onAuthChange(() => {
      // Clear ALL cached query data — roster, onboarding, everything.
      queryClient.clear();
    });
    return unsubscribe;
  }, [queryClient]);

  return {
    isUnlocked: isUnlocked(),
    lock: fireLock,
  };
}

export function CoordinatorUnlock() {
  const { t } = useSettings();
  const [password, setPassword] = useState("");
  const [error, setError] = useState(false);
  const [show, setShow] = useState(false);
  const { isUnlocked: unlocked } = useCoordinatorLock();
  const queryClient = useQueryClient();

  // Re-render when unlock state changes (simple force-update).
  const [, forceTick] = useState(0);
  useEffect(() => {
    const unsub = onAuthChange(() => forceTick((n) => n + 1));
    return unsub;
  }, []);

  if (unlocked && !show) {
    return (
      <button
        type="button"
        onClick={() => {
          fireLock();
          setPassword("");
        }}
        className="rounded-md border border-border bg-surface px-3 py-1.5 text-xs font-semibold text-danger-text transition-colors hover:bg-danger-bg"
      >
        {t("auth.lock")}
      </button>
    );
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(false);
    // Build the candidate header LOCALLY — do NOT store it yet. AuthGate
    // stays closed until the 404 confirmation arrives; only then do we
    // call unlock() which stores the credential AND fires the auth-change
    // event that opens the gate.
    const candidate = "Basic " + btoa("coordinator:" + password);
    try {
      const resp = await fetch(
        `${import.meta.env.VITE_API_URL ?? ""}/api/groups/00000000-0000-0000-0000-000000000000/onboarding`,
        { headers: { Authorization: candidate } },
      );
      if (resp.status !== 404) {
        // 401 (wrong password), 403 (bad origin), 5xx, etc. — stay locked.
        setError(true);
        return;
      }
    } catch {
      // Network failure — credentials unverified, stay locked.
      setError(true);
      return;
    }
    // 404 = credentials verified (auth passed, UUID not found).
    // NOW store and notify — AuthGate opens, protected queries fire with
    // valid credentials.
    unlock("coordinator", password);
    queryClient.invalidateQueries();
    setShow(false);
    forceTick((n) => n + 1);
  }

  return (
    <form onSubmit={onSubmit} className="flex items-center gap-2">
      <input
        type="password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        placeholder={t("auth.password")}
        autoComplete="current-password"
        className={`w-32 rounded-md border px-2 py-1.5 text-xs text-text outline-none focus:ring-2 ${
          error ? "border-danger-ring focus:ring-danger-ring/50" : "border-border focus:ring-primary/40"
        }`}
      />
      <button
        type="submit"
        className="rounded-md bg-primary px-3 py-1.5 text-xs font-semibold text-on-primary transition-colors hover:bg-primary-hover"
      >
        {t("auth.unlock")}
      </button>
      {show && (
        <button
          type="button"
          onClick={() => setShow(false)}
          className="text-xs text-muted hover:text-text"
        >
          ✕
        </button>
      )}
    </form>
  );
}
