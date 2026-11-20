import type { components } from "./schema";
import { useSyncExternalStore } from "react";

export type AuthUser = components["schemas"]["AuthUser"];
export type UserRole = components["schemas"]["UserRole"];
type AuthSession = components["schemas"]["AuthSession"];

type SessionState = { token: string; user: AuthUser; expiresAt: number };
let session: SessionState | null = null;
let refreshPromise: Promise<boolean> | null = null;
type AuthListener = () => void;
const listeners = new Set<AuthListener>();

function notify(): void {
  listeners.forEach((listener) => listener());
}

export function onAuthChange(listener: AuthListener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function isUnlocked(): boolean {
  return session !== null;
}

export function currentUser(): AuthUser | null {
  return session?.user ?? null;
}

export function useAuthUser(): AuthUser | null {
  return useSyncExternalStore(onAuthChange, currentUser, () => null);
}

export function currentSession(): Readonly<SessionState> | null {
  return session;
}

export function storeSession(value: AuthSession): void {
  session = {
    token: value.token,
    user: value.user,
    expiresAt: Date.parse(value.expiresAt),
  };
  notify();
}

export function getAuthHeader(): Record<string, string> {
  return session ? { Authorization: `Bearer ${session.token}` } : {};
}

export function lock(): void {
  session = null;
  notify();
}

export const fireLock = lock;

// ── Persistent sign-in (page-refresh resume) ────────────────────────────────
// The server keeps an HttpOnly, Path=/api/auth, SameSite=Strict cookie that
// JavaScript cannot read. On boot we try to rotate it into a fresh in-memory
// session; if it is missing/expired/revoked we simply stay locked and the
// sign-in page shows as before.
let resuming = true; // optimistic until the first resume attempt settles

export function isResuming(): boolean {
  return resuming;
}

export function useIsResuming(): boolean {
  return useSyncExternalStore(onAuthChange, isResuming, () => true);
}

export async function resumeSession(): Promise<void> {
  resuming = true;
  notify();
  try {
    const response = await fetch(`${import.meta.env.VITE_API_URL ?? ""}/api/auth/resume`, {
      method: "POST",
      credentials: "include",
      cache: "no-store",
    });
    if (response.ok) {
      const next = await response.json() as AuthSession;
      storeSession(next);
    }
  } catch {
    /* offline or API down: stay locked, show the sign-in page */
  } finally {
    resuming = false;
    notify();
  }
}

// Fire once at application start.
void resumeSession();

// Keep short-lived tokens in memory only. Refresh is serialized so concurrent
// API requests never try to rotate the same one-time token more than once.
export async function refreshSession(): Promise<boolean> {
  if (!session) return false;
  if (refreshPromise) return refreshPromise;
  const oldToken = session.token;
  refreshPromise = (async () => {
    try {
      const response = await fetch(`${import.meta.env.VITE_API_URL ?? ""}/api/auth/refresh`, {
        method: "POST",
        headers: { Authorization: `Bearer ${oldToken}` },
        cache: "no-store",
      });
      if (!response.ok) {
        if (session?.token === oldToken) lock();
        return false;
      }
      const next = await response.json() as AuthSession;
      if (session?.token !== oldToken) return false;
      storeSession(next);
      return true;
    } catch {
      if (session?.token === oldToken) lock();
      return false;
    } finally {
      refreshPromise = null;
    }
  })();
  return refreshPromise;
}

export function rejectSession(token: string): void {
  if (session?.token === token) lock();
}
